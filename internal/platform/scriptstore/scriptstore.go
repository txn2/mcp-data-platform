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
// scriptsTable is the one place the table's name is written. Every statement
// over it is built from this, so a count and a listing cannot disagree about
// which table they are reading (they did: #1795's first count named a
// `managed_scripts` that has never existed).
const scriptsTable = "scripts"

const scriptSelect = "SELECT " + scriptColumns + " FROM " + scriptsTable

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

// Delete removes a script by ID. Its versions, schedule, run history and
// carried state cascade.
//
// A delete that affects no row wraps script.ErrNotFound rather than reporting a
// generic failure: every caller reads the script before removing it, so the
// only way to reach this is somebody having removed it in between, and that is
// a not-found for the caller rather than the platform breaking.
//
// What cascaded is read inside the same transaction as the removal, because
// the surfaces report it to their caller (#1593) and only the transaction that
// performed the delete can say what was there when it ran. Probing the three
// stores separately beforehand would answer about a moment that is not the
// moment of the delete.
func (s *Store) Delete(ctx context.Context, id string) (script.Removed, error) {
	var rm script.Removed
	err := s.withTx(ctx, "delete script", func(tx *sql.Tx) error {
		var txErr error
		rm, txErr = deleteTx(ctx, tx, id)
		return txErr
	})
	if err != nil {
		return script.Removed{}, err
	}
	return rm, nil
}

// deleteTx locks the script, reads what hangs off it, and removes it.
//
// The lock is taken before the read and held to the commit. At READ COMMITTED
// each statement takes its own snapshot, so without it a child row inserted
// and committed between the read and the DELETE -- a schedule firing on
// another replica materializes a run -- would be cascaded away by a delete
// that reported no run history. A foreign key insert takes FOR KEY SHARE on
// the parent row, which FOR UPDATE conflicts with, so holding it is what makes
// the account true rather than merely likely.
//
// It is also where a script that is already gone is answered: the lock finds
// no row, which is the same not-found a second delete of one script gets.
func deleteTx(ctx context.Context, tx *sql.Tx, id string) (script.Removed, error) {
	var rm script.Removed
	var locked string
	err := tx.QueryRowContext(ctx, `SELECT id FROM scripts WHERE id = $1 FOR UPDATE`, id).Scan(&locked)
	if errors.Is(err, sql.ErrNoRows) {
		return rm, fmt.Errorf("delete script %s: %w", id, script.ErrNotFound)
	}
	if err != nil {
		return rm, fmt.Errorf("lock script for delete: %w", err)
	}
	// A state row holding {} is a script that carried nothing: a reset writes
	// the empty object rather than removing the row, and reporting that as
	// state the delete took would name what was not there.
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM script_schedules WHERE script_id = $1),
		       EXISTS (SELECT 1 FROM script_runs      WHERE script_id = $1),
		       EXISTS (SELECT 1 FROM script_state     WHERE script_id = $1
		                                                AND state <> '{}'::jsonb)`,
		id).Scan(&rm.Schedule, &rm.Runs, &rm.State); err != nil {
		return script.Removed{}, fmt.Errorf("read what a script delete cascades: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM scripts WHERE id = $1`, id); err != nil {
		return script.Removed{}, fmt.Errorf("delete script: %w", err)
	}
	return rm, nil
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

// Count returns how many scripts match the filter, ignoring its limit.
func (s *Store) Count(ctx context.Context, filter script.ListFilter) (int, error) {
	query, args := buildCountQuery(filter)
	var total int
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("count scripts: %w", err)
	}
	return total, nil
}

// CountScheduled returns how many matching scripts carry a cadence.
//
// A correlated EXISTS rather than a join: a script has at most one schedule,
// so a join could not multiply rows, but EXISTS says what is being asked and
// stops short on the first match.
func (s *Store) CountScheduled(ctx context.Context, filter script.ListFilter) (int, error) {
	query, args := buildScheduledCountQuery(filter)
	var total int
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("count scheduled scripts: %w", err)
	}
	return total, nil
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
	if filter.IDs != nil {
		q.add("id::text = ANY($%d)", pq.Array(filter.IDs))
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
	return fmt.Sprintf("%s %s LIMIT $%d", query, orderBy(filter), len(q.args)), q.args
}

// buildCountQuery counts every script the filter matches, ignoring its limit.
//
// It is the same predicate as the listing's, assembled by the same code, which
// is the point: a total assembled a second way is a total that can disagree
// with the rows it describes. The listing used to report len(rows) as its
// total, so a deployment past the page cap read its own cap back as the
// number of scripts it had, and nothing said the page was truncated (#1795).
func buildCountQuery(filter script.ListFilter) (query string, args []any) {
	q := &listQuery{}
	q.addEquality(filter)
	q.addSearch(filter)

	// The table is `scripts`, the same one scriptSelect reads. Naming it a
	// second time by hand is how the first version of this counted a
	// `managed_scripts` that does not exist -- and because the route treats a
	// failed count as "no better total available" and falls back to the page
	// length, that failure was silent and reinstated the very defect the
	// count exists to fix. It is derived from scriptSelect now, so the two
	// cannot name different tables.
	query = "SELECT COUNT(*) FROM " + scriptsTable
	if len(q.where) > 0 {
		query += " WHERE " + joinAnd(q.where)
	}
	return query, q.args
}

// buildScheduledCountQuery counts the matching scripts that have a schedule.
func buildScheduledCountQuery(filter script.ListFilter) (query string, args []any) {
	q := &listQuery{}
	q.addEquality(filter)
	q.addSearch(filter)

	q.where = append(q.where,
		"EXISTS (SELECT 1 FROM script_schedules WHERE script_schedules.script_id = "+
			scriptsTable+".id)")
	return "SELECT COUNT(*) FROM " + scriptsTable + " WHERE " + joinAnd(q.where), q.args
}

// orderBy renders the ORDER BY clause.
//
// The column is never interpolated from a caller's string: it comes from
// script.SortColumn, whose values are the package's own constants, and an
// unrecognized request has already been resolved to the default by
// ParseSortColumn. The direction is one of two literals.
//
// id breaks the tie. Ordering by a column with duplicates -- every script
// updated in the same migration, two scripts with the same display name --
// otherwise leaves the order within a tie up to the plan, so a listing read
// twice can return the same rows in a different order.
func orderBy(filter script.ListFilter) string {
	// No column asked for is the listing every caller got before ordering
	// existed: most recently updated first. Desc is not consulted in that
	// case, so a filter that names nothing cannot land on ascending by
	// leaving a bool at its zero value.
	if filter.Sort == "" {
		return "ORDER BY updated_at DESC, id DESC"
	}
	direction := "ASC"
	if filter.Desc {
		direction = "DESC"
	}
	return fmt.Sprintf("ORDER BY %s %s, id %s", string(filter.Sort), direction, direction)
}
