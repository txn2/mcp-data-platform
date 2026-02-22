package platform

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// mockToolkit implements registry.Toolkit for testing.
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

// mockConnectionListerToolkit implements both registry.Toolkit and toolkit.ConnectionLister.
type mockConnectionListerToolkit struct {
	mockToolkit
	connections []toolkit.ConnectionDetail
}

func (m *mockConnectionListerToolkit) ListConnections() []toolkit.ConnectionDetail {
	return m.connections
}

func TestHandleListConnections(t *testing.T) {
	t.Run("returns empty list when no toolkits", func(t *testing.T) {
		p := &Platform{
			toolkitRegistry: registry.NewRegistry(),
		}

		result, extra, err := p.handleListConnections(context.Background(), &mcp.CallToolRequest{})
		require.NoError(t, err)
		assert.Nil(t, extra)
		require.NotNil(t, result)
		assert.False(t, result.IsError)

		var out listConnectionsOutput
		textContent, ok := result.Content[0].(*mcp.TextContent)
		require.True(t, ok)
		err = json.Unmarshal([]byte(textContent.Text), &out)
		require.NoError(t, err)
		assert.Equal(t, 0, out.Count)
		assert.Empty(t, out.Connections)
	})

	t.Run("returns all registered toolkits", func(t *testing.T) {
		reg := registry.NewRegistry()
		require.NoError(t, reg.Register(&mockToolkit{
			kind:       "trino",
			name:       "prod",
			connection: "prod-trino",
			tools:      []string{"trino_query"},
		}))
		require.NoError(t, reg.Register(&mockToolkit{
			kind:       "datahub",
			name:       "primary",
			connection: "primary-datahub",
			tools:      []string{"datahub_search"},
		}))
		require.NoError(t, reg.Register(&mockToolkit{
			kind:       "s3",
			name:       "data-lake",
			connection: "data-lake-s3",
			tools:      []string{"s3_list_buckets"},
		}))

		p := &Platform{
			toolkitRegistry: reg,
		}

		result, extra, err := p.handleListConnections(context.Background(), &mcp.CallToolRequest{})
		require.NoError(t, err)
		assert.Nil(t, extra)
		require.NotNil(t, result)
		assert.False(t, result.IsError)

		var out listConnectionsOutput
		textContent, ok := result.Content[0].(*mcp.TextContent)
		require.True(t, ok)
		err = json.Unmarshal([]byte(textContent.Text), &out)
		require.NoError(t, err)

		assert.Equal(t, 3, out.Count)
		assert.Len(t, out.Connections, 3)

		// Build a map for assertion (order from registry.All() is not guaranteed)
		connByKind := make(map[string]connectionEntry)
		for _, c := range out.Connections {
			connByKind[c.Kind] = c
		}

		trinoConn := connByKind["trino"]
		assert.Equal(t, "prod", trinoConn.Name)
		assert.Equal(t, "prod-trino", trinoConn.Connection)

		dhConn := connByKind["datahub"]
		assert.Equal(t, "primary", dhConn.Name)
		assert.Equal(t, "primary-datahub", dhConn.Connection)

		s3Conn := connByKind["s3"]
		assert.Equal(t, "data-lake", s3Conn.Name)
		assert.Equal(t, "data-lake-s3", s3Conn.Connection)
	})

	t.Run("single toolkit returns count 1", func(t *testing.T) {
		reg := registry.NewRegistry()
		require.NoError(t, reg.Register(&mockToolkit{
			kind:       "trino",
			name:       "single",
			connection: "single-conn",
			tools:      []string{"trino_query"},
		}))

		p := &Platform{
			toolkitRegistry: reg,
		}

		result, _, err := p.handleListConnections(context.Background(), &mcp.CallToolRequest{})
		require.NoError(t, err)

		var out listConnectionsOutput
		textContent, ok := result.Content[0].(*mcp.TextContent)
		require.True(t, ok)
		err = json.Unmarshal([]byte(textContent.Text), &out)
		require.NoError(t, err)

		assert.Equal(t, 1, out.Count)
		assert.Equal(t, "trino", out.Connections[0].Kind)
		assert.Equal(t, "single", out.Connections[0].Name)
		assert.Equal(t, "single-conn", out.Connections[0].Connection)
	})
}

func TestHandleListConnections_WithConnectionLister(t *testing.T) {
	t.Run("expands multi-connection toolkit", func(t *testing.T) {
		reg := registry.NewRegistry()
		require.NoError(t, reg.Register(&mockConnectionListerToolkit{
			mockToolkit: mockToolkit{
				kind:  "trino",
				name:  "warehouse",
				tools: []string{"trino_query"},
			},
			connections: []toolkit.ConnectionDetail{
				{Name: "warehouse", Description: "Analytics warehouse", IsDefault: true},
				{Name: "elasticsearch", Description: "Sales data", IsDefault: false},
				{Name: "cassandra", Description: "", IsDefault: false},
			},
		}))
		require.NoError(t, reg.Register(&mockToolkit{
			kind:       "datahub",
			name:       "primary",
			connection: "primary-datahub",
			tools:      []string{"datahub_search"},
		}))

		p := &Platform{toolkitRegistry: reg}
		result, _, err := p.handleListConnections(context.Background(), &mcp.CallToolRequest{})
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.False(t, result.IsError)

		var out listConnectionsOutput
		textContent, ok := result.Content[0].(*mcp.TextContent)
		require.True(t, ok)
		err = json.Unmarshal([]byte(textContent.Text), &out)
		require.NoError(t, err)

		// 3 Trino connections + 1 DataHub = 4 total
		assert.Equal(t, 4, out.Count)
		assert.Len(t, out.Connections, 4)

		// Build map by connection name
		connByName := make(map[string]connectionEntry)
		for _, c := range out.Connections {
			connByName[c.Name] = c
		}

		wh := connByName["warehouse"]
		assert.Equal(t, "trino", wh.Kind)
		assert.Equal(t, "warehouse", wh.Connection)
		assert.Equal(t, "Analytics warehouse", wh.Description)
		assert.True(t, wh.IsDefault)

		es := connByName["elasticsearch"]
		assert.Equal(t, "trino", es.Kind)
		assert.Equal(t, "elasticsearch", es.Connection)
		assert.Equal(t, "Sales data", es.Description)
		assert.False(t, es.IsDefault)

		cass := connByName["cassandra"]
		assert.Equal(t, "trino", cass.Kind)
		assert.Empty(t, cass.Description)
		assert.False(t, cass.IsDefault)

		dh := connByName["primary"]
		assert.Equal(t, "datahub", dh.Kind)
		assert.Equal(t, "primary-datahub", dh.Connection)
		assert.Empty(t, dh.Description)
		assert.False(t, dh.IsDefault)
	})

	t.Run("single connection lister returns one entry", func(t *testing.T) {
		reg := registry.NewRegistry()
		require.NoError(t, reg.Register(&mockConnectionListerToolkit{
			mockToolkit: mockToolkit{
				kind:  "trino",
				name:  "prod",
				tools: []string{"trino_query"},
			},
			connections: []toolkit.ConnectionDetail{
				{Name: "prod", Description: "Production", IsDefault: true},
			},
		}))

		p := &Platform{toolkitRegistry: reg}
		result, _, err := p.handleListConnections(context.Background(), &mcp.CallToolRequest{})
		require.NoError(t, err)

		var out listConnectionsOutput
		textContent, ok := result.Content[0].(*mcp.TextContent)
		require.True(t, ok)
		err = json.Unmarshal([]byte(textContent.Text), &out)
		require.NoError(t, err)

		assert.Equal(t, 1, out.Count)
		assert.Equal(t, "prod", out.Connections[0].Name)
		assert.Equal(t, "Production", out.Connections[0].Description)
		assert.True(t, out.Connections[0].IsDefault)
	})
}

func TestPlatformToolsIncludesListConnections(t *testing.T) {
	p := &Platform{}
	tools := p.PlatformTools()

	var found bool
	for _, ti := range tools {
		if ti.Name == "list_connections" {
			found = true
			assert.Equal(t, "platform", ti.Kind)
		}
	}
	assert.True(t, found, "PlatformTools() should include list_connections")
}

func TestRegisterConnectionsTool(t *testing.T) {
	reg := registry.NewRegistry()
	require.NoError(t, reg.Register(&mockToolkit{
		kind: "trino", name: "prod", connection: "prod-trino",
		tools: []string{"trino_query"},
	}))

	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0.0"}, nil)
	p := &Platform{
		mcpServer:       server,
		toolkitRegistry: reg,
	}

	// Should not panic and should register the tool.
	p.registerConnectionsTool()

	// Invoke the registered tool through the MCP server transport to cover
	// the closure callback.
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: httpServer.URL}, nil)
	require.NoError(t, err)
	defer func() { _ = session.Close() }()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "list_connections",
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEmpty(t, result.Content)
	assert.False(t, result.IsError)

	var out listConnectionsOutput
	textContent, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	err = json.Unmarshal([]byte(textContent.Text), &out)
	require.NoError(t, err)
	assert.Equal(t, 1, out.Count)
	assert.Equal(t, "trino", out.Connections[0].Kind)
}
