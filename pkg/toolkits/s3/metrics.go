package s3

import (
	"context"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/txn2/mcp-data-platform/internal/connstate"
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

// s3Call is one S3 tool call being observed: its span, the operation label,
// the connection it names ("" for the default) and when it started.
type s3Call struct {
	span       trace.Span
	op         string
	connection string
	start      time.Time
}

// begin opens the span for one S3 call before the call is made, so the span
// parents whatever the call does inside it and its start is the call's
// start. The operation label and span name are the tool plus the operation
// it performs (s3_list.buckets, s3_object.put, ...). ChildSpan is a no-op
// outside an active trace, so when tracing is off this costs one check.
func begin(ctx context.Context, op, connection string) (context.Context, *s3Call) {
	ctx, span := observability.ChildSpan(ctx, "s3."+op,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attribute.String("s3.operation", op)))
	return ctx, &s3Call{span: span, op: op, connection: connection, start: time.Now()}
}

// observe ends the span begin opened, records one s3_operations observation
// for the finished call, and records the state the call left its connection
// in (#1898). status is StatusOK unless the handler returned an error result,
// in which case it is StatusUpstreamErr. The metric is nil-safe, so a
// tracing-only deployment (m nil) still produces the span.
func (t *Toolkit) observe(ctx context.Context, c *s3Call, result *mcp.CallToolResult) {
	status := observability.StatusOK
	state := connstate.Healthy
	if result != nil && result.IsError {
		status = observability.StatusUpstreamErr
		state = connstate.FromResultText(resultText(result), s3CredentialCodes...)
	}
	connection := c.connection
	if connection == "" {
		connection = t.name
	}
	connstate.Observe(kindS3, connection, state)
	t.metrics.RecordS3Operation(ctx, c.op, status, time.Since(c.start))
	observability.SetSpanStatus(c.span, status, nil)
	c.span.End()
}

// s3CredentialCodes are the S3 error codes for a credential the store refused:
// an unknown access key, a signature made with the wrong secret, an expired or
// malformed session token.
//
//nolint:gochecknoglobals // a read-only lookup list.
var s3CredentialCodes = []string{"InvalidAccessKeyId", "SignatureDoesNotMatch", "ExpiredToken", "InvalidToken"}

// resultText is the text of a tool result's content.
func resultText(result *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range result.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			_, _ = b.WriteString(tc.Text)
		}
	}
	return b.String()
}
