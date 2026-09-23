package scriptgrant

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/lib/pq"
)

// maxGrantedScripts caps a caller's catalog. It is far past any real set of
// scripts one application is given, and it keeps the id list bound into the
// listing query bounded.
const maxGrantedScripts = 1000

// Both caller queries match the grants naming the caller's persona, any of
// their roles, or the API key they used. An empty persona or key name matches
// nothing, because a stored principal is never empty. The statements are
// written out whole so the SQL gate can prepare them.
const (
	allowsSQL = `SELECT EXISTS (SELECT 1 FROM script_grants WHERE script_id = $1 AND (
		(principal_kind = 'persona' AND principal = $2)
		OR (principal_kind = 'role' AND principal = ANY($3))
		OR (principal_kind = 'api_key' AND principal = $4)))`
	grantedSQL = `SELECT DISTINCT script_id::text FROM script_grants WHERE (
		(principal_kind = 'persona' AND principal = $1)
		OR (principal_kind = 'role' AND principal = ANY($2))
		OR (principal_kind = 'api_key' AND principal = $3)) LIMIT $4`
)

// PostgresStore keeps grants in script_grants.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgres builds the store.
func NewPostgres(db *sql.DB) *PostgresStore { return &PostgresStore{db: db} }

// List returns a script's grants, oldest first.
func (s *PostgresStore) List(ctx context.Context, scriptID string) ([]Grant, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT script_id::text, principal_kind, principal, granted_by, created_at
		FROM script_grants WHERE script_id = $1 ORDER BY created_at, principal_kind, principal`, scriptID)
	if err != nil {
		return nil, fmt.Errorf("listing script grants: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]Grant, 0)
	for rows.Next() {
		var g Grant
		if err := rows.Scan(&g.ScriptID, &g.Kind, &g.Principal, &g.GrantedBy, &g.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning a script grant: %w", err)
		}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading script grants: %w", err)
	}
	return out, nil
}

// Add records a grant; granting what is already granted keeps the first.
func (s *PostgresStore) Add(ctx context.Context, g Grant) error {
	if _, err := s.db.ExecContext(ctx, `INSERT INTO script_grants (script_id, principal_kind, principal, granted_by)
		VALUES ($1, $2, $3, $4) ON CONFLICT (script_id, principal_kind, principal) DO NOTHING`,
		g.ScriptID, g.Kind, g.Principal, g.GrantedBy); err != nil {
		return fmt.Errorf("adding a script grant: %w", err)
	}
	return nil
}

// Remove withdraws a grant, reporting whether there was one.
func (s *PostgresStore) Remove(ctx context.Context, g Grant) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM script_grants
		WHERE script_id = $1 AND principal_kind = $2 AND principal = $3`, g.ScriptID, g.Kind, g.Principal)
	if err != nil {
		return false, fmt.Errorf("removing a script grant: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("removing a script grant: %w", err)
	}
	return n > 0, nil
}

// Allows reports whether any grant on the script names the caller.
func (s *PostgresStore) Allows(ctx context.Context, scriptID string, c Caller) (bool, error) {
	var ok bool
	err := s.db.QueryRowContext(ctx, allowsSQL, scriptID, c.Persona, pq.Array(c.Roles), c.apiKeyName()).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("checking script grants: %w", err)
	}
	return ok, nil
}

// GrantedScriptIDs lists the scripts granted to anything the caller is.
func (s *PostgresStore) GrantedScriptIDs(ctx context.Context, c Caller) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, grantedSQL, c.Persona, pq.Array(c.Roles), c.apiKeyName(), maxGrantedScripts)
	if err != nil {
		return nil, fmt.Errorf("listing granted scripts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scanning a granted script: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading granted scripts: %w", err)
	}
	return ids, nil
}
