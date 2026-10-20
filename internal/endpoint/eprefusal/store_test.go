package eprefusal

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errDown = errors.New("db down")

func mockStore(t *testing.T) (*Store, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return NewStore(db), mock
}

// Add sends every count in one statement, and sends none for no counts.
func TestStoreAdd(t *testing.T) {
	ctx := context.Background()
	s, mock := mockStore(t)
	require.NoError(t, s.Add(ctx, nil))

	hour := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	mock.ExpectExec("INSERT INTO script_endpoint_refusals").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 2))
	require.NoError(t, s.Add(ctx, []Count{
		{EndpointID: "e1", Hour: hour, Reason: ReasonBotCheck, Count: 3},
		{EndpointID: "e1", Hour: hour, Reason: ReasonOrigin, Count: 1},
	}))
	mock.ExpectExec("INSERT INTO script_endpoint_refusals").WillReturnError(errDown)
	require.ErrorIs(t, s.Add(ctx, []Count{{EndpointID: "e1", Hour: hour, Reason: ReasonBotCheck, Count: 1}}), errDown)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestStoreSinceAndSweep(t *testing.T) {
	ctx := context.Background()
	s, mock := mockStore(t)
	hour := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	cols := []string{"endpoint_id", "hour", "reason", "count"}

	mock.ExpectQuery("FROM script_endpoint_refusals").WithArgs(hour, "e1").
		WillReturnRows(sqlmock.NewRows(cols).AddRow("e1", hour, ReasonRateLimit, int64(4)))
	got, err := s.Since(ctx, "e1", hour)
	require.NoError(t, err)
	assert.Equal(t, []Count{{EndpointID: "e1", Hour: hour, Reason: ReasonRateLimit, Count: 4}}, got)

	mock.ExpectQuery("FROM script_endpoint_refusals").WillReturnRows(sqlmock.NewRows(cols))
	got, err = s.Since(ctx, "", hour)
	require.NoError(t, err)
	assert.NotNil(t, got, "no refusal is an empty list")

	mock.ExpectQuery("FROM script_endpoint_refusals").
		WillReturnRows(sqlmock.NewRows(cols).AddRow("e1", "not a time", ReasonRateLimit, int64(1)))
	_, err = s.Since(ctx, "", hour)
	require.Error(t, err)

	mock.ExpectQuery("FROM script_endpoint_refusals").
		WillReturnRows(sqlmock.NewRows(cols).AddRow("e1", hour, ReasonRateLimit, int64(1)).RowError(0, errDown))
	_, err = s.Since(ctx, "", hour)
	require.ErrorIs(t, err, errDown)

	mock.ExpectQuery("FROM script_endpoint_refusals").WillReturnError(errDown)
	_, err = s.Since(ctx, "", hour)
	require.ErrorIs(t, err, errDown)

	mock.ExpectExec("DELETE FROM script_endpoint_refusals").WithArgs(hour).WillReturnResult(sqlmock.NewResult(0, 7))
	n, err := s.Sweep(ctx, hour)
	require.NoError(t, err)
	assert.Equal(t, int64(7), n)
	mock.ExpectExec("DELETE FROM script_endpoint_refusals").WillReturnError(errDown)
	_, err = s.Sweep(ctx, hour)
	require.ErrorIs(t, err, errDown)
	assert.NoError(t, mock.ExpectationsWereMet())
}
