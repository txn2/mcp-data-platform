package datahub

import (
	"context"
	"fmt"
	"time"

	dhclient "github.com/txn2/mcp-datahub/pkg/client"
	"github.com/txn2/mcp-datahub/pkg/types"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// DataHub operation labels (issue #461). Bounded set, one per instrumented
// upstream client call.
const (
	opGetEntity        = "get_entity"
	opGetSchema        = "get_schema"
	opGetSchemas       = "get_schemas"
	opGetLineage       = "get_lineage"
	opGetColumnLineage = "get_column_lineage"
	opGetGlossaryTerm  = "get_glossary_term"
	opGetQueries       = "get_queries"
)

// SetMetrics wraps the adapter's client in an instrumenting decorator
// that records DataHub metrics AND opens a per-operation span. The caller
// installs it only when metrics OR tracing is enabled; the metric record
// is nil-safe and the span is a no-op outside an active trace, so the
// public Provider interface is unchanged and the off subsystem costs
// effectively nothing.
func (a *Adapter) SetMetrics(m *observability.Metrics) {
	a.client = &instrumentedClient{Client: a.client, metrics: m}
}

// instrumentedClient records observability.RecordDataHubRequest and a
// child span for every upstream call. It embeds Client so non-instrumented
// methods (Ping, Close) fall through unchanged.
type instrumentedClient struct {
	Client
	metrics *observability.Metrics
}

// startSpan opens the per-operation child span for a DataHub call. Paired
// with finish, which records the metric, sets span status, and wraps the
// error.
func (*instrumentedClient) startSpan(ctx context.Context, op string) (context.Context, trace.Span) {
	return observability.ChildSpan(ctx, "datahub."+op,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attribute.String("datahub.operation", op)))
}

// finish records one observation for op, ends the span, and returns the
// (wrapped) error so the caller can `return value, c.finish(...)` in a
// single line. Wrapping matches the codebase's decorator convention (see
// semantic.CachedProvider) and keeps the error chain intact for errors.Is/As.
func (c *instrumentedClient) finish(ctx context.Context, span trace.Span, op string, start time.Time, err error) error {
	status := observability.UpstreamStatus(err)
	c.metrics.RecordDataHubRequest(ctx, op, status, time.Since(start))
	observability.SetSpanStatus(span, status, err)
	span.End()
	if err != nil {
		return fmt.Errorf("datahub %s: %w", op, err)
	}
	return nil
}

// GetEntity records a get_entity observation and delegates to the wrapped client.
func (c *instrumentedClient) GetEntity(ctx context.Context, urn string) (*types.Entity, error) {
	ctx, span := c.startSpan(ctx, opGetEntity)
	start := time.Now()
	e, err := c.Client.GetEntity(ctx, urn)
	return e, c.finish(ctx, span, opGetEntity, start, err)
}

// GetSchema records a get_schema observation and delegates to the wrapped client.
func (c *instrumentedClient) GetSchema(ctx context.Context, urn string) (*types.SchemaMetadata, error) {
	ctx, span := c.startSpan(ctx, opGetSchema)
	start := time.Now()
	s, err := c.Client.GetSchema(ctx, urn)
	return s, c.finish(ctx, span, opGetSchema, start, err)
}

// GetSchemas records a get_schemas observation and delegates to the wrapped client.
func (c *instrumentedClient) GetSchemas(ctx context.Context, urns []string) (map[string]*types.SchemaMetadata, error) {
	ctx, span := c.startSpan(ctx, opGetSchemas)
	start := time.Now()
	s, err := c.Client.GetSchemas(ctx, urns)
	return s, c.finish(ctx, span, opGetSchemas, start, err)
}

// GetLineage records a get_lineage observation and delegates to the wrapped client.
func (c *instrumentedClient) GetLineage(ctx context.Context, urn string, opts ...dhclient.LineageOption) (*types.LineageResult, error) {
	ctx, span := c.startSpan(ctx, opGetLineage)
	start := time.Now()
	l, err := c.Client.GetLineage(ctx, urn, opts...)
	return l, c.finish(ctx, span, opGetLineage, start, err)
}

// GetColumnLineage records a get_column_lineage observation and delegates to the wrapped client.
func (c *instrumentedClient) GetColumnLineage(ctx context.Context, urn string) (*types.ColumnLineage, error) {
	ctx, span := c.startSpan(ctx, opGetColumnLineage)
	start := time.Now()
	l, err := c.Client.GetColumnLineage(ctx, urn)
	return l, c.finish(ctx, span, opGetColumnLineage, start, err)
}

// GetGlossaryTerm records a get_glossary_term observation and delegates to the wrapped client.
func (c *instrumentedClient) GetGlossaryTerm(ctx context.Context, urn string) (*types.GlossaryTerm, error) {
	ctx, span := c.startSpan(ctx, opGetGlossaryTerm)
	start := time.Now()
	g, err := c.Client.GetGlossaryTerm(ctx, urn)
	return g, c.finish(ctx, span, opGetGlossaryTerm, start, err)
}

// GetQueries records a get_queries observation and delegates to the wrapped client.
func (c *instrumentedClient) GetQueries(ctx context.Context, urn string) (*types.QueryList, error) {
	ctx, span := c.startSpan(ctx, opGetQueries)
	start := time.Now()
	q, err := c.Client.GetQueries(ctx, urn)
	return q, c.finish(ctx, span, opGetQueries, start, err)
}
