// Package postgres provides PostgreSQL storage for OAuth 2.1 data.
package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/oauth"
)

// logKeyError is the structured-log field key for error values.
const logKeyError = "error"

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

	// dcr is intentionally omitted from the ON CONFLICT update set: a
	// pre-registered client re-inserted on restart (stable client_id) must
	// keep its existing dcr value rather than have it reset.
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO oauth_clients (id, client_id, client_secret, name, redirect_uris, grant_types, require_pkce, created_at, active, dcr)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (client_id) DO UPDATE SET
			client_secret = EXCLUDED.client_secret,
			name = EXCLUDED.name,
			redirect_uris = EXCLUDED.redirect_uris,
			grant_types = EXCLUDED.grant_types,
			require_pkce = EXCLUDED.require_pkce,
			active = EXCLUDED.active`,
		client.ID, client.ClientID, client.ClientSecret, client.Name,
		redirectURIs, grantTypes, client.RequirePKCE, client.CreatedAt, client.Active,
		client.DynamicallyRegistered,
	)
	if err != nil {
		return fmt.Errorf("inserting client: %w", err)
	}
	return nil
}

// GetClient retrieves a client by client_id.
func (s *Store) GetClient(ctx context.Context, clientID string) (*oauth.Client, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, client_id, client_secret, name, redirect_uris, grant_types, require_pkce, created_at, active, dcr
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
		SELECT id, client_id, client_secret, name, redirect_uris, grant_types, require_pkce, created_at, active, dcr
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

// SaveAuthorizationCode stores an authorization code. Only the SHA-256
// digest of the code value is persisted (oauth.HashToken), so a database
// read never yields a usable credential.
func (s *Store) SaveAuthorizationCode(ctx context.Context, code *oauth.AuthorizationCode) error {
	claims, err := json.Marshal(code.UserClaims)
	if err != nil {
		return fmt.Errorf("marshaling user claims: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO oauth_authorization_codes (id, code, client_id, user_id, user_claims, code_challenge, redirect_uri, scope, expires_at, used, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		code.ID, oauth.HashToken(code.Code), code.ClientID, code.UserID, claims,
		code.CodeChallenge, code.RedirectURI, code.Scope,
		code.ExpiresAt, code.Used, code.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("inserting authorization code: %w", err)
	}
	return nil
}

// GetAuthorizationCode retrieves an authorization code by its raw value;
// the comparison is against the stored SHA-256 digest.
func (s *Store) GetAuthorizationCode(ctx context.Context, code string) (*oauth.AuthorizationCode, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, code, client_id, user_id, user_claims, code_challenge, redirect_uri, scope, expires_at, used, created_at
		FROM oauth_authorization_codes
		WHERE code = $1`, oauth.HashToken(code))
	return scanAuthorizationCode(row)
}

// ConsumeAuthorizationCode atomically deletes and returns an authorization
// code in a single statement, so retrieval and single-use invalidation
// cannot diverge. Returns oauth.ErrNotFound when the code does not exist,
// so the grant handler can distinguish replay from a storage outage.
func (s *Store) ConsumeAuthorizationCode(ctx context.Context, code string) (*oauth.AuthorizationCode, error) {
	row := s.db.QueryRowContext(ctx, `
		DELETE FROM oauth_authorization_codes
		WHERE code = $1
		RETURNING id, code, client_id, user_id, user_claims, code_challenge, redirect_uri, scope, expires_at, used, created_at`, oauth.HashToken(code))
	ac, err := scanAuthorizationCode(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("authorization code: %w", oauth.ErrNotFound)
	}
	return ac, err
}

// scanAuthorizationCode scans a single authorization-code row.
func scanAuthorizationCode(row *sql.Row) (*oauth.AuthorizationCode, error) {
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

// DeleteAuthorizationCode deletes an authorization code by its raw value.
func (s *Store) DeleteAuthorizationCode(ctx context.Context, code string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM oauth_authorization_codes WHERE code = $1`, oauth.HashToken(code))
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

// SaveRefreshToken stores a refresh token. Only the SHA-256 digest of the
// token value is persisted (oauth.HashToken), so a database read never
// yields a usable credential.
func (s *Store) SaveRefreshToken(ctx context.Context, token *oauth.RefreshToken) error {
	claims, err := json.Marshal(token.UserClaims)
	if err != nil {
		return fmt.Errorf("marshaling user claims: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO oauth_refresh_tokens (id, token, client_id, user_id, user_claims, scope, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		token.ID, oauth.HashToken(token.Token), token.ClientID, token.UserID,
		claims, token.Scope, token.ExpiresAt, token.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("inserting refresh token: %w", err)
	}

	// Stamp last_used_at so the DCR cleanup never reaps a client that has
	// completed a token exchange. Every issuance (authorization-code and
	// refresh grant) flows through here, so the first successful token
	// issuance marks the client used for good — independent of whether its
	// refresh token later rotates or expires. Best-effort: a failure here
	// must not fail token issuance, so it is logged rather than returned.
	if _, err := s.db.ExecContext(ctx,
		`UPDATE oauth_clients SET last_used_at = NOW() WHERE client_id = $1 AND last_used_at IS NULL`,
		token.ClientID); err != nil {
		slog.Warn("oauth store: marking client used failed", "client_id", token.ClientID, logKeyError, err)
	}
	return nil
}

// GetRefreshToken retrieves a refresh token by its raw value; the
// comparison is against the stored SHA-256 digest.
func (s *Store) GetRefreshToken(ctx context.Context, token string) (*oauth.RefreshToken, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, token, client_id, user_id, user_claims, scope, expires_at, created_at
		FROM oauth_refresh_tokens
		WHERE token = $1`, oauth.HashToken(token))
	return scanRefreshToken(row)
}

// ConsumeRefreshToken atomically deletes and returns a refresh token in a
// single statement, backing rotation: the old token is provably invalid
// before new tokens are issued. Returns oauth.ErrNotFound when the token
// does not exist, so the grant handler can distinguish an invalid token
// from a storage outage.
func (s *Store) ConsumeRefreshToken(ctx context.Context, token string) (*oauth.RefreshToken, error) {
	row := s.db.QueryRowContext(ctx, `
		DELETE FROM oauth_refresh_tokens
		WHERE token = $1
		RETURNING id, token, client_id, user_id, user_claims, scope, expires_at, created_at`, oauth.HashToken(token))
	rt, err := scanRefreshToken(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("refresh token: %w", oauth.ErrNotFound)
	}
	return rt, err
}

// scanRefreshToken scans a single refresh-token row.
func scanRefreshToken(row *sql.Row) (*oauth.RefreshToken, error) {
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

// DeleteRefreshToken deletes a refresh token by its raw value.
func (s *Store) DeleteRefreshToken(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM oauth_refresh_tokens WHERE token = $1`, oauth.HashToken(token))
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

// CleanupUnusedDCRClients hard-deletes dynamically-registered (DCR) clients
// created more than ttl ago that never completed a token exchange. Eligibility
// is last_used_at IS NULL (set on first issuance by SaveRefreshToken), which is
// race-free: a client that completed an authorization-code flow is retained for
// good, even during the brief tokenless window of refresh-token rotation or
// after its refresh token later expires. Pre-registered (config-file) clients
// have dcr = false and are never eligible, so this only reaps the
// unauthenticated /register endpoint's abandoned registrations.
func (s *Store) CleanupUnusedDCRClients(ctx context.Context, ttl time.Duration) error {
	cutoff := time.Now().Add(-ttl)
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM oauth_clients
		WHERE dcr = true
		  AND last_used_at IS NULL
		  AND created_at < $1`, cutoff)
	if err != nil {
		return fmt.Errorf("cleaning up unused DCR clients: %w", err)
	}
	return nil
}

// StartCleanupRoutine starts a background goroutine that periodically cleans
// up expired authorization codes, refresh tokens, and authorization states,
// and (when dcrTTL > 0) reaps unused dynamically-registered clients older than
// dcrTTL.
func (s *Store) StartCleanupRoutine(interval, dcrTTL time.Duration) {
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
				s.runPeriodicCleanup(ctx, dcrTTL)
			}
		}
	}()
}

// runPeriodicCleanup runs one sweep of the expiry/retention cleanups. DCR
// client cleanup runs only when dcrTTL > 0. Each step logs and continues on
// error so one failing sweep does not skip the others.
func (s *Store) runPeriodicCleanup(ctx context.Context, dcrTTL time.Duration) {
	if err := s.CleanupExpiredCodes(ctx); err != nil {
		slog.Warn("oauth store cleanup: expired codes", logKeyError, err)
	}
	if err := s.CleanupExpiredTokens(ctx); err != nil {
		slog.Warn("oauth store cleanup: expired tokens", logKeyError, err)
	}
	if err := s.CleanupExpiredStates(ctx, oauth.StateMaxAge); err != nil {
		slog.Warn("oauth store cleanup: expired states", logKeyError, err)
	}
	if dcrTTL > 0 {
		if err := s.CleanupUnusedDCRClients(ctx, dcrTTL); err != nil {
			slog.Warn("oauth store cleanup: unused DCR clients", logKeyError, err)
		}
	}
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
		&client.CreatedAt, &client.Active, &client.DynamicallyRegistered,
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
		&client.CreatedAt, &client.Active, &client.DynamicallyRegistered,
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
