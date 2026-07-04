package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testTTL = 30 * time.Minute

func TestMarkDiscovered(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectExec("INSERT INTO search_gate_discovery").
		WithArgs("sess-1", "1800 seconds").
		WillReturnResult(sqlmock.NewResult(0, 1))

	s := New(db, testTTL)
	require.NoError(t, s.MarkDiscovered(context.Background(), "sess-1"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMarkDiscovered_Error(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectExec("INSERT INTO search_gate_discovery").
		WillReturnError(errors.New("boom"))

	s := New(db, testTTL)
	err = s.MarkDiscovered(context.Background(), "sess-1")
	assert.Error(t, err)
}

func TestHasDiscovered(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery("SELECT EXISTS").
		WithArgs("sess-1").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))

	s := New(db, testTTL)
	ok, err := s.HasDiscovered(context.Background(), "sess-1")
	require.NoError(t, err)
	assert.True(t, ok)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestHasDiscovered_False(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery("SELECT EXISTS").
		WithArgs("sess-1").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

	s := New(db, testTTL)
	ok, err := s.HasDiscovered(context.Background(), "sess-1")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestHasDiscovered_Error(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery("SELECT EXISTS").
		WillReturnError(errors.New("boom"))

	s := New(db, testTTL)
	_, err = s.HasDiscovered(context.Background(), "sess-1")
	assert.Error(t, err)
}

func TestCleanup(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectExec("DELETE FROM search_gate_discovery").
		WillReturnResult(sqlmock.NewResult(0, 5))

	s := New(db, testTTL)
	require.NoError(t, s.Cleanup(context.Background()))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCleanup_Error(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectExec("DELETE FROM search_gate_discovery").
		WillReturnError(errors.New("boom"))

	s := New(db, testTTL)
	assert.Error(t, s.Cleanup(context.Background()))
}

func TestClose(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	assert.NoError(t, New(db, testTTL).Close())
}

// TestIntervalString verifies the TTL is rendered as a positive integer-seconds
// interval (and never sub-second, which Postgres would treat as 0).
func TestIntervalString(t *testing.T) {
	assert.Equal(t, "1800 seconds", intervalString(30*time.Minute))
	assert.Equal(t, "1 seconds", intervalString(0))
	assert.Equal(t, "1 seconds", intervalString(500*time.Millisecond))
}
