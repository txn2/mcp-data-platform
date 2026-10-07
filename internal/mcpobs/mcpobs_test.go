package mcpobs

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/txn2/mcp-data-platform/pkg/observability"
)

func recorderTracer(t *testing.T) (*observability.Tracer, *tracetest.SpanRecorder) {
	t.Helper()
	sr := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr), sdktrace.WithSampler(sdktrace.AlwaysSample()))
	tr := observability.NewTracerFromProvider(provider, observability.TracingConfig{Enabled: true})
	t.Cleanup(func() { _ = tr.Shutdown(context.Background()) })
	return tr, sr
}

func newMetrics(t *testing.T) *observability.Metrics {
	t.Helper()
	m, err := observability.New(observability.Config{Enabled: true, ListenAddr: ":0"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })
	return m
}

func scrape(t *testing.T, m *observability.Metrics) string {
	t.Helper()
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", http.NoBody))
	return rec.Body.String()
}

func connect(ctx context.Context, t *testing.T, server *mcp.Server) *mcp.ClientSession {
	t.Helper()
	t1, t2 := mcp.NewInMemoryTransports()
	_, err := server.Connect(ctx, t1, nil)
	require.NoError(t, err)
	sess, err := mcp.NewClient(&mcp.Implementation{Name: "tc", Version: "v0"}, nil).Connect(ctx, t2, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

// TestMiddleware_ObservesEveryMethodButToolsCall wires the observer onto a
// real server and drives it through a real client: tools/list and a
// resources/read that fails each yield a span named by the method and a
// count by method and status; the tools/call yields neither here. (The SDK
// answers initialize itself, before any receiving middleware.)
func TestMiddleware_ObservesEveryMethodButToolsCall(t *testing.T) {
	tr, sr := recorderTracer(t)
	m := newMetrics(t)

	server := mcp.NewServer(&mcp.Implementation{Name: "obs", Version: "v0"}, nil)
	server.AddTool(&mcp.Tool{Name: "echo", InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
		})
	server.AddResource(&mcp.Resource{URI: "mem://one", Name: "one"},
		func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: "mem://one", Text: "x"}}}, nil
		})
	server.AddReceivingMiddleware(Middleware(tr, m))

	ctx := context.Background()
	sess := connect(ctx, t, server)
	_, err := sess.ListTools(ctx, nil)
	require.NoError(t, err)
	_, err = sess.ReadResource(ctx, &mcp.ReadResourceParams{URI: "mem://missing"})
	require.Error(t, err)
	_, err = sess.CallTool(ctx, &mcp.CallToolParams{Name: "echo", Arguments: map[string]any{}})
	require.NoError(t, err)

	names := map[string]sdktrace.ReadOnlySpan{}
	for _, s := range sr.Ended() {
		names[s.Name()] = s
	}
	require.Contains(t, names, "tools/list")
	require.Contains(t, names, "resources/read")
	require.NotContains(t, names, MethodToolsCall, "a tools/call has its own observer")
	for _, s := range names {
		require.Equal(t, trace.SpanKindServer, s.SpanKind())
	}

	attrs := func(s sdktrace.ReadOnlySpan) map[string]string {
		out := map[string]string{}
		for _, kv := range s.Attributes() {
			out[string(kv.Key)] = kv.Value.String()
		}
		return out
	}
	list := attrs(names["tools/list"])
	assert.Equal(t, "tools/list", list[AttrMethodName])
	assert.Contains(t, list, AttrSessionID, "carried even when the transport has no session id")
	assert.NotEmpty(t, list[AttrProtocolVersion], "negotiated before tools/list")
	assert.NotContains(t, list, AttrErrorType)
	assert.Equal(t, observability.StatusOK, list["status_category"])

	read := attrs(names["resources/read"])
	assert.Equal(t, observability.StatusClientErr, read[AttrErrorType], "a URI nothing serves is the caller's error")
	assert.Equal(t, observability.StatusClientErr, read["status_category"])

	body := scrape(t, m)
	for _, want := range []string{
		`mcp_requests_total{method="tools/list",status="ok"} 1`,
		`mcp_requests_total{method="resources/read",status="client_err"} 1`,
		`mcp_request_duration_seconds_count{method="tools/list",status="ok"} 1`,
	} {
		require.Contains(t, body, want)
	}
	require.NotContains(t, body, `method="tools/call"`)
}

// TestMiddleware_ContinuesTheCallersTrace: a traceparent in params._meta
// parents the method span, as it does a tool call's (#1893).
func TestMiddleware_ContinuesTheCallersTrace(t *testing.T) {
	prev := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { otel.SetTextMapPropagator(prev) })
	tr, sr := recorderTracer(t)

	next := func(ctx context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		require.True(t, trace.SpanContextFromContext(ctx).IsValid(), "the handler runs inside the span")
		return &mcp.ListToolsResult{}, nil
	}
	h := Middleware(tr, nil)(next)
	req := &mcp.ListToolsRequest{Params: &mcp.ListToolsParams{Meta: mcp.Meta{
		"traceparent": "00-33333333333333333333333333333333-4444444444444444-01",
	}}}
	_, err := h(context.Background(), "tools/list", req)
	require.NoError(t, err)
	spans := sr.Ended()
	require.Len(t, spans, 1)
	assert.Equal(t, "33333333333333333333333333333333", spans[0].SpanContext().TraceID().String())
	assert.Equal(t, "4444444444444444", spans[0].Parent().SpanID().String())
}

func TestMiddleware_PassesThroughWhenNothingIsEnabled(t *testing.T) {
	called := false
	h := Middleware(nil, nil)(func(context.Context, string, mcp.Request) (mcp.Result, error) {
		called = true
		return nil, errors.New("boom")
	})
	_, err := h(context.Background(), "ping", &mcp.ListToolsRequest{})
	require.EqualError(t, err, "boom")
	require.True(t, called)
}

func TestMiddleware_InternalErrorIsCounted(t *testing.T) {
	m := newMetrics(t)
	h := Middleware(nil, m)(func(context.Context, string, mcp.Request) (mcp.Result, error) {
		return nil, errors.New("store down")
	})
	_, err := h(context.Background(), "prompts/list", &mcp.ListPromptsRequest{})
	require.Error(t, err)
	require.Contains(t, scrape(t, m), `mcp_requests_total{method="prompts/list",status="internal_err"} 1`)
}

func TestStatus(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{nil, observability.StatusOK},
		{errors.New("x"), observability.StatusInternalErr},
		{&jsonrpc.Error{Code: jsonrpc.CodeInternalError}, observability.StatusInternalErr},
		{&jsonrpc.Error{Code: jsonrpc.CodeInvalidParams}, observability.StatusClientErr},
		{&jsonrpc.Error{Code: jsonrpc.CodeInvalidRequest}, observability.StatusClientErr},
		{&jsonrpc.Error{Code: jsonrpc.CodeMethodNotFound}, observability.StatusClientErr},
		{mcp.ResourceNotFoundError("mem://x"), observability.StatusClientErr},
		{errors.Join(errors.New("wrapped"), &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams}), observability.StatusClientErr},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, Status(c.err), "%v", c.err)
	}
}

// TestMetaTraceCarrier reads only string traceparent/tracestate entries and
// survives a request without params.
func TestMetaTraceCarrier(t *testing.T) {
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Meta: mcp.Meta{
		"traceparent": "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01",
		"tracestate":  7,
		"other":       "x",
	}}}
	got := metaTraceCarrier(req)
	assert.Equal(t, propagation.MapCarrier{"traceparent": "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"}, got)
	assert.Nil(t, metaTraceCarrier(&mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{}}))
	var nilParams *mcp.CallToolParamsRaw
	assert.Nil(t, metaTraceCarrier(&mcp.CallToolRequest{Params: nilParams}), "a typed-nil params value is no carrier, not a panic")
}

// TestTraceContext_MetaWinsOverHeader: both carriers present, the request's
// own _meta decides the parent.
func TestTraceContext_MetaWinsOverHeader(t *testing.T) {
	prev := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { otel.SetTextMapPropagator(prev) })
	header := http.Header{}
	header.Set("traceparent", "00-11111111111111111111111111111111-2222222222222222-01")
	req := &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Meta: mcp.Meta{"traceparent": "00-33333333333333333333333333333333-4444444444444444-01"}},
		Extra:  &mcp.RequestExtra{Header: header},
	}
	sc := trace.SpanContextFromContext(TraceContext(context.Background(), req))
	assert.Equal(t, "33333333333333333333333333333333", sc.TraceID().String())
	assert.Equal(t, "4444444444444444", sc.SpanID().String())
	assert.True(t, sc.IsRemote())

	req.Params = &mcp.CallToolParamsRaw{}
	sc = trace.SpanContextFromContext(TraceContext(context.Background(), req))
	assert.Equal(t, "11111111111111111111111111111111", sc.TraceID().String(), "the header alone")

	req.Extra = nil
	assert.False(t, trace.SpanContextFromContext(TraceContext(context.Background(), req)).IsValid(), "neither: the span to come is a root")
}

func TestProtocolVersionAndSessionID_Defensive(t *testing.T) {
	assert.Empty(t, ProtocolVersion(nil))
	assert.Empty(t, ProtocolVersion(&mcp.ListToolsRequest{}))
	assert.Empty(t, sessionID(context.Background(), &mcp.ListToolsRequest{}))
	var nilSession *mcp.ServerSession
	assert.Empty(t, ProtocolVersion(&mcp.ListToolsRequest{Session: nilSession}), "a typed-nil session is handled, not dereferenced")
}

func TestObserve_DurationIsRecorded(t *testing.T) {
	m := newMetrics(t)
	h := Middleware(nil, m)(func(context.Context, string, mcp.Request) (mcp.Result, error) {
		time.Sleep(2 * time.Millisecond)
		return &mcp.ListToolsResult{}, nil
	})
	_, err := h(context.Background(), "tools/list", &mcp.ListToolsRequest{})
	require.NoError(t, err)
	body := scrape(t, m)
	i := strings.Index(body, `mcp_request_duration_seconds_sum{method="tools/list",status="ok"} `)
	require.Positive(t, i)
	require.NotContains(t, body[i:i+80], `status="ok"} 0`+"\n", "a measured duration, not zero")
}
