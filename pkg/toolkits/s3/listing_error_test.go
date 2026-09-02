package s3

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFailedListingReachesTheClientAsTheToolsOwnError pins txn2/mcp-s3#141,
// fixed in mcp-s3 v1.4.0: the toolkit this adapter builds, driven through an
// MCP server and an in-memory client against an endpoint that refuses every
// request, answers s3_list (buckets and objects) with the tool's error
// result rather than the SDK's output-validation error in its place. Before
// the fix the SDK validated the zero output struct, whose nil slices marshal
// as null, against a schema that said array, and discarded the tool's reason.
func TestFailedListingReachesTheClientAsTheToolsOwnError(t *testing.T) {
	refusing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `<Error><Code>AccessDenied</Code><Message>refused</Message></Error>`, http.StatusForbidden)
	}))
	t.Cleanup(refusing.Close)

	tk, err := New("acme", Config{
		Region:          s3TestRegionEast,
		Endpoint:        refusing.URL,
		AccessKeyID:     "a",
		SecretAccessKey: "b",
		UsePathStyle:    true,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tk.Close() })

	server := mcp.NewServer(&mcp.Implementation{Name: "s", Version: "v0"}, nil)
	tk.RegisterTools(server)
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v0"}, nil).Connect(ctx, ct, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })

	for name, args := range map[string]map[string]any{
		"buckets": {},
		"objects": {"bucket": "b"},
	} {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "s3_list", Arguments: args})
		require.NoError(t, err, "%s: the failure is a tool result, not a protocol error", name)
		require.True(t, res.IsError, "%s: the listing failed", name)
		require.NotEmpty(t, res.Content)
		content, ok := res.Content[0].(*mcp.TextContent)
		require.True(t, ok)
		assert.False(t, strings.Contains(content.Text, "validating tool output"), "%s: the SDK's validation text must not replace the tool's error: %s", name, content.Text)
		assert.Contains(t, content.Text, "failed to list", "%s: the tool's own reason reaches the client: %s", name, content.Text)
	}
}
