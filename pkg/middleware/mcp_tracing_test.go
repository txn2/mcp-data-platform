package middleware_test

import (
	"context"
	"encoding/json"
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

	// Innermost first, outermost last. Tracing must be inner to ToolCall
	// so the PlatformContext is populated when the span attributes are set.
	server.AddReceivingMiddleware(middleware.MCPTracingMiddleware(tr))
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(
		authenticator, authorizer, lookup,
		middleware.ToolCallConfig{Transport: "stdio", AdminPersona: "admin"},
	))

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

	assert.Equal(t, "tool_call", span.Name(), "root span has a stable low-cardinality name")
	assert.Equal(t, "Ok", span.Status().Code.String())

	attrs := map[string]string{}
	for _, a := range span.Attributes() {
		attrs[string(a.Key)] = a.Value.AsString()
	}
	assert.Equal(t, "trino_query", attrs["mcp.tool"])
	assert.Equal(t, "trino", attrs["mcp.toolkit_kind"])
	assert.Equal(t, "analyst", attrs["mcp.persona"])
	assert.Equal(t, "u1", attrs["mcp.user_id"], "high-cardinality user id belongs on the span")
	assert.Equal(t, "u1@example.com", attrs["mcp.user_email"])
	assert.Equal(t, "ok", attrs["status_category"], "outcome set by observability.SetSpanStatus")
}

// TestMCPTracingMiddleware_DisabledIsNoop confirms a nil/disabled tracer
// produces no spans and passes the call through unchanged.
func TestMCPTracingMiddleware_DisabledIsNoop(t *testing.T) {
	_, sr := recorderTracer(t) // recorder installed globally, but middleware uses a nil tracer
	var nilTracer *observability.Tracer

	server := mcp.NewServer(&mcp.Implementation{Name: "tracing-off", Version: "v0.0.0"}, nil)
	server.AddTool(&mcp.Tool{
		Name:        "s3_list_buckets",
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

	_, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "s3_list_buckets"})
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
