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

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
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
	// index receives a write-path enqueue after a committed write that moved
	// the text the scripts index is built from, so a created or re-described
	// script enters ranked search in roughly the time one embed takes rather
	// than waiting for the reconciler's next sweep (#1370). Nil on a store
	// built without a queue, which every Producer method tolerates.
	index *indexjobs.Producer
}

// New creates a PostgreSQL script store over db. Pass indexjobs.WithProducer to
// bind the write-path index-job producer; without it the write path enqueues
// nothing and the reconciler is the only route to the index.
func New(db *sql.DB, opts ...indexjobs.StoreOption) *Store {
	return &Store{db: db, index: indexjobs.ResolveStoreOptions(opts).Producer}
}

// scriptColumns is the column list read by every scripts SELECT, kept in one
// place so the scan order in scanScript cannot drift from the query.
const scriptColumns = `id, name, display_name, description, category, source_code, params,
	owner_email, tags, enabled, status, superseded_by,
	deprecated_at, version, created_at, updated_at`

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
	err := sc.Scan(&s.ID, &s.Name, &s.DisplayName, &s.Description, &s.Category, &s.Source, &paramsJSON,
		&s.OwnerEmail, pq.Array(&s.Tags), &s.Enabled,
		&s.Status, &s.SupersededBy, &s.DeprecatedAt, &s.Version,
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
// constraint on tags.
func normalizeSlices(s *script.Script) {
	if s.Params == nil {
		s.Params = []script.Param{}
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
func (s *Store) Create(ctx context.Context, sc *script.Script, author script.Author) error {
	normalizeSlices(sc)
	paramsJSON, err := json.Marshal(sc.Params)
	if err != nil {
		return fmt.Errorf("marshal script params: %w", err)
	}
	if sc.Status == "" {
		sc.Status = script.StatusActive
	}
	sc.Version = 1
	if err := s.withTx(ctx, "create script", func(tx *sql.Tx) error {
		row := tx.QueryRowContext(ctx, `
			INSERT INTO scripts (name, display_name, description, category, source_code, params,
			                     owner_email, tags, enabled, status, version)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 1)
			RETURNING id, created_at, updated_at`,
			sc.Name, sc.DisplayName, sc.Description, sc.Category, sc.Source, paramsJSON,
			sc.OwnerEmail, pq.Array(sc.Tags), sc.Enabled, sc.Status)
		if err := row.Scan(&sc.ID, &sc.CreatedAt, &sc.UpdatedAt); err != nil {
			return fmt.Errorf("insert script: %w", err)
		}
		return insertVersionRow(ctx, tx, versionInsert{
			ScriptID: sc.ID, Version: 1, Snapshot: sc,
			Author: author, Status: script.VersionStatusApplied,
		})
	}); err != nil {
		return err
	}
	s.index.NotifyWrite(ctx, sc.ID)
	return nil
}

// GetByName retrieves one owner's script by name. An empty owner matches
// nothing: the ownerless rows a transfer exists to adopt are addressable by id
// alone, never by a name lookup an unidentified caller could make.
func (s *Store) GetByName(ctx context.Context, ownerEmail, name string) (*script.Script, error) {
	if ownerEmail == "" {
		return nil, nil //nolint:nilnil // Store contract: nil, nil means not found
	}
	return s.getOne(ctx,
		scriptSelect+` WHERE name = $1 AND owner_email = $2`, name, ownerEmail)
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
	var indexed bool
	if err := s.withTx(ctx, "update script", func(tx *sql.Tx) error {
		var err error
		indexed, err = updateTx(ctx, tx, sc)
		return err
	}); err != nil {
		return err
	}
	if indexed {
		s.index.NotifyWrite(ctx, sc.ID)
	}
	return nil
}

// indexInvalidation is the SET fragment every write of the live script row
// carries: it drops the stored vector whenever the row's recorded text hash no
// longer matches the hash of the text the write leaves behind, so an edit never
// leaves a stale embedding ranking against a description the script no longer
// has. A metadata-only write (an owner change, a source edit — the source is not
// indexed) matches the stored hash and preserves the vector, which is what keeps
// the corpus from re-embedding itself for changes that do not alter what the
// script is for.
//
// $%[1]d is the caller's hash placeholder. The hash is indexjobs.TextHash over
// script.IndexText, the exact value the worker stores, so the two definitions
// cannot diverge.
const indexInvalidation = `,
		       embedding           = CASE WHEN embedding_text_hash IS DISTINCT FROM $%[1]d
		                                  THEN NULL ELSE embedding END,
		       embedding_model     = CASE WHEN embedding_text_hash IS DISTINCT FROM $%[1]d
		                                  THEN '' ELSE embedding_model END,
		       embedding_text_hash = CASE WHEN embedding_text_hash IS DISTINCT FROM $%[1]d
		                                  THEN NULL ELSE embedding_text_hash END`

// indexTextChanged is the RETURNING expression that reports whether the write
// just invalidated the vector. It reads the POST-update hash: a write that
// cleared the column leaves NULL, which is distinct from the non-null new hash,
// while a write that preserved it leaves exactly that hash. So it is true
// precisely when the indexed text moved, which is when the caller owes the
// queue a job.
//
// One case reports true without the text having moved: a row that was never
// embedded holds a NULL hash both before and after any write, so a metadata-only
// edit of an unembedded script enqueues a job. That is the right answer for the
// wrong reason and is left as is — the row IS a gap the queue owes, so the job
// has work to do rather than being a wasted wake-up.
const indexTextChanged = `
		 RETURNING embedding_text_hash IS DISTINCT FROM $%[1]d`

// updateTx writes the live script row within the caller's transaction,
// reporting whether the write moved the text the scripts index is built from.
func updateTx(ctx context.Context, tx *sql.Tx, sc *script.Script) (bool, error) {
	paramsJSON, err := json.Marshal(sc.Params)
	if err != nil {
		return false, fmt.Errorf("marshal script params: %w", err)
	}
	// #nosec G201 -- the only interpolation is a constant parameter index into
	// constant SQL fragments; every value is bound.
	q := `
		UPDATE scripts
		   SET name = $2, display_name = $3, description = $4, category = $5,
		       source_code = $6, params = $7,
		       owner_email = $8, tags = $9, enabled = $10, status = $11,
		       superseded_by = $12, deprecated_at = $13, version = $14,
		       updated_at = NOW()` +
		fmt.Sprintf(indexInvalidation, updateHashParam) +
		"\n\t\t WHERE id = $1" +
		fmt.Sprintf(indexTextChanged, updateHashParam)
	var changed bool
	err = tx.QueryRowContext(ctx, q,
		sc.ID, sc.Name, sc.DisplayName, sc.Description, sc.Category, sc.Source, paramsJSON,
		sc.OwnerEmail, pq.Array(sc.Tags),
		sc.Enabled, sc.Status, sc.SupersededBy, sc.DeprecatedAt, sc.Version,
		indexjobs.TextHash(script.IndexText(sc))).Scan(&changed)
	if errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("script %s not found", sc.ID)
	}
	if err != nil {
		return false, fmt.Errorf("update script: %w", err)
	}
	return changed, nil
}

// updateHashParam is updateTx's placeholder index for the new text hash, one
// past its last column value.
const updateHashParam = 15

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
	if filter.OwnerEmail != "" {
		q.add("owner_email = $%d", filter.OwnerEmail)
	}
	if filter.Enabled != nil {
		q.add("enabled = $%d", *filter.Enabled)
	}
	if filter.Status != "" {
		q.add("status = $%d", filter.Status)
	}
	if filter.Category != "" {
		q.add("category = $%d", filter.Category)
	}
	if len(filter.Tags) > 0 {
		// Overlap rather than containment: naming two tags asks for the scripts
		// carrying either, which is the union of two shelves, over a
		// GIN-indexable operator.
		q.add("tags && $%d", pq.Array(filter.Tags))
	}
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

// joinAnd renders accumulated WHERE clauses as one conjunction.
func joinAnd(where []string) string { return strings.Join(where, " AND ") }

// buildListQuery assembles the filtered listing query and its arguments.
func buildListQuery(filter script.ListFilter) (query string, args []any) {
	q := &listQuery{}
	q.addEquality(filter)
	q.addSearch(filter)

	query = scriptSelect
	if len(q.where) > 0 {
		query += " WHERE " + joinAnd(q.where)
	}
	limit := filter.Limit
	if limit <= 0 || limit > defaultListLimit {
		limit = defaultListLimit
	}
	q.args = append(q.args, limit)
	return fmt.Sprintf("%s ORDER BY updated_at DESC LIMIT $%d", query, len(q.args)), q.args
}
