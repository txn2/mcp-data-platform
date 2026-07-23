// Package postgres provides PostgreSQL storage for prompts.
package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

// Compile-time interface verification.
var _ prompt.Store = (*Store)(nil)

// Store implements prompt.Store using PostgreSQL.
type Store struct {
	db *sql.DB
}

// New creates a new PostgreSQL prompt store.
func New(db *sql.DB) *Store {
	return &Store{db: db}
}

// promptColumns is the column list read by every SELECT, kept in one place so
// the scan order in scanPrompt cannot drift from the query.
const promptColumns = `id, name, display_name, description, content, arguments,
	category, scope, personas, owner_email, source, enabled, tags, status,
	approved_by, approved_at, deprecated_at, superseded_by,
	review_requested, requested_scope, requested_personas, version,
	COALESCE(collection_id::text, ''), created_at, updated_at`

// promptSelect is the base SELECT for the prompt columns.
const promptSelect = "SELECT " + promptColumns + " FROM prompts"

// rowScanner is satisfied by *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

// promptScanDest returns the scan destinations for one prompt row in
// promptColumns order. It is the single definition of that order, shared by
// scanPrompt and the ranked-search scanners (which append their score columns),
// so the scan order cannot drift from promptColumns across call sites.
func promptScanDest(p *prompt.Prompt, argsJSON *[]byte) []any {
	return []any{
		&p.ID, &p.Name, &p.DisplayName, &p.Description, &p.Content, argsJSON,
		&p.Category, &p.Scope, pq.Array(&p.Personas), &p.OwnerEmail,
		&p.Source, &p.Enabled, pq.Array(&p.Tags), &p.Status,
		&p.ApprovedBy, &p.ApprovedAt, &p.DeprecatedAt, &p.SupersededBy,
		&p.ReviewRequested, &p.RequestedScope, pq.Array(&p.RequestedPersonas),
		&p.Version, &p.CollectionID, &p.CreatedAt, &p.UpdatedAt,
	}
}

// scanPrompt reads one row in promptColumns order into a Prompt.
func scanPrompt(sc rowScanner) (*prompt.Prompt, error) {
	p := &prompt.Prompt{}
	var argsJSON []byte
	if err := sc.Scan(promptScanDest(p, &argsJSON)...); err != nil {
		return nil, fmt.Errorf("scanning prompt row: %w", err)
	}
	if err := finishPrompt(p, argsJSON); err != nil {
		return nil, err
	}
	return p, nil
}

// finishPrompt unmarshals the arguments JSON and normalizes nil slices for a
// freshly scanned prompt. Shared by scanPrompt and the search scanners.
func finishPrompt(p *prompt.Prompt, argsJSON []byte) error {
	if err := json.Unmarshal(argsJSON, &p.Arguments); err != nil {
		return fmt.Errorf("unmarshal arguments: %w", err)
	}
	normalizeSlices(p)
	return nil
}

// normalizeSlices ensures slice fields are non-nil for stable JSON output.
func normalizeSlices(p *prompt.Prompt) {
	if p.Arguments == nil {
		p.Arguments = []prompt.Argument{}
	}
	if p.Personas == nil {
		p.Personas = []string{}
	}
	if p.Tags == nil {
		p.Tags = []string{}
	}
	if p.RequestedPersonas == nil {
		p.RequestedPersonas = []string{}
	}
}

// Create persists a new prompt. If p.ID is empty the database generates one.
// Every non-system prompt is created with version 1 and a matching applied
// snapshot in prompt_versions, in one transaction; system rows (read-only
// config mirrors re-ingested at startup) are never versioned.
func (s *Store) Create(ctx context.Context, p *prompt.Prompt) error {
	// Normalize nil slices to empty before binding: pq.Array(nil) binds SQL NULL,
	// which violates the NOT NULL constraints on personas, tags, and
	// requested_personas (each DEFAULT '{}'). The column DEFAULT does not apply
	// because the INSERT supplies an explicit value. tags is the field with no
	// input source on the create path, so without this every create fails.
	normalizeSlices(p)
	argsJSON, err := json.Marshal(p.Arguments)
	if err != nil {
		return fmt.Errorf("marshal arguments: %w", err)
	}
	if p.Status == "" {
		p.Status = prompt.StatusDraft
	}

	query := `
		INSERT INTO prompts (name, display_name, description, content, arguments,
		                     category, scope, personas, owner_email, source, enabled,
		                     tags, status, approved_by, approved_at, deprecated_at,
		                     superseded_by, review_requested, requested_scope, requested_personas,
		                     collection_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11,
		        $12, $13, $14, $15, $16, $17, $18, $19, $20, $21)
		RETURNING id, version, created_at, updated_at`

	return s.withTx(ctx, "create prompt", func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, query,
			p.Name, p.DisplayName, p.Description, p.Content, argsJSON,
			p.Category, p.Scope, pq.Array(p.Personas), p.OwnerEmail, p.Source, p.Enabled,
			pq.Array(p.Tags), p.Status, p.ApprovedBy, p.ApprovedAt, p.DeprecatedAt,
			p.SupersededBy, p.ReviewRequested, p.RequestedScope, pq.Array(p.RequestedPersonas),
			nullableID(p.CollectionID),
		).Scan(&p.ID, &p.Version, &p.CreatedAt, &p.UpdatedAt)
		if err != nil {
			return fmt.Errorf("create prompt: %w", err)
		}
		if p.Source == prompt.SourceSystem {
			return nil
		}
		// The creating prompt may already carry an approval stamp (backfills,
		// tests); bind it to v1 so the stamp and the snapshot stay together.
		return insertVersionRow(ctx, tx, versionInsert{
			PromptID:   p.ID,
			Version:    p.Version,
			Snapshot:   p,
			Author:     p.OwnerEmail,
			Status:     prompt.VersionStatusApplied,
			ApprovedBy: p.ApprovedBy,
			ApprovedAt: p.ApprovedAt,
		})
	})
}

// Get retrieves a non-personal (global or persona) prompt by name. Personal
// prompts are per-owner; use GetPersonal. Returns nil, nil if not found.
func (s *Store) Get(ctx context.Context, name string) (*prompt.Prompt, error) {
	query := promptSelect + ` WHERE name = $1 AND scope <> 'personal'`
	return s.queryOne(ctx, query, name)
}

// GetPersonal retrieves a personal prompt by its owner and name. Returns nil,
// nil if not found.
func (s *Store) GetPersonal(ctx context.Context, ownerEmail, name string) (*prompt.Prompt, error) {
	query := promptSelect + ` WHERE owner_email = $1 AND name = $2 AND scope = 'personal'`
	return s.queryOne(ctx, query, ownerEmail, name)
}

// ListPersonalByName retrieves every personal prompt with the given name across
// all owners. Personal names are unique only within an owner, so this may return
// more than one row; an admin uses it to resolve a personal prompt authored by
// another user and to disambiguate by owner. Returns an empty slice if none match.
func (s *Store) ListPersonalByName(ctx context.Context, name string) ([]prompt.Prompt, error) {
	query := promptSelect + ` WHERE name = $1 AND scope = 'personal' ORDER BY owner_email`
	rows, err := s.db.QueryContext(ctx, query, name)
	if err != nil {
		return nil, fmt.Errorf("list personal prompts by name: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var result []prompt.Prompt
	for rows.Next() {
		p, err := scanPrompt(rows)
		if err != nil {
			return nil, fmt.Errorf("scan prompt: %w", err)
		}
		result = append(result, *p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate personal prompts: %w", err)
	}
	return result, nil
}

// GetByID retrieves a prompt by ID. Returns nil, nil if not found.
func (s *Store) GetByID(ctx context.Context, id string) (*prompt.Prompt, error) {
	query := promptSelect + ` WHERE id = $1`
	return s.queryOne(ctx, query, id)
}

// queryOne runs a single-row query and maps not-found to (nil, nil). A
// caller-supplied id that fails UUID parsing (22P02) names no row and maps to
// not-found the same way, so a malformed id is a 404, not a 500.
func (s *Store) queryOne(ctx context.Context, query string, args ...any) (*prompt.Prompt, error) {
	p, err := scanPrompt(s.db.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) || pqCode(err, pqInvalidTextRepresentation) {
		return nil, nil //nolint:nilnil // Store interface contract: nil, nil means not found
	}
	if err != nil {
		return nil, fmt.Errorf("get prompt: %w", err)
	}
	return p, nil
}

// Update modifies an existing prompt identified by ID. It does not create a
// version snapshot (use UpdateWithVersion for versioned edits), but it does
// bind a first approval to the current version: when the update carries the
// draft-to-approved transition, the approval stamp is copied onto the current
// prompt_versions row in the same transaction, so "approved" always names a
// specific snapshot regardless of which path performed the approval. Like
// UpdateWithVersion, it re-validates the review gate under the row lock, so a
// write racing an approval cannot slip unreviewed content past it.
func (s *Store) Update(ctx context.Context, p *prompt.Prompt) error {
	// See Create: nil slices must be normalized to empty so pq.Array binds '{}'
	// rather than NULL into the NOT NULL personas/tags/requested_personas columns.
	normalizeSlices(p)
	return s.withTx(ctx, "update prompt", func(tx *sql.Tx) error {
		before, err := lockPrompt(ctx, tx, p.ID)
		if err != nil {
			return err
		}
		if err := requireUngated(before, p); err != nil {
			return err
		}
		if err := updateTx(ctx, tx, p); err != nil {
			return err
		}
		return stampApprovalTransition(ctx, tx, p, before.Status)
	})
}

// updateTx writes every prompt column within the caller's transaction. Shared
// by Update, UpdateWithVersion, and ApproveVersion so the embedding
// invalidation below applies identically on every write path.
func updateTx(ctx context.Context, tx *sql.Tx, p *prompt.Prompt) error {
	argsJSON, err := json.Marshal(p.Arguments)
	if err != nil {
		return fmt.Errorf("marshal arguments: %w", err)
	}

	// Clear the embedding when the indexed text changes so a content edit never
	// leaves a stale vector ranking against the old text. The CASE compares the
	// stored hash against the new text's hash (both reference the pre-update
	// row), so a metadata-only edit (status, personas) preserves the embedding
	// and avoids needless re-embedding; an actual text change drops it to NULL
	// for the reconciler to backfill. The hash is indexjobs.TextHash, the exact
	// hash the worker stores, so the two definitions cannot diverge.
	newHash := indexjobs.TextHash(prompt.IndexText(p))

	query := `
		UPDATE prompts
		SET name = $2, display_name = $3, description = $4, content = $5,
		    arguments = $6, category = $7, scope = $8, personas = $9,
		    owner_email = $10, source = $11, enabled = $12, tags = $13,
		    status = $14, approved_by = $15, approved_at = $16, deprecated_at = $17,
		    superseded_by = $18, review_requested = $19, requested_scope = $20,
		    requested_personas = $21, collection_id = $24,
		    version = CASE WHEN $23 > 0 THEN $23 ELSE version END,
		    embedding = CASE WHEN embedding_text_hash IS DISTINCT FROM $22
		                     THEN NULL ELSE embedding END,
		    embedding_model = CASE WHEN embedding_text_hash IS DISTINCT FROM $22
		                          THEN '' ELSE embedding_model END,
		    embedding_text_hash = CASE WHEN embedding_text_hash IS DISTINCT FROM $22
		                              THEN NULL ELSE embedding_text_hash END,
		    updated_at = NOW()
		WHERE id = $1`

	res, err := tx.ExecContext(ctx, query,
		p.ID, p.Name, p.DisplayName, p.Description, p.Content, argsJSON,
		p.Category, p.Scope, pq.Array(p.Personas), p.OwnerEmail, p.Source, p.Enabled,
		pq.Array(p.Tags), p.Status, p.ApprovedBy, p.ApprovedAt, p.DeprecatedAt,
		p.SupersededBy, p.ReviewRequested, p.RequestedScope, pq.Array(p.RequestedPersonas),
		newHash, p.Version, nullableID(p.CollectionID),
	)
	if err != nil {
		return fmt.Errorf("update prompt: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("prompt %s not found", p.ID)
	}
	return nil
}

// withTx runs fn inside a transaction, rolling back on error. op labels the
// begin/commit errors.
func (s *Store) withTx(ctx context.Context, op string, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin %s: %w", op, err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit %s: %w", op, err)
	}
	return nil
}

// Delete removes a non-personal prompt by name.
func (s *Store) Delete(ctx context.Context, name string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM prompts WHERE name = $1 AND scope <> 'personal'`, name)
	if err != nil {
		return fmt.Errorf("delete prompt: %w", err)
	}
	return nil
}

// DeleteByID removes a prompt by ID.
func (s *Store) DeleteByID(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM prompts WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete prompt by id: %w", err)
	}
	return nil
}

// List returns prompts matching the filter.
func (s *Store) List(ctx context.Context, filter prompt.ListFilter) ([]prompt.Prompt, error) {
	where, args := buildWhere(filter)
	// #nosec G202 -- WHERE clause built from validated parameters only (scope enum, email, bool, array, ILIKE pattern)
	query := promptSelect + where + ` ORDER BY scope, name`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list prompts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var result []prompt.Prompt
	for rows.Next() {
		p, err := scanPrompt(rows)
		if err != nil {
			return nil, fmt.Errorf("scan prompt: %w", err)
		}
		result = append(result, *p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate prompts: %w", err)
	}
	return result, nil
}

// Count returns the number of prompts matching the filter.
func (s *Store) Count(ctx context.Context, filter prompt.ListFilter) (int, error) {
	where, args := buildWhere(filter)
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM prompts`+where, args...,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count prompts: %w", err)
	}
	return count, nil
}

// buildWhere constructs a WHERE clause and parameter list from a ListFilter.
func buildWhere(f prompt.ListFilter) (clause string, params []any) {
	var conds []string
	var args []any
	idx := 1

	if f.Scope != "" {
		conds = append(conds, fmt.Sprintf("scope = $%d", idx))
		args = append(args, f.Scope)
		idx++
	}
	if f.OwnerEmail != "" {
		conds = append(conds, fmt.Sprintf("owner_email = $%d", idx))
		args = append(args, f.OwnerEmail)
		idx++
	}
	if f.Enabled != nil {
		conds = append(conds, fmt.Sprintf("enabled = $%d", idx))
		args = append(args, *f.Enabled)
		idx++
	}
	if len(f.Personas) > 0 {
		conds = append(conds, fmt.Sprintf("personas && $%d", idx))
		args = append(args, pq.Array(f.Personas))
		idx++
	}
	if f.ReviewRequested != nil {
		conds = append(conds, fmt.Sprintf("review_requested = $%d", idx))
		args = append(args, *f.ReviewRequested)
		idx++
	}
	if f.Source != "" {
		conds = append(conds, fmt.Sprintf("source = $%d", idx))
		args = append(args, f.Source)
		idx++
	}
	if f.ExcludeSource != "" {
		conds = append(conds, fmt.Sprintf("source <> $%d", idx))
		args = append(args, f.ExcludeSource)
		idx++
	}
	if f.Search != "" {
		conds = append(conds, fmt.Sprintf(
			"(name ILIKE $%d OR display_name ILIKE $%d OR description ILIKE $%d)",
			idx, idx, idx))
		args = append(args, "%"+f.Search+"%")
	}

	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}
