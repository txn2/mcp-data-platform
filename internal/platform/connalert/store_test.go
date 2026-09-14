package connalert

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

func newMockStore(t *testing.T) (*PostgresStore, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return NewPostgresStore(db), mock
}

func TestPostgresStore_Settings(t *testing.T) {
	t.Run("decodes the stored section", func(t *testing.T) {
		store, mock := newMockStore(t)
		updated := time.Date(2026, 9, 12, 9, 0, 0, 0, time.UTC)
		mock.ExpectQuery("SELECT value, updated_by, updated_at FROM platform_settings").
			WithArgs(SettingsSection).
			WillReturnRows(sqlmock.NewRows([]string{"value", "updated_by", "updated_at"}).
				AddRow([]byte(`{"enabled":true,"escalate_after_hours":6,`+
					`"recipients":["ops@example.com"]}`), "admin@example.com", updated))

		got, err := store.Get(t.Context())
		require.NoError(t, err)
		assert.True(t, got.Enabled)
		assert.Equal(t, 6, got.EscalateAfterHours)
		assert.Equal(t, []string{"ops@example.com"}, got.Recipients)
		assert.Equal(t, "admin@example.com", got.UpdatedBy, "audit columns win over the JSON")
		assert.Equal(t, updated, got.UpdatedAt)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("an absent section is ErrNotFound", func(t *testing.T) {
		store, mock := newMockStore(t)
		mock.ExpectQuery("SELECT value").WithArgs(SettingsSection).WillReturnError(sql.ErrNoRows)

		_, err := store.Get(t.Context())
		assert.ErrorIs(t, err, ErrNotFound)
	})

	t.Run("a query failure is reported", func(t *testing.T) {
		store, mock := newMockStore(t)
		mock.ExpectQuery("SELECT value").WithArgs(SettingsSection).WillReturnError(errors.New("boom"))

		_, err := store.Get(t.Context())
		assert.ErrorContains(t, err, "querying connection alert settings")
	})

	t.Run("undecodable JSON is reported", func(t *testing.T) {
		store, mock := newMockStore(t)
		mock.ExpectQuery("SELECT value").WithArgs(SettingsSection).
			WillReturnRows(sqlmock.NewRows([]string{"value", "updated_by", "updated_at"}).
				AddRow([]byte(`{`), "", time.Time{}))

		_, err := store.Get(t.Context())
		assert.ErrorContains(t, err, "decoding connection alert settings")
	})

	t.Run("Set upserts the section", func(t *testing.T) {
		store, mock := newMockStore(t)
		mock.ExpectExec("INSERT INTO platform_settings").
			WithArgs(SettingsSection, sqlmock.AnyArg(), "admin@example.com").
			WillReturnResult(sqlmock.NewResult(0, 1))

		require.NoError(t, store.Set(t.Context(),
			Settings{Enabled: true, EscalateAfterHours: 6}, "admin@example.com"))
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("a failed Set is reported", func(t *testing.T) {
		store, mock := newMockStore(t)
		mock.ExpectExec("INSERT INTO platform_settings").WillReturnError(errors.New("boom"))

		assert.ErrorContains(t, store.Set(t.Context(), Settings{}, "a@b.io"),
			"storing connection alert settings")
	})
}

// TestPostgresStore_Open covers the de-duplication contract: the caller learns
// whether it was the one that recorded the revocation, which is what decides
// whether anybody is mailed.
func TestPostgresStore_Open(t *testing.T) {
	revoked := time.Date(2026, 9, 12, 8, 0, 0, 0, time.UTC)
	alert := Alert{
		Kind: "api", Name: "billing", AuthorizedBy: "ops@example.com",
		IDPHost: "idp.example.com", Reason: "invalid_grant", RevokedAt: revoked,
	}

	t.Run("a first revocation is recorded", func(t *testing.T) {
		store, mock := newMockStore(t)
		mock.ExpectExec("INSERT INTO connection_auth_alerts").
			WithArgs("api", "billing", "ops@example.com", "idp.example.com", "invalid_grant", revoked,
				false, "").
			WillReturnResult(sqlmock.NewResult(0, 1))

		opened, err := store.Open(t.Context(), alert)
		require.NoError(t, err)
		assert.True(t, opened)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("a refused signed assertion records its marker and description", func(t *testing.T) {
		store, mock := newMockStore(t)
		mock.ExpectExec("INSERT INTO connection_auth_alerts").
			WithArgs("graphql", "erp", "", "login.example.com", "invalid_grant", revoked,
				true, "user hasn't approved this consumer").
			WillReturnResult(sqlmock.NewResult(0, 1))

		opened, err := store.Open(t.Context(), Alert{
			Kind: "graphql", Name: "erp", IDPHost: "login.example.com", Reason: "invalid_grant",
			RevokedAt: revoked, SignedAssertion: true, Description: "user hasn't approved this consumer",
		})
		require.NoError(t, err)
		assert.True(t, opened)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("a repeat while the connection is still revoked is not", func(t *testing.T) {
		store, mock := newMockStore(t)
		mock.ExpectExec("INSERT INTO connection_auth_alerts").
			WillReturnResult(sqlmock.NewResult(0, 0))

		opened, err := store.Open(t.Context(), alert)
		require.NoError(t, err)
		assert.False(t, opened, "the conflict clause is the whole de-duplication")
	})

	t.Run("a failed write is reported", func(t *testing.T) {
		store, mock := newMockStore(t)
		mock.ExpectExec("INSERT INTO connection_auth_alerts").WillReturnError(errors.New("boom"))

		_, err := store.Open(t.Context(), alert)
		assert.ErrorContains(t, err, "recording a connection revocation")
	})
}

func TestPostgresStore_Clear(t *testing.T) {
	t.Run("forgets the connection's open revocation", func(t *testing.T) {
		store, mock := newMockStore(t)
		mock.ExpectExec("DELETE FROM connection_auth_alerts").
			WithArgs("mcp", "vendor").WillReturnResult(sqlmock.NewResult(0, 1))

		require.NoError(t, store.Clear(t.Context(), "mcp", "vendor"))
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("a failed delete is reported", func(t *testing.T) {
		store, mock := newMockStore(t)
		mock.ExpectExec("DELETE FROM connection_auth_alerts").WillReturnError(errors.New("boom"))

		assert.ErrorContains(t, store.Clear(t.Context(), "mcp", "vendor"),
			"clearing a connection revocation")
	})
}

func TestPostgresStore_ClaimEscalations(t *testing.T) {
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	revoked := now.Add(-30 * time.Hour)

	t.Run("stamps and returns what it claimed", func(t *testing.T) {
		store, mock := newMockStore(t)
		mock.ExpectQuery("UPDATE connection_auth_alerts").
			WithArgs(now, now.Add(-24*time.Hour)).
			WillReturnRows(sqlmock.NewRows([]string{
				"connection_kind", "connection_name", "authorized_by",
				"idp_host", "reason", "revoked_at", "signed_assertion", "description",
			}).AddRow("api", "billing", "ops@example.com", "idp.example.com", "invalid_grant", revoked, false, ""))

		claimed, err := store.ClaimEscalations(t.Context(), 24*time.Hour, now)
		require.NoError(t, err)
		require.Len(t, claimed, 1)
		assert.Equal(t, "billing", claimed[0].Name)
		assert.Equal(t, "ops@example.com", claimed[0].AuthorizedBy)
		assert.Equal(t, revoked, claimed[0].RevokedAt)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("a refused signed assertion is never claimed", func(t *testing.T) {
		// Its one alert already went to the recipients the escalation would
		// mail, and there is no credential table to check it against.
		assert.Contains(t, claimEscalationsSQL, "AND NOT a.signed_assertion")
	})

	t.Run("claiming nothing is not an error", func(t *testing.T) {
		store, mock := newMockStore(t)
		mock.ExpectQuery("UPDATE connection_auth_alerts").
			WillReturnRows(sqlmock.NewRows([]string{
				"connection_kind", "connection_name", "authorized_by",
				"idp_host", "reason", "revoked_at", "signed_assertion", "description",
			}))

		claimed, err := store.ClaimEscalations(t.Context(), 24*time.Hour, now)
		require.NoError(t, err)
		assert.Empty(t, claimed)
	})

	t.Run("a failed claim is reported", func(t *testing.T) {
		store, mock := newMockStore(t)
		mock.ExpectQuery("UPDATE connection_auth_alerts").WillReturnError(errors.New("boom"))

		_, err := store.ClaimEscalations(t.Context(), 24*time.Hour, now)
		assert.ErrorContains(t, err, "claiming connection revocation escalations")
	})

	t.Run("an unreadable row is reported", func(t *testing.T) {
		store, mock := newMockStore(t)
		mock.ExpectQuery("UPDATE connection_auth_alerts").
			WillReturnRows(sqlmock.NewRows([]string{"connection_kind"}).AddRow("api"))

		_, err := store.ClaimEscalations(t.Context(), 24*time.Hour, now)
		assert.ErrorContains(t, err, "reading a claimed connection revocation")
	})
}

// TestSettingsOf proves the defaults reach a deployment that has never opened
// the settings page: the alert works out of the box, and only the escalation
// waits on an operator naming recipients.
func TestSettingsOf(t *testing.T) {
	t.Run("an unconfigured deployment gets the defaults", func(t *testing.T) {
		got, err := SettingsOf(t.Context(), stubSettings{err: ErrNotFound})
		require.NoError(t, err)
		assert.True(t, got.Enabled)
		assert.Equal(t, DefaultEscalateAfterHours, got.EscalateAfterHours)
		assert.Empty(t, got.EscalatesTo())
	})

	t.Run("a read failure propagates", func(t *testing.T) {
		_, err := SettingsOf(t.Context(), stubSettings{err: errors.New("boom")})
		assert.ErrorContains(t, err, "reading connection alert settings")
	})

	t.Run("a stored section wins", func(t *testing.T) {
		got, err := SettingsOf(t.Context(), stubSettings{
			settings: &Settings{Enabled: true, EscalateAfterHours: 3},
		})
		require.NoError(t, err)
		assert.Equal(t, 3, got.EscalateAfterHours)
	})
}

// stubSettings is a SettingsStore returning a fixed answer.
type stubSettings struct {
	settings *Settings
	err      error
}

func (s stubSettings) Get(context.Context) (*Settings, error) { return s.settings, s.err }

func (stubSettings) Set(context.Context, Settings, string) error { return nil }
