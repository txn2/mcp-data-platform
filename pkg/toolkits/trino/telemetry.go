package trino

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	trinoclient "github.com/txn2/mcp-trino/pkg/client"
	trinotools "github.com/txn2/mcp-trino/pkg/tools"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/txn2/mcp-data-platform/internal/connstate"
	"github.com/txn2/mcp-data-platform/internal/sqltables"
	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// Upstream telemetry (#1896). Every statement and metadata call the toolkit
// sends to Trino records trino_queries_total and trino_query_duration_seconds
// and opens a trino.<kind> client span under the calling tool_call: the
// mcp-trino tools through a middleware and query interceptor the toolkit
// installs last (queryObserver), and the platform's own statements (Exec,
// TableExists, the export query, the connection probes)
// through queryTelemetry.observe. The label and the span carry the
// statement's kind and a summary of the tables it reads, never its text.
//
// mcp-trino's client reports a query's duration and id but not its queued and
// running time, so neither is on the span.

// Metadata-call kinds, the same values the query provider reports.
const (
	kindListCatalogs = "list_catalogs"
	kindListSchemas  = "list_schemas"
	kindListTables   = "list_tables"
	kindDescribe     = "describe_table"
	kindExplain      = "explain"
)

// Span attribute keys from the OpenTelemetry database conventions.
const (
	attrDBSystem    = "db.system.name"
	attrDBOperation = "db.operation.name"
	attrDBSummary   = "db.query.summary"
	attrQueryKind   = "trino.query_kind"
	attrQueryID     = "trino.query_id"
	attrConnection  = "trino.connection"
	dbSystemTrino   = "trino"
	spanPrefix      = "trino."
)

// queryTelemetry holds the recorder the toolkit reports to. The recorder is
// read at call time, so the middleware built into the mcp-trino toolkit at
// construction reports to the one SetMetrics installs later. A nil recorder
// records nothing; the span is a no-op outside an active trace.
type queryTelemetry struct {
	metrics atomic.Pointer[observability.Metrics]
	// connection resolves the connection a call names, "" for the default, to
	// the name it is listed under, for the connection state it leaves
	// (#1898). Nil records no state.
	connection func(string) string
}

// SetMetrics installs the recorder the toolkit's Trino calls report to. The
// platform calls it on every toolkit that has one (WireToolkitMetrics) when
// metrics or tracing is on.
func (t *Toolkit) SetMetrics(m *observability.Metrics) {
	t.telemetry.metrics.Store(m)
}

// call is one Trino call being observed.
type call struct {
	span  trace.Span
	kind  string
	conn  string
	start time.Time
}

// begin opens the span for one call of kind. summary is the statement's
// summary, empty for a metadata call.
func (*queryTelemetry) begin(ctx context.Context, kind, summary, connection string) (context.Context, *call) {
	attrs := []attribute.KeyValue{
		attribute.String(attrDBSystem, dbSystemTrino),
		attribute.String(attrDBOperation, strings.ToUpper(kind)),
		attribute.String(attrQueryKind, kind),
	}
	if summary != "" {
		attrs = append(attrs, attribute.String(attrDBSummary, summary))
	}
	if connection != "" {
		attrs = append(attrs, attribute.String(attrConnection, connection))
	}
	ctx, span := observability.ChildSpan(ctx, spanPrefix+kind,
		trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(attrs...))
	return ctx, &call{span: span, kind: kind, conn: connection, start: time.Now()}
}

// finish records the call, the state it left its connection in, and ends its
// span. state is the connection state the call's outcome reads as, "" for
// none.
func (q *queryTelemetry) finish(ctx context.Context, c *call, failed bool, err error, state string) {
	status := observability.StatusOK
	if failed || err != nil {
		status = observability.StatusUpstreamErr
	}
	q.metrics.Load().RecordTrinoQuery(ctx, status, c.kind, time.Since(c.start))
	if q.connection != nil {
		connstate.Observe(kindTrino, q.connection(c.conn), state)
	}
	observability.SetSpanStatus(c.span, status, err)
	c.span.End()
}

// query runs one platform-issued statement on client and records it.
func (q *queryTelemetry) query(
	ctx context.Context, client *trinoclient.Client, connection, sql string, opts trinoclient.QueryOptions,
) (*trinoclient.QueryResult, error) {
	ctx, c := q.begin(ctx, sqltables.StatementKind(sql), sqltables.Summary(sql), connection)
	res, err := client.Query(ctx, sql, opts)
	if res != nil && res.Stats.QueryID != "" {
		c.span.SetAttributes(attribute.String(attrQueryID, res.Stats.QueryID))
	}
	q.finish(ctx, c, false, err, errorState(err))
	return res, err //nolint:wrapcheck // the caller wraps with what the statement was for
}

// queryObserver measures the mcp-trino tools' upstream calls. Before opens
// the span of a metadata tool and marks a statement tool's call; the query
// interceptor, which runs after every other interceptor and so only for a
// statement that is going to Trino, opens the statement's span with its kind;
// After records whichever was opened. A statement refused before it was sent
// (read-only, a missing connection) records nothing, since nothing reached
// Trino.
type queryObserver struct {
	q *queryTelemetry
}

// pendingCall is one tool call's observation, carried from Before to After.
type pendingCall struct {
	conn string
	c    *call
}

type pendingKey struct{}

// Before opens a metadata tool's span, or marks a statement tool's call so the
// interceptor can open its span.
func (o queryObserver) Before(ctx context.Context, tc *trinotools.ToolContext) (context.Context, error) {
	p := &pendingCall{conn: extractConnectionFromInput(tc.Input)}
	if kind, ok := metadataKind(tc); ok {
		ctx, p.c = o.q.begin(ctx, kind, "", p.conn)
	}
	return context.WithValue(ctx, pendingKey{}, p), nil
}

// Intercept opens the span of the statement a statement tool is about to send.
// It changes nothing about the statement.
func (o queryObserver) Intercept(ctx context.Context, sql string, tool trinotools.ToolName) (string, error) {
	p, ok := ctx.Value(pendingKey{}).(*pendingCall)
	if !ok || p.c != nil {
		return sql, nil
	}
	kind := sqltables.StatementKind(sql)
	if tool == trinotools.ToolExplain {
		kind = kindExplain
	}
	_, p.c = o.q.begin(ctx, kind, sqltables.Summary(sql), p.conn)
	return sql, nil
}

// After records the call Before or Intercept opened.
func (o queryObserver) After(
	ctx context.Context, _ *trinotools.ToolContext, result *mcp.CallToolResult, handlerErr error,
) (*mcp.CallToolResult, error) {
	if p, ok := ctx.Value(pendingKey{}).(*pendingCall); ok && p.c != nil {
		o.q.finish(ctx, p.c, result != nil && result.IsError, handlerErr, resultState(result, handlerErr))
	}
	return result, handlerErr
}

// metadataKind is the metadata call a tool makes, for the tools that send no
// statement of the caller's.
func metadataKind(tc *trinotools.ToolContext) (string, bool) {
	switch tc.Name {
	case trinotools.ToolDescribeTable:
		return kindDescribe, true
	case trinotools.ToolBrowse:
		return browseKind(tc.Input), true
	default:
		return "", false
	}
}

// browseKind is what a trino_browse call lists: catalogs, a catalog's schemas
// or a schema's tables.
func browseKind(input any) string {
	var in trinotools.BrowseInput
	switch v := input.(type) {
	case trinotools.BrowseInput:
		in = v
	case *trinotools.BrowseInput:
		if v != nil {
			in = *v
		}
	}
	switch {
	case in.Catalog == "":
		return kindListCatalogs
	case in.Schema == "":
		return kindListSchemas
	default:
		return kindListTables
	}
}

// observerOptions are the toolkit options that install the observer. They go
// after every other option so the interceptor runs last and the middleware's
// Before last: a call another middleware refuses opens no span.
func (t *Toolkit) observerOptions() []trinotools.ToolkitOption {
	t.telemetry.connection = t.listedConnection
	o := queryObserver{q: &t.telemetry}
	return []trinotools.ToolkitOption{
		trinotools.WithQueryInterceptor(o),
		trinotools.WithMiddleware(o),
	}
}

// listedConnection is the name a call's connection is listed under: the one it
// names, or the default connection's for a call that names none.
func (t *Toolkit) listedConnection(name string) string {
	if name != "" {
		return name
	}
	for _, c := range t.ListConnections() {
		if c.IsDefault {
			return c.Name
		}
	}
	return t.name
}

// errorState is the connection state a Trino client error leaves: a refused
// credential (HTTP 401 or 407) is auth_failed, a transport failure mcp-trino
// classifies as the upstream being unavailable is unreachable, and an error
// Trino itself answered with -- a syntax error, a missing table, access
// control, a connector whose source is down -- is healthy, since the
// connection reached Trino and was answered.
func errorState(err error) string {
	if err == nil {
		return connstate.Healthy
	}
	class, ok := trinoclient.Classify(err)
	if !ok {
		return ""
	}
	return classState(class.Category, class.Transport)
}

// classState is the connection state of a classified failure.
func classState(category trinoclient.ErrorCategory, transport *trinoclient.TransportErrorDetail) string {
	switch {
	case transport == nil:
		return connstate.Healthy
	case transport.HTTPStatus == http.StatusUnauthorized || transport.HTTPStatus == http.StatusProxyAuthRequired:
		return connstate.AuthFailed
	case category == trinoclient.CategoryUpstreamUnavailable:
		return connstate.Unreachable
	default:
		return connstate.Healthy
	}
}

// resultState is the connection state a tool call's result leaves: a success
// is healthy, and a failure is read from the classification mcp-trino puts in
// the result's structured error. A failure with no classification (the call
// was canceled, or refused before it reached Trino) records nothing.
func resultState(result *mcp.CallToolResult, handlerErr error) string {
	if handlerErr != nil {
		return errorState(handlerErr)
	}
	if result == nil || !result.IsError {
		return connstate.Healthy
	}
	out := queryOutput(result.StructuredContent)
	if out == nil || out.Error == nil {
		return ""
	}
	return classState(out.Error.Category, out.Error.Transport)
}

// queryOutput reads a result's structured content as mcp-trino's query output,
// in whichever form it arrives: the value the tool returned, or its JSON.
func queryOutput(content any) *trinotools.QueryOutput {
	switch v := content.(type) {
	case nil:
		return nil
	case *trinotools.QueryOutput:
		return v
	case trinotools.QueryOutput:
		return &v
	}
	raw, err := json.Marshal(content)
	if err != nil {
		return nil
	}
	var out trinotools.QueryOutput
	if json.Unmarshal(raw, &out) != nil {
		return nil
	}
	return &out
}
