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
}

// insert implements Store.
func (s *PostgresStore) insert(ctx context.Context, l link, tokenSHA256 string) error {
	claims, err := json.Marshal(storedClaims{
		Sub: l.Claims.Sub, Email: l.Claims.Email, Personas: l.Claims.Personas, Roles: l.Claims.Roles,
		IsAdmin: l.Claims.IsAdmin, AdminOfPersonas: l.Claims.AdminOfPersonas,
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
		         description, tags, content_type, if_exists, max_bytes, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`,
		l.ID, tokenSHA256, l.UserID, l.UserEmail, claims, string(l.Scope), l.ScopeID, l.Path, l.Filename, l.DisplayName,
		l.Description, pq.Array(tags), l.ContentType, l.IfExists, l.MaxBytes, l.ExpiresAt.UTC()); err != nil {
		return fmt.Errorf("keeping an upload link: %w", err)
	}
	return nil
}

// claim implements Store: one statement marks the link used, so two PUTs of
// one link cannot both write.
func (s *PostgresStore) claim(ctx context.Context, tokenSHA256 string, now time.Time) (*link, error) {
	var (
		l      link
		scope  string
		claims []byte
		tags   pq.StringArray
	)
	err := s.DB.QueryRowContext(ctx,
		`UPDATE resource_upload_links SET used_at = $2
		  WHERE token_sha256 = $1 AND used_at IS NULL AND expires_at > $2
		  RETURNING id, user_id, user_email, claims, scope, scope_id, path, filename, display_name, description,
		            tags, content_type, if_exists, max_bytes, expires_at`,
		tokenSHA256, now.UTC()).Scan(&l.ID, &l.UserID, &l.UserEmail, &claims, &scope, &l.ScopeID, &l.Path, &l.Filename,
		&l.DisplayName, &l.Description, &tags, &l.ContentType, &l.IfExists, &l.MaxBytes, &l.ExpiresAt)
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
	}
	return &l, nil
}

// finish implements Store.
func (s *PostgresStore) finish(ctx context.Context, id, resourceID, failure string) error {
	if _, err := s.DB.ExecContext(ctx,
		`UPDATE resource_upload_links SET resource_id = $2, error = $3 WHERE id = $1`, id, resourceID, failure); err != nil {
		return fmt.Errorf("recording an upload link's write: %w", err)
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
