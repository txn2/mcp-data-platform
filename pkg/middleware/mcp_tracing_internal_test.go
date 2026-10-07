package middleware

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
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
	setToolSpanAttributes(span, pc, false)
	span.End()
	_, span = tracer.Start(context.Background(), "opted-in")
	setToolSpanAttributes(span, pc, true)
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
