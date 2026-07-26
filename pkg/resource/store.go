package resource

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
)

// Store persists and queries resource metadata.
type Store interface {
	Insert(ctx context.Context, r Resource) error
	Get(ctx context.Context, id string) (*Resource, error)
	GetByURI(ctx context.Context, uri string) (*Resource, error)
	List(ctx context.Context, filter Filter) ([]Resource, int, error)
	Update(ctx context.Context, id string, u Update) error
	Delete(ctx context.Context, id string) error
}

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
}

// NewPostgresStore creates a resource store backed by PostgreSQL.
func NewPostgresStore(db *sql.DB) Store {
	return &postgresStore{db: db}
}

func (s *postgresStore) Insert(ctx context.Context, r Resource) error { //nolint:revive // interface impl
	query := `
		INSERT INTO resources
		(id, scope, scope_id, category, filename, display_name, description,
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
		r.ID, string(r.Scope), scopeID, r.Category, r.Filename, r.DisplayName,
		r.Description, r.MIMEType, r.SizeBytes, r.S3Key, r.URI,
		pq.Array(r.Tags), r.UploaderSub, r.UploaderEmail,
	)
	if err != nil {
		return fmt.Errorf("inserting resource: %w", err)
	}
	return nil
}

func (s *postgresStore) Get(ctx context.Context, id string) (*Resource, error) { //nolint:revive // interface impl
	query := `
		SELECT id, scope, scope_id, category, filename, display_name, description,
		       mime_type, size_bytes, s3_key, uri, tags, uploader_sub, uploader_email,
		       created_at, updated_at, last_read_at
		FROM resources WHERE id = $1
	`
	return s.scanOne(s.db.QueryRowContext(ctx, query, id))
}

func (s *postgresStore) GetByURI(ctx context.Context, uri string) (*Resource, error) { //nolint:revive // interface impl
	query := `
		SELECT id, scope, scope_id, category, filename, display_name, description,
		       mime_type, size_bytes, s3_key, uri, tags, uploader_sub, uploader_email,
		       created_at, updated_at, last_read_at
		FROM resources WHERE uri = $1
	`
	return s.scanOne(s.db.QueryRowContext(ctx, query, uri))
}

func (s *postgresStore) List(ctx context.Context, filter Filter) ([]Resource, int, error) { //nolint:revive // interface impl
	if len(filter.Scopes) == 0 {
		return nil, 0, nil
	}

	where, args := buildScopeWhere(filter)

	// Count total matching.
	countQuery := "SELECT COUNT(*) FROM resources WHERE " + where
	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting resources: %w", err)
	}
	if total == 0 {
		return nil, 0, nil
	}

	// Fetch page.
	limit := filter.Limit
	if limit <= 0 {
		limit = DefaultListLimit
	}
	if limit > MaxListLimit {
		limit = MaxListLimit
	}
	// #nosec G202 -- dynamic scope filter requires concatenation; the ORDER BY
	// comes from Sort.orderByClause, a closed set of constant strings.
	selectQuery := `
		SELECT id, scope, scope_id, category, filename, display_name, description,
		       mime_type, size_bytes, s3_key, uri, tags, uploader_sub, uploader_email,
		       created_at, updated_at, last_read_at
		FROM resources WHERE ` + where + `
		ORDER BY ` + filter.Sort.orderByClause() + `
		LIMIT $` + fmt.Sprintf("%d", len(args)+1) + ` OFFSET $` + fmt.Sprintf("%d", len(args)+2)
	args = append(args, limit, filter.Offset)

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

func (s *postgresStore) Update(ctx context.Context, id string, u Update) error { //nolint:revive // interface impl
	// Every mutable field (display name, description, tags, category) is part of
	// the indexed text, so a metadata edit invalidates the stored vector. Clearing
	// the embedding columns here makes the row a gap the indexjobs reconciler
	// re-embeds off the request path, exactly as the portal asset store does
	// (#1012); leaving them would rank the resource on its pre-edit text forever.
	setClauses := []string{"updated_at = $1", "embedding = NULL", "embedding_model = ''", "embedding_text_hash = NULL"}
	args := []any{time.Now().UTC()}
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
	if u.Category != nil {
		setClauses = append(setClauses, fmt.Sprintf("category = $%d", idx))
		args = append(args, *u.Category)
		idx++
	}

	query := fmt.Sprintf("UPDATE resources SET %s WHERE id = $%d", // #nosec G201 -- dynamic SET clause with parameterized values
		strings.Join(setClauses, ", "), idx)
	args = append(args, id)

	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("updating resource: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("resource not found: %s", id)
	}
	return nil
}

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
		&r.ID, &r.Scope, &s.scopeID, &r.Category, &r.Filename, &r.DisplayName,
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

// buildScopeWhere builds a WHERE clause for scope visibility filtering,
// plus optional category, tag, and text search filters.
func buildScopeWhere(filter Filter) (where string, args []any) {
	where, args, idx := scopeVisibilityWhere(filter.Scopes, 1)

	if filter.Category != "" {
		where += fmt.Sprintf(" AND category = $%d", idx)
		args = append(args, filter.Category)
		idx++
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
