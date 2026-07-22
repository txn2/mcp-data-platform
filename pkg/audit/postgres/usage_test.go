package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPromptUsage_EmptyIDsSkipsQuery(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{})
	out, err := store.PromptUsage(context.Background(), nil)
	assert.NoError(t, err)
	assert.Empty(t, out)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPromptUsage_AggregatesServeEvents(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{})
	last := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)

	mock.ExpectQuery("SELECT parameters->>'prompt_id', COUNT\\(\\*\\), MAX\\(timestamp\\)").
		WithArgs(pq.Array([]string{"p1", "p2"})).
		WillReturnRows(sqlmock.NewRows([]string{"prompt_id", "count", "max"}).
			AddRow("p1", int64(37), last))

	out, err := store.PromptUsage(context.Background(), []string{"p1", "p2"})
	require.NoError(t, err)
	require.Len(t, out, 1, "a prompt never served has no entry")
	assert.Equal(t, int64(37), out["p1"].RunCount)
	require.NotNil(t, out["p1"].LastRunAt)
	assert.True(t, out["p1"].LastRunAt.Equal(last))
	_, served := out["p2"]
	assert.False(t, served)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPromptUsage_QueryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{})
	mock.ExpectQuery("SELECT parameters->>'prompt_id'").
		WillReturnError(errors.New("db down"))

	_, err = store.PromptUsage(context.Background(), []string{"p1"})
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}
