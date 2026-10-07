package middleware

import (
	"context"
	"net/http"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// TestSetToolSpanAttributes covers the identifying fields landing on the
// span, and the caller's email address landing there only when the deployment
// opted in (#1892). Uses an in-memory recorder so the attributes are
// assertable.
func TestSetToolSpanAttributes(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tracer := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(sr),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	).Tracer("test")

	pc := &PlatformContext{
		ToolName: "trino_query", ToolkitKind: "trino", PersonaName: "analyst", UserID: "u1", UserEmail: "u1@example.com",
	}
	_, span := tracer.Start(context.Background(), "default")
	setToolSpanAttributes(span, pc, false, "2025-06-18")
	span.End()
	_, span = tracer.Start(context.Background(), "opted-in")
	setToolSpanAttributes(span, pc, true, "")
	span.End()

	spans := sr.Ended()
	require.Len(t, spans, 2)
	attrsOf := func(name string) map[string]string {
		for _, s := range spans {
			if s.Name() != name {
				continue
			}
			got := map[string]string{}
			for _, a := range s.Attributes() {
				got[string(a.Key)] = a.Value.AsString()
			}
			return got
		}
		t.Fatalf("no span %q", name)
		return nil
	}

	got := attrsOf("default")
	assert.Equal(t, "trino_query", got[spanAttrTool])
	assert.Equal(t, "trino_query", got[spanAttrGenAIToolName], "the convention key beside the platform's (#1893)")
	assert.Equal(t, "tools/call", got[spanAttrMethodName])
	assert.Equal(t, "execute_tool", got[spanAttrGenAIOperation])
	assert.Equal(t, "2025-06-18", got[spanAttrProtocolVersion])
	assert.Equal(t, "trino", got[spanAttrToolkitKind])
	assert.Equal(t, "analyst", got[spanAttrPersona])
	assert.Equal(t, "u1", got[spanAttrUserID])
	_, hasEmail := got[spanAttrUserEmail]
	assert.False(t, hasEmail, "the address is not on the span unless the deployment opted in")

	assert.Equal(t, "u1@example.com", attrsOf("opted-in")[spanAttrUserEmail])
}

// TestToolCallAttrs_BoundsTheToolLabel pins the two bounds on the tool label
// (#1892): a name no toolkit registers records as one fixed value, and a call
// that never carried a name records as unknown.
func TestToolCallAttrs_BoundsTheToolLabel(t *testing.T) {
	got := toolCallAttrs(&PlatformContext{ToolName: "made_up_tool_7", ToolUnregistered: true, Source: "mcp"}, nil, nil)
	assert.Equal(t, "unregistered", got.Tool)
	assert.Equal(t, "mcp", got.Source)
	got = toolCallAttrs(&PlatformContext{}, nil, nil)
	assert.Equal(t, "unknown", got.Tool)
	got = toolCallAttrs(&PlatformContext{ToolName: "trino_query"}, nil, nil)
	assert.Equal(t, "trino_query", got.Tool)
}

// TestToolCallSpanName_IsBounded: the span name takes the bounded tool name,
// so an invented tool cannot mint one (#1893).
func TestToolCallSpanName_IsBounded(t *testing.T) {
	assert.Equal(t, "tools/call trino_query", toolCallSpanName(&PlatformContext{ToolName: "trino_query"}))
	assert.Equal(t, "tools/call unregistered", toolCallSpanName(&PlatformContext{ToolName: "made_up_tool_7", ToolUnregistered: true}))
	assert.Equal(t, "tools/call unknown", toolCallSpanName(&PlatformContext{}))
}

func TestErrorType(t *testing.T) {
	assert.Equal(t, "SEARCH_REQUIRED", errorType("gate_err", "SEARCH_REQUIRED"), "the error contract's category when the tool reported one")
	assert.Equal(t, "authz_err", errorType("authz_err", ""), "else the status category")
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

// TestInboundTraceContext_MetaWinsOverHeader: both carriers present, the
// request's own _meta decides the parent.
func TestInboundTraceContext_MetaWinsOverHeader(t *testing.T) {
	otel.SetTextMapPropagator(propagation.TraceContext{})
	header := http.Header{}
	header.Set("traceparent", "00-11111111111111111111111111111111-2222222222222222-01")
	req := &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Meta: mcp.Meta{"traceparent": "00-33333333333333333333333333333333-4444444444444444-01"}},
		Extra:  &mcp.RequestExtra{Header: header},
	}
	sc := trace.SpanContextFromContext(inboundTraceContext(context.Background(), req))
	assert.Equal(t, "33333333333333333333333333333333", sc.TraceID().String())
	assert.Equal(t, "4444444444444444", sc.SpanID().String())
	assert.True(t, sc.IsRemote())

	req.Params = &mcp.CallToolParamsRaw{}
	sc = trace.SpanContextFromContext(inboundTraceContext(context.Background(), req))
	assert.Equal(t, "11111111111111111111111111111111", sc.TraceID().String(), "the header alone")

	req.Extra = nil
	assert.False(t, trace.SpanContextFromContext(inboundTraceContext(context.Background(), req)).IsValid(), "neither: the span to come is a root")
}
