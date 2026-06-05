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
	approved_by, approved_at, deprecated_at, superseded_by, review_requested,
	requested_scope, requested_personas, created_at, updated_at`

// promptSelect is the base SELECT for the prompt columns.
const promptSelect = "SELECT " + promptColumns + " FROM prompts"

// rowScanner is satisfied by *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanPrompt reads one row in promptColumns order into a Prompt.
func scanPrompt(sc rowScanner) (*prompt.Prompt, error) {
	p := &prompt.Prompt{}
	var argsJSON []byte
	if err := sc.Scan(
		&p.ID, &p.Name, &p.DisplayName, &p.Description, &p.Content, &argsJSON,
		&p.Category, &p.Scope, pq.Array(&p.Personas), &p.OwnerEmail,
		&p.Source, &p.Enabled, pq.Array(&p.Tags), &p.Status,
		&p.ApprovedBy, &p.ApprovedAt, &p.DeprecatedAt, &p.SupersededBy, &p.ReviewRequested,
		&p.RequestedScope, pq.Array(&p.RequestedPersonas), &p.CreatedAt, &p.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("scanning prompt row: %w", err)
	}
	if err := json.Unmarshal(argsJSON, &p.Arguments); err != nil {
		return nil, fmt.Errorf("unmarshal arguments: %w", err)
	}
	normalizeSlices(p)
	return p, nil
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
func (s *Store) Create(ctx context.Context, p *prompt.Prompt) error {
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
		                     superseded_by, review_requested, requested_scope, requested_personas)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11,
		        $12, $13, $14, $15, $16, $17, $18, $19, $20)
		RETURNING id, created_at, updated_at`

	err = s.db.QueryRowContext(ctx, query,
		p.Name, p.DisplayName, p.Description, p.Content, argsJSON,
		p.Category, p.Scope, pq.Array(p.Personas), p.OwnerEmail, p.Source, p.Enabled,
		pq.Array(p.Tags), p.Status, p.ApprovedBy, p.ApprovedAt, p.DeprecatedAt,
		p.SupersededBy, p.ReviewRequested, p.RequestedScope, pq.Array(p.RequestedPersonas),
	).Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return fmt.Errorf("create prompt: %w", err)
	}
	return nil
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

// GetByID retrieves a prompt by ID. Returns nil, nil if not found.
func (s *Store) GetByID(ctx context.Context, id string) (*prompt.Prompt, error) {
	query := promptSelect + ` WHERE id = $1`
	return s.queryOne(ctx, query, id)
}

// queryOne runs a single-row query and maps not-found to (nil, nil).
func (s *Store) queryOne(ctx context.Context, query string, args ...any) (*prompt.Prompt, error) {
	p, err := scanPrompt(s.db.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // Store interface contract: nil, nil means not found
	}
	if err != nil {
		return nil, fmt.Errorf("get prompt: %w", err)
	}
	return p, nil
}

// Update modifies an existing prompt identified by ID.
func (s *Store) Update(ctx context.Context, p *prompt.Prompt) error {
	argsJSON, err := json.Marshal(p.Arguments)
	if err != nil {
		return fmt.Errorf("marshal arguments: %w", err)
	}

	query := `
		UPDATE prompts
		SET name = $2, display_name = $3, description = $4, content = $5,
		    arguments = $6, category = $7, scope = $8, personas = $9,
		    owner_email = $10, source = $11, enabled = $12, tags = $13,
		    status = $14, approved_by = $15, approved_at = $16, deprecated_at = $17,
		    superseded_by = $18, review_requested = $19, requested_scope = $20,
		    requested_personas = $21, updated_at = NOW()
		WHERE id = $1`

	res, err := s.db.ExecContext(ctx, query,
		p.ID, p.Name, p.DisplayName, p.Description, p.Content, argsJSON,
		p.Category, p.Scope, pq.Array(p.Personas), p.OwnerEmail, p.Source, p.Enabled,
		pq.Array(p.Tags), p.Status, p.ApprovedBy, p.ApprovedAt, p.DeprecatedAt,
		p.SupersededBy, p.ReviewRequested, p.RequestedScope, pq.Array(p.RequestedPersonas),
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
