package connsource

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

const ordersURN = "urn:li:dataset:(urn:li:dataPlatform:trino,db.public.orders,PROD)"

// mockToolkit is a single-connection toolkit: its instance name and its
// configured connection name may differ, which is the distinction under test.
type mockToolkit struct {
	kind       string
	name       string
	connection string
	tools      []string
}

func (m *mockToolkit) Kind() string                          { return m.kind }
func (m *mockToolkit) Name() string                          { return m.name }
func (m *mockToolkit) Connection() string                    { return m.connection }
func (*mockToolkit) RegisterTools(_ *mcp.Server)             {}
func (m *mockToolkit) Tools() []string                       { return m.tools }
func (*mockToolkit) SetSemanticProvider(_ semantic.Provider) {}
func (*mockToolkit) SetQueryProvider(_ query.Provider)       {}
func (*mockToolkit) Close() error                            { return nil }

// mockConnectionListerToolkit is a multi-connection toolkit: one registered
// toolkit serving several connections, each named independently of it.
type mockConnectionListerToolkit struct {
	mockToolkit
	connections []toolkit.ConnectionDetail
}

func (m *mockConnectionListerToolkit) ListConnections() []toolkit.ConnectionDetail {
	return m.connections
}

// urnFixture builds the deployment shape the attribution must get right: a
// multi-connection Trino toolkit whose connections are named individually while
// the source map keys the toolkit itself, and a single S3 toolkit whose
// connection name differs from its instance.
func urnFixture(t *testing.T) (*Map, *registry.Registry) {
	t.Helper()

	reg := registry.NewRegistry()
	require.NoError(t, reg.Register(&mockConnectionListerToolkit{
		mockToolkit: mockToolkit{kind: "trino", name: "warehouse", tools: []string{"trino_query"}},
		connections: []toolkit.ConnectionDetail{{Name: "warehouse-a"}, {Name: "warehouse-b"}},
	}))
	require.NoError(t, reg.Register(&mockToolkit{
		kind: "s3", name: "lake", connection: "prod-lake", tools: []string{"s3_list"},
	}))

	sources := NewMap()
	sources.Add(Source{Kind: "trino", Name: "warehouse", DataHubSourceName: "trino"})
	sources.Add(Source{Kind: "s3", Name: "lake", DataHubSourceName: "s3"})
	return sources, reg
}

func TestConnectionNamesForURN(t *testing.T) {
	sources, reg := urnFixture(t)

	t.Run("expands a toolkit to the connections a persona actually names", func(t *testing.T) {
		assert.Equal(t, []string{"warehouse-a", "warehouse-b"},
			ConnectionNamesForURN(sources, reg.All(), ordersURN))
	})

	t.Run("a single-connection toolkit resolves to its connection name", func(t *testing.T) {
		assert.Equal(t, []string{"prod-lake"},
			ConnectionNamesForURN(sources, reg.All(), "urn:li:dataset:(urn:li:dataPlatform:s3,bucket/raw,PROD)"))
	})

	t.Run("a connection added after startup is a candidate", func(t *testing.T) {
		// The source map is built once at startup; the toolkit's connection set is
		// live. A connection added later must not be treated as belonging to no
		// connection (which would hide its datasets from the persona granted it).
		added := &mockConnectionListerToolkit{
			mockToolkit: mockToolkit{kind: "trino", name: "warehouse", tools: []string{"trino_query"}},
			connections: []toolkit.ConnectionDetail{{Name: "warehouse-a"}, {Name: "warehouse-c"}},
		}
		assert.Equal(t, []string{"warehouse-a", "warehouse-c"},
			ConnectionNamesForURN(sources, []registry.Toolkit{added}, ordersURN))
	})

	t.Run("a connection with its own mapping overrides its toolkit's", func(t *testing.T) {
		own := NewMap()
		own.Add(Source{Kind: "trino", Name: "warehouse", DataHubSourceName: "trino"})
		own.Add(Source{Kind: "trino", Name: "warehouse-b", DataHubSourceName: "postgres"})
		assert.Equal(t, []string{"warehouse-a"}, ConnectionNamesForURN(own, reg.All(), ordersURN),
			"warehouse-b's datasets live under a different platform")
		assert.Equal(t, []string{"warehouse-b"},
			ConnectionNamesForURN(own, reg.All(), "urn:li:dataset:(urn:li:dataPlatform:postgres,db.public.orders,PROD)"))
	})

	t.Run("no mapping attributes nothing", func(t *testing.T) {
		assert.Empty(t, ConnectionNamesForURN(nil, reg.All(), ordersURN), "no source map means no attribution")
		assert.Empty(t, ConnectionNamesForURN(sources, reg.All(), "not-a-urn"))
		assert.Empty(t, ConnectionNamesForURN(sources, nil, ordersURN),
			"no live connection can serve the URN")
	})
}
