package connoauth

import (
	"context"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/authevents"
)

type stubConfigResolver struct {
	cfg     Config
	maxLife time.Duration
	err     error
}

func (s stubConfigResolver) ResolveConfig(_ context.Context, _ Key) (Config, error) {
	return s.cfg, s.err
}

func (s stubConfigResolver) MaxLifetime(_ context.Context, _ Key) time.Duration {
	return s.maxLife
}

func TestShouldRefreshAccessTokenWindow(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	r := &Refresher{
		cfg: RefresherConfig{}.withDefaults(),
		now: func() time.Time { return now },
	}
	// Access expires in 4 minutes → within the 5-minute lead window.
	row := PersistedToken{ExpiresAt: now.Add(4 * time.Minute), RefreshToken: "rt"}
	if !r.shouldRefresh(row, 0) {
		t.Error("should refresh when access token within access lead window")
	}
	// Access expires in 10 minutes → outside the window, no other signal.
	row.ExpiresAt = now.Add(10 * time.Minute)
	if r.shouldRefresh(row, 0) {
		t.Error("should NOT refresh when no signal is within lead window")
	}
}

func TestShouldRefreshRefreshDeadlineWindow(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	r := &Refresher{
		cfg: RefresherConfig{}.withDefaults(),
		now: func() time.Time { return now },
	}
	row := PersistedToken{
		ExpiresAt:        now.Add(2 * time.Hour),    // not within access lead
		RefreshExpiresAt: now.Add(30 * time.Minute), // within 1h refresh lead
		RefreshToken:     "rt",
	}
	if !r.shouldRefresh(row, 0) {
		t.Error("should refresh when refresh deadline within lead window")
	}
}

func TestShouldRefreshSyntheticMaxLifetime(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	r := &Refresher{
		cfg: RefresherConfig{}.withDefaults(),
		now: func() time.Time { return now },
	}
	// Token issued 59.5 days ago; configured max 60d. Synthetic
	// deadline is 12h from now — well within the 1h refresh lead?
	// 12h is BEYOND 1h, so should NOT refresh yet.
	row := PersistedToken{
		ExpiresAt:       now.Add(2 * time.Hour),
		AuthenticatedAt: now.Add(-59*24*time.Hour - 12*time.Hour),
		RefreshToken:    "rt",
	}
	if r.shouldRefresh(row, 60*24*time.Hour) {
		t.Error("should NOT refresh when synthetic deadline > refresh lead time")
	}
	// Token issued 59 days 23 hours ago: synthetic deadline is 1
	// hour from now — within the lead window.
	row.AuthenticatedAt = now.Add(-59*24*time.Hour - 23*time.Hour)
	if !r.shouldRefresh(row, 60*24*time.Hour) {
		t.Error("should refresh when synthetic deadline within refresh lead window")
	}
}

func TestProcessRowSkipsNoRefreshToken(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	r := NewRefresher(store, stubConfigResolver{}, nil, NoopLocker{}, RefresherConfig{})
	r.now = func() time.Time { return time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC) }
	// processRow returns early when refresh_token is empty.
	r.processRow(context.Background(), PersistedToken{
		Key:       Key{Kind: KindMCP, Name: "x"},
		ExpiresAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	// No panic, no errors — reaching here is the assertion.
}

func TestProcessRowSkipsConfigNotResolvable(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	r := NewRefresher(store, stubConfigResolver{err: ErrConfigNotResolvable},
		nil, NoopLocker{}, RefresherConfig{})
	r.now = func() time.Time { return time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC) }
	r.processRow(context.Background(), PersistedToken{
		Key:          Key{Kind: KindMCP, Name: "deleted-conn"},
		ExpiresAt:    time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		RefreshToken: "rt",
	})
	// reaching here without panic asserts the skip-on-NotResolvable path.
}

func TestNoopLockerAlwaysGrants(t *testing.T) {
	t.Parallel()
	release, ok, err := NoopLocker{}.TryLock(context.Background(), Key{Kind: "mcp", Name: "x"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !ok {
		t.Error("NoopLocker.TryLock should always grant")
	}
	release() // must not panic
}

type alwaysContendedLocker struct{}

func (alwaysContendedLocker) TryLock(_ context.Context, _ Key) (release func(), ok bool, err error) {
	return nil, false, nil
}

func TestProcessRowSkipsOnLockContention(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	_ = store.Set(context.Background(), PersistedToken{
		Key:          Key{Kind: KindMCP, Name: "x"},
		AccessToken:  "at",
		RefreshToken: "rt",
		ExpiresAt:    time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	r := NewRefresher(store, stubConfigResolver{cfg: Config{TokenURL: "https://idp/token"}},
		authevents.NewWriter(authevents.NewMemoryStore(), nil),
		alwaysContendedLocker{}, RefresherConfig{})
	r.now = func() time.Time { return time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC) }
	// Should return without panicking. The contended lock skips the
	// Reacquire path entirely.
	for _, row := range []PersistedToken{
		{
			Key: Key{Kind: KindMCP, Name: "x"}, RefreshToken: "rt",
			ExpiresAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		},
	} {
		r.processRow(context.Background(), row)
	}
}

func TestRefresherStartStop(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	r := NewRefresher(store, stubConfigResolver{}, nil, NoopLocker{},
		RefresherConfig{Interval: time.Hour})
	r.Start()
	// Second Start is a no-op.
	r.Start()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := r.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	// Stop after stop is a no-op.
	if err := r.Stop(context.Background()); err != nil {
		t.Fatalf("Stop(after-stop): %v", err)
	}
}

func TestRefresherConfigDefaults(t *testing.T) {
	t.Parallel()
	c := RefresherConfig{}.withDefaults()
	if c.Interval != defaultRefresherInterval {
		t.Errorf("Interval = %v, want %v", c.Interval, defaultRefresherInterval)
	}
	if c.AccessLeadTime != defaultAccessLeadTime {
		t.Errorf("AccessLeadTime = %v, want %v", c.AccessLeadTime, defaultAccessLeadTime)
	}
	if c.RefreshLeadTime != defaultRefreshLeadTime {
		t.Errorf("RefreshLeadTime = %v, want %v", c.RefreshLeadTime, defaultRefreshLeadTime)
	}
}

func TestAdvisoryLockKeyStable(t *testing.T) {
	t.Parallel()
	a := advisoryLockKey(Key{Kind: "mcp", Name: "alpha"})
	b := advisoryLockKey(Key{Kind: "mcp", Name: "alpha"})
	if a != b {
		t.Errorf("advisoryLockKey should be deterministic: %d != %d", a, b)
	}
	c := advisoryLockKey(Key{Kind: "api", Name: "alpha"})
	if a == c {
		t.Errorf("advisoryLockKey should differ across kinds for same name")
	}
}
