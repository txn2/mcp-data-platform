package trino

import (
	"context"
	"fmt"
	"strings"
	"time"

	trinoclient "github.com/txn2/mcp-trino/pkg/client"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// query_kind labels for Trino metadata operations (issue #461). SQL queries use
// the SQL verb (see queryKind); the catalog/metadata calls the provider makes
// during cross-enrichment use these fixed kinds so trino_queries reflects all
// Trino traffic the query provider generates, not just COUNT estimates.
const (
	kindListCatalogs = "list_catalogs"
	kindListSchemas  = "list_schemas"
	kindListTables   = "list_tables"
	kindDescribe     = "describe_table"
	kindOther        = "other"
)

// SetMetrics wraps the adapter's client in an instrumenting decorator
// that records Trino metrics AND opens a per-call span. The caller
// installs it only when metrics OR tracing is enabled (see
// Platform.observabilityEnabled); the decorator's metric record is
// nil-safe and its span is a no-op outside an active trace, so whichever
// subsystem is off costs effectively nothing.
func (a *Adapter) SetMetrics(m *observability.Metrics) {
	a.client = &instrumentedClient{Client: a.client, metrics: m}
}

// instrumentedClient records observability.RecordTrinoQuery and a child
// span for every upstream Trino call. It embeds Client so Ping/Close
// fall through unchanged.
type instrumentedClient struct {
	Client
	metrics *observability.Metrics
}

// startSpan opens the per-call child span for a Trino operation. Paired
// with finish, which records the metric, sets span status, and wraps the
// error. Returns the (possibly span-carrying) context so the call runs
// under the span.
func (*instrumentedClient) startSpan(ctx context.Context, kind string) (context.Context, trace.Span) {
	return observability.ChildSpan(ctx, "trino."+kind,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attribute.String("trino.query_kind", kind)))
}

// finish records one observation for the given query_kind, ends the span,
// and returns the (wrapped) error so callers can `return value, c.finish(...)`
// in one line. Wrapping matches the codebase's decorator convention and
// preserves the error chain for errors.Is/As.
func (c *instrumentedClient) finish(ctx context.Context, span trace.Span, kind string, start time.Time, err error) error {
	status := observability.UpstreamStatus(err)
	c.metrics.RecordTrinoQuery(ctx, status, kind, time.Since(start))
	observability.SetSpanStatus(span, status, err)
	span.End()
	if err != nil {
		return fmt.Errorf("trino %s: %w", kind, err)
	}
	return nil
}

// Query records a query observation (query_kind from the SQL verb) and delegates.
func (c *instrumentedClient) Query(ctx context.Context, sql string, opts trinoclient.QueryOptions) (*trinoclient.QueryResult, error) {
	kind := queryKind(sql)
	ctx, span := c.startSpan(ctx, kind)
	start := time.Now()
	r, err := c.Client.Query(ctx, sql, opts)
	return r, c.finish(ctx, span, kind, start, err)
}

// ListCatalogs records a list_catalogs observation and delegates.
func (c *instrumentedClient) ListCatalogs(ctx context.Context) ([]string, error) {
	ctx, span := c.startSpan(ctx, kindListCatalogs)
	start := time.Now()
	r, err := c.Client.ListCatalogs(ctx)
	return r, c.finish(ctx, span, kindListCatalogs, start, err)
}

// ListSchemas records a list_schemas observation and delegates.
func (c *instrumentedClient) ListSchemas(ctx context.Context, catalog string) ([]string, error) {
	ctx, span := c.startSpan(ctx, kindListSchemas)
	start := time.Now()
	r, err := c.Client.ListSchemas(ctx, catalog)
	return r, c.finish(ctx, span, kindListSchemas, start, err)
}

// ListTables records a list_tables observation and delegates.
func (c *instrumentedClient) ListTables(ctx context.Context, catalog, schema string) ([]trinoclient.TableInfo, error) {
	ctx, span := c.startSpan(ctx, kindListTables)
	start := time.Now()
	r, err := c.Client.ListTables(ctx, catalog, schema)
	return r, c.finish(ctx, span, kindListTables, start, err)
}

// DescribeTable records a describe_table observation and delegates.
func (c *instrumentedClient) DescribeTable(ctx context.Context, catalog, schema, table string) (*trinoclient.TableInfo, error) {
	ctx, span := c.startSpan(ctx, kindDescribe)
	start := time.Now()
	r, err := c.Client.DescribeTable(ctx, catalog, schema, table)
	return r, c.finish(ctx, span, kindDescribe, start, err)
}

// queryKind extracts a bounded query_kind label from a SQL statement by taking
// its leading keyword. Unknown or empty statements map to "other" so the label
// can never grow unbounded from arbitrary SQL.
func queryKind(sql string) string {
	fields := strings.Fields(strings.TrimSpace(sql))
	if len(fields) == 0 {
		return kindOther
	}
	switch verb := strings.ToLower(fields[0]); verb {
	case "select", "insert", "update", "delete", "merge",
		"show", "describe", "desc", "explain", "create", "drop", "alter", "call", "with":
		return verb
	default:
		return kindOther
	}
}
