package platform

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
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
