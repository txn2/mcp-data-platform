package datahub

import (
	"context"
	"strings"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	dhtools "github.com/txn2/mcp-datahub/pkg/tools"
	"github.com/txn2/mcp-datahub/pkg/types"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// Upstream telemetry (#1896). Every datahub_* tool call records one
// datahub_requests_total observation and opens a datahub.<operation> client
// span under the calling tool_call, the operation being what the tool does
// (get_lineage, browse, create, update, delete). The two reads the platform
// makes through the toolkit's client outside a tool (GetEntity and
// GetGlossaryTerm, for the prompt and resource sources) record the same way.
//
// The middleware is the toolkit's last, so its Before runs only for a call
// every other middleware let through and the span it opens is always ended.

// telemetry holds the recorder the toolkit reports to, read at call time so
// the middleware built into the mcp-datahub toolkit at construction reports
// to the one SetMetrics installs later.
type telemetry struct {
	metrics atomic.Pointer[observability.Metrics]
}

// SetMetrics installs the recorder the toolkit's DataHub calls report to. The
// platform calls it on every toolkit that has one (WireToolkitMetrics) when
// metrics or tracing is on.
func (t *Toolkit) SetMetrics(m *observability.Metrics) {
	t.telemetry.metrics.Store(m)
}

const (
	spanPrefix    = "datahub."
	attrOperation = "datahub.operation"
	toolPrefix    = "datahub_"
)

// observedCall is one DataHub operation being observed.
type observedCall struct {
	span  trace.Span
	op    string
	start time.Time
}

// begin opens the span for one operation.
func (*telemetry) begin(ctx context.Context, op string) (context.Context, *observedCall) {
	ctx, span := observability.ChildSpan(ctx, spanPrefix+op,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attribute.String(attrOperation, op)))
	return ctx, &observedCall{span: span, op: op, start: time.Now()}
}

// finish records the operation and ends its span.
func (tm *telemetry) finish(ctx context.Context, c *observedCall, failed bool, err error) {
	status := observability.StatusOK
	if failed || err != nil {
		status = observability.StatusUpstreamErr
	}
	tm.metrics.Load().RecordDataHubRequest(ctx, c.op, status, time.Since(c.start))
	observability.SetSpanStatus(c.span, status, err)
	c.span.End()
}

// toolObserver is the middleware that measures each tool call.
type toolObserver struct {
	tm *telemetry
}

type observedKey struct{}

// Before opens the call's span.
func (o toolObserver) Before(ctx context.Context, tc *dhtools.ToolContext) (context.Context, error) {
	ctx, c := o.tm.begin(ctx, strings.TrimPrefix(string(tc.ToolName), toolPrefix))
	return context.WithValue(ctx, observedKey{}, c), nil
}

// After records the call Before opened.
func (o toolObserver) After(ctx context.Context, _ *dhtools.ToolContext, result *mcp.CallToolResult, err error) (*mcp.CallToolResult, error) {
	if c, ok := ctx.Value(observedKey{}).(*observedCall); ok {
		o.tm.finish(ctx, c, result != nil && result.IsError, err)
	}
	return result, err
}

// GetEntity reads one entity through the toolkit's client, recorded as
// get_entity.
func (t *Toolkit) GetEntity(ctx context.Context, urn string) (*types.Entity, error) {
	ctx, c := t.telemetry.begin(ctx, opGetEntity)
	e, err := t.client.GetEntity(ctx, urn)
	t.telemetry.finish(ctx, c, false, err)
	return e, err //nolint:wrapcheck // the caller wraps with what the read was for
}

// GetGlossaryTerm reads one glossary term through the toolkit's client,
// recorded as get_glossary_term.
func (t *Toolkit) GetGlossaryTerm(ctx context.Context, urn string) (*types.GlossaryTerm, error) {
	ctx, c := t.telemetry.begin(ctx, opGetGlossaryTerm)
	g, err := t.client.GetGlossaryTerm(ctx, urn)
	t.telemetry.finish(ctx, c, false, err)
	return g, err //nolint:wrapcheck // the caller wraps with what the read was for
}

// The two operations the toolkit reads outside a tool, named as the semantic
// provider names them.
const (
	opGetEntity       = "get_entity"
	opGetGlossaryTerm = "get_glossary_term"
)
