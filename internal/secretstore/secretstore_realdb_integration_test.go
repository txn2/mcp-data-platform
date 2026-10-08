//go:build integration

package secretstore

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
)

// TestSecretsRealDB runs a secret through its life against a migrated
// database: created with its value encrypted, rescoped without retyping the
// value, rotated, looked up in and out of scope, and deleted.
func TestSecretsRealDB(t *testing.T) {
	db := testdb.New(t)
	s := NewStore(db, prefixEncryptor{})
	ctx := context.Background()

	value := "hunter22"
	sec, created, err := s.Put(ctx, Write{Name: "portal_password", Description: "vendor portal", Value: &value, AllowConnections: []string{"grid"}, Actor: "admin@example.com"})
	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, []string{}, sec.AllowPersonas)

	var stored string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT value FROM gateway_secrets WHERE name = 'portal_password'`).Scan(&stored))
	assert.Equal(t, "enc:hunter22", stored, "the value is encrypted at rest")

	_, created, err = s.Put(ctx, Write{Name: "portal_password", Description: "rescoped", AllowConnections: []string{"grid", "vendor"}, AllowPersonas: []string{"admin"}, Actor: "other@example.com"})
	require.NoError(t, err)
	assert.False(t, created)
	got, err := s.Lookup(ctx, "vendor", "admin")("portal_password")
	require.NoError(t, err)
	assert.Equal(t, "hunter22", got, "an update without a value keeps the stored one")

	rotated := "correct-horse"
	_, _, err = s.Put(ctx, Write{Name: "portal_password", AllowConnections: []string{"grid"}, Value: &rotated})
	require.NoError(t, err)
	got, err = s.Lookup(ctx, "grid", "analyst")("portal_password")
	require.NoError(t, err, "personas cleared by the update")
	assert.Equal(t, "correct-horse", got, "a rotated value is read on the next call")

	_, err = s.Lookup(ctx, "vendor", "")("portal_password")
	require.ErrorContains(t, err, "may not be sent through connection")

	list, err := s.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "admin@example.com", list[0].CreatedBy)
	assert.Equal(t, "", list[0].UpdatedBy)

	require.NoError(t, s.Delete(ctx, "portal_password"))
	require.ErrorIs(t, s.Delete(ctx, "portal_password"), ErrNotFound)
	_, err = s.Get(ctx, "portal_password")
	require.ErrorIs(t, err, ErrNotFound)
}
