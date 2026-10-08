package trino

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	trinoclient "github.com/txn2/mcp-trino/pkg/client"
	trinotools "github.com/txn2/mcp-trino/pkg/tools"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/txn2/mcp-data-platform/internal/connstate"
	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// secretLiteral is a value a statement carries that must never reach a label
// or a span.
const secretLiteral = "card-4111-1111"

// telemetryRig is a multi-connection toolkit against a stub coordinator,
// registered on a real MCP server whose receiving middleware opens a root
// "tool_call" span the way the platform's tracing middleware does, with a
// recording tracer and a scrapeable recorder.
type telemetryRig struct {
	tk      *Toolkit
	metrics *observability.Metrics
	spans   *tracetest.SpanRecorder
	session *mcp.ClientSession
}

func newTelemetryRig(t *testing.T) *telemetryRig {
	t.Helper()
	srv := trinoStub(t, http.StatusOK)
	host, port := hostPortOf(t, srv)
	off := false
	tk, err := NewMulti(MultiConfig{
		DefaultConnection: "warehouse",
		Instances: map[string]Config{
			"warehouse": {Host: host, Port: port, User: "u", SSL: &off},
			"readonly":  {Host: host, Port: port, User: "u", SSL: &off, ReadOnly: true},
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tk.Close() })

	m, err := observability.New(observability.Config{Enabled: true})
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })
	tk.SetMetrics(m)

	sr := tracetest.NewSpanRecorder()
	tr := observability.NewTracerFromProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr)), observability.TracingConfig{Enabled: true})

	server := mcp.NewServer(&mcp.Implementation{Name: "trino-test", Version: "v0"}, nil)
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method != "tools/call" {
				return next(ctx, method, req)
			}
			ctx, span := tr.Start(ctx, "tool_call")
			defer span.End()
			return next(ctx, method, req)
		}
	})
	tk.RegisterTools(server)
	st, ct := mcp.NewInMemoryTransports()
	_, err = server.Connect(context.Background(), st, nil)
	require.NoError(t, err)
	sess, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v0"}, nil).Connect(context.Background(), ct, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sess.Close() })
	return &telemetryRig{tk: tk, metrics: m, spans: sr, session: sess}
}

func (r *telemetryRig) call(t *testing.T, tool string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := r.session.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	require.NoError(t, err)
	return res
}

func (r *telemetryRig) scrape(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(r.metrics.Handler())
	defer srv.Close()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, http.NoBody)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck // test cleanup
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(body)
}

// trinoSpans are the ended spans named trino.*.
func (r *telemetryRig) trinoSpans() []sdktrace.ReadOnlySpan {
	var out []sdktrace.ReadOnlySpan
	for _, s := range r.spans.Ended() {
		if strings.HasPrefix(s.Name(), spanPrefix) {
			out = append(out, s)
		}
	}
	return out
}

func spanAttr(s sdktrace.ReadOnlySpan, key string) string {
	for _, kv := range s.Attributes() {
		if string(kv.Key) == key {
			return kv.Value.String()
		}
	}
	return ""
}

func parentName(spans []sdktrace.ReadOnlySpan, child sdktrace.ReadOnlySpan) string {
	for _, s := range spans {
		if s.SpanContext().SpanID() == child.Parent().SpanID() {
			return s.Name()
		}
	}
	return ""
}

// A trino_query call records its statement under the statement's kind, as a
// trino.select child of the tool_call span whose attributes name the
// statement type and the tables it reads, and carry no literal of it.
func TestTelemetry_ToolStatementIsRecordedByKindWithoutItsText(t *testing.T) {
	r := newTelemetryRig(t)
	res := r.call(t, string(trinotools.ToolQuery), map[string]any{
		"sql":        "SELECT * FROM hive.sales.orders WHERE card = '" + secretLiteral + "'",
		"connection": "warehouse",
	})
	require.False(t, res.IsError, "%v", res.Content)

	body := r.scrape(t)
	assert.Contains(t, body, `trino_queries_total{query_kind="select",status="ok"} 1`)
	assert.Contains(t, body, `trino_query_duration_seconds_count{query_kind="select"} 1`)
	assert.NotContains(t, body, secretLiteral)

	spans := r.trinoSpans()
	require.Len(t, spans, 1)
	s := spans[0]
	assert.Equal(t, "trino.select", s.Name())
	assert.Equal(t, "tool_call", parentName(r.spans.Ended(), s))
	assert.Equal(t, "trino", spanAttr(s, attrDBSystem))
	assert.Equal(t, "SELECT", spanAttr(s, attrDBOperation))
	assert.Equal(t, "SELECT hive.sales.orders", spanAttr(s, attrDBSummary))
	assert.Equal(t, "warehouse", spanAttr(s, attrConnection))
	for _, kv := range s.Attributes() {
		assert.NotContains(t, kv.Value.String(), secretLiteral, "attribute %s carries the literal", kv.Key)
	}
}

// A statement the read-only interceptor refuses never reached Trino, so it is
// neither counted nor given a span.
func TestTelemetry_RefusedStatementIsNotRecorded(t *testing.T) {
	r := newTelemetryRig(t)
	res := r.call(t, string(trinotools.ToolExecute), map[string]any{
		"sql":        "DELETE FROM hive.sales.orders",
		"connection": "readonly",
	})
	require.True(t, res.IsError)
	assert.NotContains(t, r.scrape(t), `query_kind="delete"`)
	assert.Empty(t, r.trinoSpans())
}

// trino_browse and trino_describe_table send no statement of the caller's;
// they are recorded as the metadata call they make.
func TestTelemetry_MetadataToolsAreRecordedAsTheirCall(t *testing.T) {
	r := newTelemetryRig(t)
	r.call(t, string(trinotools.ToolBrowse), map[string]any{"connection": "warehouse"})
	r.call(t, string(trinotools.ToolBrowse), map[string]any{"connection": "warehouse", "catalog": "hive"})
	r.call(t, string(trinotools.ToolBrowse), map[string]any{"connection": "warehouse", "catalog": "hive", "schema": "sales"})
	r.call(t, string(trinotools.ToolDescribeTable), map[string]any{
		"connection": "warehouse", "catalog": "hive", "schema": "sales", "table": "orders",
	})

	body := r.scrape(t)
	for _, kind := range []string{kindListCatalogs, kindListSchemas, kindListTables, kindDescribe} {
		assert.Contains(t, body, `query_kind="`+kind+`"`)
	}
	names := map[string]bool{}
	for _, s := range r.trinoSpans() {
		names[s.Name()] = true
		assert.Equal(t, "tool_call", parentName(r.spans.Ended(), s))
	}
	assert.Equal(t, map[string]bool{
		"trino.list_catalogs": true, "trino.list_schemas": true, "trino.list_tables": true, "trino.describe_table": true,
	}, names)
}

// The platform's own statements -- a registration's DDL, an existence lookup,
// a caller's SELECT, a connection probe -- are recorded like the tools'.
func TestTelemetry_PlatformStatementsAreRecorded(t *testing.T) {
	r := newTelemetryRig(t)
	ctx := context.Background()
	require.NoError(t, r.tk.Exec(ctx, "warehouse", "CREATE TABLE hive.scratch.t (a int)"))
	_, err := r.tk.TableExists(ctx, "warehouse", "hive", "scratch", "t")
	require.NoError(t, err)
	_, err = r.tk.Query(ctx, "", "SELECT 1", trinoclient.QueryOptions{Limit: 1})
	require.NoError(t, err)
	_ = r.tk.ProbeConnection(ctx, "readonly")

	body := r.scrape(t)
	assert.Contains(t, body, `trino_queries_total{query_kind="create",status="ok"} 1`)
	assert.Contains(t, body, `trino_queries_total{query_kind="select",status="ok"} 3`)
}

// Query reports a connection it cannot resolve without sending anything.
func TestTelemetry_QueryUnknownConnection(t *testing.T) {
	r := newTelemetryRig(t)
	_, err := r.tk.Query(context.Background(), "ghost", "SELECT 1", trinoclient.QueryOptions{})
	require.Error(t, err)
	assert.NotContains(t, r.scrape(t), "trino_queries_total")
}

func TestBrowseKind(t *testing.T) {
	assert.Equal(t, kindListCatalogs, browseKind(nil))
	assert.Equal(t, kindListCatalogs, browseKind((*trinotools.BrowseInput)(nil)))
	assert.Equal(t, kindListSchemas, browseKind(&trinotools.BrowseInput{Catalog: "hive"}))
	assert.Equal(t, kindListTables, browseKind(trinotools.BrowseInput{Catalog: "hive", Schema: "s"}))
}

// stateToolkit is a toolkit whose "warehouse" connection talks to a
// coordinator answering every request with status.
func stateToolkit(t *testing.T, status int) *Toolkit {
	t.Helper()
	host, port := hostPortOf(t, trinoStub(t, status))
	off := false
	tk, err := NewMulti(MultiConfig{
		DefaultConnection: "warehouse",
		Instances: map[string]Config{
			"warehouse": {Host: host, Port: port, User: "u", SSL: &off},
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tk.Close() })
	return tk
}

// A statement the platform sends leaves its connection in the state Trino's
// answer reads as (#1898): a 401 is a refused credential and a coordinator
// that answers is healthy. (A refused dial is retried by the client for two
// minutes, so it is held by TestErrorState instead.)
func TestTelemetry_PlatformStatementsRecordTheConnectionsState(t *testing.T) {
	for _, tc := range []struct {
		status int
		conn   string
		want   string
	}{
		{http.StatusOK, "warehouse", connstate.Healthy},
		{http.StatusUnauthorized, "warehouse", connstate.AuthFailed},
	} {
		tk := stateToolkit(t, tc.status)
		_, _ = tk.Query(context.Background(), tc.conn, "SELECT 1", trinoclient.QueryOptions{})
		got, _ := connstate.State(kindTrino, tc.conn)
		assert.Equal(t, tc.want, got, "HTTP %d on %s", tc.status, tc.conn)
	}
}

// A tool call that names no connection records the default connection's
// state, under the name list_connections reports it by.
func TestTelemetry_ToolCallRecordsTheDefaultConnectionsState(t *testing.T) {
	r := newTelemetryRig(t)
	res := r.call(t, string(trinotools.ToolQuery), map[string]any{"sql": "SELECT 1"})
	require.False(t, res.IsError, "%v", res.Content)
	got, ok := connstate.State(kindTrino, "warehouse")
	assert.True(t, ok)
	assert.Equal(t, connstate.Healthy, got)
}

func TestResultState(t *testing.T) {
	unavailable := &trinotools.QueryOutput{Error: &trinotools.QueryError{
		Category:  trinoclient.CategoryUpstreamUnavailable,
		Transport: &trinoclient.TransportErrorDetail{Kind: trinoclient.TransportConnectionRefused},
	}}
	refused := &trinotools.QueryOutput{Error: &trinotools.QueryError{
		Category:  trinoclient.CategoryClientInput,
		Transport: &trinoclient.TransportErrorDetail{HTTPStatus: http.StatusUnauthorized},
	}}
	answered := trinotools.QueryOutput{Error: &trinotools.QueryError{Category: trinoclient.CategoryClientInput}}
	asJSON := map[string]any{"error": map[string]any{
		"category": "upstream_unavailable", "transport": map[string]any{"kind": "connection_refused"},
	}}
	for _, tc := range []struct {
		name   string
		result *mcp.CallToolResult
		err    error
		want   string
	}{
		{"success", &mcp.CallToolResult{}, nil, connstate.Healthy},
		{"nil result", nil, nil, connstate.Healthy},
		{"unreachable", &mcp.CallToolResult{IsError: true, StructuredContent: unavailable}, nil, connstate.Unreachable},
		{"credential", &mcp.CallToolResult{IsError: true, StructuredContent: refused}, nil, connstate.AuthFailed},
		{"answered refusal", &mcp.CallToolResult{IsError: true, StructuredContent: answered}, nil, connstate.Healthy},
		{"json form", &mcp.CallToolResult{IsError: true, StructuredContent: asJSON}, nil, connstate.Unreachable},
		{"unclassified", &mcp.CallToolResult{IsError: true}, nil, ""},
		{"unmarshalable", &mcp.CallToolResult{IsError: true, StructuredContent: func() {}}, nil, ""},
		{"handler error", nil, context.Canceled, ""},
	} {
		assert.Equal(t, tc.want, resultState(tc.result, tc.err), tc.name)
	}
}

func TestErrorState(t *testing.T) {
	refused := fmt.Errorf("query: %w", &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED})
	assert.Equal(t, connstate.Healthy, errorState(nil))
	assert.Equal(t, connstate.Unreachable, errorState(refused))
	assert.Equal(t, connstate.Healthy, errorState(errors.New("line 1:1: mismatched input")))
	assert.Empty(t, errorState(context.Canceled), "a canceled call says nothing about the upstream")
}
