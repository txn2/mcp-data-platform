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
// returns each of the shapes it must handle: a JSON text block, text that is
// not JSON at all, and a JSON array.
//
// Since #1419 a script calls any tool its author can call, so a tool whose
// answer is text — a gateway-proxied upstream tool carries no structured
// content of its own unless enrichment fires — must reach the script rather
// than fail a run whose call succeeded. It arrives under TextResultKey, which
// is one rule an author can hold rather than a shape per tool.
func TestSessionCaller_ResultShapes(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name    string
		text    string
		want    map[string]any
		wantErr string
	}{
		{"json text block", `{"rows":[]}`, map[string]any{"rows": []any{}}, ""},
		{"prose", "not json at all", map[string]any{TextResultKey: "not json at all"}, ""},
		{"a json array", `[1,2]`, map[string]any{TextResultKey: `[1,2]`}, ""},
		// A SUCCESSFUL call that carried no text carried no text. Handing the
		// script firstText's error placeholder here would give it a sentence
		// about a failure that did not happen, as data.
		{"no text at all", "", map[string]any{TextResultKey: ""}, ""},
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

// TestSessionCaller_DeclaresReadOnly drives the annotation read a draft's write
// barrier falls back to for a tool no classification rule names, against a real
// listing over a real session.
func TestSessionCaller_DeclaresReadOnly(t *testing.T) {
	ctx := context.Background()
	server := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "v0"}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name: "vendor__list_contacts", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{}, nil, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "vendor__create_invoice"},
		func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{}, nil, nil
		})

	t1, t2 := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, t1, nil)
	require.NoError(t, err)
	defer func() { _ = serverSession.Close() }()
	client := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v0"}, nil)
	session, err := client.Connect(ctx, t2, nil)
	require.NoError(t, err)
	defer func() { _ = session.Close() }()

	caller := &SessionCaller{session: session}

	readOnly, known := caller.DeclaresReadOnly(ctx, "vendor__list_contacts")
	assert.True(t, known, "the server advertises it")
	assert.True(t, readOnly, "and declares it read-only")

	readOnly, known = caller.DeclaresReadOnly(ctx, "vendor__create_invoice")
	assert.True(t, known)
	assert.False(t, readOnly, "declaring nothing is not declaring a read")

	readOnly, known = caller.DeclaresReadOnly(ctx, "not_advertised")
	assert.False(t, known, "a tool the listing does not carry is unknown")
	assert.False(t, readOnly)
}

// TestSessionCaller_DeclaresReadOnlyOnAClosedSession pins the answer when the
// listing cannot be read at all: unknown, which leaves the barrier's
// deny-by-default to decide rather than reporting a read.
func TestSessionCaller_DeclaresReadOnlyOnAClosedSession(t *testing.T) {
	ctx := context.Background()
	server := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "v0"}, nil)
	t1, t2 := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, t1, nil)
	require.NoError(t, err)
	client := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v0"}, nil)
	session, err := client.Connect(ctx, t2, nil)
	require.NoError(t, err)
	require.NoError(t, session.Close())
	require.NoError(t, serverSession.Close())

	readOnly, known := (&SessionCaller{session: session}).DeclaresReadOnly(ctx, "anything")
	assert.False(t, known)
	assert.False(t, readOnly)
}
