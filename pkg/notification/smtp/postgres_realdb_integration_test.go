//go:build integration

package smtp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
)

// passthrough is a no-op StringEncryptor for realdb round-trips.
type passthrough struct{}

func (passthrough) Encrypt(s string) (string, error) { return s, nil }
func (passthrough) Decrypt(s string) (string, error) { return s, nil }

// TestSettingsStoreRealDB round-trips SMTP settings against the real schema.
func TestSettingsStoreRealDB(t *testing.T) {
	db := testdb.New(t)
	store := NewPostgresStore(db, passthrough{})
	ctx := context.Background()

	_, err := store.Get(ctx)
	require.ErrorIs(t, err, ErrNotFound)

	in := Settings{
		Enabled: true, Host: "smtp.example.com", Port: 587,
		Username: "mailer", Password: "secret", From: "p@example.com",
		FromName: "Platform", TLSMode: TLSModeStartTLS,
	}
	require.NoError(t, store.Set(ctx, in, "admin@example.com"))

	got, err := store.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, "smtp.example.com", got.Host)
	require.Equal(t, "secret", got.Password)
	require.Equal(t, "admin@example.com", got.UpdatedBy)
	require.False(t, got.UpdatedAt.IsZero())

	// Empty password on update keeps the stored one.
	in.Password = ""
	in.Host = "smtp2.example.com"
	require.NoError(t, store.Set(ctx, in, "admin2@example.com"))
	got, err = store.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, "smtp2.example.com", got.Host)
	require.Equal(t, "secret", got.Password)
}
