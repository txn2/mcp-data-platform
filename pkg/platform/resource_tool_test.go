package platform

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeTestResourcePlatform returns a minimal Platform with the given resource registry.
func makeTestResourcePlatform(registry map[string]mcp.ResourceHandler) *Platform {
	return &Platform{resourceRegistry: registry}
}

func TestHandleReadResource_Found(t *testing.T) {
	p := makeTestResourcePlatform(map[string]mcp.ResourceHandler{
		"brand://theme": func(_ context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{
				Contents: []*mcp.ResourceContents{{URI: "brand://theme", Text: `{"color":"blue"}`}},
			}, nil
		},
	})

	result, extra, err := p.handleReadResource(context.Background(), &mcp.CallToolRequest{}, "brand://theme")
	require.NoError(t, err)
	assert.Nil(t, extra)
	require.NotNil(t, result)
	assert.False(t, result.IsError)
	require.Len(t, result.Content, 1)
	text, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	assert.Equal(t, `{"color":"blue"}`, text.Text)
}

func TestHandleReadResource_NotFound(t *testing.T) {
	p := makeTestResourcePlatform(map[string]mcp.ResourceHandler{})

	result, extra, err := p.handleReadResource(context.Background(), &mcp.CallToolRequest{}, "brand://missing")
	require.NoError(t, err)
	assert.Nil(t, extra)
	require.NotNil(t, result)
	assert.True(t, result.IsError)
	text, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, text.Text, "not found")
	assert.Contains(t, text.Text, "brand://missing")
}

func TestHandleReadResource_HandlerError(t *testing.T) {
	p := makeTestResourcePlatform(map[string]mcp.ResourceHandler{
		"brand://broken": func(_ context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return nil, errors.New("disk read failure")
		},
	})

	result, _, err := p.handleReadResource(context.Background(), &mcp.CallToolRequest{}, "brand://broken")
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.IsError)
	text, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, text.Text, "disk read failure")
}

func TestHandleReadResource_EmptyContents(t *testing.T) {
	p := makeTestResourcePlatform(map[string]mcp.ResourceHandler{
		"brand://empty": func(_ context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{}}, nil
		},
	})

	result, _, err := p.handleReadResource(context.Background(), &mcp.CallToolRequest{}, "brand://empty")
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.IsError)
	text, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, text.Text, "no content")
}

func TestRegisterResourceTool_Empty(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0.0"}, nil)
	p := &Platform{
		mcpServer:        server,
		resourceRegistry: map[string]mcp.ResourceHandler{},
	}

	// Must not panic; tool should NOT be registered.
	p.registerResourceTool()

	// Verify tool is absent by trying to call it through a real transport.
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: httpServer.URL}, nil)
	require.NoError(t, err)
	defer func() { _ = session.Close() }()

	_, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "read_resource"})
	assert.Error(t, err, "read_resource should not exist when registry is empty")
}

func TestRegisterResourceTool_NonEmpty(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0.0"}, nil)
	p := &Platform{
		mcpServer: server,
		resourceRegistry: map[string]mcp.ResourceHandler{
			"brand://theme": func(_ context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
				return &mcp.ReadResourceResult{
					Contents: []*mcp.ResourceContents{{URI: "brand://theme", Text: "{}"}},
				}, nil
			},
			"hints://operational": func(_ context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
				return &mcp.ReadResourceResult{
					Contents: []*mcp.ResourceContents{{URI: "hints://operational", Text: "[]"}},
				}, nil
			},
		},
	}

	p.registerResourceTool()

	// Exercise through real transport — verifies the tool is registered and callable.
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: httpServer.URL}, nil)
	require.NoError(t, err)
	defer func() { _ = session.Close() }()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "read_resource",
		Arguments: map[string]any{"uri": "brand://theme"},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError)

	// Also verify both URIs appear in the description — list tools to check.
	tools, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	var desc string
	for _, t := range tools.Tools {
		if t.Name == "read_resource" {
			desc = t.Description
			break
		}
	}
	assert.Contains(t, desc, "brand://theme")
	assert.Contains(t, desc, "hints://operational")
}

func TestResourceRegistryPopulatedFromCustomResources(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0.0"}, nil)
	p := &Platform{
		mcpServer:        server,
		resourceRegistry: make(map[string]mcp.ResourceHandler),
		config: &Config{
			Resources: ResourcesConfig{
				Custom: []CustomResourceDef{
					{
						URI:      "brand://theme",
						Name:     "Brand Theme",
						MIMEType: "application/json",
						Content:  `{"color":"blue"}`,
					},
					{
						URI:      "brand://logo",
						Name:     "Brand Logo",
						MIMEType: "image/svg+xml",
						Content:  `<svg/>`,
					},
				},
			},
		},
	}

	p.registerCustomResources()

	assert.Contains(t, p.resourceRegistry, "brand://theme", "registry should contain brand://theme")
	assert.Contains(t, p.resourceRegistry, "brand://logo", "registry should contain brand://logo")

	// Handlers must be callable and return the configured content.
	result, err := p.resourceRegistry["brand://theme"](context.Background(), nil)
	require.NoError(t, err)
	require.Len(t, result.Contents, 1)
	assert.Equal(t, `{"color":"blue"}`, result.Contents[0].Text)
}
