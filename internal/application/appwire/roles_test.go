package appwire

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptstore"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	postgreskit "github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// TestLiveRoles refuses each run-role operation with no application
// database toolkit registered.
func TestLiveRoles(t *testing.T) {
	l := liveRoles{reg: registry.NewRegistry()}
	ctx := context.Background()
	_, _, err := l.CreateRunRole(ctx, "orders", "shop")
	require.ErrorIs(t, err, errNoKit)
	_, err = l.GrantRunRole(ctx, "orders", "shop")
	require.ErrorIs(t, err, errNoKit)
	require.ErrorIs(t, l.DropRunRole(ctx, "orders", "shop"), errNoKit)
	assert.False(t, members(registry.NewRegistry())(ctx, "orders", "a", "b"))

	pg := postgreskit.NewMulti(postgreskit.MultiConfig{DefaultName: "x"})
	t.Cleanup(func() { _ = pg.Close() })
	reg := registry.NewRegistry()
	require.NoError(t, reg.Register(pg))
	live := liveRoles{reg: reg}
	_, _, err = live.CreateRunRole(ctx, "orders", "shop")
	require.Error(t, err, "the toolkit refuses a connection it does not serve")
	_, err = live.GrantRunRole(ctx, "orders", "shop")
	require.Error(t, err)
	require.Error(t, live.CloneSchema(ctx, "orders", "shop", "shop_dev"), "the toolkit refuses a connection it does not serve")
	require.Error(t, live.DropRunRole(ctx, "orders", "shop"))
	assert.False(t, members(reg)(ctx, "orders", "a", "b"))
}

// With no application database toolkit, a clone is refused; a library is
// read by its name (#2709).
func TestCloneSchemaAndLibraryNamed(t *testing.T) {
	require.ErrorIs(t, liveRoles{reg: registry.NewRegistry()}.CloneSchema(context.Background(), "orders", "shop", "shop_dev"), errNoKit)

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	libs := libraries{Store: scriptstore.New(db), db: db}
	mock.ExpectQuery("SELECT id FROM scripts WHERE library AND name").WithArgs("shop-sql").WillReturnError(sql.ErrNoRows)
	_, err = libs.LibraryNamed(context.Background(), "shop-sql")
	require.ErrorContains(t, err, "reading library shop-sql")
}
