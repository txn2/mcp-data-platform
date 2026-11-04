//go:build integration

package appschema

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/migrationstore"
	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

func realSchema(t *testing.T, db testdb.AppDB) (*Schema, *migrationstore.Store, *postgres.Toolkit) {
	t.Helper()
	multi, err := postgres.ParseMultiConfig("shop", map[string]map[string]any{"shop": db.Target})
	require.NoError(t, err)
	tk := postgres.NewMulti(multi)
	tk.SetConnectGuard(postgres.OwnDatabaseGuard(db.Platform))
	store := migrationstore.New(db.Platform)
	tk.SetMigrationStore(store)
	t.Cleanup(func() { _ = tk.Close() })
	return &Schema{Kit: func() Kit { return tk }, Store: store}, store, tk
}

func put(t *testing.T, store *migrationstore.Store, version int, up, down string) {
	t.Helper()
	_, err := store.Put(context.Background(), postgres.Migration{
		Connection: "shop", Schema: "shop", Version: version, Name: "m", Up: up, Down: down,
		Transactional: true, Checksum: postgres.ChecksumOf(up, down, true),
	})
	require.NoError(t, err)
}

func tableExists(t *testing.T, tk *postgres.Toolkit, table string) bool {
	t.Helper()
	res, err := tk.Query(context.Background(), postgres.Statement{
		Connection: "shop", SQL: "SELECT to_regclass($1) IS NOT NULL AS ok", Params: []any{"shop." + table},
	})
	require.NoError(t, err)
	require.Len(t, res.Rows, 1)
	return res.Rows[0][0] == true
}

// #2411: a release applies the pending migrations; one whose migration fails
// changes nothing, its passing migrations included; a rollback reverts the
// migrations since an earlier release when each has a down migration and,
// when one has none, reverts none and says which.
func TestRelease_RealDB(t *testing.T) {
	testdb.OnAppDBTargets(t, func(t *testing.T) {
		db := testdb.AppDBTarget(t)
		s, store, tk := realSchema(t, db)
		ctx := context.Background()

		put(t, store, 1, "CREATE TABLE products (id bigint PRIMARY KEY, name text NOT NULL)", "DROP TABLE products")
		applied, err := s.Release(ctx, "shop", "shop", "admin@example.com")
		require.NoError(t, err)
		assert.Equal(t, []int{1}, applied.Versions)
		assert.Equal(t, 1, applied.Head)

		put(t, store, 2, "CREATE TABLE carts (id bigint PRIMARY KEY)", "DROP TABLE carts")
		put(t, store, 3, "ALTER TABLE missing ADD COLUMN x int", "")
		_, err = s.Release(ctx, "shop", "shop", "admin@example.com")
		var failed *FailedError
		require.ErrorAs(t, err, &failed)
		assert.False(t, tableExists(t, tk, "carts"), "a release with a failing migration applies none of its migrations")
		st, err := s.State(ctx, "shop", "shop")
		require.NoError(t, err)
		assert.Equal(t, 1, st.Applied)
		assert.Equal(t, []int{2, 3}, st.Pending)

		// Replace the failing migration with one that works.
		put(t, store, 3, "CREATE TABLE orders (id bigint PRIMARY KEY)", "DROP TABLE orders")
		applied, err = s.Release(ctx, "shop", "shop", "admin@example.com")
		require.NoError(t, err)
		assert.Equal(t, []int{2, 3}, applied.Versions)
		assert.True(t, tableExists(t, tk, "orders"))

		reverted, err := s.Rollback(ctx, "shop", "shop", "admin@example.com", 1)
		require.NoError(t, err)
		assert.Equal(t, []int{3, 2}, reverted.Versions)
		assert.Equal(t, 1, reverted.Head)
		assert.Empty(t, reverted.Kept)
		assert.False(t, tableExists(t, tk, "carts"))

		put(t, store, 2, "CREATE TABLE carts (id bigint PRIMARY KEY)", "")
		_, err = s.Release(ctx, "shop", "shop", "admin@example.com")
		require.NoError(t, err)
		kept, err := s.Rollback(ctx, "shop", "shop", "admin@example.com", 1)
		require.NoError(t, err)
		assert.Empty(t, kept.Versions)
		assert.Equal(t, "migration 2 has no down migration, so no migration was reverted", kept.Kept)
		assert.True(t, tableExists(t, tk, "carts"), "nothing is reverted when one migration cannot be")

		events, err := store.Events(ctx, "shop", "shop", 50)
		require.NoError(t, err)
		require.NotEmpty(t, events)
		assert.Contains(t, events[0].Detail, "application release", "the history says a release applied it")
	})
}
