package whpgstate

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newMock(t *testing.T) (*States, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return New(db), mock
}

func TestWrites(t *testing.T) {
	s, mock := newMock(t)
	at := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	mock.ExpectExec(`INSERT INTO webhook_pg_listeners \(source, replica, listening_since, last_error\)`).
		WithArgs("src", "r1", at).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE webhook_pg_listeners SET replica = ''`).WithArgs("src", "r1").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`SET delivered = delivered \+ \$2`).WithArgs("src", int64(3), int64(1), at).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO webhook_pg_listeners \(source, last_error, last_error_at\)`).
		WithArgs("src", "lost", at).WillReturnResult(sqlmock.NewResult(0, 1))

	ctx := context.Background()
	require.NoError(t, s.Listening(ctx, "src", "r1", at))
	require.NoError(t, s.Stopped(ctx, "src", "r1"))
	require.NoError(t, s.Filed(ctx, "src", 3, 1, at))
	require.NoError(t, s.Failed(ctx, "src", "lost", at))
	require.NoError(t, mock.ExpectationsWereMet())
	assert.Nil(t, New(nil))
}

func TestWrite_MissingSourceIsDroppedAndOtherFailuresAreNot(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(`UPDATE webhook_pg_listeners`).WillReturnError(&pq.Error{Code: foreignKeyViolation})
	mock.ExpectExec(`UPDATE webhook_pg_listeners`).WillReturnError(errors.New("connection refused"))
	require.NoError(t, s.Stopped(context.Background(), "gone", "r1"), "a deleted source's state write changes nothing")
	err := s.Stopped(context.Background(), "src", "r1")
	require.ErrorContains(t, err, "connection refused")
}

func TestGet(t *testing.T) {
	s, mock := newMock(t)
	since := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	cols := []string{"replica", "listening_since", "last_event_at", "delivered", "rejected", "last_error", "last_error_at"}
	mock.ExpectQuery(`FROM webhook_pg_listeners WHERE source = \$1`).WithArgs("src").
		WillReturnRows(sqlmock.NewRows(cols).AddRow("r1", since, nil, int64(4), int64(2), "", nil))
	mock.ExpectQuery(`FROM webhook_pg_listeners`).WithArgs("none").WillReturnRows(sqlmock.NewRows(cols))
	mock.ExpectQuery(`FROM webhook_pg_listeners`).WithArgs("bad").WillReturnError(errors.New("db down"))

	st, err := s.Get(context.Background(), "src")
	require.NoError(t, err)
	assert.Equal(t, "r1", st.Replica)
	assert.True(t, since.Equal(*st.ListeningSince))
	assert.Equal(t, int64(4), st.Delivered)
	assert.Equal(t, int64(2), st.Rejected)

	st, err = s.Get(context.Background(), "none")
	require.NoError(t, err)
	assert.Equal(t, State{}, st, "no replica has listened")

	_, err = s.Get(context.Background(), "bad")
	require.ErrorContains(t, err, "db down")
}
