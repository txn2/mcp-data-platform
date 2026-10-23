package scriptlayer

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// show_scripts is registered wherever scripts are kept, including a deployment
// that cannot execute them: seeing what exists does not depend on a worker.
func TestRegisterTool_RegistersShowScripts(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "v0"}, nil)
	New(Config{Store: newMemStore()}).RegisterTool(server)

	tools := listToolNames(t, server)
	assert.Contains(t, tools, ToolNameShowScripts)
	assert.NotContains(t, tools, ToolNameRunScript, "no queue, no run_script")
}

// The registered tool is reached end to end, over a real session, so the
// binding between the advertised tool and the handler is proved rather than
// assumed.
func TestShowScripts_CalledOverASession(t *testing.T) {
	ctx := context.Background()
	server := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "v0"}, nil)
	New(Config{Store: newMemStore(), PortalURL: "https://portal.example.com"}).
		RegisterTool(server)

	t1, t2 := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, t1, nil)
	require.NoError(t, err)
	defer func() { _ = serverSession.Close() }()
	client := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v0"}, nil)
	session, err := client.Connect(ctx, t2, nil)
	require.NoError(t, err)
	defer func() { _ = session.Close() }()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      ToolNameShowScripts,
		Arguments: map[string]any{"search": "sales"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, resultText(res))

	out := resultFields(t, res)
	assert.Equal(t, "https://portal.example.com/portal/automations", out["url"])
	assert.Equal(t, "sales", out["search"])
}

func TestRegisterTool_NoDatabaseRegistersNoShowScripts(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "v0"}, nil)
	New(Config{}).RegisterTool(server)

	assert.NotContains(t, listToolNames(t, server), ToolNameShowScripts)
}
