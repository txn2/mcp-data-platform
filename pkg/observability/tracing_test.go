package observability

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// recorderTracer builds a *Tracer backed by an in-memory span recorder so
// tests can assert on the spans produced. Always-sample so every span is
// recorded.
func recorderTracer(t *testing.T) (*Tracer, *tracetest.SpanRecorder) {
	t.Helper()
	sr := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(sr),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	tr := NewTracerFromProvider(provider, TracingConfig{Enabled: true})
	t.Cleanup(func() { _ = tr.Shutdown(context.Background()) })
	return tr, sr
}

func TestTracingConfigFromEnv_Defaults(t *testing.T) {
	// No env set in this test process → tracing disabled, defaults applied.
	cfg := TracingConfigFromEnv()
	assert.False(t, cfg.Enabled)
	assert.Equal(t, DefaultOTLPEndpoint, cfg.Endpoint)
	assert.True(t, cfg.Insecure)
	assert.InEpsilon(t, DefaultSamplerArg, cfg.SamplerArg, 0.0001)
	assert.Equal(t, DefaultServiceName, cfg.ServiceName)
}

func TestTracingConfigFromEnv_Overrides(t *testing.T) {
	t.Setenv(envTracesEnabled, "true")
	t.Setenv(envOTLPEndpoint, "collector:4317")
	t.Setenv(envOTLPInsecure, "false")
	t.Setenv(envTracesSamplerArg, "0.5")
	t.Setenv(envServiceName, "custom-svc")

	cfg := TracingConfigFromEnv()
	assert.True(t, cfg.Enabled)
	assert.Equal(t, "collector:4317", cfg.Endpoint)
	assert.False(t, cfg.Insecure)
	assert.InEpsilon(t, 0.5, cfg.SamplerArg, 0.0001)
	assert.Equal(t, "custom-svc", cfg.ServiceName)
}

func TestParseFloatEnv_Clamping(t *testing.T) {
	const key = "OTEL_TEST_FLOAT"
	tests := []struct {
		name string
		set  bool
		val  string
		want float64
	}{
		{"unset returns default", false, "", 0.3},
		{"empty returns default", true, "", 0.3},
		{"unparsable returns default", true, "abc", 0.3},
		{"in range", true, "0.7", 0.7},
		{"above one clamps to one", true, "5", 1},
		{"below zero clamps to zero", true, "-2", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.set {
				t.Setenv(key, tt.val)
			}
			assert.InDelta(t, tt.want, parseFloatEnv(key, 0.3), 0.0001)
		})
	}
}

func TestNewTracer_DisabledReturnsNil(t *testing.T) {
	tr, err := NewTracer(TracingConfig{Enabled: false})
	require.NoError(t, err)
	assert.Nil(t, tr)
	// A nil *Tracer is a valid no-op recorder.
	assert.False(t, tr.Enabled())
	_, span := tr.Start(context.Background(), "x")
	span.End() // must not panic
	assert.NoError(t, tr.Shutdown(context.Background()))
}

func TestNewTracer_EnabledLazyExporter(t *testing.T) {
	// Enabled with an unreachable collector must NOT block or error:
	// the OTLP exporter connects lazily.
	tr, err := NewTracer(TracingConfig{
		Enabled:     true,
		Endpoint:    "127.0.0.1:4317",
		Insecure:    true,
		SamplerArg:  1,
		ServiceName: "test",
	})
	require.NoError(t, err)
	require.NotNil(t, tr)
	t.Cleanup(func() { _ = tr.Shutdown(context.Background()) })
	assert.True(t, tr.Enabled())
}

func TestTracer_StartRecordsSpan(t *testing.T) {
	tr, sr := recorderTracer(t)
	_, span := tr.Start(context.Background(), "root")
	span.End()
	require.NoError(t, tr.Shutdown(context.Background()))
	spans := sr.Ended()
	require.Len(t, spans, 1)
	assert.Equal(t, "root", spans[0].Name())
}

func TestChildSpan_NoopOutsideTrace(t *testing.T) {
	// No active span in ctx → ChildSpan returns a non-recording span.
	_, span := ChildSpan(context.Background(), "child")
	defer span.End()
	assert.False(t, span.IsRecording(), "child span must not record outside an active trace")
	assert.False(t, span.SpanContext().IsValid(), "no-op span carries no valid span context")
}

func TestChildSpan_NestsUnderActiveTrace(t *testing.T) {
	tr, sr := recorderTracer(t)
	ctx, root := tr.Start(context.Background(), "root")

	_, child := ChildSpan(ctx, "child")
	assert.True(t, child.IsRecording(), "child span must record within an active trace")
	child.End()
	root.End()

	require.NoError(t, tr.Shutdown(context.Background()))
	spans := sr.Ended()
	require.Len(t, spans, 2)
	// Child shares the root's trace and parents to it.
	byName := map[string]sdktrace.ReadOnlySpan{}
	for _, s := range spans {
		byName[s.Name()] = s
	}
	require.Contains(t, byName, "child")
	require.Contains(t, byName, "root")
	assert.Equal(t, byName["root"].SpanContext().TraceID(), byName["child"].SpanContext().TraceID())
	assert.Equal(t, byName["root"].SpanContext().SpanID(), byName["child"].Parent().SpanID())
}

func TestSetSpanStatus(t *testing.T) {
	tr, sr := recorderTracer(t)

	_, okSpan := tr.Start(context.Background(), "ok")
	SetSpanStatus(okSpan, StatusOK, nil)
	okSpan.End()

	_, errSpan := tr.Start(context.Background(), "err")
	SetSpanStatus(errSpan, StatusUpstreamErr, errors.New("boom"))
	errSpan.End()

	require.NoError(t, tr.Shutdown(context.Background()))
	spans := sr.Ended()
	require.Len(t, spans, 2)

	byName := map[string]sdktrace.ReadOnlySpan{}
	for _, s := range spans {
		byName[s.Name()] = s
	}
	// OK span carries codes.Ok and the status_category attribute.
	assert.Equal(t, "Ok", byName["ok"].Status().Code.String())
	assert.True(t, hasStringAttr(byName["ok"], attrStatusCategory, StatusOK))
	// Error span carries codes.Error, the error message, and a recorded event.
	assert.Equal(t, "Error", byName["err"].Status().Code.String())
	assert.Equal(t, "boom", byName["err"].Status().Description)
	assert.NotEmpty(t, byName["err"].Events(), "RecordError should add an exception event")
}

func hasStringAttr(s sdktrace.ReadOnlySpan, key, val string) bool {
	for _, a := range s.Attributes() {
		if string(a.Key) == key && a.Value.AsString() == val {
			return true
		}
	}
	return false
}

// Compile-time assurance that the no-op tracer satisfies the interface
// the call sites use.
var _ trace.Tracer = noopTracer
