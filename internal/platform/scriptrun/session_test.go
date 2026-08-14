package scriptrun

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSessionCaller_TextOnlyResultIsParsed covers the fallback for a tool that
// answers with a JSON text block and no structured content.
func TestSessionCaller_TextOnlyResultIsParsed(t *testing.T) {
	assert.Equal(t, "boom", firstText(&mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: "boom"}},
	}))
	assert.Contains(t, firstText(&mcp.CallToolResult{}), "no details")
	assert.Contains(t, firstText(&mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: ""}},
	}), "no details")
}

// TestSessionCaller_ResultShapes drives the caller against a server whose tool
// returns each of the three shapes it must handle: structured content, a JSON
// text block, and text that is not JSON at all.
func TestSessionCaller_ResultShapes(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name    string
		text    string
		want    map[string]any
		wantErr string
	}{
		{"json text block", `{"rows":[]}`, map[string]any{"rows": []any{}}, ""},
		{"unparseable text", "not json at all", nil, "no structured result"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "v0"}, nil)
			mcp.AddTool(server, &mcp.Tool{Name: "echo"},
				func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
					return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: tc.text}}}, nil, nil
				})

			t1, t2 := mcp.NewInMemoryTransports()
			serverSession, err := server.Connect(ctx, t1, nil)
			require.NoError(t, err)
			defer func() { _ = serverSession.Close() }()
			client := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v0"}, nil)
			session, err := client.Connect(ctx, t2, nil)
			require.NoError(t, err)
			defer func() { _ = session.Close() }()

			got, err := (&SessionCaller{session: session}).CallTool(ctx, "echo", nil)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}

	t.Run("unknown tool", func(t *testing.T) {
		server := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "v0"}, nil)
		t1, t2 := mcp.NewInMemoryTransports()
		serverSession, err := server.Connect(ctx, t1, nil)
		require.NoError(t, err)
		defer func() { _ = serverSession.Close() }()
		client := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v0"}, nil)
		session, err := client.Connect(ctx, t2, nil)
		require.NoError(t, err)
		defer func() { _ = session.Close() }()

		_, err = (&SessionCaller{session: session}).CallTool(ctx, "missing", nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing")
	})
}
