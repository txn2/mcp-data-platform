package accessreq

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var reqCols = []string{
	"id", "target_type", "target_id", "requester_id", "requester_email", "owner_email", "message", "status",
	"permission", "decided_by", "decided_at", "share_id", "created_at", "expires_at",
}

func mockStore(t *testing.T) (*Store, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return NewStore(db), mock
}

// row is one pending request expiring at expires.
func row(expires time.Time) *sqlmock.Rows {
	return sqlmock.NewRows(reqCols).AddRow("r1", "asset", "a1", "u1", "u1@example.com", "o@example.com", "", StatusPending,
		"", "", nil, "", time.Now(), expires)
}

func TestStoreOpen(t *testing.T) {
	s, mock := mockStore(t)
	ctx := context.Background()
	later := time.Now().Add(time.Hour)

	mock.ExpectQuery("SELECT .* FROM access_requests").WillReturnRows(sqlmock.NewRows(reqCols))
	mock.ExpectQuery("SELECT count").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("INSERT INTO access_requests").WillReturnRows(row(later))
	got, created, err := s.Open(ctx, Request{ID: "r1", TargetType: "asset", TargetID: "a1", RequesterID: "u1", ExpiresAt: later})
	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, "r1", got.ID)

	mock.ExpectQuery("SELECT .* FROM access_requests").WillReturnRows(row(later))
	_, created, err = s.Open(ctx, Request{TargetType: "asset", TargetID: "a1", RequesterID: "u1"})
	require.NoError(t, err)
	assert.False(t, created, "the pending one is returned")

	mock.ExpectQuery("SELECT .* FROM access_requests").WillReturnRows(sqlmock.NewRows(reqCols))
	mock.ExpectQuery("SELECT count").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(MaxPerDay))
	_, _, err = s.Open(ctx, Request{TargetType: "asset", TargetID: "a1", RequesterID: "u1"})
	assert.ErrorIs(t, err, ErrTooMany)

	mock.ExpectQuery("SELECT .* FROM access_requests").WillReturnRows(sqlmock.NewRows(reqCols))
	mock.ExpectQuery("SELECT count").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("INSERT INTO access_requests").WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery("SELECT .* FROM access_requests").WillReturnRows(row(later))
	_, created, err = s.Open(ctx, Request{TargetType: "asset", TargetID: "a1", RequesterID: "u1"})
	require.NoError(t, err)
	assert.False(t, created, "a racing request returns the one that landed")

	mock.ExpectQuery("SELECT .* FROM access_requests").WillReturnError(errors.New("down"))
	_, _, err = s.Open(ctx, Request{})
	assert.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestStoreReadsAndDecides(t *testing.T) {
	s, mock := mockStore(t)
	ctx := context.Background()
	mock.ExpectQuery("SELECT .* FROM access_requests WHERE id").WillReturnRows(row(time.Now().Add(-time.Minute)))
	got, err := s.Get(ctx, "r1")
	require.NoError(t, err)
	assert.Equal(t, StatusExpired, got.Status, "a pending request past its expiry reads as expired")
	mock.ExpectQuery("SELECT .* FROM access_requests WHERE id").WillReturnRows(sqlmock.NewRows(reqCols))
	_, err = s.Get(ctx, "x")
	assert.ErrorIs(t, err, ErrNotFound)

	mock.ExpectQuery("SELECT .* FROM access_requests").WillReturnRows(row(time.Now().Add(time.Hour)))
	list, err := s.ListForOwner(ctx, "o@example.com", "", "", false)
	require.NoError(t, err)
	assert.Len(t, list, 1)
	mock.ExpectQuery("SELECT .* FROM access_requests").WillReturnError(errors.New("down"))
	_, err = s.ListForOwner(ctx, "o@example.com", "", "", false)
	assert.Error(t, err)

	mock.ExpectExec("UPDATE access_requests").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.Decide(ctx, "r1", Decision{Status: StatusApproved, Permission: "viewer", DecidedBy: "o@example.com", ShareID: "s1"}))
	mock.ExpectExec("UPDATE access_requests").WillReturnResult(sqlmock.NewResult(0, 0))
	assert.ErrorIs(t, s.Decide(ctx, "r1", Decision{Status: StatusDeclined, DecidedBy: "o"}), ErrDecided)
	mock.ExpectExec("UPDATE access_requests").WillReturnError(errors.New("down"))
	assert.Error(t, s.Decide(ctx, "r1", Decision{Status: StatusDeclined, DecidedBy: "o"}))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCleanMessage(t *testing.T) {
	msg, err := CleanMessage("  hi  ")
	require.NoError(t, err)
	assert.Equal(t, "hi", msg)
}
