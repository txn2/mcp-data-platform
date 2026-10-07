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

// begin opens the span for one S3 call before the call is made, so the span
// parents whatever the call does inside it and its start is the call's
// start. The operation label and span name are the tool plus the operation
// it performs (s3_list.buckets, s3_object.put, ...). ChildSpan is a no-op
// outside an active trace, so when tracing is off this costs one check.
func begin(ctx context.Context, op string) (context.Context, trace.Span, time.Time) {
	ctx, span := observability.ChildSpan(ctx, "s3."+op,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attribute.String("s3.operation", op)))
	return ctx, span, time.Now()
}

// observe ends the span begin opened and records one s3_operations
// observation for the finished call. status is StatusOK unless the handler
// returned an error result, in which case it is StatusUpstreamErr. The metric
// is nil-safe, so a tracing-only deployment (m nil) still produces the span.
func (t *Toolkit) observe(ctx context.Context, span trace.Span, op string, start time.Time, result *mcp.CallToolResult) {
	status := observability.StatusOK
	if result != nil && result.IsError {
		status = observability.StatusUpstreamErr
	}
	t.metrics.RecordS3Operation(ctx, op, status, time.Since(start))
	observability.SetSpanStatus(span, status, nil)
	span.End()
}
