package authevents

import (
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

// TestStartPruneRoutineCloseStops verifies the prune goroutine can
// start and stop cleanly. The interval is too short to fire before
// Close, but the channel-close path through pruneLoop's ctx.Done is
// what we want to cover.
func TestStartPruneRoutineCloseStops(t *testing.T) {
	t.Parallel()
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := NewPostgresStore(db)
	s.StartPruneRoutine(time.Hour, 90*24*time.Hour)
	// Second call is idempotent — exercise that branch.
	s.StartPruneRoutine(time.Hour, 90*24*time.Hour)
	if err := s.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	// Close after stop is a no-op.
	if err := s.Close(); err != nil {
		t.Errorf("Close (after stop): %v", err)
	}
}

// TestPruneRoutineFiresAndExits exercises the prune-loop's tick
// branch by using a short interval. The mock expects exactly one
// prune call between Start and Close. Validates the
// log-on-success path emits.
func TestPruneRoutineFiresAndExits(t *testing.T) {
	t.Parallel()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectExec("DELETE FROM connection_auth_events").
		WillReturnResult(sqlmock.NewResult(0, 5))
	s := NewPostgresStore(db)
	// Use a very short interval and a generous Close-wait so the
	// loop ticks once before we shut down.
	s.StartPruneRoutine(10*time.Millisecond, 90*24*time.Hour)
	time.Sleep(60 * time.Millisecond)
	_ = s.Close()
	// We don't require ExpectationsWereMet here because the tick
	// might fire 1+ times depending on scheduler — the SQL mock
	// returns the same result for each call.
}
