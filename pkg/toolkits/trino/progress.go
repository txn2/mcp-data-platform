package trino

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	trinotools "github.com/txn2/mcp-trino/pkg/tools"

	"github.com/txn2/mcp-data-platform/pkg/mcpcontext"
)

// mcpProgressNotifier adapts *mcp.ServerSession to trinotools.ProgressNotifier.
// It bridges the platform's MCP session and progress token to the trino toolkit's
// progress notification interface.
type mcpProgressNotifier struct {
	session *mcp.ServerSession
	token   any
}

// Notify sends a progress notification to the MCP client via the server session.
func (n *mcpProgressNotifier) Notify(ctx context.Context, progress, total float64, message string) error {
	//nolint:wrapcheck // MCP SDK session error returned as-is; wrapping would break protocol handling
	return n.session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{
		ProgressToken: n.token,
		Progress:      progress,
		Total:         total,
		Message:       message,
	})
}

// ProgressInjector is a trinotools.ToolMiddleware that creates an
// mcpProgressNotifier per-request from the ServerSession and progress token
// stored in context by MCPToolCallMiddleware.
type ProgressInjector struct{}

// Before reads the server session and progress token from context and, if both
// are present, creates an mcpProgressNotifier and stores it in context via
// trinotools.WithProgressNotifier.
func (*ProgressInjector) Before(ctx context.Context, _ *trinotools.ToolContext) (context.Context, error) {
	session := mcpcontext.GetServerSession(ctx)
	token := mcpcontext.GetProgressToken(ctx)
	if session != nil && token != nil {
		notifier := &mcpProgressNotifier{session: session, token: token}
		ctx = trinotools.WithProgressNotifier(ctx, notifier)
	}
	return ctx, nil
}

// After is a no-op — progress notifications are sent during tool execution, not after.
func (*ProgressInjector) After(
	_ context.Context,
	_ *trinotools.ToolContext,
	result *mcp.CallToolResult,
	handlerErr error,
) (*mcp.CallToolResult, error) {
	return result, handlerErr
}
