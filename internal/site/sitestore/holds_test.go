package sitestore

import (
	"context"
	"database/sql"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/site"
)

// expectHold expects one attempt at a hold of kind: the site's lock, the
// expired holds cleared, the read of what stands in its way, and, when
// nothing does, the insert.
func expectHold(mock sqlmock.Sqlmock, kind string, blocked bool) {
	mock.ExpectBegin()
	mock.ExpectQuery(q("SELECT 1 FROM sites WHERE id = $1 FOR UPDATE")).WithArgs("site_1").WillReturnRows(sqlmock.NewRows([]string{"x"}).AddRow(1))
	mock.ExpectExec(q("DELETE FROM site_object_holds WHERE site_id = $1 AND expires_at <= clock_timestamp()")).WithArgs("site_1").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(q("FROM site_object_holds WHERE site_id = $1 AND kind <> $2")).WithArgs("site_1", kind, jobFresh.Seconds()).
		WillReturnRows(sqlmock.NewRows([]string{"blocked"}).AddRow(blocked))
	if blocked {
		mock.ExpectCommit()
		return
	}
	mock.ExpectExec(q("INSERT INTO site_object_holds")).WithArgs(sqlmock.AnyArg(), "site_1", kind, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
}

func quickHolds(s *Store) {
	s.holds = holdTiming{wait: 3 * time.Millisecond, poll: time.Millisecond}
}

func TestHoldUpload(t *testing.T) {
	ctx := context.Background()
	t.Run("taken at once", func(t *testing.T) {
		s, mock := newMock(t)
		expectHold(mock, holdUpload, false)
		h, err := s.HoldUpload(ctx, "site_1")
		require.NoError(t, err)
		require.NotNil(t, h)
		assert.Equal(t, defaultUploadFor, h.ttl)
	})
	t.Run("waits out a sweep, then takes it", func(t *testing.T) {
		s, mock := newMock(t)
		quickHolds(s)
		expectHold(mock, holdUpload, true)
		expectHold(mock, holdUpload, false)
		h, err := s.HoldUpload(ctx, "site_1")
		require.NoError(t, err)
		require.NotNil(t, h)
	})
	t.Run("refused when a sweep holds the files past the wait", func(t *testing.T) {
		s, mock := newMock(t)
		quickHolds(s)
		for range 4 {
			expectHold(mock, holdUpload, true)
		}
		_, err := s.HoldUpload(ctx, "site_1")
		require.ErrorIs(t, err, ErrCleanupRunning)
	})
	t.Run("a canceled publish stops waiting", func(t *testing.T) {
		s, mock := newMock(t)
		s.holds = holdTiming{wait: time.Hour, poll: time.Hour}
		_ = mock
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		_, err := s.HoldUpload(canceled, "site_1")
		require.ErrorIs(t, err, context.Canceled)
	})
	t.Run("a cancel while waiting", func(t *testing.T) {
		s, mock := newMock(t)
		waiting, cancel := context.WithCancel(ctx)
		// The publish is canceled at the moment it starts to wait, after
		// its first try found the files held.
		s.holds = holdTiming{wait: time.Hour, poll: time.Hour, after: func(time.Duration) <-chan time.Time {
			cancel()
			return nil
		}}
		expectHold(mock, holdUpload, true)
		_, err := s.HoldUpload(waiting, "site_1")
		require.ErrorIs(t, err, context.Canceled)
	})
}

func TestHoldSweep(t *testing.T) {
	ctx := context.Background()
	t.Run("taken", func(t *testing.T) {
		s, mock := newMock(t)
		expectHold(mock, holdSweep, false)
		h, err := s.HoldSweep(ctx, "site_1")
		require.NoError(t, err)
		require.NotNil(t, h)
		assert.Equal(t, defaultSweepFor, h.ttl)
	})
	t.Run("nil while a publish holds the files or a job runs", func(t *testing.T) {
		s, mock := newMock(t)
		expectHold(mock, holdSweep, true)
		h, err := s.HoldSweep(ctx, "site_1")
		require.NoError(t, err)
		assert.Nil(t, h)
	})
	t.Run("no such site", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectBegin()
		mock.ExpectQuery(q("FOR UPDATE")).WillReturnError(sql.ErrNoRows)
		mock.ExpectRollback()
		_, err := s.HoldSweep(ctx, "site_1")
		require.ErrorIs(t, err, site.ErrNotFound)
	})
	failures := []struct {
		name, msg string
		expect    func(sqlmock.Sqlmock)
	}{
		{"the lock fails", "lock site", func(m sqlmock.Sqlmock) {
			m.ExpectQuery(q("FOR UPDATE")).WillReturnError(errDown)
		}},
		{"clearing expired holds fails", "clear expired holds", func(m sqlmock.Sqlmock) {
			m.ExpectQuery(q("FOR UPDATE")).WillReturnRows(sqlmock.NewRows([]string{"x"}).AddRow(1))
			m.ExpectExec(q("DELETE FROM site_object_holds")).WillReturnError(errDown)
		}},
		{"reading the holds fails", "read the site's holds", func(m sqlmock.Sqlmock) {
			m.ExpectQuery(q("FOR UPDATE")).WillReturnRows(sqlmock.NewRows([]string{"x"}).AddRow(1))
			m.ExpectExec(q("DELETE FROM site_object_holds")).WillReturnResult(sqlmock.NewResult(0, 0))
			m.ExpectQuery(q("kind <> $2")).WillReturnError(errDown)
		}},
		{"the insert fails", "hold the site's files", func(m sqlmock.Sqlmock) {
			m.ExpectQuery(q("FOR UPDATE")).WillReturnRows(sqlmock.NewRows([]string{"x"}).AddRow(1))
			m.ExpectExec(q("DELETE FROM site_object_holds")).WillReturnResult(sqlmock.NewResult(0, 0))
			m.ExpectQuery(q("kind <> $2")).WillReturnRows(sqlmock.NewRows([]string{"blocked"}).AddRow(false))
			m.ExpectExec(q("INSERT INTO site_object_holds")).WillReturnError(errDown)
		}},
	}
	for _, tc := range failures {
		t.Run(tc.name, func(t *testing.T) {
			s, mock := newMock(t)
			mock.ExpectBegin()
			tc.expect(mock)
			mock.ExpectRollback()
			_, err := s.HoldSweep(ctx, "site_1")
			require.ErrorContains(t, err, tc.msg)
		})
	}
}

func TestHold_RenewAndRelease(t *testing.T) {
	ctx := context.Background()
	var none *Hold
	require.NoError(t, none.Renew(ctx), "a nil hold holds nothing")
	none.Release(ctx)
	require.NoError(t, (&Hold{}).Renew(ctx), "nor does a zero one")
	(&Hold{}).Release(ctx)

	held := func(t *testing.T) (*Hold, sqlmock.Sqlmock) {
		t.Helper()
		s, mock := newMock(t)
		h := &Hold{db: s.db, id: "shold_1", ttl: time.Minute, renewAfter: time.Minute}
		return h, mock
	}
	t.Run("a hold renewed a moment ago is left alone", func(t *testing.T) {
		h, _ := held(t)
		h.renewed.Store(time.Now().UnixNano())
		require.NoError(t, h.Renew(ctx))
	})
	t.Run("an old renewal is renewed", func(t *testing.T) {
		h, mock := held(t)
		mock.ExpectExec(q("UPDATE site_object_holds SET expires_at")).WithArgs("shold_1", time.Minute.Seconds()).WillReturnResult(sqlmock.NewResult(0, 1))
		require.NoError(t, h.Renew(ctx))
	})
	t.Run("a hold that ran out is ErrHoldExpired", func(t *testing.T) {
		h, mock := held(t)
		mock.ExpectExec(q("UPDATE site_object_holds")).WillReturnResult(sqlmock.NewResult(0, 0))
		require.ErrorIs(t, h.Renew(ctx), ErrHoldExpired)
	})
	t.Run("a renewal that fails says so", func(t *testing.T) {
		h, mock := held(t)
		mock.ExpectExec(q("UPDATE site_object_holds")).WillReturnError(errDown)
		require.ErrorContains(t, h.Renew(ctx), "renew the hold")
	})
	t.Run("release deletes it", func(t *testing.T) {
		h, mock := held(t)
		mock.ExpectExec(q("DELETE FROM site_object_holds WHERE id = $1")).WithArgs("shold_1").WillReturnResult(sqlmock.NewResult(0, 1))
		h.Release(ctx)
	})
}

func TestRecordRelease_ChecksThePublishsHold(t *testing.T) {
	ctx := context.Background()
	recording := func(t *testing.T) (*Store, sqlmock.Sqlmock, context.Context) {
		t.Helper()
		s, mock := newMock(t)
		mock.ExpectBegin()
		mock.ExpectQuery(q("SELECT 1 FROM sites WHERE id = $1 FOR UPDATE")).WithArgs("site_1").WillReturnRows(sqlmock.NewRows([]string{"x"}).AddRow(1))
		mock.ExpectQuery(q("SELECT COALESCE(MAX(number), 0) + 1")).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(2))
		mock.ExpectQuery(q("INSERT INTO site_releases")).WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(rowTime))
		return s, mock, WithHold(ctx, &Hold{db: s.db, id: "shold_1"})
	}
	assert.Nil(t, HoldOf(ctx))
	t.Run("a live hold records", func(t *testing.T) {
		s, mock, held := recording(t)
		require.NotNil(t, HoldOf(held))
		mock.ExpectQuery(q("FROM site_object_holds WHERE id = $1 AND expires_at > clock_timestamp()")).WithArgs("shold_1").
			WillReturnRows(sqlmock.NewRows([]string{"live"}).AddRow(true))
		mock.ExpectExec(q("INSERT INTO site_release_files")).WillReturnResult(sqlmock.NewResult(0, 2))
		mock.ExpectCommit()
		require.NoError(t, s.RecordRelease(held, newRelease(), nil))
	})
	t.Run("a hold that ran out records nothing", func(t *testing.T) {
		s, mock, held := recording(t)
		mock.ExpectQuery(q("FROM site_object_holds WHERE id = $1")).WillReturnRows(sqlmock.NewRows([]string{"live"}).AddRow(false))
		mock.ExpectRollback()
		require.ErrorIs(t, s.RecordRelease(held, newRelease(), nil), ErrHoldExpired)
	})
	t.Run("a hold that cannot be read records nothing", func(t *testing.T) {
		s, mock, held := recording(t)
		mock.ExpectQuery(q("FROM site_object_holds WHERE id = $1")).WillReturnError(errDown)
		mock.ExpectRollback()
		require.ErrorContains(t, s.RecordRelease(held, newRelease(), nil), "read the publish's hold")
	})
}
