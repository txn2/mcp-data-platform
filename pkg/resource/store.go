package resource

import (
	"context"
	"database/sql"
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

// S3Client abstracts blob storage operations for resources.
type S3Client interface {
	PutObject(ctx context.Context, bucket, key string, data []byte, contentType string) error
	GetObject(ctx context.Context, bucket, key string) (body []byte, contentType string, err error)
	DeleteObject(ctx context.Context, bucket, key string) error
}

// --- PostgreSQL Store ---

type postgresStore struct {
	db *sql.DB
}

// NewPostgresStore creates a resource store backed by PostgreSQL.
func NewPostgresStore(db *sql.DB) Store {
	return &postgresStore{db: db}
}

func (s *postgresStore) Insert(ctx context.Context, r Resource) error {
	query := `
		INSERT INTO resources
		(id, scope, scope_id, category, filename, display_name, description,
		 mime_type, size_bytes, s3_key, uri, tags, uploader_sub, uploader_email)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
	`
	scopeID := sql.NullString{String: r.ScopeID, Valid: r.ScopeID != ""}
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

func (s *postgresStore) Get(ctx context.Context, id string) (*Resource, error) {
	query := `
		SELECT id, scope, scope_id, category, filename, display_name, description,
		       mime_type, size_bytes, s3_key, uri, tags, uploader_sub, uploader_email,
		       created_at, updated_at
		FROM resources WHERE id = $1
	`
	return s.scanOne(s.db.QueryRowContext(ctx, query, id))
}

func (s *postgresStore) GetByURI(ctx context.Context, uri string) (*Resource, error) {
	query := `
		SELECT id, scope, scope_id, category, filename, display_name, description,
		       mime_type, size_bytes, s3_key, uri, tags, uploader_sub, uploader_email,
		       created_at, updated_at
		FROM resources WHERE uri = $1
	`
	return s.scanOne(s.db.QueryRowContext(ctx, query, uri))
}

func (s *postgresStore) List(ctx context.Context, filter Filter) ([]Resource, int, error) {
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
		limit = 100
	}
	selectQuery := `
		SELECT id, scope, scope_id, category, filename, display_name, description,
		       mime_type, size_bytes, s3_key, uri, tags, uploader_sub, uploader_email,
		       created_at, updated_at
		FROM resources WHERE ` + where + `
		ORDER BY updated_at DESC
		LIMIT $` + fmt.Sprintf("%d", len(args)+1) + ` OFFSET $` + fmt.Sprintf("%d", len(args)+2)
	args = append(args, limit, filter.Offset)

	rows, err := s.db.QueryContext(ctx, selectQuery, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("listing resources: %w", err)
	}
	defer rows.Close()

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

func (s *postgresStore) Update(ctx context.Context, id string, u Update) error {
	setClauses := []string{"updated_at = $1"}
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

	query := fmt.Sprintf("UPDATE resources SET %s WHERE id = $%d",
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

func (s *postgresStore) Delete(ctx context.Context, id string) error {
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

func (s *postgresStore) scanOne(row *sql.Row) (*Resource, error) {
	var r Resource
	var scopeID sql.NullString
	var tags []string
	err := row.Scan(
		&r.ID, &r.Scope, &scopeID, &r.Category, &r.Filename, &r.DisplayName,
		&r.Description, &r.MIMEType, &r.SizeBytes, &r.S3Key, &r.URI,
		pq.Array(&tags), &r.UploaderSub, &r.UploaderEmail,
		&r.CreatedAt, &r.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scanning resource: %w", err)
	}
	r.ScopeID = scopeID.String
	if tags != nil {
		r.Tags = tags
	} else {
		r.Tags = []string{}
	}
	return &r, nil
}

func (s *postgresStore) scanRow(rows *sql.Rows) (*Resource, error) {
	var r Resource
	var scopeID sql.NullString
	var tags []string
	err := rows.Scan(
		&r.ID, &r.Scope, &scopeID, &r.Category, &r.Filename, &r.DisplayName,
		&r.Description, &r.MIMEType, &r.SizeBytes, &r.S3Key, &r.URI,
		pq.Array(&tags), &r.UploaderSub, &r.UploaderEmail,
		&r.CreatedAt, &r.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scanning resource row: %w", err)
	}
	r.ScopeID = scopeID.String
	if tags != nil {
		r.Tags = tags
	} else {
		r.Tags = []string{}
	}
	return &r, nil
}

// buildScopeWhere builds a WHERE clause for scope visibility filtering,
// plus optional category, tag, and text search filters.
func buildScopeWhere(filter Filter) (string, []any) {
	// Build scope OR conditions.
	var scopeConds []string
	var args []any
	idx := 1

	for _, sf := range filter.Scopes {
		if sf.Scope == ScopeGlobal {
			scopeConds = append(scopeConds, fmt.Sprintf("(scope = $%d AND scope_id IS NULL)", idx))
			args = append(args, string(ScopeGlobal))
			idx++
		} else {
			scopeConds = append(scopeConds, fmt.Sprintf("(scope = $%d AND scope_id = $%d)", idx, idx+1))
			args = append(args, string(sf.Scope), sf.ScopeID)
			idx += 2
		}
	}

	where := "(" + strings.Join(scopeConds, " OR ") + ")"

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
		pattern := "%" + filter.Query + "%"
		where += fmt.Sprintf(" AND (display_name ILIKE $%d OR description ILIKE $%d)", idx, idx+1)
		args = append(args, pattern, pattern)
	}

	return where, args
}

// Verify interface compliance.
var _ Store = (*postgresStore)(nil)
