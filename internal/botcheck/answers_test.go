package botcheck

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mockAnswers(t *testing.T) (*PostgresAnswers, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, mock.ExpectationsWereMet())
		_ = db.Close()
	})
	return NewAnswerStore(db), mock
}

func TestPostgresAnswers(t *testing.T) {
	s, mock := mockAnswers(t)
	ctx := context.Background()
	at := time.Unix(1700000000, 0)

	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO bot_check_answers")).WithArgs("pow:n", "orders", at).
		WillReturnRows(sqlmock.NewRows([]string{"challenge_id"}).AddRow("pow:n"))
	first, err := s.Use(ctx, "pow:n", "orders", at)
	require.NoError(t, err)
	assert.True(t, first)

	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO bot_check_answers")).WillReturnError(sql.ErrNoRows)
	first, err = s.Use(ctx, "pow:n", "orders", at)
	require.NoError(t, err)
	assert.False(t, first, "an answer recorded before is not the first")

	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO bot_check_answers")).WillReturnError(errors.New("boom"))
	_, err = s.Use(ctx, "pow:n", "orders", at)
	require.ErrorContains(t, err, "recording a bot check answer")

	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM bot_check_answers")).WithArgs("orders", at).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(7))
	n, err := s.Recent(ctx, "orders", at)
	require.NoError(t, err)
	assert.Equal(t, 7, n)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*)")).WillReturnError(errors.New("boom"))
	_, err = s.Recent(ctx, "orders", at)
	require.Error(t, err)

	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM bot_check_answers")).WithArgs(at).WillReturnResult(sqlmock.NewResult(0, 3))
	swept, err := s.Sweep(ctx, at)
	require.NoError(t, err)
	assert.Equal(t, int64(3), swept)
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM bot_check_answers")).WillReturnError(errors.New("boom"))
	_, err = s.Sweep(ctx, at)
	require.Error(t, err)
}
