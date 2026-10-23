package personaaccesswire

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/connreach"
	"github.com/txn2/mcp-data-platform/internal/platform/tableregister"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/trino"
)

// scratchToolkit is a Trino toolkit carrying a scratch catalog on one
// connection that accepts writes.
type scratchToolkit struct{}

func (scratchToolkit) Kind() string                          { return kindTrino }
func (scratchToolkit) Name() string                          { return "trino" }
func (scratchToolkit) Connection() string                    { return "" }
func (scratchToolkit) RegisterTools(*mcp.Server)             {}
func (scratchToolkit) Tools() []string                       { return []string{"trino_query", "trino_execute"} }
func (scratchToolkit) SetSemanticProvider(semantic.Provider) {}
func (scratchToolkit) SetQueryProvider(query.Provider)       {}
func (scratchToolkit) Close() error                          { return nil }
func (scratchToolkit) Exec(context.Context, string, string) error {
	return nil
}

func (scratchToolkit) ScratchTarget(conn string) (trino.ScratchConfig, bool) {
	if conn == "acme-trino" {
		return trino.ScratchConfig{Catalog: "scratch", Schema: "uploads"}, true
	}
	return trino.ScratchConfig{}, false
}
func (scratchToolkit) AcceptsWrites(conn string) bool { return conn == "acme-trino" }
func (scratchToolkit) TableExists(context.Context, string, string, string, string) (bool, error) {
	return true, nil
}

func (scratchToolkit) TableLocation(context.Context, string, string, string, string) (location string, found bool, err error) {
	return "", false, nil
}

var _ tableregister.Executor = scratchToolkit{}

type fixedLister []connreach.Connection

func (f fixedLister) ForPersona(context.Context, string, bool) []connreach.Connection { return f }

// The inventory is read from the live toolkits: each tool with its kind, each
// connection with the scratch catalog its Trino toolkit carries.
func TestInventoryReadsTheToolkits(t *testing.T) {
	reg := registry.NewRegistry()
	require.NoError(t, reg.Register(scratchToolkit{}))
	conns := fixedLister{{Name: "acme-trino", Kind: kindTrino}, {Name: "warehouse", Kind: kindTrino}, {Name: "lake", Kind: "s3"}}
	inv := inventory(reg, func() []string { return []string{"trino_query", "search"} }, nil, conns)(t.Context())

	require.Len(t, inv.Tools, 2)
	assert.Equal(t, kindTrino, inv.Tools[0].Kind)
	assert.Empty(t, inv.Tools[1].Kind, "a platform tool reaches no connection")
	require.Len(t, inv.Connections, 3)
	require.NotNil(t, inv.Connections[0].Scratch)
	assert.Equal(t, "uploads", inv.Connections[0].Scratch.Schema)
	assert.True(t, inv.Connections[0].Scratch.Writes)
	assert.Nil(t, inv.Connections[1].Scratch)
	assert.Empty(t, inv.Operations)
	assert.Empty(t, inv.Registrations)

	assert.Nil(t, scratchExecutor(registry.NewRegistry()))
}

// The channels that deliver through a connection are named on it, read from
// the channel store.
func TestChannelsByConnection(t *testing.T) {
	assert.Empty(t, channelsByConnection(t.Context(), nil))
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	mock.ExpectQuery("FROM notification_channels").WillReturnError(assert.AnError)
	assert.Empty(t, channelsByConnection(t.Context(), db))
}
