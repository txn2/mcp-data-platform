package connoauth

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Store persists OAuth tokens for the authorization_code grant so a
// one-time browser-based authentication grants long-running background
// access. Keyed by (kind, name) so the same backing table serves
// multiple connection-toolkit families without collision.
type Store interface {
	// Get returns the persisted token for the key or ErrTokenNotFound
	// when no row exists.
	Get(ctx context.Context, key Key) (*PersistedToken, error)
	// Set inserts or replaces the token row for the key.
	Set(ctx context.Context, t PersistedToken) error
	// Delete removes the token row, forcing a re-auth on the next
	// call. Returns nil (not ErrTokenNotFound) for missing rows so
	// idempotent cleanup callers don't need to special-case absence.
	Delete(ctx context.Context, key Key) error
	// List returns metadata for every persisted row. Used by the
	// background refresher to decide which connections need
	// proactive refresh. AccessToken and RefreshToken are NOT
	// populated in the returned slice — the refresher only needs
	// deadlines, kind, and name to pick targets; the per-row Get
	// loads the secret material when it actually refreshes.
	List(ctx context.Context) ([]PersistedToken, error)
}

// FieldEncryptor abstracts the platform's at-rest field encryption so
// this package doesn't import pkg/platform (which would create a
// dependency cycle via the toolkit composition wiring). The platform
// supplies its concrete FieldEncryptor at startup; the noop fallback
// below is used in dev when ENCRYPTION_KEY is unset (the platform
// logs a startup warning on this code path).
type FieldEncryptor interface {
	Encrypt(plaintext string) (string, error)
	Decrypt(ciphertext string) (string, error)
}

// noopEncryptor is the fallback used when no FieldEncryptor is wired.
// Storing refresh tokens unencrypted is poor practice; the platform
// surfaces a startup warning when this path is active so operators
// know to set the encryption key in production.
type noopEncryptor struct{}

// Encrypt returns the plaintext unchanged.
func (noopEncryptor) Encrypt(s string) (string, error) { return s, nil }

// Decrypt returns the input unchanged.
func (noopEncryptor) Decrypt(s string) (string, error) { return s, nil }

// errInvalidKey is the validation error returned by every Store
// method when called with an unpopulated Key. Internal — the public
// surface returns it wrapped via fmt.Errorf so callers see the
// method name in stack traces.
var errInvalidKey = errors.New("connoauth: invalid key (kind and name required)")

// MemoryStore is a process-local Store used in tests and as a
// fallback when no database is configured. Tokens DO NOT survive
// process restarts.
type MemoryStore struct {
	mu     sync.Mutex
	tokens map[Key]PersistedToken
}

// NewMemoryStore returns an in-process Store. Production deployments
// use NewPostgresStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{tokens: map[Key]PersistedToken{}}
}

// Get returns the in-memory token or ErrTokenNotFound.
func (s *MemoryStore) Get(_ context.Context, key Key) (*PersistedToken, error) {
	if !key.IsValid() {
		return nil, errInvalidKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tokens[key]
	if !ok {
		return nil, ErrTokenNotFound
	}
	return &t, nil
}

// Set stores a token in process memory, stamping UpdatedAt.
func (s *MemoryStore) Set(_ context.Context, t PersistedToken) error {
	if !t.Key.IsValid() {
		return errInvalidKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t.UpdatedAt = time.Now().UTC()
	s.tokens[t.Key] = t
	return nil
}

// Delete removes the in-memory token row for the key. Idempotent.
func (s *MemoryStore) Delete(_ context.Context, key Key) error {
	if !key.IsValid() {
		return errInvalidKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tokens, key)
	return nil
}

// List returns metadata for every persisted row. AccessToken is
// blanked and RefreshToken is replaced with the refreshTokenSentinel
// when the underlying row has a non-empty refresh token (and left
// empty otherwise). The sentinel matches PostgresStore.List's
// behavior so the Refresher's `if row.RefreshToken == "" { return }`
// branch works the same against either backend — without this
// alignment, a Refresher backed by MemoryStore would silently skip
// every row.
func (s *MemoryStore) List(_ context.Context) ([]PersistedToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]PersistedToken, 0, len(s.tokens))
	for _, t := range s.tokens {
		hasRefresh := t.RefreshToken != ""
		t.AccessToken = ""
		t.RefreshToken = ""
		if hasRefresh {
			t.RefreshToken = refreshTokenSentinel
		}
		out = append(out, t)
	}
	return out, nil
}

// refreshTokenSentinel is the marker both Store backends emit from
// List to signal "this row has a refresh token, but the actual value
// has not been loaded." Distinct from "" so the Refresher's
// no-refresh-token skip branch can distinguish.
const refreshTokenSentinel = "(present)"
