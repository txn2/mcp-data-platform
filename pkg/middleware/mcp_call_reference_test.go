package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// callReferenceHandler returns a handler producing result, and records whether
// it ran.
func callReferenceHandler(result mcp.Result, err error) mcp.MethodHandler {
	return func(context.Context, string, mcp.Request) (mcp.Result, error) {
		return result, err
	}
}

func callReferenceRequest() mcp.Request {
	return &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "trino_query"}}
}

// readCallReference returns the reference block appended to a result, if any.
func readCallReference(t *testing.T, result mcp.Result) (CallReference, bool) {
	t.Helper()
	callResult, ok := result.(*mcp.CallToolResult)
	require.True(t, ok)
	for _, c := range callResult.Content {
		tc, isText := c.(*mcp.TextContent)
		if !isText {
			continue
		}
		var block map[string]CallReference
		if err := json.Unmarshal([]byte(tc.Text), &block); err != nil {
			continue
		}
		if ref, present := block[CallReferenceKey]; present && ref.CallID != "" {
			return ref, true
		}
	}
	return CallReference{}, false
}

// A data call gets its own identifier back, in the content and in the
// structured output, so an agent can cite it when it saves an asset.
func TestCallReferenceMiddlewareStampsDataCalls(t *testing.T) {
	result := &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: `{"rows":[]}`}},
		StructuredContent: map[string]any{"rows": []any{}},
	}
	mw := MCPCallReferenceMiddleware([]string{"trino", "api"})
	handler := mw(callReferenceHandler(result, nil))

	pc := NewPlatformContext("req-1")
	pc.EventID = "evt-123"
	pc.ToolkitKind = "trino"
	ctx := WithPlatformContext(context.Background(), pc)

	got, err := handler(ctx, methodToolsCall, callReferenceRequest())
	require.NoError(t, err)

	ref, ok := readCallReference(t, got)
	require.True(t, ok, "the result must carry the call's own reference")
	assert.Equal(t, "evt-123", ref.CallID)
	assert.Equal(t, "mcp:call:evt-123", ref.Reference)

	structured, ok := result.StructuredContent.(map[string]any)
	require.True(t, ok, "the reference is mirrored into structured output")
	assert.Contains(t, structured, CallReferenceKey)
	assert.Contains(t, structured, "rows", "mirroring must not drop what the tool returned")
}

// Saving an asset, reading memory, and searching are not an asset's sources,
// so their results are not spent on an identifier nothing can cite.
func TestCallReferenceMiddlewareSkipsNonSourceKinds(t *testing.T) {
	result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}
	handler := MCPCallReferenceMiddleware([]string{"trino"})(callReferenceHandler(result, nil))

	pc := NewPlatformContext("req-1")
	pc.EventID = "evt-123"
	pc.ToolkitKind = "portal"

	got, err := handler(WithPlatformContext(context.Background(), pc), methodToolsCall, callReferenceRequest())
	require.NoError(t, err)
	_, ok := readCallReference(t, got)
	assert.False(t, ok)
}

func TestCallReferenceMiddlewareSkipsErrorResults(t *testing.T) {
	result := &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: "SYNTAX_ERROR"}},
	}
	handler := MCPCallReferenceMiddleware([]string{"trino"})(callReferenceHandler(result, nil))

	pc := NewPlatformContext("req-1")
	pc.EventID = "evt-123"
	pc.ToolkitKind = "trino"

	got, err := handler(WithPlatformContext(context.Background(), pc), methodToolsCall, callReferenceRequest())
	require.NoError(t, err)
	_, ok := readCallReference(t, got)
	assert.False(t, ok, "a failed call is recorded, but its result is not a citation token")
}

func TestCallReferenceMiddlewarePassThrough(t *testing.T) {
	sourceKinds := []string{"trino"}
	stamped := func(t *testing.T, ctx context.Context, result mcp.Result, method string) bool {
		t.Helper()
		handler := MCPCallReferenceMiddleware(sourceKinds)(callReferenceHandler(result, nil))
		got, err := handler(ctx, method, callReferenceRequest())
		require.NoError(t, err)
		if got == nil {
			return false
		}
		_, ok := readCallReference(t, got)
		return ok
	}

	newResult := func() mcp.Result {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}
	}
	dataCall := func() context.Context {
		pc := NewPlatformContext("req-1")
		pc.EventID = "evt-1"
		pc.ToolkitKind = "trino"
		return WithPlatformContext(context.Background(), pc)
	}

	t.Run("another method", func(t *testing.T) {
		assert.False(t, stamped(t, dataCall(), newResult(), "tools/list"))
	})
	t.Run("no platform context", func(t *testing.T) {
		assert.False(t, stamped(t, context.Background(), newResult(), methodToolsCall))
	})
	t.Run("no minted event id", func(t *testing.T) {
		pc := NewPlatformContext("req-1")
		pc.ToolkitKind = "trino"
		assert.False(t, stamped(t, WithPlatformContext(context.Background(), pc), newResult(), methodToolsCall))
	})
	t.Run("result is not a tool call result", func(t *testing.T) {
		listResult := &mcp.ListToolsResult{}
		handler := MCPCallReferenceMiddleware(sourceKinds)(callReferenceHandler(listResult, nil))
		got, err := handler(dataCall(), methodToolsCall, callReferenceRequest())
		require.NoError(t, err)
		assert.Same(t, listResult, got, "a result that is not a tool call is returned untouched")
	})
}

// A handler that failed outright is returned untouched.
func TestCallReferenceMiddlewareHandlerError(t *testing.T) {
	wantErr := errors.New("boom")
	handler := MCPCallReferenceMiddleware([]string{"trino"})(callReferenceHandler(nil, wantErr))

	pc := NewPlatformContext("req-1")
	pc.EventID = "evt-1"
	pc.ToolkitKind = "trino"

	_, err := handler(WithPlatformContext(context.Background(), pc), methodToolsCall, callReferenceRequest())
	assert.ErrorIs(t, err, wantErr)
}

// A tool that returned no structured output gets one synthesized from the
// reference alone, which is how the enrichment middleware behaves too.
func TestCallReferenceMiddlewareWithoutStructuredContent(t *testing.T) {
	result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "plain text rows"}}}
	handler := MCPCallReferenceMiddleware([]string{"trino"})(callReferenceHandler(result, nil))

	pc := NewPlatformContext("req-1")
	pc.EventID = "evt-9"
	pc.ToolkitKind = "trino"

	_, err := handler(WithPlatformContext(context.Background(), pc), methodToolsCall, callReferenceRequest())
	require.NoError(t, err)

	structured, ok := result.StructuredContent.(map[string]any)
	require.True(t, ok)
	assert.Contains(t, structured, CallReferenceKey)
}
