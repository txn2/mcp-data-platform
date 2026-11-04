package appwire

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/registry"
	postgreskit "github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

func TestNothingWithoutADatabase(t *testing.T) {
	assert.Nil(t, Service(Deps{}))
	assert.Nil(t, Bundler(nil, Deps{}))
	assert.Nil(t, Register(nil, Deps{}))
}

func TestRegister_AddsTheTool(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	server := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "v0"}, nil)
	svc := Register(server, Deps{DB: db})
	require.NotNil(t, svc)
	assert.Nil(t, svc.Assets, "a missing store is nil, never an interface holding a nil")
	assert.NotNil(t, Bundler(svc, Deps{DB: db}))

	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()
	_, err = server.Connect(ctx, st, nil)
	require.NoError(t, err)
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v0"}, nil).Connect(ctx, ct, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })
	tools, err := cs.ListTools(ctx, nil)
	require.NoError(t, err)
	require.Len(t, tools.Tools, 1)
	assert.Equal(t, "manage_application", tools.Tools[0].Name)
}

func TestKitAndConnections(t *testing.T) {
	assert.Nil(t, Kit(nil)(), "no registry, no toolkit")
	assert.False(t, hasConnection(nil)("orders"))
	reg := registry.NewRegistry()
	assert.Nil(t, Kit(reg)())
	multi, err := postgreskit.ParseMultiConfig("orders", map[string]map[string]any{})
	require.NoError(t, err)
	pg := postgreskit.NewMulti(multi)
	t.Cleanup(func() { _ = pg.Close() })
	require.NoError(t, reg.Register(pg))
	assert.NotNil(t, Kit(reg)())
	assert.False(t, hasConnection(reg)("orders"), "a connection the toolkit does not hold")
}

func TestSourceReads(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()

	mock.ExpectQuery("SELECT EXISTS").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	ok, err := sources{db: db}.Exists(ctx, "hook")
	require.NoError(t, err)
	assert.True(t, ok)
	mock.ExpectQuery("SELECT EXISTS").WillReturnError(errors.New("down"))
	_, err = sources{db: db}.Exists(ctx, "hook")
	require.Error(t, err)

	read := formConnection(db)
	mock.ExpectQuery("SELECT connection_name").WillReturnRows(sqlmock.NewRows([]string{"connection_name"}).AddRow("warehouse"))
	conn, err := read(ctx, "form-signup")
	require.NoError(t, err)
	assert.Equal(t, "warehouse", conn)
	mock.ExpectQuery("SELECT connection_name").WillReturnRows(sqlmock.NewRows([]string{"connection_name"}))
	conn, err = read(ctx, "form-none")
	require.NoError(t, err)
	assert.Empty(t, conn)
	mock.ExpectQuery("SELECT connection_name").WillReturnError(errors.New("down"))
	_, err = read(ctx, "x")
	require.Error(t, err)
}
