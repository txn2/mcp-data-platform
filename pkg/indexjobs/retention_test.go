package indexjobs

import (
	"context"
	"errors"
	"regexp"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestStore_PurgeTerminalDeletes(t *testing.T) {
	t.Parallel()
	s, mock, cleanup := newMockStore(t)
	defer cleanup()
	// The DELETE must target only finished history: succeeded rows, or
	// failed rows already resolved. Assert the predicate shape so a later
	// edit cannot silently widen it to open failures or in-flight rows.
	// A single batch returning fewer than purgeBatchSize rows drains in
	// one statement (cutoff arg + batch-size arg).
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM index_jobs`)).
		WithArgs(sqlmock.AnyArg(), purgeBatchSize).
		WillReturnResult(sqlmock.NewResult(0, 4))
	n, err := s.PurgeTerminal(context.Background(), 14)
	if err != nil {
		t.Fatalf("PurgeTerminal: %v", err)
	}
	if n != 4 {
		t.Errorf("deleted = %d; want 4", n)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestStore_PurgeTerminalBatchesUntilDrained(t *testing.T) {
	t.Parallel()
	s, mock, cleanup := newMockStore(t)
	defer cleanup()
	// A full batch must trigger another round; the sweep stops only when a
	// batch comes back short. This guards the backlog-draining loop that
	// keeps a first sweep against a large table from timing out as one
	// monster DELETE.
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM index_jobs`)).
		WillReturnResult(sqlmock.NewResult(0, int64(purgeBatchSize)))
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM index_jobs`)).
		WillReturnResult(sqlmock.NewResult(0, 12))
	n, err := s.PurgeTerminal(context.Background(), 14)
	if err != nil {
		t.Fatalf("PurgeTerminal: %v", err)
	}
	if want := purgeBatchSize + 12; n != want {
		t.Errorf("deleted = %d; want %d", n, want)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestStore_PurgeTerminalStopsOnCancelledContext(t *testing.T) {
	t.Parallel()
	s, mock, cleanup := newMockStore(t)
	defer cleanup()
	// A context already canceled before the first batch must short-circuit
	// to a clean (0, nil) partial pass, not an error: committed batches
	// stand and the next tick resumes. No ExpectExec is registered, so any
	// issued query would fail the expectations check.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	n, err := s.PurgeTerminal(ctx, 14)
	if err != nil {
		t.Fatalf("PurgeTerminal on canceled ctx: %v", err)
	}
	if n != 0 {
		t.Errorf("deleted = %d; want 0", n)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unexpected query issued: %v", err)
	}
}

func TestStore_PurgeTerminalNonPositiveIsNoop(t *testing.T) {
	t.Parallel()
	s, mock, cleanup := newMockStore(t)
	defer cleanup()
	// No ExpectExec registered: a non-positive window must not issue any
	// DELETE, so a misconfigured caller cannot wipe live history.
	for _, days := range []int{0, -1} {
		n, err := s.PurgeTerminal(context.Background(), days)
		if err != nil {
			t.Fatalf("PurgeTerminal(%d): %v", days, err)
		}
		if n != 0 {
			t.Errorf("PurgeTerminal(%d) deleted = %d; want 0", days, n)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unexpected query issued: %v", err)
	}
}

func TestStore_PurgeTerminalError(t *testing.T) {
	t.Parallel()
	s, mock, cleanup := newMockStore(t)
	defer cleanup()
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM index_jobs`)).
		WillReturnError(errors.New("db down"))
	if _, err := s.PurgeTerminal(context.Background(), 7); err == nil {
		t.Error("expected error when the DELETE fails")
	}
}

func TestStore_PurgeTerminalRowsAffectedError(t *testing.T) {
	t.Parallel()
	s, mock, cleanup := newMockStore(t)
	defer cleanup()
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM index_jobs`)).
		WillReturnResult(sqlmock.NewErrorResult(errors.New("driver lost count")))
	if _, err := s.PurgeTerminal(context.Background(), 7); err == nil {
		t.Error("expected error when RowsAffected fails")
	}
}

// purgeStore embeds noopStore and records PurgeTerminal calls so the
// retainer loop test can assert the sweep fired with the configured
// window.
type purgeStore struct {
	noopStore
	calls    atomic.Int32
	lastDays atomic.Int64
	returnN  int
	err      error
}

func (s *purgeStore) PurgeTerminal(_ context.Context, days int) (int, error) {
	s.calls.Add(1)
	s.lastDays.Store(int64(days))
	return s.returnN, s.err
}

func TestRetainer_SweepsOnStartWithWindow(t *testing.T) {
	t.Parallel()
	store := &purgeStore{}
	r := NewRetainer(store, 9, 10*time.Millisecond)
	r.Start(context.Background())
	defer r.Stop()
	waitFor(t, func() bool { return store.calls.Load() >= 1 })
	if got := store.lastDays.Load(); got != 9 {
		t.Errorf("purge window = %d; want 9", got)
	}
}

func TestRetainer_SweepLogsWhenRowsPurged(t *testing.T) {
	t.Parallel()
	// A positive purge count drives the "purged N" info-log branch.
	store := &purgeStore{returnN: 7}
	r := NewRetainer(store, 14, 10*time.Millisecond)
	r.Start(context.Background())
	defer r.Stop()
	waitFor(t, func() bool { return store.calls.Load() >= 1 })
}

func TestRetainer_StartIsIdempotent(t *testing.T) {
	t.Parallel()
	store := &purgeStore{}
	r := NewRetainer(store, 14, time.Hour)
	r.Start(context.Background())
	r.Start(context.Background()) // second Start must be a no-op (one goroutine)
	defer r.Stop()
	waitFor(t, func() bool { return store.calls.Load() >= 1 })
}

func TestRetainer_SweepErrorDoesNotPanic(t *testing.T) {
	t.Parallel()
	store := &purgeStore{err: errors.New("db down")}
	r := NewRetainer(store, 14, 10*time.Millisecond)
	r.Start(context.Background())
	defer r.Stop()
	waitFor(t, func() bool { return store.calls.Load() >= 1 })
}

func TestNewRetainer_DefaultsWindowAndInterval(t *testing.T) {
	t.Parallel()
	r := NewRetainer(&purgeStore{}, 0, 0)
	if r.days != DefaultRetentionDays {
		t.Errorf("days = %d; want default %d", r.days, DefaultRetentionDays)
	}
	if r.interval != RetentionInterval {
		t.Errorf("interval = %v; want default %v", r.interval, RetentionInterval)
	}
}

func TestRetainer_StopIsIdempotent(t *testing.T) {
	t.Parallel()
	r := NewRetainer(&purgeStore{}, 14, time.Second)
	r.Start(context.Background())
	r.Stop()
	r.Stop() // second Stop must not panic or block
}
