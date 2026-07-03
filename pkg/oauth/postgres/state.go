package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/oauth"
)

// The Store also implements oauth.StateStore, persisting in-flight
// authorization state (the link between an /oauth/authorize redirect and
// its upstream IdP callback) to the oauth_authorization_states table. In
// multi-replica deployments the callback can land on a different replica
// than the one that started the flow, so this state cannot live in
// process memory. The payload is an opaque JSON blob: it is only ever
// fetched by key, never queried by field.
var _ oauth.StateStore = (*Store)(nil)

// SaveState stores an authorization state, replacing any existing state
// under the same key (the prompt=none retry path re-saves the state with
// PromptNoneAttempted set).
func (s *Store) SaveState(ctx context.Context, key string, state *oauth.AuthorizationState) error {
	payload, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshaling authorization state: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO oauth_authorization_states (state_key, payload, created_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (state_key) DO UPDATE SET payload = EXCLUDED.payload`,
		key, payload, state.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("inserting authorization state: %w", err)
	}
	return nil
}

// GetState retrieves an authorization state.
func (s *Store) GetState(ctx context.Context, key string) (*oauth.AuthorizationState, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT payload FROM oauth_authorization_states WHERE state_key = $1`, key)

	var payload []byte
	if err := row.Scan(&payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, oauth.ErrStateNotFound
		}
		return nil, fmt.Errorf("scanning authorization state: %w", err)
	}

	var state oauth.AuthorizationState
	if err := json.Unmarshal(payload, &state); err != nil {
		return nil, fmt.Errorf("unmarshaling authorization state: %w", err)
	}
	return &state, nil
}

// DeleteState removes an authorization state.
func (s *Store) DeleteState(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM oauth_authorization_states WHERE state_key = $1`, key)
	if err != nil {
		return fmt.Errorf("deleting authorization state: %w", err)
	}
	return nil
}

// CleanupExpiredStates removes states older than maxAge.
func (s *Store) CleanupExpiredStates(ctx context.Context, maxAge time.Duration) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM oauth_authorization_states
		WHERE created_at <= NOW() - make_interval(secs => $1)`,
		maxAge.Seconds(),
	)
	if err != nil {
		return fmt.Errorf("cleaning up expired authorization states: %w", err)
	}
	return nil
}
