package middleware

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestServerRequest creates a test server request for tools/call.
func newTestServerRequest(params *mcp.CallToolParamsRaw) *mcp.ServerRequest[*mcp.CallToolParamsRaw] {
	return &mcp.ServerRequest[*mcp.CallToolParamsRaw]{
		Params: params,
	}
}

func TestProvenanceTrackerRecordAndHarvest(t *testing.T) {
	tracker := NewProvenanceTracker()

	tracker.Record("sess1", "trino_query", map[string]any{"sql": "SELECT 1"})
	tracker.Record("sess1", "datahub_search", map[string]any{"query": "sales"})
	tracker.Record("sess2", "trino_query", nil)

	calls := tracker.Harvest("sess1")
	assert.Len(t, calls, 2)
	assert.Equal(t, "trino_query", calls[0].ToolName)
	assert.Equal(t, "datahub_search", calls[1].ToolName)
	assert.NotEmpty(t, calls[0].Timestamp)
	assert.Equal(t, "SELECT 1", calls[0].Parameters["sql"])

	// Harvest clears the session
	calls = tracker.Harvest("sess1")
	assert.Nil(t, calls)

	// Other sessions are untouched
	calls = tracker.Harvest("sess2")
	assert.Len(t, calls, 1)
}

func TestProvenanceContextRoundtrip(t *testing.T) {
	calls := []ProvenanceToolCall{
		{ToolName: "tool1", Timestamp: "2024-01-01T00:00:00Z"},
	}
	ctx := WithProvenanceToolCalls(context.Background(), calls)
	got := GetProvenanceToolCalls(ctx)
	require.Len(t, got, 1)
	assert.Equal(t, "tool1", got[0].ToolName)

	// Empty context
	got = GetProvenanceToolCalls(context.Background())
	assert.Nil(t, got)
}

func TestMCPProvenanceMiddleware_Records(t *testing.T) {
	tracker := NewProvenanceTracker()

	base := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return &mcp.CallToolResult{}, nil
	}

	handler := MCPProvenanceMiddleware(tracker, "save_artifact")(base)

	args, _ := json.Marshal(map[string]any{"sql": "SELECT 1"})
	req := newTestServerRequest(&mcp.CallToolParamsRaw{
		Name:      "trino_query",
		Arguments: args,
	})

	ctx := WithPlatformContext(context.Background(), &PlatformContext{SessionID: "sess1"})

	_, err := handler(ctx, methodToolsCall, req)
	require.NoError(t, err)

	// Verify the call was recorded
	calls := tracker.Harvest("sess1")
	assert.Len(t, calls, 1)
	assert.Equal(t, "trino_query", calls[0].ToolName)
}

func TestMCPProvenanceMiddleware_HarvestsOnSave(t *testing.T) {
	tracker := NewProvenanceTracker()

	// Pre-load some calls
	tracker.Record("sess1", "trino_query", nil)
	tracker.Record("sess1", "datahub_search", nil)

	var capturedCalls []ProvenanceToolCall
	base := func(ctx context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		capturedCalls = GetProvenanceToolCalls(ctx)
		return &mcp.CallToolResult{}, nil
	}

	handler := MCPProvenanceMiddleware(tracker, "save_artifact")(base)

	req := newTestServerRequest(&mcp.CallToolParamsRaw{
		Name: "save_artifact",
	})

	ctx := WithPlatformContext(context.Background(), &PlatformContext{SessionID: "sess1"})

	_, err := handler(ctx, methodToolsCall, req)
	require.NoError(t, err)

	// Verify provenance was injected into context
	assert.Len(t, capturedCalls, 2)
	assert.Equal(t, "trino_query", capturedCalls[0].ToolName)

	// Verify session was cleared
	assert.Nil(t, tracker.Harvest("sess1"))
}

func TestMCPProvenanceMiddleware_NonToolsCall(t *testing.T) {
	tracker := NewProvenanceTracker()

	base := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return &mcp.CallToolResult{}, nil
	}

	handler := MCPProvenanceMiddleware(tracker, "save_artifact")(base)

	_, err := handler(context.Background(), "tools/list", nil)
	require.NoError(t, err)

	// Nothing should be recorded
	assert.Nil(t, tracker.Harvest(""))
}

func TestMCPProvenanceMiddleware_SessionIsolation(t *testing.T) {
	tracker := NewProvenanceTracker()
	tracker.Record("sess1", "tool_a", nil)
	tracker.Record("sess2", "tool_b", nil)

	// Harvest sess1 only
	calls := tracker.Harvest("sess1")
	assert.Len(t, calls, 1)
	assert.Equal(t, "tool_a", calls[0].ToolName)

	// sess2 still has its call
	calls = tracker.Harvest("sess2")
	assert.Len(t, calls, 1)
	assert.Equal(t, "tool_b", calls[0].ToolName)
}

func TestMCPProvenanceMiddleware_NoSessionContext(t *testing.T) {
	tracker := NewProvenanceTracker()

	base := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return &mcp.CallToolResult{}, nil
	}

	handler := MCPProvenanceMiddleware(tracker, "save_artifact")(base)

	req := newTestServerRequest(&mcp.CallToolParamsRaw{
		Name: "trino_query",
	})

	// Call without PlatformContext — empty session ID should skip recording.
	_, err := handler(context.Background(), methodToolsCall, req)
	require.NoError(t, err)

	calls := tracker.Harvest("")
	assert.Nil(t, calls) // empty session ID is not recorded
}

func TestMCPProvenanceMiddleware_EmptyToolName(t *testing.T) {
	tracker := NewProvenanceTracker()

	base := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return &mcp.CallToolResult{}, nil
	}

	handler := MCPProvenanceMiddleware(tracker, "save_artifact")(base)

	// Request with empty tool name — extractToolName succeeds but records it.
	req := newTestServerRequest(&mcp.CallToolParamsRaw{Name: ""})

	_, err := handler(context.Background(), methodToolsCall, req)
	require.NoError(t, err)
}

func TestProvenanceTracker_MaxCallsPerSession(t *testing.T) {
	tracker := NewProvenanceTracker()

	// Record more than the max
	for range maxCallsPerSession + 20 {
		tracker.Record("sess1", "tool_x", nil)
	}

	calls := tracker.Harvest("sess1")
	assert.Len(t, calls, maxCallsPerSession)
}

func TestProvenanceTracker_EmptySessionSkipsRecording(t *testing.T) {
	tracker := NewProvenanceTracker()

	tracker.Record("", "tool_x", nil)

	calls := tracker.Harvest("")
	assert.Nil(t, calls)
}

func TestProvenanceTracker_CleanupBefore(t *testing.T) {
	tracker := NewProvenanceTracker()

	tracker.Record("old_sess", "tool_a", nil)
	// Manually set an old timestamp
	tracker.mu.Lock()
	tracker.sessions["old_sess"][0].Timestamp = "2020-01-01T00:00:00Z"
	tracker.mu.Unlock()

	tracker.Record("new_sess", "tool_b", nil)

	removed := tracker.CleanupBefore(time.Now().Add(-1 * time.Hour))
	assert.Equal(t, 1, removed) // old_sess removed

	assert.Nil(t, tracker.Harvest("old_sess"))
	calls := tracker.Harvest("new_sess")
	assert.Len(t, calls, 1)
}

func TestExtractToolParams_NilCases(t *testing.T) {
	// Request with nil arguments.
	req := newTestServerRequest(&mcp.CallToolParamsRaw{Name: "test"})
	assert.Nil(t, extractToolParams(req))

	// Request with invalid JSON arguments.
	req = newTestServerRequest(&mcp.CallToolParamsRaw{
		Name:      "test",
		Arguments: json.RawMessage(`{invalid`),
	})
	assert.Nil(t, extractToolParams(req))

	// Request with valid arguments.
	args, _ := json.Marshal(map[string]any{"key": "val"})
	req = newTestServerRequest(&mcp.CallToolParamsRaw{
		Name:      "test",
		Arguments: args,
	})
	result := extractToolParams(req)
	assert.Equal(t, "val", result["key"])
}
