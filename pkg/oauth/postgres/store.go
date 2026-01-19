// Package postgres provides PostgreSQL storage for OAuth.
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
	db *sql.DB
}

// New creates a new PostgreSQL OAuth store.
func New(db *sql.DB) *Store {
	return &Store{db: db}
}

// CreateClient creates a new OAuth client.
func (s *Store) CreateClient(ctx context.Context, client *oauth.Client) error {
	redirectURIs, err := json.Marshal(client.RedirectURIs)
	if err != nil {
		return fmt.Errorf("marshaling redirect URIs: %w", err)
	}

	grantTypes, err := json.Marshal(client.GrantTypes)
	if err != nil {
		return fmt.Errorf("marshaling grant types: %w", err)
	}

	query := `
		INSERT INTO oauth_clients (id, client_id, client_secret, name, redirect_uris, grant_types, require_pkce, created_at, active)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`

	_, err = s.db.ExecContext(ctx, query,
		client.ID,
		client.ClientID,
		client.ClientSecret,
		client.Name,
		redirectURIs,
		grantTypes,
		client.RequirePKCE,
		client.CreatedAt,
		client.Active,
	)

	return err
}

// GetClient retrieves a client by client ID.
func (s *Store) GetClient(ctx context.Context, clientID string) (*oauth.Client, error) {
	query := `
		SELECT id, client_id, client_secret, name, redirect_uris, grant_types, require_pkce, created_at, active
		FROM oauth_clients
		WHERE client_id = $1
	`

	var client oauth.Client
	var redirectURIs, grantTypes []byte

	err := s.db.QueryRowContext(ctx, query, clientID).Scan(
		&client.ID,
		&client.ClientID,
		&client.ClientSecret,
		&client.Name,
		&redirectURIs,
		&grantTypes,
		&client.RequirePKCE,
		&client.CreatedAt,
		&client.Active,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("client not found")
	}
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal(redirectURIs, &client.RedirectURIs); err != nil {
		return nil, fmt.Errorf("unmarshaling redirect URIs: %w", err)
	}
	if err := json.Unmarshal(grantTypes, &client.GrantTypes); err != nil {
		return nil, fmt.Errorf("unmarshaling grant types: %w", err)
	}

	return &client, nil
}

// UpdateClient updates a client.
func (s *Store) UpdateClient(ctx context.Context, client *oauth.Client) error {
	redirectURIs, err := json.Marshal(client.RedirectURIs)
	if err != nil {
		return fmt.Errorf("marshaling redirect URIs: %w", err)
	}

	grantTypes, err := json.Marshal(client.GrantTypes)
	if err != nil {
		return fmt.Errorf("marshaling grant types: %w", err)
	}

	query := `
		UPDATE oauth_clients
		SET name = $2, redirect_uris = $3, grant_types = $4, require_pkce = $5, active = $6
		WHERE client_id = $1
	`

	_, err = s.db.ExecContext(ctx, query,
		client.ClientID,
		client.Name,
		redirectURIs,
		grantTypes,
		client.RequirePKCE,
		client.Active,
	)

	return err
}

// DeleteClient deletes a client.
func (s *Store) DeleteClient(ctx context.Context, clientID string) error {
	query := `DELETE FROM oauth_clients WHERE client_id = $1`
	_, err := s.db.ExecContext(ctx, query, clientID)
	return err
}

// ListClients lists all clients.
func (s *Store) ListClients(ctx context.Context) ([]*oauth.Client, error) {
	query := `
		SELECT id, client_id, client_secret, name, redirect_uris, grant_types, require_pkce, created_at, active
		FROM oauth_clients
		ORDER BY created_at DESC
	`

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	clients := make([]*oauth.Client, 0)
	for rows.Next() {
		var client oauth.Client
		var redirectURIs, grantTypes []byte

		if err := rows.Scan(
			&client.ID,
			&client.ClientID,
			&client.ClientSecret,
			&client.Name,
			&redirectURIs,
			&grantTypes,
			&client.RequirePKCE,
			&client.CreatedAt,
			&client.Active,
		); err != nil {
			return nil, err
		}

		if err := json.Unmarshal(redirectURIs, &client.RedirectURIs); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(grantTypes, &client.GrantTypes); err != nil {
			return nil, err
		}

		clients = append(clients, &client)
	}

	return clients, rows.Err()
}

// SaveAuthorizationCode saves an authorization code.
func (s *Store) SaveAuthorizationCode(ctx context.Context, code *oauth.AuthorizationCode) error {
	userClaims, err := json.Marshal(code.UserClaims)
	if err != nil {
		return fmt.Errorf("marshaling user claims: %w", err)
	}

	query := `
		INSERT INTO oauth_authorization_codes
		(id, code, client_id, user_id, user_claims, code_challenge, redirect_uri, scope, expires_at, used, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`

	_, err = s.db.ExecContext(ctx, query,
		code.ID,
		code.Code,
		code.ClientID,
		code.UserID,
		userClaims,
		code.CodeChallenge,
		code.RedirectURI,
		code.Scope,
		code.ExpiresAt,
		code.Used,
		code.CreatedAt,
	)

	return err
}

// GetAuthorizationCode retrieves an authorization code.
func (s *Store) GetAuthorizationCode(ctx context.Context, codeValue string) (*oauth.AuthorizationCode, error) {
	query := `
		SELECT id, code, client_id, user_id, user_claims, code_challenge, redirect_uri, scope, expires_at, used, created_at
		FROM oauth_authorization_codes
		WHERE code = $1
	`

	var code oauth.AuthorizationCode
	var userClaims []byte

	err := s.db.QueryRowContext(ctx, query, codeValue).Scan(
		&code.ID,
		&code.Code,
		&code.ClientID,
		&code.UserID,
		&userClaims,
		&code.CodeChallenge,
		&code.RedirectURI,
		&code.Scope,
		&code.ExpiresAt,
		&code.Used,
		&code.CreatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("authorization code not found")
	}
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal(userClaims, &code.UserClaims); err != nil {
		return nil, fmt.Errorf("unmarshaling user claims: %w", err)
	}

	return &code, nil
}

// DeleteAuthorizationCode deletes an authorization code.
func (s *Store) DeleteAuthorizationCode(ctx context.Context, codeValue string) error {
	query := `DELETE FROM oauth_authorization_codes WHERE code = $1`
	_, err := s.db.ExecContext(ctx, query, codeValue)
	return err
}

// CleanupExpiredCodes removes expired authorization codes.
func (s *Store) CleanupExpiredCodes(ctx context.Context) error {
	query := `DELETE FROM oauth_authorization_codes WHERE expires_at < $1`
	_, err := s.db.ExecContext(ctx, query, time.Now())
	return err
}

// SaveRefreshToken saves a refresh token.
func (s *Store) SaveRefreshToken(ctx context.Context, token *oauth.RefreshToken) error {
	userClaims, err := json.Marshal(token.UserClaims)
	if err != nil {
		return fmt.Errorf("marshaling user claims: %w", err)
	}

	query := `
		INSERT INTO oauth_refresh_tokens (id, token, client_id, user_id, user_claims, scope, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`

	_, err = s.db.ExecContext(ctx, query,
		token.ID,
		token.Token,
		token.ClientID,
		token.UserID,
		userClaims,
		token.Scope,
		token.ExpiresAt,
		token.CreatedAt,
	)

	return err
}

// GetRefreshToken retrieves a refresh token.
func (s *Store) GetRefreshToken(ctx context.Context, tokenValue string) (*oauth.RefreshToken, error) {
	query := `
		SELECT id, token, client_id, user_id, user_claims, scope, expires_at, created_at
		FROM oauth_refresh_tokens
		WHERE token = $1
	`

	var token oauth.RefreshToken
	var userClaims []byte

	err := s.db.QueryRowContext(ctx, query, tokenValue).Scan(
		&token.ID,
		&token.Token,
		&token.ClientID,
		&token.UserID,
		&userClaims,
		&token.Scope,
		&token.ExpiresAt,
		&token.CreatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("refresh token not found")
	}
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal(userClaims, &token.UserClaims); err != nil {
		return nil, fmt.Errorf("unmarshaling user claims: %w", err)
	}

	return &token, nil
}

// DeleteRefreshToken deletes a refresh token.
func (s *Store) DeleteRefreshToken(ctx context.Context, tokenValue string) error {
	query := `DELETE FROM oauth_refresh_tokens WHERE token = $1`
	_, err := s.db.ExecContext(ctx, query, tokenValue)
	return err
}

// DeleteRefreshTokensForClient deletes all refresh tokens for a client.
func (s *Store) DeleteRefreshTokensForClient(ctx context.Context, clientID string) error {
	query := `DELETE FROM oauth_refresh_tokens WHERE client_id = $1`
	_, err := s.db.ExecContext(ctx, query, clientID)
	return err
}

// CleanupExpiredTokens removes expired refresh tokens.
func (s *Store) CleanupExpiredTokens(ctx context.Context) error {
	query := `DELETE FROM oauth_refresh_tokens WHERE expires_at < $1`
	_, err := s.db.ExecContext(ctx, query, time.Now())
	return err
}

// Verify interface compliance.
var _ oauth.Storage = (*Store)(nil)
