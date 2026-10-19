package levelalert

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/bgloop"
)

func TestPostgresStore(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	s := NewPostgresStore(db)
	ctx := context.Background()
	since := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

	mock.ExpectQuery(`SELECT key, level, title, since FROM alert_conditions WHERE family = \$1`).
		WithArgs("webhook").
		WillReturnRows(sqlmock.NewRows([]string{"key", "level", "title", "since"}).
			AddRow("webhook:spool_age:a", "critical", "spool age", since))
	got, err := s.List(ctx, "webhook")
	require.NoError(t, err)
	assert.Equal(t, []Record{{Key: "webhook:spool_age:a", Level: Critical, Title: "spool age", Since: since}}, got)

	mock.ExpectQuery(`SELECT key, level, title, since FROM alert_conditions`).WillReturnError(errors.New("down"))
	_, err = s.List(ctx, "webhook")
	require.Error(t, err)

	mock.ExpectQuery(`SELECT key, level, title, since FROM alert_conditions`).
		WillReturnRows(sqlmock.NewRows([]string{"key"}).AddRow("x"))
	_, err = s.List(ctx, "webhook")
	require.Error(t, err, "a row that does not scan is an error")

	mock.ExpectQuery(`SELECT key, level, title, since FROM alert_conditions`).
		WillReturnRows(sqlmock.NewRows([]string{"key", "level", "title", "since"}).
			AddRow("k", "warning", "t", since).RowError(0, errors.New("broken")))
	_, err = s.List(ctx, "webhook")
	require.Error(t, err)

	mock.ExpectQuery(`SELECT key, level, title, since FROM alert_conditions`).
		WillReturnRows(sqlmock.NewRows([]string{"key", "level", "title", "since"}))
	empty, err := s.List(ctx, "storage")
	require.NoError(t, err)
	assert.NotNil(t, empty)

	mock.ExpectExec(`INSERT INTO alert_conditions`).
		WithArgs("k", "webhook", "warning", "t", since).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.Upsert(ctx, "webhook", Finding{Key: "k", Level: Warning, Title: "t"}, since))
	mock.ExpectExec(`INSERT INTO alert_conditions`).WillReturnError(errors.New("down"))
	require.Error(t, s.Upsert(ctx, "webhook", Finding{Key: "k"}, since))

	mock.ExpectExec(`DELETE FROM alert_conditions WHERE key = \$1`).WithArgs("k").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.Delete(ctx, "k"))
	mock.ExpectExec(`DELETE FROM alert_conditions`).WillReturnError(errors.New("down"))
	require.Error(t, s.Delete(ctx, "k"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLocked(t *testing.T) {
	ctx := context.Background()
	t.Run("held elsewhere is skipped", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()
		mock.ExpectQuery(`SELECT pg_try_advisory_lock`).WithArgs(int64(7)).
			WillReturnRows(sqlmock.NewRows([]string{"ok"}).AddRow(false))
		ran := false
		err = Locked(ctx, db, 7, func(context.Context) error { ran = true; return nil })
		require.ErrorIs(t, err, bgloop.ErrSkipped)
		assert.False(t, ran)
	})
	t.Run("taken runs and unlocks", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()
		mock.ExpectQuery(`SELECT pg_try_advisory_lock`).WillReturnRows(sqlmock.NewRows([]string{"ok"}).AddRow(true))
		mock.ExpectExec(`SELECT pg_advisory_unlock`).WithArgs(int64(7)).WillReturnError(errors.New("gone"))
		want := errors.New("body")
		err = Locked(ctx, db, 7, func(context.Context) error { return want })
		require.ErrorIs(t, err, want)
		require.NoError(t, mock.ExpectationsWereMet())
	})
	t.Run("lock query fails", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()
		mock.ExpectQuery(`SELECT pg_try_advisory_lock`).WillReturnError(errors.New("down"))
		require.Error(t, Locked(ctx, db, 7, func(context.Context) error { return nil }))
	})
	t.Run("no connection", func(t *testing.T) {
		db, _, err := sqlmock.New()
		require.NoError(t, err)
		_ = db.Close()
		require.Error(t, Locked(ctx, db, 7, func(context.Context) error { return nil }))
	})
}
