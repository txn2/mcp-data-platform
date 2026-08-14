// Package scriptstore is the PostgreSQL implementation of the managed-script
// store contract (pkg/script). It is built only by internal/platform/
// scriptlayer, which is why it lives under internal/ rather than beside the
// domain: an implementation seam with one composition-root caller is not part
// of the module's supported import surface (docs/library/stability.md).
//
// The layout follows pkg/prompt/postgres closely — a column list defined once
// so the scan order cannot drift from the query, a withTx helper, and version
// writes transactional with the scripts row they touch.
package scriptstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Compile-time interface verification.
var (
	_ script.Store        = (*Store)(nil)
	_ script.VersionStore = (*Store)(nil)
)

// defaultListLimit caps a List with no explicit limit, so a caller that forgets
// one cannot pull the whole table into a tool response.
const defaultListLimit = 200

// Store implements script.Store and script.VersionStore using PostgreSQL.
type Store struct {
	db *sql.DB
}

// New creates a PostgreSQL script store over db.
func New(db *sql.DB) *Store { return &Store{db: db} }

// scriptColumns is the column list read by every scripts SELECT, kept in one
// place so the scan order in scanScript cannot drift from the query.
const scriptColumns = `id, name, display_name, description, source_code, params,
	scope, personas, owner_email, tags, enabled, status, superseded_by,
	deprecated_at, version, COALESCE(approved_version_id::text, ''),
	created_at, updated_at`

// scriptSelect is the base SELECT for the script columns.
const scriptSelect = "SELECT " + scriptColumns + " FROM scripts"

// rowScanner is satisfied by *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanScript reads one row in scriptColumns order into a Script.
func scanScript(sc rowScanner) (*script.Script, error) {
	s := &script.Script{}
	var paramsJSON []byte
	err := sc.Scan(&s.ID, &s.Name, &s.DisplayName, &s.Description, &s.Source, &paramsJSON,
		&s.Scope, pq.Array(&s.Personas), &s.OwnerEmail, pq.Array(&s.Tags), &s.Enabled,
		&s.Status, &s.SupersededBy, &s.DeprecatedAt, &s.Version, &s.ApprovedVersionID,
		&s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("scanning script row: %w", err)
	}
	if err := json.Unmarshal(paramsJSON, &s.Params); err != nil {
		return nil, fmt.Errorf("unmarshal script params: %w", err)
	}
	normalizeSlices(s)
	return s, nil
}

// normalizeSlices ensures slice fields are non-nil for stable JSON output and
// for binding: pq.Array(nil) binds SQL NULL, which violates the NOT NULL
// constraints on personas and tags.
func normalizeSlices(s *script.Script) {
	if s.Params == nil {
		s.Params = []script.Param{}
	}
	if s.Personas == nil {
		s.Personas = []string{}
	}
	if s.Tags == nil {
		s.Tags = []string{}
	}
}

// withTx runs fn inside a transaction, rolling back on error. op names the
// operation for error wrapping.
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

// Create persists a new script and its v1 snapshot in one transaction, so a
// script never exists without the version history that explains it.
func (s *Store) Create(ctx context.Context, sc *script.Script) error {
	normalizeSlices(sc)
	paramsJSON, err := json.Marshal(sc.Params)
	if err != nil {
		return fmt.Errorf("marshal script params: %w", err)
	}
	if sc.Status == "" {
		sc.Status = script.StatusDraft
	}
	sc.Version = 1
	return s.withTx(ctx, "create script", func(tx *sql.Tx) error {
		row := tx.QueryRowContext(ctx, `
			INSERT INTO scripts (name, display_name, description, source_code, params,
			                     scope, personas, owner_email, tags, enabled, status, version)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, 1)
			RETURNING id, created_at, updated_at`,
			sc.Name, sc.DisplayName, sc.Description, sc.Source, paramsJSON,
			sc.Scope, pq.Array(sc.Personas), sc.OwnerEmail, pq.Array(sc.Tags),
			sc.Enabled, sc.Status)
		if err := row.Scan(&sc.ID, &sc.CreatedAt, &sc.UpdatedAt); err != nil {
			return fmt.Errorf("insert script: %w", err)
		}
		return insertVersionRow(ctx, tx, versionInsert{
			ScriptID: sc.ID, Version: 1, Snapshot: sc,
			Author: sc.OwnerEmail, Status: script.VersionStatusApplied,
		})
	})
}

// Get retrieves a shared (global or persona) script by its globally unique name.
func (s *Store) Get(ctx context.Context, name string) (*script.Script, error) {
	return s.getOne(ctx, scriptSelect+` WHERE name = $1 AND scope <> 'personal'`, name)
}

// GetPersonal retrieves a personal script by owner and name.
func (s *Store) GetPersonal(ctx context.Context, ownerEmail, name string) (*script.Script, error) {
	return s.getOne(ctx,
		scriptSelect+` WHERE name = $1 AND owner_email = $2 AND scope = 'personal'`, name, ownerEmail)
}

// GetByID retrieves a script by ID.
func (s *Store) GetByID(ctx context.Context, id string) (*script.Script, error) {
	return s.getOne(ctx, scriptSelect+` WHERE id = $1`, id)
}

// getOne runs a single-row script query, mapping no rows to nil, nil.
func (s *Store) getOne(ctx context.Context, query string, args ...any) (*script.Script, error) {
	sc, err := scanScript(s.db.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // Store contract: nil, nil means not found
	}
	if err != nil {
		return nil, fmt.Errorf("get script: %w", err)
	}
	return sc, nil
}

// Update writes the live script row. It does not touch version history; use
// UpdateWithVersion (through script.ApplyEdit) for edits that must be
// snapshotted.
func (s *Store) Update(ctx context.Context, sc *script.Script) error {
	normalizeSlices(sc)
	return s.withTx(ctx, "update script", func(tx *sql.Tx) error {
		return updateTx(ctx, tx, sc)
	})
}

// updateTx writes the live script row within the caller's transaction.
func updateTx(ctx context.Context, tx *sql.Tx, sc *script.Script) error {
	paramsJSON, err := json.Marshal(sc.Params)
	if err != nil {
		return fmt.Errorf("marshal script params: %w", err)
	}
	res, err := tx.ExecContext(ctx, `
		UPDATE scripts
		   SET name = $2, display_name = $3, description = $4, source_code = $5,
		       params = $6, scope = $7, personas = $8, owner_email = $9, tags = $10,
		       enabled = $11, status = $12, superseded_by = $13, deprecated_at = $14,
		       version = $15, updated_at = NOW()
		 WHERE id = $1`,
		sc.ID, sc.Name, sc.DisplayName, sc.Description, sc.Source, paramsJSON,
		sc.Scope, pq.Array(sc.Personas), sc.OwnerEmail, pq.Array(sc.Tags),
		sc.Enabled, sc.Status, sc.SupersededBy, sc.DeprecatedAt, sc.Version)
	if err != nil {
		return fmt.Errorf("update script: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("script %s not found", sc.ID)
	}
	return nil
}

// Delete removes a script by ID. Its versions cascade.
func (s *Store) Delete(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM scripts WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete script: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("script %s not found", id)
	}
	return nil
}

// List returns scripts matching the filter, newest first.
func (s *Store) List(ctx context.Context, filter script.ListFilter) ([]script.Script, error) {
	query, args := buildListQuery(filter)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list scripts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []script.Script{}
	for rows.Next() {
		sc, err := scanScript(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate scripts: %w", err)
	}
	return out, nil
}

// listQuery accumulates a filtered listing's WHERE clauses and their bound
// arguments, so each clause is added next to the value it binds.
type listQuery struct {
	where []string
	args  []any
}

// add appends one clause whose format string carries a single placeholder.
func (q *listQuery) add(clause string, arg any) {
	q.args = append(q.args, arg)
	q.where = append(q.where, fmt.Sprintf(clause, len(q.args)))
}

// addEquality appends the plain equality filters.
func (q *listQuery) addEquality(filter script.ListFilter) {
	if filter.Scope != "" {
		q.add("scope = $%d", filter.Scope)
	}
	if len(filter.Personas) > 0 {
		q.add("personas && $%d", pq.Array(filter.Personas))
	}
	if filter.OwnerEmail != "" {
		q.add("owner_email = $%d", filter.OwnerEmail)
	}
	if filter.Enabled != nil {
		q.add("enabled = $%d", *filter.Enabled)
	}
	if filter.Status != "" {
		q.add("status = $%d", filter.Status)
	}
}

// addVisibility appends the query-side form of script.VisibleTo: global
// scripts, the persona-scoped scripts of one persona, and one owner's personal
// scripts. It is one clause so the list a caller sees and the scripts they may
// read cannot diverge.
func (q *listQuery) addVisibility(filter script.ListFilter) {
	if filter.VisibleTo == "" {
		return
	}
	q.args = append(q.args, filter.VisibleTo, pq.Array([]string{filter.VisiblePersona}))
	owner, persona := len(q.args)-1, len(q.args)
	q.where = append(q.where, fmt.Sprintf(
		"(scope = 'global' OR (scope = 'persona' AND personas && $%d) OR (scope = 'personal' AND owner_email = $%d))",
		persona, owner))
}

// addSearch appends the substring filter: one bound argument matched against
// three columns, so the placeholder index is repeated rather than passed
// through add's single-placeholder form.
func (q *listQuery) addSearch(filter script.ListFilter) {
	if filter.Search == "" {
		return
	}
	q.args = append(q.args, "%"+filter.Search+"%")
	n := len(q.args)
	q.where = append(q.where, fmt.Sprintf(
		"(name ILIKE $%d OR display_name ILIKE $%d OR description ILIKE $%d)", n, n, n))
}

// buildListQuery assembles the filtered listing query and its arguments.
func buildListQuery(filter script.ListFilter) (query string, args []any) {
	q := &listQuery{}
	q.addEquality(filter)
	q.addVisibility(filter)
	q.addSearch(filter)

	query = scriptSelect
	if len(q.where) > 0 {
		query += " WHERE " + strings.Join(q.where, " AND ")
	}
	limit := filter.Limit
	if limit <= 0 || limit > defaultListLimit {
		limit = defaultListLimit
	}
	q.args = append(q.args, limit)
	return fmt.Sprintf("%s ORDER BY updated_at DESC LIMIT $%d", query, len(q.args)), q.args
}
