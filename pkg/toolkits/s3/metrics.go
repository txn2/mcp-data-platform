package s3

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// SetMetrics installs the recorder the S3 handlers report to. The platform
// calls this before RegisterTools, when metrics OR tracing is enabled; the
// handlers record nil-safely (no-op when m is disabled) and emit spans only
// inside an active trace, which is what makes a tracing-only deployment (m
// nil) still produce S3 spans.
func (t *Toolkit) SetMetrics(m *observability.Metrics) {
	t.metrics = m
}

// observe records one s3_operations observation AND one span for a finished
// call. The operation label and span name are the tool plus the operation it
// performed (s3_list.buckets, s3_object.put, ...); status is StatusOK unless
// the handler returned an error result, in which case it is StatusUpstreamErr.
//
// The span is created with an explicit start timestamp and ended immediately,
// so it carries the true operation duration. It is a no-op outside an active
// trace (ChildSpan), so when tracing is off only the nil-safe metric runs.
func (t *Toolkit) observe(ctx context.Context, op string, start time.Time, result *mcp.CallToolResult) {
	status := observability.StatusOK
	if result != nil && result.IsError {
		status = observability.StatusUpstreamErr
	}
	t.metrics.RecordS3Operation(ctx, op, status, time.Since(start))

	_, span := observability.ChildSpan(ctx, "s3."+op,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithTimestamp(start),
		trace.WithAttributes(attribute.String("s3.operation", op)))
	observability.SetSpanStatus(span, status, nil)
	span.End()
}
