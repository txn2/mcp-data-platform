package connauth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"go.uber.org/goleak"

	"github.com/txn2/mcp-data-platform/pkg/authevents"
	"github.com/txn2/mcp-data-platform/pkg/connoauth"
	"github.com/txn2/mcp-data-platform/pkg/platform/fieldcrypt"
)

// TestMain fails the package if any test leaks a goroutine. New starts the
// auth-event prune routine and StartRefresher starts the refresh loop, so this
// guards the shutdown contract: every Handle a test constructs must be Closed
// and every started refresher Stopped.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// stubResolver satisfies connoauth.ConfigResolver. ResolveConfig always returns
// ErrConfigNotResolvable so the refresher's per-row processing is a no-op — the
// Start/Stop scaffolding is exercised without a real IdP.
type stubResolver struct{}

func (stubResolver) ResolveConfig(_ context.Context, _ connoauth.Key) (connoauth.Config, error) {
	return connoauth.Config{}, connoauth.ErrConfigNotResolvable
}

func (stubResolver) MaxLifetime(_ context.Context, _ connoauth.Key) time.Duration {
	return 0
}

// newHandle builds a Handle over a sqlmock-backed *sql.DB and registers cleanup
// that closes both the handle (reaping the prune goroutine) and the mock db.
// The prune routine's first tick is 24h out, so the mock is never queried by it.
func newHandle(t *testing.T) *Handle {
	t.Helper()
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	h := New(db, nil)
	t.Cleanup(func() {
		_ = h.Close()
		_ = db.Close()
	})
	return h
}

func TestNew_NilDBReturnsNil(t *testing.T) {
	t.Parallel()
	if h := New(nil, nil); h != nil {
		t.Fatalf("New(nil db) = %v, want nil (no-op without a database)", h)
	}
}

func TestNew_WiresStoreWriterAndAuthEventStore(t *testing.T) {
	t.Parallel()
	h := newHandle(t)
	if h == nil {
		t.Fatal("New with a db must return a non-nil handle")
	}
	if h.Store() == nil {
		t.Error("Store() = nil, want the connection_oauth_tokens store")
	}
	if h.AuthEventStore() == nil {
		t.Error("AuthEventStore() = nil, want the auth-event store")
	}
	if h.AuthEventWriter() == nil {
		t.Error("AuthEventWriter() = nil, want a writer over the auth-event store")
	}
}

func TestNew_WithEncryptorWiresStore(t *testing.T) {
	t.Parallel()
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	// A non-nil encryptor exercises the pass-through branch (the store
	// receives the platform's at-rest encryptor rather than the noop
	// fallback). NewRestFieldEncryptor(nil) is a non-nil *RestFieldEncryptor.
	h := New(db, fieldcrypt.NewRestFieldEncryptor(nil))
	t.Cleanup(func() {
		_ = h.Close()
		_ = db.Close()
	})
	if h.Store() == nil {
		t.Error("Store() = nil, want the encrypted-store handle")
	}
}

func TestNilHandleAccessorsAreSafe(t *testing.T) {
	t.Parallel()
	var h *Handle
	if h.Store() != nil {
		t.Error("nil handle Store() must be nil")
	}
	if h.AuthEventStore() != nil {
		t.Error("nil handle AuthEventStore() must be nil")
	}
	if h.AuthEventWriter() != nil {
		t.Error("nil handle AuthEventWriter() must be nil")
	}
	// The nil-safe contract: even the nil writer's methods short-circuit.
	h.AuthEventWriter().TokenDeletedAdmin(context.Background(), "k", "n", "actor")
	// Stop / Close / StartRefresher must all no-op on a nil handle.
	if err := h.Stop(context.Background()); err != nil {
		t.Errorf("nil handle Stop() = %v, want nil", err)
	}
	if err := h.Close(); err != nil {
		t.Errorf("nil handle Close() = %v, want nil", err)
	}
	h.StartRefresher(stubResolver{}, connoauth.NoopLocker{}) // must not panic
}

func TestAuthEventWriterIsNilSafe(t *testing.T) {
	t.Parallel()
	// Explicit nil *authevents.Writer confirms the platform contract that a
	// writer returned from the handle can be used without nil-checks.
	var w *authevents.Writer
	w.TokenDeletedAdmin(context.Background(), "k", "n", "actor") // must not panic
}

func TestStartRefresher_NilResolverNoop(t *testing.T) {
	t.Parallel()
	h := newHandle(t)
	h.StartRefresher(nil, connoauth.NoopLocker{})
	if h.refresher != nil {
		t.Error("nil resolver must not build a refresher")
	}
}

func TestStartRefresher_StartStopRoundTrip(t *testing.T) {
	t.Parallel()
	h := newHandle(t)
	h.StartRefresher(stubResolver{}, connoauth.NoopLocker{})
	if h.refresher == nil {
		t.Fatal("StartRefresher must build the refresh loop")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := h.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestStartRefresher_IdempotentDoesNotLeak(t *testing.T) {
	t.Parallel()
	h := newHandle(t)
	h.StartRefresher(stubResolver{}, connoauth.NoopLocker{})
	first := h.refresher
	// A second call must be a no-op: the refresher is built once, so the
	// first loop's goroutine is not orphaned. goleak (TestMain) enforces this.
	h.StartRefresher(stubResolver{}, connoauth.NoopLocker{})
	if h.refresher != first {
		t.Error("StartRefresher rebuilt the refresher on a repeat call")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := h.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestStop_WrapsErrorWhenContextAlreadyDone(t *testing.T) {
	// Not parallel: the refresher loop goroutine is left to wind down on its
	// own (Stop canceled its context but returned before the loop closed done);
	// TestMain's goleak check retries long enough for it to exit.
	h := newHandle(t)
	h.StartRefresher(stubResolver{}, connoauth.NoopLocker{})
	// A pre-canceled wait context: Stop calls cancel() on the loop and then
	// selects between the loop's done channel and ctx.Done(). done cannot have
	// closed yet (the loop goroutine has not been scheduled), so the already-done
	// ctx wins and Stop returns the wrapped error rather than a clean nil.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := h.Stop(ctx)
	if err == nil {
		t.Fatal("Stop with an already-canceled context must return the wrapped error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Stop error = %v, want a wrapped context.Canceled", err)
	}
}

func TestStop_NoopWhenNeverStarted(t *testing.T) {
	t.Parallel()
	h := newHandle(t)
	if err := h.Stop(context.Background()); err != nil {
		t.Errorf("Stop with no refresher started = %v, want nil", err)
	}
}

func TestClose_StopsPruneRoutineIdempotently(t *testing.T) {
	t.Parallel()
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer func() { _ = db.Close() }()

	h := New(db, nil)
	// First Close reaps the prune goroutine; a second is a safe no-op.
	if err := h.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := h.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
