// Package postgres provides PostgreSQL storage for OAuth 2.1 data.
package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/oauth"
)

// Store implements oauth.Storage using PostgreSQL.
type Store struct {
	db     *sql.DB
	cancel context.CancelFunc
	done   chan struct{}
}

// New creates a new PostgreSQL OAuth store.
func New(db *sql.DB) *Store {
	return &Store{db: db}
}

// Verify Store implements oauth.Storage at compile time.
var _ oauth.Storage = (*Store)(nil)

// CreateClient stores a new OAuth client.
func (s *Store) CreateClient(ctx context.Context, client *oauth.Client) error {
	redirectURIs, err := json.Marshal(client.RedirectURIs)
	if err != nil {
		return fmt.Errorf("marshaling redirect URIs: %w", err)
	}
	grantTypes, err := json.Marshal(client.GrantTypes)
	if err != nil {
		return fmt.Errorf("marshaling grant types: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO oauth_clients (id, client_id, client_secret, name, redirect_uris, grant_types, require_pkce, created_at, active)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		client.ID, client.ClientID, client.ClientSecret, client.Name,
		redirectURIs, grantTypes, client.RequirePKCE, client.CreatedAt, client.Active,
	)
	if err != nil {
		return fmt.Errorf("inserting client: %w", err)
	}
	return nil
}

// GetClient retrieves a client by client_id.
func (s *Store) GetClient(ctx context.Context, clientID string) (*oauth.Client, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, client_id, client_secret, name, redirect_uris, grant_types, require_pkce, created_at, active
		FROM oauth_clients
		WHERE client_id = $1 AND active = true`, clientID)

	return scanClient(row)
}

// UpdateClient updates an existing client.
func (s *Store) UpdateClient(ctx context.Context, client *oauth.Client) error {
	redirectURIs, err := json.Marshal(client.RedirectURIs)
	if err != nil {
		return fmt.Errorf("marshaling redirect URIs: %w", err)
	}
	grantTypes, err := json.Marshal(client.GrantTypes)
	if err != nil {
		return fmt.Errorf("marshaling grant types: %w", err)
	}

	result, err := s.db.ExecContext(ctx, `
		UPDATE oauth_clients
		SET client_secret = $1, name = $2, redirect_uris = $3, grant_types = $4, require_pkce = $5, active = $6
		WHERE client_id = $7`,
		client.ClientSecret, client.Name, redirectURIs, grantTypes,
		client.RequirePKCE, client.Active, client.ClientID,
	)
	if err != nil {
		return fmt.Errorf("updating client: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("client not found: %s", client.ClientID)
	}
	return nil
}

// DeleteClient marks a client as inactive.
func (s *Store) DeleteClient(ctx context.Context, clientID string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE oauth_clients SET active = false WHERE client_id = $1`, clientID)
	if err != nil {
		return fmt.Errorf("deleting client: %w", err)
	}
	return nil
}

// ListClients returns all active clients.
func (s *Store) ListClients(ctx context.Context) (_ []*oauth.Client, retErr error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, client_id, client_secret, name, redirect_uris, grant_types, require_pkce, created_at, active
		FROM oauth_clients
		WHERE active = true`)
	if err != nil {
		return nil, fmt.Errorf("querying clients: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && retErr == nil {
			retErr = fmt.Errorf("closing client rows: %w", closeErr)
		}
	}()

	var clients []*oauth.Client
	for rows.Next() {
		client, scanErr := scanClientRow(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		clients = append(clients, client)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating client rows: %w", err)
	}
	return clients, nil
}

// SaveAuthorizationCode stores an authorization code.
func (s *Store) SaveAuthorizationCode(ctx context.Context, code *oauth.AuthorizationCode) error {
	claims, err := json.Marshal(code.UserClaims)
	if err != nil {
		return fmt.Errorf("marshaling user claims: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO oauth_authorization_codes (id, code, client_id, user_id, user_claims, code_challenge, redirect_uri, scope, expires_at, used, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		code.ID, code.Code, code.ClientID, code.UserID, claims,
		code.CodeChallenge, code.RedirectURI, code.Scope,
		code.ExpiresAt, code.Used, code.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("inserting authorization code: %w", err)
	}
	return nil
}

// GetAuthorizationCode retrieves an authorization code.
func (s *Store) GetAuthorizationCode(ctx context.Context, code string) (*oauth.AuthorizationCode, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, code, client_id, user_id, user_claims, code_challenge, redirect_uri, scope, expires_at, used, created_at
		FROM oauth_authorization_codes
		WHERE code = $1`, code)

	var ac oauth.AuthorizationCode
	var claimsJSON []byte
	err := row.Scan(
		&ac.ID, &ac.Code, &ac.ClientID, &ac.UserID, &claimsJSON,
		&ac.CodeChallenge, &ac.RedirectURI, &ac.Scope,
		&ac.ExpiresAt, &ac.Used, &ac.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scanning authorization code: %w", err)
	}
	if claimsJSON != nil {
		if unmarshalErr := json.Unmarshal(claimsJSON, &ac.UserClaims); unmarshalErr != nil {
			return nil, fmt.Errorf("unmarshaling user claims: %w", unmarshalErr)
		}
	}
	return &ac, nil
}

// DeleteAuthorizationCode deletes an authorization code.
func (s *Store) DeleteAuthorizationCode(ctx context.Context, code string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM oauth_authorization_codes WHERE code = $1`, code)
	if err != nil {
		return fmt.Errorf("deleting authorization code: %w", err)
	}
	return nil
}

// CleanupExpiredCodes removes expired authorization codes.
func (s *Store) CleanupExpiredCodes(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM oauth_authorization_codes WHERE expires_at <= NOW()`)
	if err != nil {
		return fmt.Errorf("cleaning up expired codes: %w", err)
	}
	return nil
}

// SaveRefreshToken stores a refresh token.
func (s *Store) SaveRefreshToken(ctx context.Context, token *oauth.RefreshToken) error {
	claims, err := json.Marshal(token.UserClaims)
	if err != nil {
		return fmt.Errorf("marshaling user claims: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO oauth_refresh_tokens (id, token, client_id, user_id, user_claims, scope, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		token.ID, token.Token, token.ClientID, token.UserID,
		claims, token.Scope, token.ExpiresAt, token.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("inserting refresh token: %w", err)
	}
	return nil
}

// GetRefreshToken retrieves a refresh token.
func (s *Store) GetRefreshToken(ctx context.Context, token string) (*oauth.RefreshToken, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, token, client_id, user_id, user_claims, scope, expires_at, created_at
		FROM oauth_refresh_tokens
		WHERE token = $1`, token)

	var rt oauth.RefreshToken
	var claimsJSON []byte
	err := row.Scan(
		&rt.ID, &rt.Token, &rt.ClientID, &rt.UserID,
		&claimsJSON, &rt.Scope, &rt.ExpiresAt, &rt.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scanning refresh token: %w", err)
	}
	if claimsJSON != nil {
		if unmarshalErr := json.Unmarshal(claimsJSON, &rt.UserClaims); unmarshalErr != nil {
			return nil, fmt.Errorf("unmarshaling user claims: %w", unmarshalErr)
		}
	}
	return &rt, nil
}

// DeleteRefreshToken deletes a refresh token.
func (s *Store) DeleteRefreshToken(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM oauth_refresh_tokens WHERE token = $1`, token)
	if err != nil {
		return fmt.Errorf("deleting refresh token: %w", err)
	}
	return nil
}

// DeleteRefreshTokensForClient deletes all refresh tokens for a client.
func (s *Store) DeleteRefreshTokensForClient(ctx context.Context, clientID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM oauth_refresh_tokens WHERE client_id = $1`, clientID)
	if err != nil {
		return fmt.Errorf("deleting refresh tokens for client: %w", err)
	}
	return nil
}

// CleanupExpiredTokens removes expired refresh tokens.
func (s *Store) CleanupExpiredTokens(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM oauth_refresh_tokens WHERE expires_at <= NOW()`)
	if err != nil {
		return fmt.Errorf("cleaning up expired tokens: %w", err)
	}
	return nil
}

// StartCleanupRoutine starts a background goroutine that periodically
// cleans up expired authorization codes and refresh tokens.
func (s *Store) StartCleanupRoutine(interval time.Duration) {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.done = make(chan struct{})

	go func() {
		defer close(s.done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = s.CleanupExpiredCodes(ctx)
				_ = s.CleanupExpiredTokens(ctx)
			}
		}
	}()
}

// Close stops the cleanup routine and releases resources.
func (s *Store) Close() error {
	if s.cancel != nil {
		s.cancel()
		<-s.done
	}
	return nil
}

// scanClient scans a single client row from a QueryRow result.
func scanClient(row *sql.Row) (*oauth.Client, error) {
	var client oauth.Client
	var redirectURIsJSON, grantTypesJSON []byte
	err := row.Scan(
		&client.ID, &client.ClientID, &client.ClientSecret, &client.Name,
		&redirectURIsJSON, &grantTypesJSON, &client.RequirePKCE,
		&client.CreatedAt, &client.Active,
	)
	if err != nil {
		return nil, fmt.Errorf("scanning client: %w", err)
	}
	if err := json.Unmarshal(redirectURIsJSON, &client.RedirectURIs); err != nil {
		return nil, fmt.Errorf("unmarshaling redirect URIs: %w", err)
	}
	if err := json.Unmarshal(grantTypesJSON, &client.GrantTypes); err != nil {
		return nil, fmt.Errorf("unmarshaling grant types: %w", err)
	}
	return &client, nil
}

// scanClientRow scans a single client row from a Rows iterator.
func scanClientRow(rows *sql.Rows) (*oauth.Client, error) {
	var client oauth.Client
	var redirectURIsJSON, grantTypesJSON []byte
	err := rows.Scan(
		&client.ID, &client.ClientID, &client.ClientSecret, &client.Name,
		&redirectURIsJSON, &grantTypesJSON, &client.RequirePKCE,
		&client.CreatedAt, &client.Active,
	)
	if err != nil {
		return nil, fmt.Errorf("scanning client row: %w", err)
	}
	if err := json.Unmarshal(redirectURIsJSON, &client.RedirectURIs); err != nil {
		return nil, fmt.Errorf("unmarshaling redirect URIs: %w", err)
	}
	if err := json.Unmarshal(grantTypesJSON, &client.GrantTypes); err != nil {
		return nil, fmt.Errorf("unmarshaling grant types: %w", err)
	}
	return &client, nil
}
