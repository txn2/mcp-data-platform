package middleware

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// TestSetToolSpanAttributes covers both the nil-PlatformContext path
// (auth rejected before context population — the function must add nothing
// and not panic) and the populated path (the identifying fields land on
// the span). Uses an in-memory recorder so the attributes are assertable.
func TestSetToolSpanAttributes(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tracer := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(sr),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	).Tracer("test")

	// Nil PlatformContext: no attributes, no panic.
	_, nilSpan := tracer.Start(context.Background(), "nil-pc")
	setToolSpanAttributes(nilSpan, nil)
	nilSpan.End()

	// Populated PlatformContext: identifying fields land on the span.
	_, span := tracer.Start(context.Background(), "with-pc")
	setToolSpanAttributes(span, &PlatformContext{
		ToolName: "trino_query", ToolkitKind: "trino", PersonaName: "analyst", UserID: "u1",
	})
	span.End()

	spans := sr.Ended()
	require.Len(t, spans, 2)
	byName := map[string]sdktrace.ReadOnlySpan{}
	for _, s := range spans {
		byName[s.Name()] = s
	}
	assert.Empty(t, byName["nil-pc"].Attributes(), "nil PlatformContext must add no attributes")

	got := map[string]string{}
	for _, a := range byName["with-pc"].Attributes() {
		got[string(a.Key)] = a.Value.AsString()
	}
	assert.Equal(t, "trino_query", got[spanAttrTool])
	assert.Equal(t, "trino", got[spanAttrToolkitKind])
	assert.Equal(t, "analyst", got[spanAttrPersona])
	assert.Equal(t, "u1", got[spanAttrUserID])
}
