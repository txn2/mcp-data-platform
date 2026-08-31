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
	// Folders returns every folder holding a resource the filter admits, with
	// the exact number filed under each at every depth.
	//
	// It exists because a folder is derived from the paths in use rather than
	// stored, and the portal used to derive it in the browser from the paged
	// listing: drawing four folder names cost a fetch of the whole library, the
	// counts read "25+" until the last page arrived, and the root offered a
	// Load-more control over rows it never displayed (#1555). One grouped query
	// answers it exactly and in one round trip.
	Folders(ctx context.Context, filter Filter) ([]Folder, error)
	// SetThumbnail records a capture: the object it was stored under and the
	// moment it was taken.
	//
	// It is not an Update. Update bumps updated_at and drops the stored
	// embedding, and a capture must do neither: bumping the timestamp would
	// mark the capture that just landed as older than the row it came from,
	// which is the definition of pending, so every capture would queue itself
	// again forever (#1554).
	SetThumbnail(ctx context.Context, id string, t ThumbnailCapture) error
	// ClearThumbnail forgets a capture, which is how a wrong tile is asked to
	// be taken again.
	ClearThumbnail(ctx context.Context, id, variant string) error
	// PendingThumbnails lists resources whose capture is missing or older than
	// the file it came from, most recently changed first, capped at limit.
	PendingThumbnails(ctx context.Context, filter Filter, limit int) ([]Resource, error)
	// Tags returns the distinct tags carried by the resources the filter
	// admits, so the tag facet offers what the library holds rather than what
	// one page of it happened to carry.
	Tags(ctx context.Context, filter Filter) ([]string, error)
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
		       created_at, updated_at, last_read_at,
		       thumbnail_s3_key, thumbnail_dark_s3_key,
		       thumbnail_captured_at, thumbnail_dark_captured_at`

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
	// No scopes and no unrestricted listing is a caller who named a library
	// they may not read: no rows, and no statement, since an empty scope set
	// builds no predicate to run.
	if len(filter.Scopes) == 0 && !filter.AllScopes {
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

// MaxThumbnailSourceBytes is the largest resource a capture is attempted from.
//
// Capture renders the document a second time and rasterizes it on the main
// thread, so its cost tracks the file. It is the same cap the asset queue
// applies, and the outliers it excludes are exactly the ones that stall the tab
// doing the work.
const MaxThumbnailSourceBytes = 1 << 20 // 1 MB

// thumbnailCapturableTypes are the content families a browser can rasterize
// into a tile, matched as fragments of the media type so every spelling of a
// family is covered. Images are here because a capture DOWNSCALES them: the
// tile used to be the original object, so an image cost its full size to draw
// and anything past the cutoff drew nothing (#1554).
//
// Everything else -- PDF, spreadsheets, archives, binaries -- has no renderer,
// keeps its content-type icon, and is never offered.
var thumbnailCapturableTypes = []string{"html", "svg", "markdown", "csv", "json", "image/"}

// thumbnailThemeableTypes are the families rendered on a forced background and
// so needing a second capture for dark mode. HTML, SVG and a raster image carry
// their own colors and store a single image.
var thumbnailThemeableTypes = []string{"markdown", "csv", "json"}

// ThumbnailVariantLight and ThumbnailVariantDark name the two captures a
// resource can carry. A content type that brings its own colors stores only
// the light one and serves it in both modes.
const (
	ThumbnailVariantLight = "light"
	ThumbnailVariantDark  = "dark"
)

// ThumbnailCapture is one stored capture: which of the two it is, the object it
// was written to, and when it was taken.
type ThumbnailCapture struct {
	Variant    string
	S3Key      string
	CapturedAt time.Time
}

// thumbnailColumns names the pair a variant writes.
func thumbnailColumns(variant string) (keyCol, atCol string) {
	if variant == ThumbnailVariantDark {
		return "thumbnail_dark_s3_key", "thumbnail_dark_captured_at"
	}
	return "thumbnail_s3_key", "thumbnail_captured_at"
}

// SetThumbnail records a capture against the resource.
func (s *postgresStore) SetThumbnail(ctx context.Context, id string, t ThumbnailCapture) error { //nolint:revive // interface impl
	keyCol, atCol := thumbnailColumns(t.Variant)
	// #nosec G201 -- the column names come from thumbnailColumns, a closed set
	// of constants; the values are bound.
	query := fmt.Sprintf("UPDATE resources SET %s = $1, %s = $2 WHERE id = $3", keyCol, atCol)
	res, err := s.db.ExecContext(ctx, query, t.S3Key, t.CapturedAt, id)
	if err != nil {
		return fmt.Errorf("recording thumbnail: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("resource not found: %s", id)
	}
	return nil
}

// ClearThumbnail forgets a capture, leaving the resource pending again.
func (s *postgresStore) ClearThumbnail(ctx context.Context, id, variant string) error { //nolint:revive // interface impl
	keyCol, atCol := thumbnailColumns(variant)
	// #nosec G201 -- closed set of column names, as above.
	query := fmt.Sprintf("UPDATE resources SET %s = '', %s = NULL WHERE id = $1", keyCol, atCol)
	res, err := s.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("clearing thumbnail: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("resource not found: %s", id)
	}
	return nil
}

// buildPendingThumbnails renders the statement PendingThumbnails runs.
//
// Pending is: no capture recorded, or a capture older than the row it came
// from. The dark variant is asked only of the types that carry one -- a file
// that brings its own colors stores a single image and serves it in both
// modes, so reading its empty dark key as pending would offer it forever, which
// is the mistake the asset predicate documents at length.
//
// The size cap is the same one the asset queue applies: capture renders the
// document a second time and rasterizes it on the main thread, so the cost
// tracks the file, and the outliers are exactly the ones that stall the tab
// they are captured in.
func buildPendingThumbnails(filter Filter, limit int) (query string, args []any) {
	where, args := buildScopeWhere(filter)
	idx := len(args) + 1

	typePatterns := make([]string, 0, len(thumbnailCapturableTypes))
	for _, fragment := range thumbnailCapturableTypes {
		typePatterns = append(typePatterns, "%"+fragment+"%")
	}
	themeablePatterns := make([]string, 0, len(thumbnailThemeableTypes))
	for _, fragment := range thumbnailThemeableTypes {
		themeablePatterns = append(themeablePatterns, "%"+fragment+"%")
	}

	// #nosec G201 -- the only interpolation is the scope predicate and the
	// placeholder numbering; every value is bound.
	query = fmt.Sprintf(`
		%s FROM resources
		WHERE %s
		  AND mime_type ILIKE ANY($%d)
		  AND size_bytes <= $%d
		  AND (
		        thumbnail_s3_key = ''
		     OR thumbnail_captured_at IS NULL
		     OR thumbnail_captured_at < updated_at
		     OR (
		          mime_type ILIKE ANY($%d)
		          AND (
		                thumbnail_dark_s3_key = ''
		             OR thumbnail_dark_captured_at IS NULL
		             OR thumbnail_dark_captured_at < updated_at
		          )
		        )
		  )
		ORDER BY updated_at DESC
		LIMIT $%d`, selectResource, where, idx, idx+1, idx+2, idx+3)

	args = append(args, pq.Array(typePatterns), MaxThumbnailSourceBytes, pq.Array(themeablePatterns), limit)
	return query, args
}

// PendingThumbnails lists the resources whose capture is missing or behind.
func (s *postgresStore) PendingThumbnails(ctx context.Context, filter Filter, limit int) ([]Resource, error) { //nolint:revive // interface impl
	if len(filter.Scopes) == 0 && !filter.AllScopes {
		return nil, nil
	}
	if limit <= 0 {
		limit = DefaultListLimit
	}

	query, args := buildPendingThumbnails(filter, limit)
	// #nosec G701 -- the statement is assembled by buildPendingThumbnails from
	// constants and placeholder numbers alone: the projection is selectResource,
	// the column names and the type fragments are package constants, and every
	// caller-supplied value -- the scopes, the patterns, the size cap, the limit
	// -- is bound as a parameter rather than written into the text.
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing pending thumbnails: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Resource
	for rows.Next() {
		r, err := s.scanRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating pending thumbnail rows: %w", err)
	}
	return out, nil
}

// buildFolders renders the statement Folders runs, with the arguments its
// visibility predicate binds.
//
// A folder's count is everything beneath it at EVERY depth, so a resource at
// "a/b/c" counts toward "a", "a/b" and "a/b/c". That is what the lateral
// expansion does: each row is turned into its own chain of ancestor paths, and
// the chain is what is grouped. Doing it in SQL rather than over a fetched page
// is the whole point -- the browser used to derive this from the listing and
// could only ever report what had arrived (#1555).
//
// It is assembled here rather than written as a constant because the visibility
// predicate is built from the caller's scopes, so a test hands a representative
// rendering to a real PostgreSQL to parse and plan (#1512).
func buildFolders(filter Filter) (query string, args []any) {
	where, args := buildScopeWhere(filter)
	// generate_subscripts walks the path's segments; array_to_string rebuilds
	// the prefix ending at each one. A path is validated to at most 8 segments,
	// so the expansion is bounded.
	// #nosec G202 -- the only interpolation is the scope predicate above, whose
	// values are bound as parameters.
	query = `
		SELECT chain.folder, COUNT(*) AS count
		FROM resources r
		CROSS JOIN LATERAL (
			SELECT array_to_string(parts[1:i], '/') AS folder
			FROM (SELECT string_to_array(r.path, '/') AS parts) AS p,
			     generate_subscripts(p.parts, 1) AS i
		) AS chain
		WHERE ` + where + `
		GROUP BY chain.folder
		ORDER BY chain.folder`
	return query, args
}

// Folders returns every folder the filter admits, with the exact number of
// resources filed under each at every depth.
func (s *postgresStore) Folders(ctx context.Context, filter Filter) ([]Folder, error) { //nolint:revive // interface impl
	// The same guard List applies: no scopes and no unrestricted listing is a
	// caller who named a library they may not read, which has no folders.
	if len(filter.Scopes) == 0 && !filter.AllScopes {
		return nil, nil
	}

	query, args := buildFolders(filter)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing folders: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var folders []Folder
	for rows.Next() {
		var f Folder
		if err := rows.Scan(&f.Path, &f.Count); err != nil {
			return nil, fmt.Errorf("scanning folder row: %w", err)
		}
		// A resource with an empty path has no folder to report. Validation
		// refuses one, so this is a guard against a row that predates it
		// rather than an expected shape.
		if f.Path == "" {
			continue
		}
		folders = append(folders, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating folder rows: %w", err)
	}
	return folders, nil
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
	scopeID     sql.NullString
	tags        []string
	lastRead    sql.NullTime
	captured    sql.NullTime
	darkCapture sql.NullTime
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
		&r.ThumbnailS3Key, &r.ThumbnailDarkS3Key, &s.captured, &s.darkCapture,
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
	// A NULL capture time leaves the pointer nil, which is how "never captured"
	// is told apart from a capture taken at the zero time (#1554).
	if s.captured.Valid {
		t := s.captured.Time
		r.ThumbnailCapturedAt = &t
	}
	if s.darkCapture.Valid {
		t := s.darkCapture.Time
		r.ThumbnailDarkCapturedAt = &t
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

// unrestrictedVisibility is the visibility clause of a listing that spans every
// library: a constant the planner folds away, rather than a predicate built
// from a scope set. Only a platform administrator's unnarrowed listing reaches
// it (see ListScopes).
const unrestrictedVisibility = "TRUE"

// listVisibilityWhere is the visibility clause a listing runs under: the
// caller's scopes, or the constant TRUE for a listing that spans every library.
//
// Separate from scopeVisibilityWhere because only the listing path can be
// unrestricted. The ranked search shares the scope predicate and is deliberately
// membership-scoped whoever asks: an administrator's authority over a management
// surface is not a widening of what an agent reads (see ListScopes).
func listVisibilityWhere(filter Filter) (where string, args []any, next int) {
	if filter.AllScopes {
		return unrestrictedVisibility, nil, 1
	}
	return scopeVisibilityWhere(filter.Scopes, 1)
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
	where, args, idx := listVisibilityWhere(filter)

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

// buildTags renders the statement Tags runs. The tags column is a text array,
// so the rollup unnests it and takes the distinct values.
func buildTags(filter Filter) (query string, args []any) {
	where, args := buildScopeWhere(filter)
	// #nosec G202 -- the only interpolation is the scope predicate above, whose
	// values are bound as parameters.
	query = `
		SELECT DISTINCT tag
		FROM resources r
		CROSS JOIN LATERAL unnest(r.tags) AS tag
		WHERE ` + where + `
		ORDER BY tag`
	return query, args
}

// Tags returns the distinct tags carried by the resources the filter admits.
func (s *postgresStore) Tags(ctx context.Context, filter Filter) ([]string, error) { //nolint:revive // interface impl
	if len(filter.Scopes) == 0 && !filter.AllScopes {
		return nil, nil
	}

	query, args := buildTags(filter)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing tags: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var tags []string
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			return nil, fmt.Errorf("scanning tag row: %w", err)
		}
		tags = append(tags, tag)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating tag rows: %w", err)
	}
	return tags, nil
}

// Verify interface compliance.
var _ Store = (*postgresStore)(nil)
