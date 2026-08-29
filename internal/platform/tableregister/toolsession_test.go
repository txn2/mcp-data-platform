package tableregister

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	portaltoolkit "github.com/txn2/mcp-data-platform/pkg/toolkits/portal"
)

// The acceptance case for #1428, over the assembled system: a person uploads a
// CSV as a managed resource, and the agent in the conversation registers it as
// a table without a portal step.
//
// Nothing here is hand-fed to a handler. The tool is registered on a real MCP
// server by the real portal toolkit, the arguments travel over a client
// session (so the schema they are checked against is the advertised one), the
// adapter resolves the reference and the caller, and the real registrar issues
// the DDL. What this proves that the per-package tests do not is that the
// reference an agent holds actually reaches the DDL: the tool's `reference`
// argument, the adapter's resolver map, and the registrar's Source are wired
// to each other rather than merely correct in isolation.
func TestManageTableOverAnMCPSession(t *testing.T) {
	const resourceID = "res_1"

	h := newHarness(t, func(h *harness) {
		h.objects = &fakeObjects{
			body:    []byte(csvBody),
			bodyCT:  "text/csv",
			entries: []ObjectEntry{{Key: "resources/res_1/vendor_rebates.csv", Size: int64(len(csvBody))}},
		}
	})
	adapter := NewToolAdapter(h.reg, []string{"admin"}, map[string]Subject{
		KindResource: resourceSubjectFor(Record{
			ID: resourceID, Name: "Vendor rebates", Bucket: "resources",
			Key: "resources/res_1/vendor_rebates.csv", ContentType: "text/csv", OwnerID: "u1",
		}),
	}, nil)
	require.NotNil(t, adapter)

	toolkit := portaltoolkit.New(portaltoolkit.Config{Name: "portal"})
	toolkit.SetTableRegistrar(adapter)

	ctx := callerContext("alice@example.com", "analyst")
	client := connectSession(ctx, t, toolkit)

	// The reference is the one a search hit carries, passed verbatim.
	res, err := client.CallTool(ctx, &mcp.CallToolParams{
		Name: "manage_table",
		Arguments: map[string]any{
			"action": "register", "reference": "mcp:resource:" + resourceID, "connection": "scratch",
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, textOf(t, res))

	body := decodeToolResult(t, res)
	assert.Equal(t, "scratch.uploads.analyst_vendor_rebates", body["query_table"])
	assert.Contains(t, body["sample_sql"], "CAST",
		"the join an agent writes next needs the cast, so the answer carries it")

	// The DDL actually ran, against the directory the uploaded file sits in.
	require.Len(t, h.trino.statements, 2)
	assert.Contains(t, h.trino.statements[1], "external_location = 's3://resources/resources/res_1/'")
	assert.Contains(t, h.trino.statements[1], `"store_id" VARCHAR`)

	// A second caller, who did not upload the file, is answered as if it were
	// not there -- over a session of their own, not just at the seam.
	strangerCtx := callerContextFor("u2", "mallory@example.com", "analyst")
	refused, err := connectSession(strangerCtx, t, toolkit).CallTool(strangerCtx, &mcp.CallToolParams{
		Name: "manage_table",
		Arguments: map[string]any{
			"action": "register", "reference": "mcp:resource:" + resourceID, "connection": "scratch",
		},
	})
	require.NoError(t, err)
	require.True(t, refused.IsError)
	assert.Contains(t, textOf(t, refused), "no stored file you can register")
	assert.Len(t, h.trino.statements, 2, "the refused call issued no DDL")
}

// connectSession serves the toolkit's tools over an in-memory transport and
// returns a client session for them.
//
// The identity a handler sees comes from the context the SERVER was connected
// with, not from the one passed to CallTool, so a second caller needs a second
// session rather than a second argument.
func connectSession(ctx context.Context, t *testing.T, toolkit *portaltoolkit.Toolkit) *mcp.ClientSession {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)
	toolkit.RegisterTools(server)
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSess, err := server.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = serverSess.Close() })
	client, err := mcp.NewClient(&mcp.Implementation{Name: "agent", Version: "1.0"}, nil).
		Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func textOf(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	require.NotEmpty(t, res.Content)
	tc, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	return tc.Text
}

func decodeToolResult(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	var body map[string]any
	require.NoError(t, json.Unmarshal([]byte(textOf(t, res)), &body))
	return body
}
