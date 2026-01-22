package oauth

import (
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

	// CodeChallengeMethod is the PKCE method (S256 or plain).
	CodeChallengeMethod string

	// Scope is the requested scope.
	Scope string

	// UpstreamState is the state for the upstream IdP (e.g., Keycloak).
	UpstreamState string

	// CreatedAt is when this state was created.
	CreatedAt time.Time
}

// StateStore manages authorization states for the OAuth flow.
type StateStore interface {
	// Save stores an authorization state.
	Save(key string, state *AuthorizationState) error

	// Get retrieves an authorization state.
	Get(key string) (*AuthorizationState, error)

	// Delete removes an authorization state.
	Delete(key string) error

	// Cleanup removes expired states.
	Cleanup(maxAge time.Duration) error
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

// Save stores an authorization state.
func (s *MemoryStateStore) Save(key string, state *AuthorizationState) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.states[key] = state
	return nil
}

// Get retrieves an authorization state.
func (s *MemoryStateStore) Get(key string) (*AuthorizationState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	state, ok := s.states[key]
	if !ok {
		return nil, ErrStateNotFound
	}
	return state, nil
}

// Delete removes an authorization state.
func (s *MemoryStateStore) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.states, key)
	return nil
}

// Cleanup removes states older than maxAge.
func (s *MemoryStateStore) Cleanup(maxAge time.Duration) error {
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
