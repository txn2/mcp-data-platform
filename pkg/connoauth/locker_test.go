package connoauth

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// The refresher's lock is a try-lock on the process's lock session (#2169):
// taken, it is released by its release; held elsewhere, it is refused; a
// statement that fails is an error naming the lock.
func TestPostgresLocker_TryLock(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	locker := NewPostgresLocker(db)
	key := Key{Kind: KindAPI, Name: "crm"}
	lockQ := regexp.QuoteMeta("SELECT pg_try_advisory_lock($1)")
	unlockQ := regexp.QuoteMeta("SELECT pg_advisory_unlock($1)")

	mock.ExpectQuery(lockQ).WithArgs(advisoryLockKey(key)).WillReturnRows(sqlmock.NewRows([]string{"l"}).AddRow(true))
	mock.ExpectExec(unlockQ).WithArgs(advisoryLockKey(key)).WillReturnResult(sqlmock.NewResult(0, 0))
	release, ok, err := locker.TryLock(context.Background(), key)
	if err != nil || !ok {
		t.Fatalf("a free lock: ok=%v err=%v", ok, err)
	}
	release()

	mock.ExpectQuery(lockQ).WillReturnRows(sqlmock.NewRows([]string{"l"}).AddRow(false))
	if _, ok, err := locker.TryLock(context.Background(), key); err != nil || ok {
		t.Fatalf("a lock held elsewhere: ok=%v err=%v", ok, err)
	}

	down := errors.New("down")
	mock.ExpectQuery(lockQ).WillReturnError(down)
	if _, _, err := locker.TryLock(context.Background(), key); !errors.Is(err, down) {
		t.Fatalf("a failed statement: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}
