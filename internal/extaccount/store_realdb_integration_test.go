//go:build integration

package extaccount

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
)

// TestPostgresStoreRealDB holds the store to the contract the in-memory store
// the service tests use keeps: one account per address, a link claimed once
// while live, conversion killing the links, removal cascading to them, and
// the share revoke and count by address.
func TestPostgresStoreRealDB(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	s := NewPostgresStore(db)
	now := time.Now().UTC().Truncate(time.Microsecond)

	a := Account{ID: "acc1", Email: "Client@Example.org", Name: "Client", Persona: "client", CreatedBy: "admin"}
	require.NoError(t, s.Insert(ctx, a))
	assert.ErrorIs(t, s.Insert(ctx, Account{ID: "acc2", Email: "client@example.org", Persona: "client"}), ErrExists)
	got, err := s.GetByEmail(ctx, "client@EXAMPLE.org")
	require.NoError(t, err)
	assert.Equal(t, "acc1", got.ID)
	_, err = s.Get(ctx, "missing")
	assert.ErrorIs(t, err, ErrNotFound)

	require.NoError(t, s.SetExpiry(ctx, "acc1", new(now.Add(time.Hour))))
	require.NoError(t, s.SetSuspended(ctx, "acc1", &now))
	require.NoError(t, s.TouchSignIn(ctx, "acc1", now))
	got, err = s.Get(ctx, "acc1")
	require.NoError(t, err)
	assert.NotNil(t, got.ExpiresAt)
	assert.NotNil(t, got.SuspendedAt)
	assert.NotNil(t, got.LastSignInAt)
	require.NoError(t, s.SetSuspended(ctx, "acc1", nil))
	assert.ErrorIs(t, s.SetExpiry(ctx, "missing", nil), ErrNotFound)

	require.NoError(t, s.InsertLink(ctx, "h1", "acc1", now.Add(time.Minute)))
	require.NoError(t, s.InsertLink(ctx, "h2", "acc1", now.Add(-time.Minute)))
	n, err := s.CountLinksSince(ctx, "acc1", now.Add(-time.Hour))
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	id, ok, err := s.ClaimLink(ctx, "h1", now)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "acc1", id)
	_, ok, err = s.ClaimLink(ctx, "h1", now)
	require.NoError(t, err)
	assert.False(t, ok, "claimed once")
	_, ok, err = s.ClaimLink(ctx, "h2", now)
	require.NoError(t, err)
	assert.False(t, ok, "expired")

	_, err = db.ExecContext(ctx, `INSERT INTO portal_assets (id, owner_id, name, content_type, s3_bucket, s3_key)
		VALUES ('asset-2092', 'owner', 'shared', 'text/html', 'b', 'k-2092')`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO portal_shares (id, asset_id, token, created_by, shared_with_email, permission)
		VALUES ('sh1', 'asset-2092', 'tok-2092', 'owner', 'client@example.org', 'viewer')`)
	require.NoError(t, err)
	count, err := s.CountSharesTo(ctx, "CLIENT@example.org")
	require.NoError(t, err)
	assert.Equal(t, 1, count)
	require.NoError(t, s.RevokeSharesTo(ctx, "client@example.org"))
	count, err = s.CountSharesTo(ctx, "client@example.org")
	require.NoError(t, err)
	assert.Zero(t, count)

	require.NoError(t, s.InsertLink(ctx, "h3", "acc1", now.Add(time.Minute)))
	converted, err := s.Convert(ctx, "client@example.org", now)
	require.NoError(t, err)
	assert.True(t, converted)
	_, ok, err = s.ClaimLink(ctx, "h3", now)
	require.NoError(t, err)
	assert.False(t, ok, "a converted account's links are gone")
	converted, err = s.Convert(ctx, "client@example.org", now)
	require.NoError(t, err)
	assert.False(t, converted, "converted once")

	list, err := s.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.NoError(t, s.Delete(ctx, "acc1"))
	assert.ErrorIs(t, s.Delete(ctx, "acc1"), ErrNotFound)
}
