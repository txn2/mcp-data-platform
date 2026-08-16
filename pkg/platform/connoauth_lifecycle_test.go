package platform

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/txn2/mcp-data-platform/pkg/connoauth"
)

// stubConfigResolver satisfies connoauth.ConfigResolver for the
// platform-level delegation smoke tests below. ResolveConfig always
// returns ErrConfigNotResolvable so the refresher's per-row processing
// is a no-op and we exercise the Start/Stop scaffolding without needing
// a real IdP. The connauth package owns the deeper refresher/prune
// lifecycle tests; these assert only that the Platform methods delegate
// through the connauth.Handle correctly.
type stubConfigResolver struct{}

func (stubConfigResolver) ResolveConfig(_ context.Context, _ connoauth.Key) (connoauth.Config, error) {
	return connoauth.Config{}, connoauth.ErrConfigNotResolvable
}

func (stubConfigResolver) MaxLifetime(_ context.Context, _ connoauth.Key) time.Duration {
	return 0
}

func TestStartConnOAuthRefresherNilHandleIsNoop(t *testing.T) {
	t.Parallel()
	// No connAuth handle (no DB) → StartConnOAuthRefresher must be a
	// safe no-op and StopConnOAuthRefresher must report nothing to stop.
	p := &Platform{}
	p.StartConnOAuthRefresher(stubConfigResolver{}, false)
	if err := p.StopConnOAuthRefresher(context.Background()); err != nil {
		t.Errorf("Stop after no-op start = %v, want nil", err)
	}
}

func TestStartConnOAuthRefresherNilResolverIsNoop(t *testing.T) {
	t.Parallel()
	p := &Platform{connAuth: newTestConnAuth(t)}
	p.StartConnOAuthRefresher(nil, false)
	// Nothing started; Stop is a clean no-op.
	if err := p.StopConnOAuthRefresher(context.Background()); err != nil {
		t.Errorf("Stop after nil-resolver start = %v, want nil", err)
	}
}

func TestStartConnOAuthRefresherSingleReplicaRoundTrip(t *testing.T) {
	t.Parallel()
	p := &Platform{connAuth: newTestConnAuth(t)}
	p.StartConnOAuthRefresher(stubConfigResolver{}, false)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := p.StopConnOAuthRefresher(ctx); err != nil {
		t.Fatalf("StopConnOAuthRefresher: %v", err)
	}
}

func TestStartConnOAuthRefresherMultiReplicaSelectsPostgresLocker(t *testing.T) {
	t.Parallel()
	// With multiReplica=true and a non-nil p.db, the platform selects the
	// PostgresLocker branch (vs the NoopLocker default). We can't observe
	// the locker from outside connauth, so this test exercises the branch
	// for coverage and asserts the start/stop round-trip still succeeds.
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	p := &Platform{db: db, connAuth: newTestConnAuth(t)}
	p.StartConnOAuthRefresher(stubConfigResolver{}, true)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := p.StopConnOAuthRefresher(ctx); err != nil {
		t.Fatalf("StopConnOAuthRefresher: %v", err)
	}
}

func TestStopConnOAuthRefresherWithCanceledContext(t *testing.T) {
	t.Parallel()
	// A started refresher plus an already-canceled context. Which arm of
	// connoauth's stop-wait wins — the loop's done channel or the canceled
	// context — is a goroutine-scheduling race this level cannot control
	// (the store the loop ticks against is owned by connauth.Handle), so
	// the assertion is on the contract both arms satisfy: Stop returns
	// promptly, and any error it returns carries the platform's wrap over
	// the context error. connoauth owns the deterministic proof that a
	// still-running loop takes the context arm
	// (TestRefresherStopSurfacesContextErrorWhileTickInFlight).
	p := &Platform{connAuth: newTestConnAuth(t)}
	p.StartConnOAuthRefresher(stubConfigResolver{}, false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := p.StopConnOAuthRefresher(ctx)
	if err == nil {
		// The loop drained before the wait began — an equally successful stop.
		return
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error %v does not wrap context.Canceled", err)
	}
	if !strings.Contains(err.Error(), "stop connoauth refresher") {
		t.Errorf("error %v is missing the platform-level wrap", err)
	}
}

func TestStopConnOAuthRefresherNoopWhenNeverStarted(t *testing.T) {
	t.Parallel()
	p := &Platform{}
	if err := p.StopConnOAuthRefresher(context.Background()); err != nil {
		t.Errorf("StopConnOAuthRefresher with no start should be no-op, got %v", err)
	}
}

func TestStopConnOAuthRefresherDuringShutdownNoopWhenNil(t *testing.T) {
	t.Parallel()
	p := &Platform{}
	var errs []error
	p.stopConnOAuthRefresherDuringShutdown(&errs)
	if len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestStopConnOAuthRefresherDuringShutdownStopsStartedRefresher(t *testing.T) {
	t.Parallel()
	p := &Platform{connAuth: newTestConnAuth(t)}
	p.StartConnOAuthRefresher(stubConfigResolver{}, false)
	var errs []error
	p.stopConnOAuthRefresherDuringShutdown(&errs)
	if len(errs) != 0 {
		t.Errorf("expected clean shutdown, got %v", errs)
	}
}

func TestAuthEventStoreNilWhenNoHandle(t *testing.T) {
	t.Parallel()
	p := &Platform{}
	if got := p.AuthEventStore(); got != nil {
		t.Errorf("AuthEventStore() with nil handle = %v, want nil", got)
	}
}

func TestCloseAuthEventStoreNoopWhenNil(t *testing.T) {
	t.Parallel()
	p := &Platform{}
	var errs []error
	p.closeAuthEventStore(&errs)
	if len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestAuthEventWriterNilSafeWhenNoHandle(t *testing.T) {
	t.Parallel()
	p := &Platform{}
	// Writer is nil — but the Writer's methods are nil-safe, so the
	// nil return is still usable downstream without a nil-check.
	w := p.AuthEventWriter()
	if w != nil {
		t.Errorf("AuthEventWriter() with no handle = %v, want nil", w)
	}
	w.TokenDeletedAdmin(context.Background(), "k", "n", "actor") // must not panic
}
