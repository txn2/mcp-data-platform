package mcpobs

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// TestWithTraceMeta_RoundTripsThroughTraceContext: what WithTraceMeta writes
// is what TraceContext reads, so a call over an in-process session continues
// the caller's trace (#1897); with no trace the meta is left as it was.
func TestWithTraceMeta_RoundTripsThroughTraceContext(t *testing.T) {
	prev := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { otel.SetTextMapPropagator(prev) })

	assert.Nil(t, WithTraceMeta(context.Background(), nil), "no trace, no meta")
	kept := mcp.Meta{"other": "x"}
	assert.Equal(t, mcp.Meta{"other": "x"}, WithTraceMeta(context.Background(), kept))

	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{1, 2, 3},
		SpanID:     trace.SpanID{4, 5, 6},
		TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), sc)
	meta := WithTraceMeta(ctx, mcp.Meta{"site": "1:2"})
	require.Contains(t, meta, "traceparent")
	assert.Equal(t, "1:2", meta["site"])

	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Meta: meta}}
	got := trace.SpanContextFromContext(TraceContext(context.Background(), req))
	assert.Equal(t, sc.TraceID(), got.TraceID())
	assert.Equal(t, sc.SpanID(), got.SpanID())
}
