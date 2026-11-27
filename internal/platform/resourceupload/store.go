package resourceupload

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// PostgresStore keeps the links in resource_upload_links.
type PostgresStore struct {
	DB *sql.DB
}

// NewStore is the links' store over db, nil without one.
func NewStore(db *sql.DB) *PostgresStore {
	if db == nil {
		return nil
	}
	return &PostgresStore{DB: db}
}

// storedClaims are the claims a link writes with, as kept. A claim resolved per
// request (an external folder, #2290) is not kept, so a link never writes
// further than its issuer could at the moment of the write.
type storedClaims struct {
	Sub             string   `json:"sub"`
	Email           string   `json:"email"`
	Personas        []string `json:"personas"`
	Roles           []string `json:"roles"`
	IsAdmin         bool     `json:"is_admin"`
	AdminOfPersonas []string `json:"admin_of_personas"`
	// NoGlobal keeps a person of an external persona out of Global at the
	// write as at the issue; folder grants are read again when the file
	// arrives (#2684).
	NoGlobal bool `json:"no_global,omitempty"`
}

// insert implements Store.
func (s *PostgresStore) insert(ctx context.Context, l link, tokenSHA256 string) error {
	claims, err := json.Marshal(storedClaims{
		Sub: l.Claims.Sub, Email: l.Claims.Email, Personas: l.Claims.Personas, Roles: l.Claims.Roles,
		IsAdmin: l.Claims.IsAdmin, AdminOfPersonas: l.Claims.AdminOfPersonas, NoGlobal: l.Claims.NoGlobal,
	})
	if err != nil {
		return fmt.Errorf("keeping an upload link: %w", err)
	}
	tags := l.Tags
	if tags == nil {
		tags = []string{}
	}
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO resource_upload_links
		        (id, token_sha256, user_id, user_email, claims, scope, scope_id, path, filename, display_name,
		         description, tags, content_type, if_exists, max_bytes, expires_at, max_files)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)`,
		l.ID, tokenSHA256, l.UserID, l.UserEmail, claims, string(l.Scope), l.ScopeID, l.Path, l.Filename, l.DisplayName,
		l.Description, pq.Array(tags), l.ContentType, l.IfExists, l.MaxBytes, l.ExpiresAt.UTC(), max(l.MaxFiles, 1)); err != nil {
		return fmt.Errorf("keeping an upload link: %w", err)
	}
	return nil
}

// claim implements Store: one statement takes one of the link's places, so
// two PUTs racing for its last place cannot both write.
func (s *PostgresStore) claim(ctx context.Context, tokenSHA256 string, now time.Time) (*link, error) {
	var (
		l      link
		scope  string
		claims []byte
		tags   pq.StringArray
	)
	err := s.DB.QueryRowContext(ctx,
		`UPDATE resource_upload_links SET used_at = $2, files_used = files_used + 1
		  WHERE token_sha256 = $1 AND revoked_at IS NULL AND files_used < max_files AND expires_at > $2
		  RETURNING id, user_id, user_email, claims, scope, scope_id, path, filename, display_name, description,
		            tags, content_type, if_exists, max_bytes, expires_at, max_files, files_used`,
		tokenSHA256, now.UTC()).Scan(&l.ID, &l.UserID, &l.UserEmail, &claims, &scope, &l.ScopeID, &l.Path, &l.Filename,
		&l.DisplayName, &l.Description, &tags, &l.ContentType, &l.IfExists, &l.MaxBytes, &l.ExpiresAt, &l.MaxFiles, &l.FilesUsed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoLink
	}
	if err != nil {
		return nil, fmt.Errorf("reading an upload link: %w", err)
	}
	var c storedClaims
	if err := json.Unmarshal(claims, &c); err != nil {
		return nil, fmt.Errorf("reading an upload link's claims: %w", err)
	}
	l.Scope, l.Tags = resource.Scope(scope), []string(tags)
	l.Claims = resource.Claims{
		Sub: c.Sub, Email: c.Email, Personas: c.Personas, Roles: c.Roles, IsAdmin: c.IsAdmin, AdminOfPersonas: c.AdminOfPersonas,
		NoGlobal: c.NoGlobal,
	}
	return &l, nil
}

// finish implements Store: a filed file is added to what the link wrote, and
// a PUT that filed nothing gives its place back.
func (s *PostgresStore) finish(ctx context.Context, id, resourceID, failure string) error {
	var err error
	if resourceID == "" {
		_, err = s.DB.ExecContext(ctx,
			`UPDATE resource_upload_links SET error = $2, files_used = GREATEST(files_used - 1, 0) WHERE id = $1`,
			id, failure)
	} else {
		_, err = s.DB.ExecContext(ctx,
			`UPDATE resource_upload_links SET resource_id = $2, error = '', resource_ids = array_append(resource_ids, $2)
			  WHERE id = $1`, id, resourceID)
	}
	if err != nil {
		return fmt.Errorf("recording an upload link's write: %w", err)
	}
	return nil
}

// open implements Store.
func (s *PostgresStore) open(ctx context.Context, userID, email string, now time.Time) ([]Open, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, scope, scope_id, path, filename, content_type, max_bytes, max_files, files_used, created_at, expires_at
		   FROM resource_upload_links
		  WHERE (user_id = $1 OR ($2 <> '' AND user_email = $2))
		    AND revoked_at IS NULL AND files_used < max_files AND expires_at > $3
		  ORDER BY created_at DESC LIMIT 200`, userID, email, now.UTC())
	if err != nil {
		return nil, fmt.Errorf("listing upload links: %w", err)
	}
	defer rows.Close() //nolint:errcheck // read-only
	out := []Open{}
	for rows.Next() {
		var o Open
		if err := rows.Scan(&o.ID, &o.Scope, &o.ScopeID, &o.Path, &o.Filename, &o.ContentType, &o.MaxBytes,
			&o.MaxFiles, &o.FilesUsed, &o.CreatedAt, &o.ExpiresAt); err != nil {
			return nil, fmt.Errorf("reading upload links: %w", err)
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading upload links: %w", err)
	}
	return out, nil
}

// revoke implements Store.
func (s *PostgresStore) revoke(ctx context.Context, id string, by resource.Claims, now time.Time) error {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE resource_upload_links SET revoked_at = $5
		  WHERE id = $1 AND revoked_at IS NULL AND ($4 OR user_id = $2 OR ($3 <> '' AND user_email = $3))`,
		id, by.Sub, by.Email, by.IsAdmin, now.UTC())
	if err != nil {
		return fmt.Errorf("revoking an upload link: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoLink
	}
	return nil
}

// sweep implements Store.
func (s *PostgresStore) sweep(ctx context.Context, cutoff time.Time) error {
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM resource_upload_links WHERE expires_at < $1`, cutoff.UTC()); err != nil {
		return fmt.Errorf("sweeping upload links: %w", err)
	}
	return nil
}
