package s3

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	s3tools "github.com/txn2/mcp-s3/pkg/tools"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// SetMetrics installs the per-operation S3 observability middleware
// (metrics + spans) by rebuilding the underlying mcp-s3 toolkit; this must
// happen before RegisterTools registers the tool handlers, so the platform
// wires toolkit observability ahead of tool registration (see
// Platform.WireToolkitMetrics). The platform calls this only when metrics
// OR tracing is enabled, so the middleware is installed unconditionally
// here — it records metrics nil-safely (no-op when m is disabled) and
// emits spans only inside an active trace, which is what makes a
// tracing-only deployment (m nil) still produce S3 spans.
func (t *Toolkit) SetMetrics(m *observability.Metrics) {
	t.metrics = m
	t.s3Toolkit = createToolkit(t.client, t.config, m, true)
}

// newMetricsMiddleware returns an mcp-s3 ToolMiddleware that records one
// s3_operations observation AND one span per tool execution. The operation
// label/span name is the S3 tool name; status is StatusOK unless the
// handler errored or returned an error result, in which case it is
// StatusUpstreamErr.
//
// The span is created in the After hook with an explicit start timestamp
// (tc.StartTime) and ended immediately, so it carries the true operation
// duration without needing a Before hook to stash span state across the
// handler. It is a no-op outside an active trace (ChildSpan), so when
// tracing is off only the nil-safe metric record runs.
func newMetricsMiddleware(m *observability.Metrics) s3tools.ToolMiddleware {
	return s3tools.NewMiddlewareFunc(
		"platform_observability",
		nil, // no Before hook; ToolContext.StartTime gives us the start instant.
		func(ctx context.Context, tc *s3tools.ToolContext, result *mcp.CallToolResult, handlerErr error) (*mcp.CallToolResult, error) {
			op := string(tc.ToolName)
			status := observability.StatusOK
			if handlerErr != nil || (result != nil && result.IsError) {
				status = observability.StatusUpstreamErr
			}
			m.RecordS3Operation(ctx, op, status, time.Since(tc.StartTime))

			_, span := observability.ChildSpan(ctx, "s3."+op,
				trace.WithSpanKind(trace.SpanKindClient),
				trace.WithTimestamp(tc.StartTime),
				trace.WithAttributes(attribute.String("s3.operation", op)))
			observability.SetSpanStatus(span, status, handlerErr)
			span.End()
			return result, handlerErr
		},
	)
}
