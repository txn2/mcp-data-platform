package advisorylock

import (
	"context"
	"database/sql/driver"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	lockQ     = regexp.QuoteMeta("SELECT pg_try_advisory_lock($1)")
	unlockQ   = regexp.QuoteMeta("SELECT pg_advisory_unlock($1)")
	pairLockQ = regexp.QuoteMeta("SELECT pg_try_advisory_lock($1, $2)")
	pairFreeQ = regexp.QuoteMeta("SELECT pg_advisory_unlock($1, $2)")
	errDown   = errors.New("down")
)

func newMock(t *testing.T) (*Session, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return New(db), mock
}

func granted(got bool) *sqlmock.Rows { return sqlmock.NewRows([]string{"l"}).AddRow(got) }

// Two locks share the session, which goes back to the pool when the last is
// released, and a release is safe to call twice.
func TestTry_TakesAndReleases(t *testing.T) {
	s, mock := newMock(t)
	ctx := context.Background()
	mock.ExpectQuery(lockQ).WithArgs(int64(1)).WillReturnRows(granted(true))
	mock.ExpectQuery(lockQ).WithArgs(int64(2)).WillReturnRows(granted(true))
	mock.ExpectExec(unlockQ).WithArgs(int64(1)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(unlockQ).WithArgs(int64(2)).WillReturnResult(sqlmock.NewResult(0, 0))

	first, ok, err := s.Try(ctx, ID(1))
	require.NoError(t, err)
	require.True(t, ok)
	second, ok, err := s.Try(ctx, ID(2))
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, 1, s.db.Stats().InUse, "two locks hold one connection")

	first()
	first()
	assert.Equal(t, 1, s.db.Stats().InUse, "a lock still held keeps the session")
	second()
	assert.Equal(t, 0, s.db.Stats().InUse, "the session goes back to the pool with its last lock")
	require.NoError(t, mock.ExpectationsWereMet())
}

// A key this process holds is refused without asking the database, since
// PostgreSQL would grant the holding session the key again.
func TestTry_RefusesAKeyThisProcessHolds(t *testing.T) {
	s, mock := newMock(t)
	ctx := context.Background()
	mock.ExpectQuery(lockQ).WithArgs(int64(7)).WillReturnRows(granted(true))
	release, ok, err := s.Try(ctx, ID(7))
	require.NoError(t, err)
	require.True(t, ok)

	_, ok, err = s.Try(ctx, ID(7))
	require.NoError(t, err)
	assert.False(t, ok, "a second holder in one process")

	mock.ExpectExec(unlockQ).WithArgs(int64(7)).WillReturnResult(sqlmock.NewResult(0, 0))
	release()
	require.NoError(t, mock.ExpectationsWereMet())
}

// A key another session holds is refused, and the session is not kept for it.
func TestTry_AnotherSessionHoldsIt(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(lockQ).WillReturnRows(granted(false))
	_, ok, err := s.Try(context.Background(), ID(3))
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Equal(t, 0, s.db.Stats().InUse)
	require.NoError(t, mock.ExpectationsWereMet())
}

// The two-integer form takes and releases its own statements.
func TestTry_PairKey(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(pairLockQ).WithArgs(int32(9), int32(2)).WillReturnRows(granted(true))
	mock.ExpectExec(pairFreeQ).WithArgs(int32(9), int32(2)).WillReturnResult(sqlmock.NewResult(0, 0))
	release, ok, err := s.Try(context.Background(), Pair(9, 2))
	require.NoError(t, err)
	require.True(t, ok)
	release()
	require.NoError(t, mock.ExpectationsWereMet())
}

// A failed take is an error and holds nothing; a done context asks nothing;
// a pool that cannot open a session says so.
func TestTry_Failures(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(lockQ).WillReturnError(errDown)
	_, ok, err := s.Try(context.Background(), ID(1))
	require.ErrorIs(t, err, errDown)
	assert.False(t, ok)
	assert.Equal(t, 0, s.db.Stats().InUse)

	done, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = s.Try(done, ID(1))
	require.ErrorIs(t, err, context.Canceled)
	require.NoError(t, mock.ExpectationsWereMet())

	_ = s.db.Close()
	_, _, err = s.Try(context.Background(), ID(1))
	require.ErrorContains(t, err, "acquiring the lock session")
}

// An unlock that fails ends the session, since a session that may still hold
// the lock must not go back to the pool; a lock that ended with it releases
// as a no-op, and the next take opens a new session.
func TestRelease_AFailedUnlockEndsTheSession(t *testing.T) {
	s, mock := newMock(t)
	ctx := context.Background()
	mock.ExpectQuery(lockQ).WithArgs(int64(1)).WillReturnRows(granted(true))
	mock.ExpectQuery(lockQ).WithArgs(int64(2)).WillReturnRows(granted(true))
	mock.ExpectExec(unlockQ).WithArgs(int64(1)).WillReturnError(errDown)

	first, _, err := s.Try(ctx, ID(1))
	require.NoError(t, err)
	second, _, err := s.Try(ctx, ID(2))
	require.NoError(t, err)
	gen := s.gen
	first()
	assert.Nil(t, s.conn, "the session ended")
	assert.Empty(t, s.held, "every lock on it ended with it")
	second() // no statement: its lock ended with the session
	require.NoError(t, mock.ExpectationsWereMet())
	assert.Equal(t, gen, s.gen, "nothing reopened the session")
}

// Held answers from the process for a key it holds, and otherwise asks the
// database by taking the key and giving it straight back.
func TestHeld(t *testing.T) {
	s, mock := newMock(t)
	ctx := context.Background()

	mock.ExpectQuery(lockQ).WithArgs(int64(5)).WillReturnRows(granted(true))
	mock.ExpectExec(unlockQ).WithArgs(int64(5)).WillReturnResult(sqlmock.NewResult(0, 0))
	held, err := s.Held(ctx, ID(5))
	require.NoError(t, err)
	assert.False(t, held, "free")

	mock.ExpectQuery(lockQ).WithArgs(int64(5)).WillReturnRows(granted(false))
	held, err = s.Held(ctx, ID(5))
	require.NoError(t, err)
	assert.True(t, held, "another replica holds it")

	mock.ExpectQuery(lockQ).WithArgs(int64(5)).WillReturnRows(granted(true))
	release, _, err := s.Try(ctx, ID(5))
	require.NoError(t, err)
	held, err = s.Held(ctx, ID(5))
	require.NoError(t, err)
	assert.True(t, held, "this process holds it")

	mock.ExpectExec(unlockQ).WithArgs(int64(5)).WillReturnResult(sqlmock.NewResult(0, 0))
	release()

	mock.ExpectQuery(lockQ).WillReturnError(errDown)
	_, err = s.Held(ctx, ID(5))
	require.ErrorIs(t, err, errDown)
	require.NoError(t, mock.ExpectationsWereMet())
}

// For keeps one session per database handle, and Try takes on it.
func TestFor_OneSessionPerHandle(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	other, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = other.Close() })

	assert.Same(t, For(db), For(db))
	assert.NotSame(t, For(db), For(other))

	mock.ExpectQuery(lockQ).WithArgs(int64(4)).WillReturnRows(granted(true))
	mock.ExpectExec(unlockQ).WithArgs(int64(4)).WillReturnResult(sqlmock.NewResult(0, 0))
	release, ok, err := Try(context.Background(), db, ID(4))
	require.NoError(t, err)
	require.True(t, ok)
	_, ok, err = For(db).Try(context.Background(), ID(4))
	require.NoError(t, err)
	assert.False(t, ok, "Try and For share the session")
	release()
	require.NoError(t, mock.ExpectationsWereMet())
}

// A session whose connection broke is ended: the locks on it are gone, a
// release of one asks nothing, and the next take opens a new session.
func TestTry_ABrokenSessionIsEnded(t *testing.T) {
	s, mock := newMock(t)
	ctx := context.Background()
	mock.ExpectQuery(lockQ).WithArgs(int64(1)).WillReturnRows(granted(true))
	held, _, err := s.Try(ctx, ID(1))
	require.NoError(t, err)

	mock.ExpectQuery(lockQ).WithArgs(int64(2)).WillReturnError(driver.ErrBadConn)
	_, _, err = s.Try(ctx, ID(2))
	require.ErrorIs(t, err, driver.ErrBadConn)
	assert.Nil(t, s.conn, "the broken session was ended")
	assert.Empty(t, s.held, "its locks went with it")
	held() // no statement: the lock ended with the session
	require.NoError(t, mock.ExpectationsWereMet())
}
