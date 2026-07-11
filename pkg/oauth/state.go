package oauth

import (
	"context"
	"sync"
	"time"
)

// AuthorizationState holds state for linking upstream IdP callbacks
// to original client authorization requests.
type AuthorizationState struct {
	// ClientID is the OAuth client's client_id (e.g., Claude Desktop).
	ClientID string

	// RedirectURI is where to send the client after authentication.
	RedirectURI string

	// State is the client's original state parameter.
	State string

	// CodeChallenge is the PKCE challenge from the client.
	CodeChallenge string

	// CodeChallengeMethod is the PKCE method (S256; the only supported method).
	CodeChallengeMethod string

	// Scope is the requested scope.
	Scope string

	// UpstreamState is the state for the upstream IdP (e.g., Keycloak).
	UpstreamState string

	// PromptNoneAttempted tracks whether we already tried prompt=none
	// for this flow. Used to prevent infinite redirect loops when the
	// upstream IdP returns login_required.
	PromptNoneAttempted bool

	// CreatedAt is when this state was created.
	CreatedAt time.Time
}

// StateMaxAge is how long an in-flight authorization state stays valid.
// The window spans the user's whole trip through the upstream IdP login
// form, so it is generous relative to the auth-code TTL.
const StateMaxAge = time.Hour

// StateStore manages authorization states for the OAuth flow. In
// multi-replica deployments the store must be shared (database-backed):
// the /oauth/authorize redirect and the upstream IdP callback can land
// on different replicas.
type StateStore interface {
	// SaveState stores an authorization state, replacing any existing
	// state under the same key.
	SaveState(ctx context.Context, key string, state *AuthorizationState) error

	// GetState retrieves an authorization state.
	GetState(ctx context.Context, key string) (*AuthorizationState, error)

	// DeleteState removes an authorization state.
	DeleteState(ctx context.Context, key string) error

	// CleanupExpiredStates removes states older than maxAge.
	CleanupExpiredStates(ctx context.Context, maxAge time.Duration) error
}

// MemoryStateStore is an in-memory implementation of StateStore.
type MemoryStateStore struct {
	mu     sync.RWMutex
	states map[string]*AuthorizationState
}

// NewMemoryStateStore creates a new in-memory state store.
func NewMemoryStateStore() *MemoryStateStore {
	return &MemoryStateStore{
		states: make(map[string]*AuthorizationState),
	}
}

// SaveState stores an authorization state.
func (s *MemoryStateStore) SaveState(_ context.Context, key string, state *AuthorizationState) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.states[key] = state
	return nil
}

// GetState retrieves an authorization state.
func (s *MemoryStateStore) GetState(_ context.Context, key string) (*AuthorizationState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	state, ok := s.states[key]
	if !ok {
		return nil, ErrStateNotFound
	}
	return state, nil
}

// DeleteState removes an authorization state.
func (s *MemoryStateStore) DeleteState(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.states, key)
	return nil
}

// CleanupExpiredStates removes states older than maxAge.
func (s *MemoryStateStore) CleanupExpiredStates(_ context.Context, maxAge time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := time.Now().Add(-maxAge)
	for key, state := range s.states {
		if state.CreatedAt.Before(cutoff) {
			delete(s.states, key)
		}
	}
	return nil
}

// Verify MemoryStateStore implements StateStore.
var _ StateStore = (*MemoryStateStore)(nil)
