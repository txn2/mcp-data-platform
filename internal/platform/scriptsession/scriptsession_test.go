package scriptsession

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/internal/platform/toolratelimit"
	"github.com/txn2/mcp-data-platform/pkg/middleware"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/scriptcallsite"
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

// A call made with a call site on its context carries it to the server in the
// request's _meta (#1907), which is where the audit middleware reads it.
func TestSessionCaller_SendsTheCallSite(t *testing.T) {
	ctx := context.Background()
	server := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "v0"}, nil)
	var got []mcp.Meta
	server.AddTool(&mcp.Tool{Name: "trino_query", InputSchema: json.RawMessage(`{"type":"object"}`)},
		func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			got = append(got, req.Params.Meta)
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "{}"}}}, nil
		})
	t1, t2 := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, t1, nil)
	require.NoError(t, err)
	defer func() { _ = serverSession.Close() }()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v0"}, nil).Connect(ctx, t2, nil)
	require.NoError(t, err)
	defer func() { _ = session.Close() }()
	caller := &SessionCaller{session: session}

	_, err = caller.CallTool(scriptcallsite.With(ctx, []string{"9:6", "22:20"}), "trino_query", map[string]any{})
	require.NoError(t, err)
	_, err = caller.CallTool(ctx, "trino_query", map[string]any{})
	require.NoError(t, err)

	require.Len(t, got, 2)
	assert.Equal(t, []string{"9:6", "22:20"}, scriptcallsite.FromMeta(got[0]))
	assert.Nil(t, scriptcallsite.FromMeta(got[1]), "a call with no site sends none")
}

// TestRefusalError pins how a failed result becomes the error a Caller returns:
// the envelope's code and interval are read as data, the text is the result's
// own, and a result without the envelope is the plain error it always was.
func TestRefusalError(t *testing.T) {
	text := &mcp.TextContent{Text: "refused"}
	cases := []struct {
		name string
		res  *mcp.CallToolResult
		code string
		wait time.Duration
	}{
		{"no structured content", &mcp.CallToolResult{Content: []mcp.Content{text}}, "", 0},
		{"structured content without an envelope", &mcp.CallToolResult{
			Content: []mcp.Content{text}, StructuredContent: map[string]any{"rows": []any{}},
		}, "", 0},
		{"an envelope without a code", &mcp.CallToolResult{
			Content: []mcp.Content{text}, StructuredContent: map[string]any{"error": map[string]any{"message": "m"}},
		}, "", 0},
		{"an envelope naming no interval", &mcp.CallToolResult{
			Content: []mcp.Content{text}, StructuredContent: map[string]any{"error": map[string]any{"code": "unauthorized"}},
		}, "unauthorized", 0},
		{"a rate-limit envelope", &mcp.CallToolResult{
			Content: []mcp.Content{text},
			StructuredContent: map[string]any{"error": map[string]any{
				"code": "rate_limited", "retry_after_seconds": float64(3),
			}},
		}, "rate_limited", 3 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := refusalError(tc.res)
			require.Error(t, err)
			assert.Equal(t, "refused", err.Error(), "the text the author reads is the tool's own")
			var refusal *RefusalError
			if tc.code == "" {
				assert.False(t, errors.As(err, &refusal), "no envelope, no typed refusal")
				return
			}
			require.True(t, errors.As(err, &refusal))
			assert.Equal(t, tc.code, refusal.Code)
			assert.Equal(t, tc.wait, refusal.RetryAfter)
		})
	}
}

// TestSessionCaller_ReadsTheEnvelopeOverTheWire drives the production Caller
// against a tool that refuses with BuildErrorResult, so the field the limiter
// sets is proven to survive JSON and arrive as the refusal the engine paces on.
func TestSessionCaller_ReadsTheEnvelopeOverTheWire(t *testing.T) {
	ctx := context.Background()
	server := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "v0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "refuse"},
		func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			pe := middleware.NewToolError(toolratelimit.CodeRateLimited, "rate_limited", "too many calls", "pause")
			pe.RetryAfterSeconds = 2
			return middleware.BuildErrorResult(pe), nil, nil
		})
	caller, cleanup, err := Connect(ctx, server, "test")
	require.NoError(t, err)
	defer cleanup()

	_, err = caller.CallTool(ctx, "refuse", nil)
	require.Error(t, err)
	var refusal *RefusalError
	require.True(t, errors.As(err, &refusal))
	assert.Equal(t, toolratelimit.CodeRateLimited, refusal.Code)
	assert.Equal(t, 2*time.Second, refusal.RetryAfter)
	assert.Contains(t, err.Error(), "too many calls")
	assert.Contains(t, err.Error(), "code: rate_limited")
}

// TestSessionCaller_KeepsAClassifiedEnvelope drives a typed tool that fails
// with a classification in its output, as mcp-trino's trino_query does
// (#2032): the refusal carries retryable and the whole envelope, details
// included, so the engine can record the run as the upstream's and a script
// can be handed the envelope.
func TestSessionCaller_KeepsAClassifiedEnvelope(t *testing.T) {
	ctx := context.Background()
	server := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "v0"}, nil)
	type classified struct {
		Error map[string]any `json:"error"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "trino_query"},
		func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, classified, error) {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "Query failed: EXTERNAL: The connection attempt failed."}}},
				classified{Error: map[string]any{
					"code": "trino_query_failed", "category": "upstream_unavailable", "retryable": true,
					"message": "EXTERNAL: The connection attempt failed.",
					"trino":   map[string]any{"error_type": "EXTERNAL", "error_name": "JDBC_ERROR", "sql_state": "08001"},
				}}, nil
		})
	caller, cleanup, err := Connect(ctx, server, "test")
	require.NoError(t, err)
	defer cleanup()

	_, err = caller.CallTool(ctx, "trino_query", nil)
	var refusal *RefusalError
	require.True(t, errors.As(err, &refusal))
	assert.Equal(t, "trino_query_failed", refusal.Code)
	assert.True(t, refusal.Retryable)
	assert.Equal(t, "Query failed: EXTERNAL: The connection attempt failed.", err.Error(), "the text is the tool's own")
	trino, _ := refusal.Envelope["trino"].(map[string]any)
	assert.Equal(t, "08001", trino["sql_state"])
}

func TestNewRefusal(t *testing.T) {
	r := NewRefusal("text", map[string]any{"code": "rate_limited", "retry_after_seconds": float64(2), "retryable": true})
	assert.Equal(t, "rate_limited", r.Code)
	assert.True(t, r.Retryable)
	assert.Equal(t, 2*time.Second, r.RetryAfter)
	assert.Equal(t, "text", r.Error())
	assert.False(t, NewRefusal("t", map[string]any{"code": "x"}).Retryable, "absent retryable is false")
}
