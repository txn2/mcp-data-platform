package connlimit

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

var (
	tryLockQ = regexp.QuoteMeta("SELECT pg_try_advisory_lock($1, $2)")
	unlockQ  = regexp.QuoteMeta("SELECT pg_advisory_unlock($1, $2)")
)

func lockRow(got bool) *sqlmock.Rows { return sqlmock.NewRows([]string{"l"}).AddRow(got) }

func sharedLimiter(t *testing.T) (*Limiter, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return New(db, nil), mock
}

// A shared slot is the first free advisory lock among the connection's
// slots; a call finding every slot held waits and looks again, and Release
// unlocks the slot it took.
func TestShared_TakesTheFirstFreeSlotAndWaits(t *testing.T) {
	l, mock := sharedLimiter(t)
	space := keySpace("api", "crm")
	mock.ExpectQuery(tryLockQ).WithArgs(space, int32(0)).WillReturnRows(lockRow(false))
	mock.ExpectQuery(tryLockQ).WithArgs(space, int32(1)).WillReturnRows(lockRow(false))
	mock.ExpectQuery(tryLockQ).WithArgs(space, int32(0)).WillReturnRows(lockRow(false))
	mock.ExpectQuery(tryLockQ).WithArgs(space, int32(1)).WillReturnRows(lockRow(true))
	mock.ExpectExec(unlockQ).WithArgs(space, int32(1)).WillReturnResult(sqlmock.NewResult(0, 0))

	slot, waited, err := l.Acquire(context.Background(), "api", "crm", 2)
	if err != nil {
		t.Fatal(err)
	}
	if waited < firstPoll {
		t.Errorf("a call that found every slot held waited %s, less than one poll", waited)
	}
	slot.Release()
	slot.Release()
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

// A call whose time runs out while every slot is held fails naming the limit.
func TestShared_TimesOutWaiting(t *testing.T) {
	l, mock := sharedLimiter(t)
	for range 20 {
		mock.ExpectQuery(tryLockQ).WillReturnRows(lockRow(false))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*firstPoll)
	defer cancel()
	_, _, err := l.Acquire(ctx, "api", "crm", 1)
	var we *WaitError
	if !errors.As(err, &we) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a wait past the deadline: %v", err)
	}
}

// A deadline reached during the lock query, rather than between two looks,
// is still reported as the call's time running out.
func TestShared_DeadlineDuringTheQuery(t *testing.T) {
	l, mock := sharedLimiter(t)
	mock.ExpectQuery(tryLockQ).WillDelayFor(time.Second).WillReturnRows(lockRow(false))
	ctx, cancel := context.WithTimeout(context.Background(), firstPoll)
	defer cancel()
	_, _, err := l.Acquire(ctx, "api", "crm", 1)
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "time ran out") {
		t.Fatalf("a deadline during the lock query: %v", err)
	}
}

// A failed lock query fails the call rather than waiting forever, and an
// unlock that fails gives the slot back all the same.
func TestShared_Failures(t *testing.T) {
	l, mock := sharedLimiter(t)
	down := errors.New("db down")
	mock.ExpectQuery(tryLockQ).WillReturnError(down)
	if _, _, err := l.Acquire(context.Background(), "api", "crm", 1); !errors.Is(err, down) {
		t.Fatalf("a failed lock query: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}

	l, mock = sharedLimiter(t)
	mock.ExpectQuery(tryLockQ).WillReturnRows(lockRow(true))
	mock.ExpectExec(unlockQ).WillReturnError(down)
	slot, _, err := l.Acquire(context.Background(), "api", "crm", 1)
	if err != nil {
		t.Fatal(err)
	}
	slot.Release()
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}

	// No database connection to reserve: the call fails naming the limit.
	closed, _ := sharedLimiter(t)
	_ = closed.db.Close()
	if _, _, err := closed.Acquire(context.Background(), "api", "crm", 1); err == nil {
		t.Fatal("a closed database took a slot")
	}
}

// A wait of a second or more is reported to the tenth of a second.
func TestWaitError_Text(t *testing.T) {
	e := &WaitError{Connection: "crm", Limit: 1, Waited: 1234 * time.Millisecond, Err: errors.New("boom")}
	if got := e.Error(); got != `taking one of connection "crm"'s 1 call slots (max_concurrent_calls) failed after 1.2s: boom` {
		t.Errorf("WaitError text: %s", got)
	}
}
