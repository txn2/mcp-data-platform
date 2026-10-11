package middleware

import (
	"context"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/mcptask"
)

// A call answered with a task (#2009) is not recorded when the handle goes
// back: its row is written when the work ends, with what the work produced
// and the PlatformContext it ran under.
func TestMCPAuditMiddleware_RecordsATaskWhenItsWorkEnds(t *testing.T) {
	logger := newCapturingAuditLogger()
	finish := make(chan func(), 1)
	mw := MCPAuditMiddleware(logger)(func(ctx context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		finish <- func() {
			own := *GetPlatformContext(ctx)
			own.EnrichmentApplied = true
			done := WithPlatformContext(context.WithoutCancel(ctx), &own)
			mcptask.Complete(done, &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "exported 10 rows"}}}, nil)
		}
		return mcptask.NewCreateResult(&mcptask.Snapshot{ID: "dpt_1", Status: mcptask.StatusWorking}), nil
	})

	pc := NewPlatformContext("req-1")
	pc.UserID, pc.ToolName = testAuditEmail, "trino_export"
	res, err := mw(WithPlatformContext(context.Background(), pc), testAuditMethodCall,
		createAuditTestRequest(t, "trino_export", map[string]any{"sql": "SELECT 1"}))
	require.NoError(t, err)
	assert.True(t, mcptask.IsTask(res))
	assert.Empty(t, logger.Events(), "the handle says nothing about what the call did")

	(<-finish)()
	events := logger.Events()
	require.Len(t, events, 1)
	assert.True(t, events[0].Success)
	assert.Equal(t, "trino_export", events[0].ToolName)
	assert.True(t, events[0].EnrichmentApplied, "read from the work's own PlatformContext")
	assert.Equal(t, len("exported 10 rows"), events[0].ResponseChars)
}

// A failed task is recorded as a failure.
func TestMCPAuditMiddleware_RecordsAFailedTask(t *testing.T) {
	logger := newCapturingAuditLogger()
	mw := MCPAuditMiddleware(logger)(func(ctx context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		mcptask.Complete(ctx, nil, errors.New("upstream gone"))
		return mcptask.NewCreateResult(&mcptask.Snapshot{ID: "dpt_1", Status: mcptask.StatusWorking}), nil
	})
	pc := NewPlatformContext("req-2")
	_, err := mw(WithPlatformContext(context.Background(), pc), testAuditMethodCall,
		createAuditTestRequest(t, "api_export", nil))
	require.NoError(t, err)
	events := logger.Events()
	require.Len(t, events, 1)
	assert.False(t, events[0].Success)
	assert.Contains(t, events[0].ErrorMessage, "upstream gone")
}
