package resource

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
)

// Store persists and queries resource metadata.
type Store interface {
	Insert(ctx context.Context, r Resource) error
	Get(ctx context.Context, id string) (*Resource, error)
	// GetByIDs returns the resources among ids that exist, keyed by id. An id
	// with no row is simply absent: the caller is reading a set of references
	// and a missing one is an answer, not a failure.
	//
	// It is the read a listing over records that POINT AT resources needs, so
	// a page costs one query rather than one per row.
	GetByIDs(ctx context.Context, ids []string) (map[string]*Resource, error)
	GetByURI(ctx context.Context, uri string) (*Resource, error)
	List(ctx context.Context, filter Filter) ([]Resource, int, error)
	Update(ctx context.Context, id string, u Update) error
	// Move refiles resources, rewriting the four columns that say where each one
	// lives -- scope, scope_id, path and uri -- and recording the URI each used
	// to answer to so an already-written citation keeps resolving.
	//
	// It is separate from Update because it is not a metadata edit: it changes
	// who can see the file, it changes the resource's address, and the address
	// is UNIQUE, so it is the one write on this table that another resource can
	// refuse. A caller must be prepared for ErrURIConflict.
	//
	// It takes a batch because renaming a folder is one relocation per resource
	// beneath it and a half-renamed folder is not a state anyone should be able
	// to observe (#1529). Every element commits or none does, which is also what
	// makes the batch refusable as a whole.
	//
	// The blob is not touched. The S3 key embeds the scope only because
	// BuildS3Key composed it at creation; nothing re-derives it on read, so the
	// object stays where it is and the row keeps pointing at it.
	Move(ctx context.Context, moves []Move) error
	Delete(ctx context.Context, id string) error
}

// ErrURIConflict is returned by Move when the target library already holds a
// resource at the URI the moved resource would take. The caller names the
// collision from its own read; this is the store's report of the constraint
// the database enforced, which is what closes the gap between that read and
// the write.
var ErrURIConflict = errors.New("a resource already occupies that URI")

// IsNotFound reports whether an error from a Store read means the resource does
// not exist, as opposed to the read having failed.
//
// The Postgres store surfaces a missing row as a wrapped sql.ErrNoRows rather
// than as (nil, nil), so a caller that must distinguish "deleted" from "the
// database is down" cannot do it by nil-checking the result. Getting that
// distinction wrong is not cosmetic: a prompt attachment whose resource was
// deleted has to degrade to a flagged broken link, while a failed read has to
// fail closed.
func IsNotFound(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}

// S3Client abstracts blob storage operations for resources.
type S3Client interface {
	PutObject(ctx context.Context, bucket, key string, data []byte, contentType string) error
	GetObject(ctx context.Context, bucket, key string) (body []byte, contentType string, err error)
	DeleteObject(ctx context.Context, bucket, key string) error
}

// selectColumns is the column list every read shares, in the order resourceScan
// expects them.
const selectColumns = `id, scope, scope_id, path, filename, display_name, description,
		       mime_type, size_bytes, s3_key, uri, tags, uploader_sub, uploader_email,
		       created_at, updated_at, last_read_at`

// selectResource opens every read with that column list, so the projection is
// written once rather than at each of the five call sites.
const selectResource = `SELECT ` + selectColumns

const (
	// DefaultListLimit is used when no limit is specified in a list query.
	DefaultListLimit = 100
	// MaxListLimit caps a client-supplied page size so a single list request
	// cannot pull an unbounded window.
	MaxListLimit = 200
)

// --- PostgreSQL Store ---

type postgresStore struct {
	db *sql.DB
	// index receives a write-path index-job enqueue after a resource write
	// commits, so an uploaded or re-described resource enters ranked search in
	// roughly the time one embed takes rather than on the reconciler's next
	// sweep (#1256). Nil when no queue is wired; every call on it is nil-safe.
	index *indexjobs.Producer
}

// NewPostgresStore creates a resource store backed by PostgreSQL. Pass
// indexjobs.WithProducer to have resource writes enqueue their own index job;
// without it, resources are indexed on the reconciler's next sweep.
//
// Every notify fires after the write commits, never before: a job claimed while
// the row still holds its pre-write text (or its previous blob) would have the
// worker stamp that snapshot as current.
func NewPostgresStore(db *sql.DB, opts ...indexjobs.StoreOption) Store {
	return &postgresStore{db: db, index: indexjobs.ResolveStoreOptions(opts).Producer}
}

func (s *postgresStore) Insert(ctx context.Context, r Resource) error { //nolint:revive // interface impl
	query := `
		INSERT INTO resources
		(id, scope, scope_id, path, filename, display_name, description,
		 mime_type, size_bytes, s3_key, uri, tags, uploader_sub, uploader_email)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
	`
	scopeID := sql.NullString{String: r.ScopeID, Valid: r.ScopeID != ""}
	// Normalize nil tags to empty: pq.Array(nil) binds SQL NULL, which violates
	// the NOT NULL constraint on the tags TEXT[] column (the column DEFAULT '{}'
	// does not apply once the INSERT supplies an explicit value). A caller that
	// omits tags would otherwise fail with error 23502.
	if r.Tags == nil {
		r.Tags = []string{}
	}
	_, err := s.db.ExecContext(ctx, query,
		r.ID, string(r.Scope), scopeID, r.Path, r.Filename, r.DisplayName,
		r.Description, r.MIMEType, r.SizeBytes, r.S3Key, r.URI,
		pq.Array(r.Tags), r.UploaderSub, r.UploaderEmail,
	)
	if err != nil {
		return fmt.Errorf("inserting resource: %w", err)
	}
	s.index.NotifyWrite(ctx, r.ID)
	return nil
}

func (s *postgresStore) Get(ctx context.Context, id string) (*Resource, error) { //nolint:revive // interface impl
	query := selectResource + `
		FROM resources WHERE id = $1
	`
	return s.scanOne(s.db.QueryRowContext(ctx, query, id))
}

func (s *postgresStore) GetByIDs(ctx context.Context, ids []string) (map[string]*Resource, error) { //nolint:revive // interface impl
	if len(ids) == 0 {
		return map[string]*Resource{}, nil
	}
	query := selectResource + `
		FROM resources WHERE id = ANY($1)
	`
	rows, err := s.db.QueryContext(ctx, query, pq.Array(ids))
	if err != nil {
		return nil, fmt.Errorf("reading resources by id: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make(map[string]*Resource, len(ids))
	for rows.Next() {
		r, err := s.scanRow(rows)
		if err != nil {
			return nil, err
		}
		out[r.ID] = r
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating resources by id: %w", err)
	}
	return out, nil
}

// GetByURI resolves a resource by the address it is cited under, falling back to
// the addresses it used to answer to.
//
// A live URI always wins. The alias table records every URI a resource has
// vacated by being moved (#1502), and consulting it second is what keeps an
// alias from shadowing whichever resource occupies that address now -- a person
// who moves a file out of their library and uploads another under the same name
// must reach the new one by its own URI.
func (s *postgresStore) GetByURI(ctx context.Context, uri string) (*Resource, error) { //nolint:revive // interface impl
	query := selectResource + `
		FROM resources WHERE uri = $1
	`
	res, err := s.scanOne(s.db.QueryRowContext(ctx, query, uri))
	if err == nil || !IsNotFound(err) {
		return res, err
	}
	// The alias table is read as a subquery rather than joined. Both tables have
	// a uri column, so a join puts two of them in scope and PostgreSQL rejects
	// the unqualified selectColumns projection as ambiguous (#1506). Reading
	// from resources alone keeps that column list usable here, as it is in every
	// other read in this file.
	aliased := selectResource + `
		FROM resources
		WHERE id = (SELECT resource_id FROM resource_uri_aliases WHERE uri = $1)`
	return s.scanOne(s.db.QueryRowContext(ctx, aliased, uri))
}

// buildList renders the count and the page statements a listing runs, with the
// arguments the scope filter binds. The page statement takes two further
// arguments, the limit and offset listPageBounds returns, which are appended
// after the count has run.
//
// Both are assembled from the caller's scope filter and sort, so neither exists
// in the source; they are built here so a test can hand a representative
// rendering to a real PostgreSQL to parse and plan (#1512).
func buildList(filter Filter) (countQuery, selectQuery string, args []any) {
	where, args := buildScopeWhere(filter)
	countQuery = "SELECT COUNT(*) FROM resources WHERE " + where
	// #nosec G202 -- dynamic scope filter requires concatenation; the ORDER BY
	// comes from Sort.orderByClause, a closed set of constant strings.
	selectQuery = selectResource + `
		FROM resources WHERE ` + where + `
		ORDER BY ` + filter.Sort.orderByClause() + `
		LIMIT $` + fmt.Sprintf("%d", len(args)+1) + ` OFFSET $` + fmt.Sprintf("%d", len(args)+2)
	return countQuery, selectQuery, args
}

// listPageBounds returns the limit and offset the page statement binds, with
// the limit clamped into the listing bounds.
func listPageBounds(filter Filter) []any {
	limit := filter.Limit
	if limit <= 0 {
		limit = DefaultListLimit
	}
	if limit > MaxListLimit {
		limit = MaxListLimit
	}
	return []any{limit, filter.Offset}
}

func (s *postgresStore) List(ctx context.Context, filter Filter) ([]Resource, int, error) { //nolint:revive // interface impl
	if len(filter.Scopes) == 0 {
		return nil, 0, nil
	}

	countQuery, selectQuery, args := buildList(filter)

	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting resources: %w", err)
	}
	if total == 0 {
		return nil, 0, nil
	}
	args = append(args, listPageBounds(filter)...)

	rows, err := s.db.QueryContext(ctx, selectQuery, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("listing resources: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var resources []Resource
	for rows.Next() {
		r, err := s.scanRow(rows)
		if err != nil {
			return nil, 0, err
		}
		resources = append(resources, *r)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterating resource rows: %w", err)
	}
	return resources, total, nil
}

// buildUpdate renders the UPDATE for a metadata edit and its arguments. The SET
// list is assembled from whichever fields the caller supplied, so the statement
// exists only at run time; it is a function so a test can hand a representative
// rendering to a real PostgreSQL to parse and plan (#1512).
//
// Every mutable field (display name, description, tags) is part of the indexed
// text, so a metadata edit invalidates the stored vector. Clearing
// the embedding columns here makes the row a gap, which Update hands straight
// to the index worker instead of waiting for a reconciler sweep, exactly as the
// portal asset store does (#1012, #1256); leaving them would rank the resource
// on its pre-edit text forever.
func buildUpdate(id string, u Update) (query string, args []any) {
	setClauses := []string{"updated_at = $1", "embedding = NULL", "embedding_model = ''", "embedding_text_hash = NULL"}
	args = []any{time.Now().UTC()}
	idx := 2

	if u.DisplayName != nil {
		setClauses = append(setClauses, fmt.Sprintf("display_name = $%d", idx))
		args = append(args, *u.DisplayName)
		idx++
	}
	if u.Description != nil {
		setClauses = append(setClauses, fmt.Sprintf("description = $%d", idx))
		args = append(args, *u.Description)
		idx++
	}
	if u.Tags != nil {
		setClauses = append(setClauses, fmt.Sprintf("tags = $%d", idx))
		args = append(args, pq.Array(u.Tags))
		idx++
	}
	query = fmt.Sprintf("UPDATE resources SET %s WHERE id = $%d", // #nosec G201 -- dynamic SET clause with parameterized values
		strings.Join(setClauses, ", "), idx)
	return query, append(args, id)
}

func (s *postgresStore) Update(ctx context.Context, id string, u Update) error { //nolint:revive // interface impl
	query, args := buildUpdate(id, u)

	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("updating resource: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("resource not found: %s", id)
	}
	s.index.NotifyWrite(ctx, id)
	return nil
}

// Move rewrites where resources are filed and records the addresses they leave.
//
// Every write is one transaction. A row's new address and the alias for its old
// one are the same fact stated twice: a commit that carried only the first would
// leave every citation of the old URI dangling, and one that carried only the
// second would advertise an address the resource does not have. A folder rename
// is many of those pairs and takes the same transaction, because a half-renamed
// folder is not a state anyone should be able to observe.
//
// Nothing happens for an empty batch; the caller checks first whether the
// destination is where the resource already is, and this is the second door on
// it.
func (s *postgresStore) Move(ctx context.Context, moves []Move) error { //nolint:revive // interface impl
	if len(moves) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning move: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := parkAddresses(ctx, tx, moves); err != nil {
		return err
	}
	for _, m := range moves {
		if err := applyMove(ctx, tx, m); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		if isUniqueViolation(err) {
			return ErrURIConflict
		}
		return fmt.Errorf("committing move: %w", err)
	}
	// The folder path is part of IndexText, so a resource that changed folders
	// is ranked on text that is now stale; applyMove drops its vector, and this
	// hands the row to the index worker rather than waiting for a reconciler
	// sweep. A library-only move changed no indexed text, and the worker dedups
	// by text hash, so the notify costs a hash compare and no embed call.
	for _, m := range moves {
		s.index.NotifyWrite(ctx, m.ID)
	}
	return nil
}

// parkAddresses moves every row in a multi-row batch off its current URI before
// any of them takes a new one.
//
// Renaming a folder up its own tree hands one resource an address another
// resource in the same batch is still holding: renaming a/b to a turns
// a/b/x.csv into a/x.csv and a/b/b/x.csv into a/b/x.csv, and whichever of those
// two is written first collides with the row that has not moved yet. The
// collision is transient and the UNIQUE constraint is not deferrable, so the
// batch vacates every address first and the ordering stops mattering.
//
// The parked value is derived from the row's own primary key, so it is unique,
// and it carries a unit separator, which no resource URI contains: a scheme, a
// library and validated path segments have no control characters in them. A
// single-row batch is left alone -- one row cannot collide with itself, and the
// extra statement would be paid on every ordinary move.
func parkAddresses(ctx context.Context, tx *sql.Tx, moves []Move) error {
	if len(moves) < 2 {
		return nil
	}
	ids := make([]string, 0, len(moves))
	for _, m := range moves {
		ids = append(ids, m.ID)
	}
	const park = `UPDATE resources SET uri = $1 || id WHERE id = ANY($2)`
	if _, err := tx.ExecContext(ctx, park, parkedURIPrefix, pq.Array(ids)); err != nil {
		return fmt.Errorf("vacating resource addresses: %w", err)
	}
	return nil
}

// parkedURIPrefix marks an address held only for the duration of a multi-row
// move. See parkAddresses.
const parkedURIPrefix = "\x1frelocating:"

// applyMove writes one relocation and its aliases inside the batch transaction.
//
// The embedding is dropped only when the folder path actually changes. The path
// is part of IndexText and the library is not, so clearing it on a library-only
// move would re-embed a row whose indexed text is identical. The comparison is
// made by the database against the row's stored value, which is the only place
// that knows what the path was.
func applyMove(ctx context.Context, tx *sql.Tx, m Move) error {
	scopeID := sql.NullString{String: m.ScopeID, Valid: m.ScopeID != ""}
	const update = `
		UPDATE resources SET
			scope = $2, scope_id = $3, path = $4, uri = $5, updated_at = $6,
			embedding           = CASE WHEN path = $4 THEN embedding           ELSE NULL END,
			embedding_model     = CASE WHEN path = $4 THEN embedding_model     ELSE '' END,
			embedding_text_hash = CASE WHEN path = $4 THEN embedding_text_hash ELSE NULL END
		WHERE id = $1`
	res, err := tx.ExecContext(ctx, update, m.ID, string(m.Scope), scopeID, m.Path, m.URI, time.Now().UTC())
	if err != nil {
		if isUniqueViolation(err) {
			return ErrURIConflict
		}
		return fmt.Errorf("moving resource: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("resource not found: %s", m.ID)
	}
	return readdressAliases(ctx, tx, m)
}

// readdressAliases records the address a move vacates and clears the one it
// takes, inside the move's own transaction.
//
// The vacated address becomes an alias so an already-written citation keeps
// resolving; ON CONFLICT hands it to the resource that vacated it most recently,
// which is the only reading available once two resources have both left the same
// address. The address the resource now holds stops being an alias of anything:
// moving a file back to where it came from would otherwise leave a row claiming
// the resource's own current URI, which GetByURI never reaches and a later move
// would resurrect as a stale pointer.
func readdressAliases(ctx context.Context, tx *sql.Tx, m Move) error {
	if m.FromURI != "" && m.FromURI != m.URI {
		const alias = `
			INSERT INTO resource_uri_aliases (uri, resource_id) VALUES ($1, $2)
			ON CONFLICT (uri) DO UPDATE SET resource_id = EXCLUDED.resource_id, created_at = NOW()`
		if _, err := tx.ExecContext(ctx, alias, m.FromURI, m.ID); err != nil {
			return fmt.Errorf("recording previous resource uri: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM resource_uri_aliases WHERE uri = $1`, m.URI); err != nil {
		return fmt.Errorf("clearing resource uri alias: %w", err)
	}
	return nil
}

// isUniqueViolation reports whether a database error is a unique-constraint
// rejection. The resources table has exactly one such constraint, on uri, so a
// violation from the move path is always the address being taken.
func isUniqueViolation(err error) bool {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		return pqErr.Code == uniqueViolationCode
	}
	// A driver that does not surface a *pq.Error still reports the constraint
	// in its message; CreateResource already reads it that way on insert.
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate key") || strings.Contains(msg, "unique constraint")
}

// uniqueViolationCode is PostgreSQL's SQLSTATE for a unique-constraint
// violation. Untyped so it compares against pq.Error.Code without naming the
// deprecated pq.ErrorCode.
const uniqueViolationCode = "23505"

func (s *postgresStore) Delete(ctx context.Context, id string) error { //nolint:revive // interface impl
	res, err := s.db.ExecContext(ctx, "DELETE FROM resources WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("deleting resource: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("resource not found: %s", id)
	}
	return nil
}

// --- helpers ---

// resourceScan holds the landing spots for the nullable columns of a resource
// row. One value carries them all so adding a nullable column touches this type
// and nothing else at the call sites.
type resourceScan struct {
	scopeID  sql.NullString
	tags     []string
	lastRead sql.NullTime
}

// dest returns the scan destinations for a resource row, in the column order
// every resource SELECT projects (the list path, the by-id/by-uri reads, and the
// ranked search). Declared once so a column added to one query cannot silently
// misalign another's scan. The ranked-search callers append their score columns
// to the returned slice.
func (s *resourceScan) dest(r *Resource) []any {
	return []any{
		&r.ID, &r.Scope, &s.scopeID, &r.Path, &r.Filename, &r.DisplayName,
		&r.Description, &r.MIMEType, &r.SizeBytes, &r.S3Key, &r.URI,
		pq.Array(&s.tags), &r.UploaderSub, &r.UploaderEmail,
		&r.CreatedAt, &r.UpdatedAt, &s.lastRead,
	}
}

// finish applies the nullable-column conventions after a scan: a NULL scope_id
// reads as the empty string (global resources), nil tags read as an empty slice
// so the JSON encoding is [] rather than null, and a NULL last_read_at leaves
// the pointer nil, which is how "never read" is distinguished from "read at the
// zero time".
func (s *resourceScan) finish(r *Resource) {
	r.ScopeID = s.scopeID.String
	if s.tags != nil {
		r.Tags = s.tags
	} else {
		r.Tags = []string{}
	}
	if s.lastRead.Valid {
		t := s.lastRead.Time
		r.LastReadAt = &t
	}
}

func (*postgresStore) scanOne(row *sql.Row) (*Resource, error) { //nolint:revive // interface-adjacent helper
	var r Resource
	var sc resourceScan
	if err := row.Scan(sc.dest(&r)...); err != nil {
		return nil, fmt.Errorf("scanning resource: %w", err)
	}
	sc.finish(&r)
	return &r, nil
}

func (*postgresStore) scanRow(rows *sql.Rows) (*Resource, error) { //nolint:revive // interface-adjacent helper
	var r Resource
	var sc resourceScan
	if err := rows.Scan(sc.dest(&r)...); err != nil {
		return nil, fmt.Errorf("scanning resource row: %w", err)
	}
	sc.finish(&r)
	return &r, nil
}

// scopeVisibilityWhere builds the parenthesized OR of scope-visibility
// conditions for the given scopes, binding placeholders from startIdx. It
// returns the clause, its arguments, and the next free placeholder index, so
// both the list path (placeholders from $1) and the ranked search path (which
// binds the query vector and text first) derive the same visibility predicate
// from one implementation.
func scopeVisibilityWhere(scopes []ScopeFilter, startIdx int) (where string, args []any, next int) {
	conds := make([]string, 0, len(scopes))
	idx := startIdx
	for _, sf := range scopes {
		if sf.Scope == ScopeGlobal {
			conds = append(conds, fmt.Sprintf("(scope = $%d AND scope_id IS NULL)", idx))
			args = append(args, string(ScopeGlobal))
			idx++
			continue
		}
		conds = append(conds, fmt.Sprintf("(scope = $%d AND scope_id = $%d)", idx, idx+1))
		args = append(args, string(sf.Scope), sf.ScopeID)
		idx += 2
	}
	return "(" + strings.Join(conds, " OR ") + ")", args, idx
}

// likePrefix escapes a value so it matches literally inside a LIKE pattern. A
// folder name cannot contain % or _ today, but the pattern is built from a
// caller-supplied path and a filter that silently widened on a wildcard would
// list resources from folders the person did not open.
func likePrefix(v string) string {
	return strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(v)
}

// buildScopeWhere builds a WHERE clause for scope visibility filtering,
// plus optional path, tag, and text search filters.
func buildScopeWhere(filter Filter) (where string, args []any) {
	where, args, idx := scopeVisibilityWhere(filter.Scopes, 1)

	if filter.Path != "" {
		// The folder itself and everything beneath it. Two bindings rather than
		// one so the LIKE pattern is built here instead of by the caller, and so
		// the equality arm can use the index on path without the planner having
		// to reason about a pattern that happens to have no wildcard.
		where += fmt.Sprintf(" AND (path = $%d OR path LIKE $%d)", idx, idx+1)
		args = append(args, filter.Path, likePrefix(filter.Path)+"/%")
		idx += 2
	}
	if filter.Tag != "" {
		where += fmt.Sprintf(" AND $%d = ANY(tags)", idx)
		args = append(args, filter.Tag)
		idx++
	}
	if filter.Query != "" {
		escaped := strings.NewReplacer("%", "\\%", "_", "\\_").Replace(filter.Query)
		pattern := "%" + escaped + "%"
		where += fmt.Sprintf(" AND (display_name ILIKE $%d OR description ILIKE $%d)", idx, idx+1)
		args = append(args, pattern, pattern)
	}

	return where, args
}

// Verify interface compliance.
var _ Store = (*postgresStore)(nil)
