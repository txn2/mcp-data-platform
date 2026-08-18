package notices

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPostgresWatermarkGetReturnsTheStoredInstant(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	at := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT delivered_at FROM user_notice_watermarks")).
		WithArgs("owner@example.com").
		WillReturnRows(sqlmock.NewRows([]string{"delivered_at"}).AddRow(at))

	got, err := NewPostgresWatermarkStore(db).Get(context.Background(), "owner@example.com")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, at, *got)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresWatermarkGetNeverBriefedIsAnAbsenceNotAnError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT delivered_at FROM user_notice_watermarks")).
		WithArgs("new@example.com").
		WillReturnError(sql.ErrNoRows)

	got, err := NewPostgresWatermarkStore(db).Get(context.Background(), "new@example.com")
	require.NoError(t, err)
	assert.Nil(t, got)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresWatermarkGetPropagatesRealFailures(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT delivered_at FROM user_notice_watermarks")).
		WillReturnError(errors.New("connection refused"))

	_, err = NewPostgresWatermarkStore(db).Get(context.Background(), "owner@example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading notice watermark")
}

func TestPostgresWatermarkSetUpsertsAndNeverMovesBackwards(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	// The pattern spans the conflict clause and its guard, so the upsert only
	// matches while it still refuses to move a watermark backwards: two
	// sessions may brief the same person at once, and the later delivery is the
	// one that decides what the next session has already seen.
	mock.ExpectExec(regexp.QuoteMeta(
		"ON CONFLICT (user_key) DO UPDATE\n\t\t   SET delivered_at = EXCLUDED.delivered_at, updated_at = NOW()\n"+
			"\t\t   WHERE user_notice_watermarks.delivered_at < EXCLUDED.delivered_at")).
		WithArgs("owner@example.com", at).
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, NewPostgresWatermarkStore(db).Set(context.Background(), "owner@example.com", at))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresWatermarkSetPropagatesFailures(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO user_notice_watermarks")).
		WillReturnError(errors.New("connection refused"))

	err = NewPostgresWatermarkStore(db).Set(context.Background(), "owner@example.com", time.Now())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "advancing notice watermark")
}

func TestNewBuildsAWatermarkBackedHandle(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	h := New(db, &fakeAssets{}, &fakeShares{}, &fakeThreads{})
	require.NotNil(t, h)
	assert.IsType(t, &PostgresWatermarkStore{}, h.marks)

	assert.Nil(t, New(db, nil, &fakeShares{}, &fakeThreads{}), "a missing store means no notices")
	assert.Nil(t, New(db, &fakeAssets{}, nil, &fakeThreads{}))
	assert.Nil(t, New(db, &fakeAssets{}, &fakeShares{}, nil))
}
