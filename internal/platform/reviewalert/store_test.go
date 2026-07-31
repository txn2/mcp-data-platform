package reviewalert

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newMockSettingsStore(t *testing.T) (*PostgresStore, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return NewPostgresStore(db), mock
}

func TestPostgresStoreGet(t *testing.T) {
	t.Run("decodes the stored section", func(t *testing.T) {
		store, mock := newMockSettingsStore(t)
		updated := time.Date(2026, 7, 30, 9, 0, 0, 0, time.UTC)
		mock.ExpectQuery("SELECT value, updated_by, updated_at FROM platform_settings").
			WithArgs(SettingsSection).
			WillReturnRows(sqlmock.NewRows([]string{"value", "updated_by", "updated_at"}).
				AddRow([]byte(`{"enabled":true,"pending_threshold":25,"oldest_pending_days":30,`+
					`"cooldown_hours":12,"recipients":["ops@example.com"]}`), "admin@example.com", updated))

		got, err := store.Get(context.Background())
		require.NoError(t, err)
		assert.True(t, got.Enabled)
		assert.Equal(t, 25, got.PendingThreshold)
		assert.Equal(t, 12, got.CooldownHours)
		assert.Equal(t, []string{"ops@example.com"}, got.Recipients)
		assert.Equal(t, "admin@example.com", got.UpdatedBy, "audit columns win over the JSON")
		assert.Equal(t, updated, got.UpdatedAt)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("an absent section is ErrNotFound", func(t *testing.T) {
		store, mock := newMockSettingsStore(t)
		mock.ExpectQuery("SELECT value").WithArgs(SettingsSection).WillReturnError(sql.ErrNoRows)

		_, err := store.Get(context.Background())
		assert.ErrorIs(t, err, ErrNotFound)
	})

	t.Run("a query failure is reported", func(t *testing.T) {
		store, mock := newMockSettingsStore(t)
		mock.ExpectQuery("SELECT value").WithArgs(SettingsSection).WillReturnError(errors.New("boom"))

		_, err := store.Get(context.Background())
		assert.ErrorContains(t, err, "querying review queue alert settings")
	})

	t.Run("undecodable JSON is reported", func(t *testing.T) {
		store, mock := newMockSettingsStore(t)
		mock.ExpectQuery("SELECT value").WithArgs(SettingsSection).
			WillReturnRows(sqlmock.NewRows([]string{"value", "updated_by", "updated_at"}).
				AddRow([]byte("not json"), "", time.Now()))

		_, err := store.Get(context.Background())
		assert.ErrorContains(t, err, "decoding review queue alert settings")
	})
}

func TestPostgresStoreSet(t *testing.T) {
	t.Run("upserts the section without the audit columns in the JSON", func(t *testing.T) {
		store, mock := newMockSettingsStore(t)
		mock.ExpectExec("INSERT INTO platform_settings").
			WithArgs(SettingsSection,
				[]byte(`{"enabled":true,"pending_threshold":0,"oldest_pending_days":30,`+
					`"cooldown_hours":24,"recipients":["ops@example.com"]}`),
				"admin@example.com").
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := store.Set(context.Background(), Settings{
			Enabled: true, OldestPendingDays: 30, CooldownHours: 24,
			Recipients: []string{"ops@example.com"},
			// Stale copies that must not reach the JSON value.
			UpdatedBy: "someone-else@example.com", UpdatedAt: time.Now(),
		}, "admin@example.com")
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("a write failure is reported", func(t *testing.T) {
		store, mock := newMockSettingsStore(t)
		mock.ExpectExec("INSERT INTO platform_settings").WillReturnError(errors.New("boom"))

		err := store.Set(context.Background(), Settings{}, "admin@example.com")
		assert.ErrorContains(t, err, "storing review queue alert settings")
	})
}

// stubSettings serves a fixed settings result. It models the store contract:
// Get returns ErrNotFound rather than a nil-nil pair when nothing is stored.
type stubSettings struct {
	settings *Settings
	err      error
}

func (s *stubSettings) Get(context.Context) (*Settings, error) { return s.settings, s.err }

func (*stubSettings) Set(context.Context, Settings, string) error { return nil }

func TestSettingsOf(t *testing.T) {
	t.Run("an unconfigured alert falls back to the defaults", func(t *testing.T) {
		got, err := SettingsOf(context.Background(), &stubSettings{err: ErrNotFound})
		require.NoError(t, err)
		assert.Equal(t, DefaultSettings(), got)
	})

	t.Run("a stored configuration is returned as written", func(t *testing.T) {
		stored := Settings{Enabled: true, PendingThreshold: 5, CooldownHours: 1}
		got, err := SettingsOf(context.Background(), &stubSettings{settings: &stored})
		require.NoError(t, err)
		assert.Equal(t, stored, got)
	})

	t.Run("a read failure propagates", func(t *testing.T) {
		_, err := SettingsOf(context.Background(), &stubSettings{err: errors.New("boom")})
		assert.ErrorContains(t, err, "boom")
	})
}
