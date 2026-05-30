package s3

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	s3tools "github.com/txn2/mcp-s3/pkg/tools"

	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// SetMetrics enables per-operation S3 metrics. It rebuilds the underlying
// mcp-s3 toolkit with a metrics middleware installed; this must happen before
// RegisterTools registers the tool handlers, so the platform wires toolkit
// metrics ahead of tool registration (see Platform.WireToolkitMetrics). No-op
// when metrics are disabled, leaving the public toolkit behavior unchanged.
func (t *Toolkit) SetMetrics(m *observability.Metrics) {
	if !m.Enabled() {
		return
	}
	t.metrics = m
	t.s3Toolkit = createToolkit(t.client, t.config, m)
}

// newMetricsMiddleware returns an mcp-s3 ToolMiddleware that records one
// s3_operations observation per tool execution. The operation label is the S3
// tool name; status is StatusOK unless the handler errored or returned an
// error result, in which case it is StatusUpstreamErr.
func newMetricsMiddleware(m *observability.Metrics) s3tools.ToolMiddleware {
	return s3tools.NewMiddlewareFunc(
		"platform_metrics",
		nil, // no Before hook; ToolContext.StartTime gives us the start instant.
		func(ctx context.Context, tc *s3tools.ToolContext, result *mcp.CallToolResult, handlerErr error) (*mcp.CallToolResult, error) {
			status := observability.StatusOK
			if handlerErr != nil || (result != nil && result.IsError) {
				status = observability.StatusUpstreamErr
			}
			m.RecordS3Operation(ctx, string(tc.ToolName), status, time.Since(tc.StartTime))
			return result, handlerErr
		},
	)
}
