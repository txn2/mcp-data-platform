package middleware_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// recorderTracer builds an always-sampling *observability.Tracer backed by
// an in-memory span recorder for assertions.
func recorderTracer(t *testing.T) (*observability.Tracer, *tracetest.SpanRecorder) {
	t.Helper()
	sr := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(sr),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	tr := observability.NewTracerFromProvider(provider, observability.TracingConfig{Enabled: true})
	t.Cleanup(func() { _ = tr.Shutdown(context.Background()) })
	return tr, sr
}

// TestMCPTracingMiddleware_IntegrationRecordsSpan wires the real tracing
// middleware + tool-call middleware behind an in-memory MCP server, calls
// a tool, and asserts a span was produced for the assembled system with
// the tool name and bounded + high-cardinality attributes. This proves
// end-to-end span production, not just that the recorder works alone.
func TestMCPTracingMiddleware_IntegrationRecordsSpan(t *testing.T) {
	tr, sr := recorderTracer(t)

	server := mcp.NewServer(&mcp.Implementation{Name: "tracing-test", Version: "v0.0.0"}, nil)
	server.AddTool(&mcp.Tool{
		Name:        "trino_query",
		Description: "test",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"sql":{"type":"string"}}}`),
	}, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
	})

	authenticator := &fakeAuthn{user: &middleware.UserInfo{UserID: "u1", Email: "u1@example.com", Roles: []string{"analyst"}}}
	authorizer := &fakeAuthz{persona: "analyst"}
	lookup := &fakeLookup{kind: "trino", name: "prod", conn: "primary"}

	// Innermost first, outermost last. Tracing is OUTER to ToolCall, as the
	// platform registers it (#1892): it attaches the PlatformContext that
	// ToolCall fills in and reads it after the call returns.
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(
		authenticator, authorizer, lookup,
		middleware.ToolCallConfig{Transport: "stdio", AdminPersona: "admin"},
	))
	server.AddReceivingMiddleware(middleware.MCPTracingMiddleware(tr))

	ctx := context.Background()
	sess := mustConnect(ctx, t, server)
	defer func() { _ = sess.Close() }()

	res, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name:      "trino_query",
		Arguments: map[string]any{"sql": "SELECT 1"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)

	require.NoError(t, tr.Shutdown(context.Background()))
	spans := sr.Ended()
	require.Len(t, spans, 1, "exactly one tool-call span")
	span := spans[0]

	assert.Equal(t, "tools/call trino_query", span.Name(), "the convention's {mcp.method.name} {target}, target bounded (#1893)")
	assert.Equal(t, "Ok", span.Status().Code.String())
	assert.False(t, span.Parent().IsValid(), "no caller context: a root span")

	attrs := map[string]string{}
	for _, a := range span.Attributes() {
		attrs[string(a.Key)] = a.Value.AsString()
	}
	assert.Equal(t, "trino_query", attrs["mcp.tool"])
	assert.Equal(t, "trino_query", attrs["gen_ai.tool.name"], "the convention key beside the platform's")
	assert.Equal(t, "tools/call", attrs["mcp.method.name"])
	assert.Equal(t, "execute_tool", attrs["gen_ai.operation.name"])
	assert.NotEmpty(t, attrs["mcp.protocol.version"], "the revision the session negotiated")
	_, hasErrType := attrs["error.type"]
	assert.False(t, hasErrType, "a succeeded call carries no error.type")
	assert.Equal(t, "trino", attrs["mcp.toolkit_kind"])
	assert.Equal(t, "analyst", attrs["mcp.persona"])
	assert.Equal(t, "u1", attrs["mcp.user_id"], "high-cardinality user id belongs on the span")
	_, hasEmail := attrs["mcp.user_email"]
	assert.False(t, hasEmail, "the address stays off the span unless OTEL_TRACES_INCLUDE_USER_EMAIL opts in (#1892)")
	assert.Equal(t, "ok", attrs["status_category"], "outcome set by observability.SetSpanStatus")
}

// TestMCPTracingMiddleware_DisabledIsNoop confirms a nil/disabled tracer
// produces no spans and passes the call through unchanged.
func TestMCPTracingMiddleware_DisabledIsNoop(t *testing.T) {
	_, sr := recorderTracer(t) // recorder installed globally, but middleware uses a nil tracer
	var nilTracer *observability.Tracer

	server := mcp.NewServer(&mcp.Implementation{Name: "tracing-off", Version: "v0.0.0"}, nil)
	server.AddTool(&mcp.Tool{
		Name:        "s3_list",
		Description: "test",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
	})
	server.AddReceivingMiddleware(middleware.MCPTracingMiddleware(nilTracer))
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(
		&fakeAuthn{user: &middleware.UserInfo{UserID: "u1", Roles: []string{"analyst"}}},
		&fakeAuthz{persona: "analyst"},
		&fakeLookup{kind: "s3", name: "prod", conn: "primary"},
		middleware.ToolCallConfig{Transport: "stdio", AdminPersona: "admin"},
	))

	ctx := context.Background()
	sess := mustConnect(ctx, t, server)
	defer func() { _ = sess.Close() }()

	_, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "s3_list"})
	require.NoError(t, err)

	assert.Empty(t, sr.Ended(), "disabled tracer must produce no spans")
}

// BenchmarkMCPTracingMiddleware_Disabled measures the hot-path overhead of
// the tracing middleware when tracing is OFF (the default). The issue's
// open question asks whether the OTel hop costs anything per tool call at
// typical QPS; a nil tracer should be a single nil-pointer compare.
func BenchmarkMCPTracingMiddleware_Disabled(b *testing.B) {
	var nilTracer *observability.Tracer
	handler := middleware.MCPTracingMiddleware(nilTracer)(
		func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
			return &mcp.CallToolResult{}, nil
		})
	ctx := context.Background()
	req := &mcp.CallToolRequest{}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = handler(ctx, "tools/call", req)
	}
}

// BenchmarkMCPTracingMiddleware_Enabled measures the per-call cost with an
// always-sampling recorder (worst case: every call produces a span).
func BenchmarkMCPTracingMiddleware_Enabled(b *testing.B) {
	sr := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(sr),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	tr := observability.NewTracerFromProvider(provider, observability.TracingConfig{Enabled: true})
	defer func() { _ = tr.Shutdown(context.Background()) }()

	handler := middleware.MCPTracingMiddleware(tr)(
		func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
			return &mcp.CallToolResult{}, nil
		})
	ctx := middleware.WithPlatformContext(context.Background(),
		&middleware.PlatformContext{ToolName: "trino_query", ToolkitKind: "trino", PersonaName: "analyst"})
	req := &mcp.CallToolRequest{}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = handler(ctx, "tools/call", req)
	}
}

// callerTraceparent is a sampled W3C traceparent a caller might send.
const callerTraceparent = "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"

// tracedServer is an MCP server with the tool-call and tracing middleware as
// the platform registers them, for the inbound-context tests.
func tracedServer(tr *observability.Tracer) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "tracing-inbound", Version: "v0.0.0"}, nil)
	server.AddTool(&mcp.Tool{
		Name:        "trino_query",
		Description: "test",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"sql":{"type":"string"}}}`),
	}, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
	})
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(
		&fakeAuthn{user: &middleware.UserInfo{UserID: "u1", Roles: []string{"analyst"}}},
		&fakeAuthz{persona: "analyst"},
		&fakeLookup{kind: "trino", name: "prod", conn: "primary"},
		middleware.ToolCallConfig{Transport: "http", AdminPersona: "admin"},
	))
	server.AddReceivingMiddleware(middleware.MCPTracingMiddleware(tr))
	return server
}

// assertContinuesCallerTrace: the one tool-call span has the caller's trace
// id and the caller's span as its parent.
func assertContinuesCallerTrace(t *testing.T, sr *tracetest.SpanRecorder) {
	t.Helper()
	var toolSpans []sdktrace.ReadOnlySpan
	for _, sp := range sr.Ended() {
		if strings.HasPrefix(sp.Name(), "tools/call") {
			toolSpans = append(toolSpans, sp)
		}
	}
	require.Len(t, toolSpans, 1, "exactly one tool-call span")
	span := toolSpans[0]
	assert.Equal(t, "0af7651916cd43dd8448eb211c80319c", span.SpanContext().TraceID().String(), "the caller's trace continues")
	assert.Equal(t, "b7ad6b7169203331", span.Parent().SpanID().String(), "the caller's span is the parent")
	assert.True(t, span.Parent().IsRemote())
}

// TestMCPTracingMiddleware_ContinuesTraceFromMeta: a tools/call whose
// params._meta carries a sampled traceparent produces a span in the caller's
// trace, under the caller's span (#1893), through the real server and
// middleware chain.
func TestMCPTracingMiddleware_ContinuesTraceFromMeta(t *testing.T) {
	tr, sr := recorderTracer(t)
	ctx := context.Background()
	sess := mustConnect(ctx, t, tracedServer(tr))
	defer func() { _ = sess.Close() }()

	res, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name:      "trino_query",
		Arguments: map[string]any{"sql": "SELECT 1"},
		Meta:      mcp.Meta{"traceparent": callerTraceparent},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	require.NoError(t, tr.Shutdown(context.Background()))
	assertContinuesCallerTrace(t, sr)
}

// traceparentTransport adds the caller's traceparent header to every request,
// as an instrumented HTTP client does.
type traceparentTransport struct{ base http.RoundTripper }

func (tp traceparentTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("traceparent", callerTraceparent)
	return tp.base.RoundTrip(r) //nolint:wrapcheck // the transport's own error, as a transport returns it
}

// TestMCPTracingMiddleware_ContinuesTraceFromHTTPHeader: over the streamable
// HTTP transport, the traceparent header on the tools/call POST reaches the
// middleware as the request's header and the span continues the caller's
// trace (#1893).
func TestMCPTracingMiddleware_ContinuesTraceFromHTTPHeader(t *testing.T) {
	tr, sr := recorderTracer(t)
	server := tracedServer(tr)
	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	defer httpServer.Close()

	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "caller", Version: "v0.0.0"}, nil)
	sess, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   httpServer.URL,
		HTTPClient: &http.Client{Transport: traceparentTransport{base: http.DefaultTransport}},
	}, nil)
	require.NoError(t, err)
	defer func() { _ = sess.Close() }()

	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "trino_query", Arguments: map[string]any{"sql": "SELECT 1"}})
	require.NoError(t, err)
	require.False(t, res.IsError)
	require.NoError(t, tr.Shutdown(context.Background()))
	assertContinuesCallerTrace(t, sr)
}
