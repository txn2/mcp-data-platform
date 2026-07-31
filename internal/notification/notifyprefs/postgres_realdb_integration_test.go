//go:build integration

package notifyprefs

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// TestPrefsStoreRealDB round-trips preferences against the real schema,
// including the CHECK constraint on mode.
func TestPrefsStoreRealDB(t *testing.T) {
	db := testdb.New(t)
	store := NewPostgresStore(db)
	ctx := context.Background()

	p, err := store.Get(ctx, "new@example.com")
	require.NoError(t, err)
	require.Equal(t, notification.ModeImmediate, p.Mode)

	mode := notification.ModeDaily
	off := false
	p, err = store.Set(ctx, "new@example.com", notification.PrefsUpdate{Mode: &mode, CommentsEnabled: &off})
	require.NoError(t, err)
	require.Equal(t, notification.ModeDaily, p.Mode)
	require.False(t, p.CommentsEnabled)
	require.True(t, p.SharesEnabled)

	p, err = store.Get(ctx, "new@example.com")
	require.NoError(t, err)
	require.Equal(t, notification.ModeDaily, p.Mode)
	require.False(t, p.CommentsEnabled)
}
