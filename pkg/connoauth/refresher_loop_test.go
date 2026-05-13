package connoauth

import (
	"context"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/authevents"
)

// TestRefresherTickListsRowsAndSkipsNoRefreshToken exercises the
// tick() path through to processRow() for a row without a refresh
// token. Coverage target: tick + processRow's no-refresh branch.
func TestRefresherTickListsRowsAndSkipsNoRefreshToken(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	// Seed a row with no refresh token — processRow's first guard
	// kicks in and returns immediately.
	_ = store.Set(context.Background(), PersistedToken{
		Key:         Key{Kind: KindMCP, Name: "alpha"},
		AccessToken: "at",
	})
	r := NewRefresher(store, stubConfigResolver{}, nil, NoopLocker{}, RefresherConfig{})
	r.now = func() time.Time { return time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC) }
	r.tick(context.Background())
	// reaching here without panic asserts the tick + processRow path.
}

// TestRefresherTickProcessesRowWithRefreshToken exercises the full
// path through processRow for a row with a refresh token: config
// resolution, shouldRefresh check, lock acquire, Reacquire call.
// The stub resolver returns a config that points at a non-existent
// IdP so Reacquire fails — but the test target is the path, not the
// result.
func TestRefresherTickProcessesRowWithRefreshToken(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	_ = store.Set(context.Background(), PersistedToken{
		Key:          Key{Kind: KindMCP, Name: "alpha"},
		AccessToken:  "at",
		RefreshToken: "rt",
		// Already-expired access token forces shouldRefresh to true.
		ExpiresAt: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	writer := authevents.NewWriter(authevents.NewMemoryStore(), nil)
	r := NewRefresher(store, stubConfigResolver{
		cfg: Config{TokenURL: "http://127.0.0.1:1/never-listens"},
	}, writer, NoopLocker{}, RefresherConfig{})
	r.now = func() time.Time { return time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC) }
	r.tick(context.Background())
	// reaching here without panic asserts the tick+processRow+lock+
	// Reacquire path. The Reacquire surfaces a transport error which
	// processRow logs but doesn't propagate.
}

// TestRefresherProcessRowResolverNonSentinelError exercises the
// branch where ResolveConfig returns a non-ErrConfigNotResolvable
// error (transient resolver failure). The row is skipped with a
// warn log rather than treated as missing.
func TestRefresherProcessRowResolverNonSentinelError(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	r := NewRefresher(store, transientResolverErr{}, nil, NoopLocker{}, RefresherConfig{})
	r.now = func() time.Time { return time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC) }
	r.processRow(context.Background(), PersistedToken{
		Key:          Key{Kind: KindMCP, Name: "alpha"},
		RefreshToken: refreshTokenSentinel,
		ExpiresAt:    time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
	})
}

type transientResolverErr struct{}

func (transientResolverErr) ResolveConfig(_ context.Context, _ Key) (Config, error) {
	return Config{}, &transientError{}
}
func (transientResolverErr) MaxLifetime(_ context.Context, _ Key) time.Duration { return 0 }

type transientError struct{}

func (*transientError) Error() string { return "transient: db unreachable" }
