package apigateway

import (
	"context"
	"errors"
	"fmt"

	"github.com/txn2/mcp-data-platform/pkg/connoauth"
)

// NewConnOAuthTokenStore returns a TokenStore implementation backed
// by the unified connoauth.Store (connection_oauth_tokens table).
// Used by the platform's WireAPIGatewayTokenStore when a database is
// configured — replaces the per-kind apigateway_oauth_tokens path so
// the admin layer's unified OAuth handler and the toolkit's
// Authenticator agree on the underlying storage row.
//
// This adapter exists because the toolkit's existing TokenStore
// interface keys on a bare connection name while connoauth.Store
// keys on (kind, name). Wrapping the unified store keeps the toolkit
// code unchanged during the rollout — a follow-up may replace the
// in-toolkit oauth2AuthorizationCodeAuth with a direct
// connoauth.Source so this shim becomes unnecessary.
func NewConnOAuthTokenStore(store connoauth.Store) TokenStore {
	return &connOAuthTokenStore{store: store}
}

type connOAuthTokenStore struct {
	store connoauth.Store
}

// Get reads the row for connection from the unified table under
// kind="api" (the HTTP API gateway toolkit kind).
func (s *connOAuthTokenStore) Get(ctx context.Context, connection string) (*PersistedToken, error) {
	p, err := s.store.Get(ctx, connoauth.Key{Kind: connoauth.KindAPI, Name: connection})
	if err != nil {
		if errors.Is(err, connoauth.ErrTokenNotFound) {
			return nil, ErrTokenNotFound
		}
		return nil, fmt.Errorf("apigateway: load oauth token: %w", err)
	}
	return &PersistedToken{
		ConnectionName:   p.Key.Name,
		AccessToken:      p.AccessToken,
		RefreshToken:     p.RefreshToken,
		ExpiresAt:        p.ExpiresAt,
		RefreshExpiresAt: p.RefreshExpiresAt,
		Scope:            p.Scope,
		AuthenticatedBy:  p.AuthenticatedBy,
		AuthenticatedAt:  p.AuthenticatedAt,
		UpdatedAt:        p.UpdatedAt,
	}, nil
}

// Set writes the toolkit's PersistedToken into the unified table
// under kind="api".
func (s *connOAuthTokenStore) Set(ctx context.Context, t PersistedToken) error {
	if err := s.store.Set(ctx, connoauth.PersistedToken{
		Key:              connoauth.Key{Kind: connoauth.KindAPI, Name: t.ConnectionName},
		AccessToken:      t.AccessToken,
		RefreshToken:     t.RefreshToken,
		ExpiresAt:        t.ExpiresAt,
		RefreshExpiresAt: t.RefreshExpiresAt,
		Scope:            t.Scope,
		AuthenticatedBy:  t.AuthenticatedBy,
		AuthenticatedAt:  t.AuthenticatedAt,
	}); err != nil {
		return fmt.Errorf("apigateway: persist oauth token: %w", err)
	}
	return nil
}

// Delete removes the row for connection from the unified table.
func (s *connOAuthTokenStore) Delete(ctx context.Context, connection string) error {
	if err := s.store.Delete(ctx, connoauth.Key{Kind: connoauth.KindAPI, Name: connection}); err != nil {
		return fmt.Errorf("apigateway: delete oauth token: %w", err)
	}
	return nil
}
