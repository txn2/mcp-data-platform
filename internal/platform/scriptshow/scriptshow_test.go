package scriptshow

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fields(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	require.Len(t, res.Content, 1)
	text, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	out := map[string]any{}
	require.NoError(t, json.Unmarshal([]byte(text.Text), &out))
	return out
}

func TestHandle_ReturnsPresentationOnlyPayload(t *testing.T) {
	res := handle("", input{})
	require.False(t, res.IsError)
	out := fields(t, res)
	// It opens a page and carries no script data, which is what keeps it
	// useless to an agent as a data source.
	assert.Equal(t, true, out["shown"])
	assert.Contains(t, out, "message")
	assert.Contains(t, out, "hint")
	assert.NotContains(t, out, "scripts", "show_scripts must not return script data")
	assert.NotContains(t, out, "runs")
	assert.NotContains(t, out, "count")
}

func TestHandle_NamesThePagesWhenTheDeploymentHasAnAddress(t *testing.T) {
	out := fields(t, handle("https://portal.example.com", input{Search: "sales"}))
	assert.Equal(t, "https://portal.example.com/portal/automations", out["url"])
	assert.Equal(t, "sales", out["search"])
}

// A deployment that has not been told its own public address gets no link
// rather than a guessed one, which would work from inside the cluster and
// nowhere else.
func TestHandle_NoAddressIsNoLink(t *testing.T) {
	assert.NotContains(t, fields(t, handle("", input{})), "url")
}

func TestSchemaIsAnObjectWithSearch(t *testing.T) {
	s, ok := schema().(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "object", s["type"])
	assert.Equal(t, false, s["additionalProperties"])
	props, ok := s["properties"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, props, "search")
}

// The registered tool is reached over a real session, so the binding between
// the advertised tool and its handler is proved rather than assumed.
func TestRegister_CalledOverASession(t *testing.T) {
	ctx := context.Background()
	server := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "v0"}, nil)
	Register(server, "https://portal.example.com")
	t1, t2 := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, t1, nil)
	require.NoError(t, err)
	defer func() { _ = serverSession.Close() }()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v0"}, nil).Connect(ctx, t2, nil)
	require.NoError(t, err)
	defer func() { _ = session.Close() }()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: ToolName, Arguments: map[string]any{"search": "sales"}})
	require.NoError(t, err)
	require.False(t, res.IsError)
	out := fields(t, res)
	assert.Equal(t, "https://portal.example.com/portal/automations", out["url"])
	assert.Equal(t, "sales", out["search"])
}
