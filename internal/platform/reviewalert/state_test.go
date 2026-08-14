package reviewalert

import (
	"context"
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newMockAlertStore(t *testing.T) (*PostgresStore, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return NewPostgresStore(db, KnowledgeTarget()), mock
}

func TestPostgresStoreClaimAlert(t *testing.T) {
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)

	t.Run("an accepted claim reports won and stamps the cooldown bound", func(t *testing.T) {
		store, mock := newMockAlertStore(t)
		mock.ExpectExec("INSERT INTO review_alert_state").
			WithArgs(KnowledgeTarget().Queue, now, now.Add(-6*time.Hour)).
			WillReturnResult(sqlmock.NewResult(0, 1))

		won, err := store.ClaimAlert(context.Background(), 6*time.Hour, now)
		require.NoError(t, err)
		assert.True(t, won)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("a claim the cooldown rejects reports not won", func(t *testing.T) {
		store, mock := newMockAlertStore(t)
		mock.ExpectExec("INSERT INTO review_alert_state").
			WillReturnResult(sqlmock.NewResult(0, 0))

		won, err := store.ClaimAlert(context.Background(), time.Hour, now)
		require.NoError(t, err)
		assert.False(t, won, "the conditional upsert matched no row, so another check owns this window")
	})

	t.Run("a write failure is reported", func(t *testing.T) {
		store, mock := newMockAlertStore(t)
		mock.ExpectExec("INSERT INTO review_alert_state").WillReturnError(errors.New("boom"))

		_, err := store.ClaimAlert(context.Background(), time.Hour, now)
		assert.ErrorContains(t, err, "claiming knowledge_review alert")
	})

	t.Run("an undeterminable row count is reported rather than assumed won", func(t *testing.T) {
		store, mock := newMockAlertStore(t)
		mock.ExpectExec("INSERT INTO review_alert_state").
			WillReturnResult(sqlmock.NewErrorResult(errors.New("no RowsAffected")))

		won, err := store.ClaimAlert(context.Background(), time.Hour, now)
		assert.ErrorContains(t, err, "reading knowledge_review alert claim result")
		assert.False(t, won)
	})
}

func TestPostgresStoreClear(t *testing.T) {
	t.Run("drops the over-threshold marker", func(t *testing.T) {
		store, mock := newMockAlertStore(t)
		mock.ExpectExec("UPDATE review_alert_state SET alerting = FALSE").
			WillReturnResult(sqlmock.NewResult(0, 1))

		require.NoError(t, store.Clear(context.Background()))
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("a write failure is reported", func(t *testing.T) {
		store, mock := newMockAlertStore(t)
		mock.ExpectExec("UPDATE review_alert_state").WillReturnError(errors.New("boom"))

		assert.ErrorContains(t, store.Clear(context.Background()), "clearing knowledge_review alert state")
	})
}
