// Package obs owns the platform's observability layer: the metrics recorder,
// its /metrics HTTP listener, and the (independently-gated) OTel tracer.
//
// Split out of pkg/platform (#756) so the assembly and teardown of these three
// coupled handles is one testable unit with an explicit constructor, rather
// than three fields mutated in place on the Platform god-struct. Every accessor
// is nil-receiver-safe and returns nil-safe observability handles, so callers
// record and trace unconditionally and a disabled deployment behaves as a
// zero-overhead no-op.
package obs

import (
	"fmt"
	"log/slog"

	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// Layer owns the metrics recorder, its listener, and the tracer.
type Layer struct {
	metrics  *observability.Metrics
	listener *observability.Listener
	tracer   *observability.Tracer
}

// Assemble reads metrics and tracing config from the environment and builds the
// recorder, its listener, and the tracer. It returns a non-nil Layer even when
// both subsystems are disabled (the handles are nil and nil-safe). NewTracer
// installs the global OTel TracerProvider when tracing is enabled, so toolkit
// adapters and the tracing middleware emit nested spans without an injected
// handle.
func Assemble() (*Layer, error) {
	cfg := observability.ConfigFromEnv()
	m, err := observability.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("metrics: %w", err)
	}
	l := &Layer{metrics: m, listener: observability.NewListener(m)}
	if m != nil {
		slog.Info("observability: metrics recorder enabled", "listen", cfg.ListenAddr)
	}

	tcfg := observability.TracingConfigFromEnv()
	tr, err := observability.NewTracer(tcfg)
	if err != nil {
		return nil, fmt.Errorf("tracing: %w", err)
	}
	l.tracer = tr
	if tr != nil {
		slog.Info("observability: tracing enabled", "endpoint", tcfg.Endpoint, "sampler_ratio", tcfg.SamplerArg)
	}
	return l, nil
}

// New builds a Layer from explicit handles, deriving the /metrics listener from
// the recorder. Assemble is the env-driven factory; New is for callers that
// hold the handles directly (config-driven wiring, tests).
func New(metrics *observability.Metrics, tracer *observability.Tracer) *Layer {
	return &Layer{
		metrics:  metrics,
		listener: observability.NewListener(metrics),
		tracer:   tracer,
	}
}

// Metrics returns the recorder, or nil when metrics are disabled or the layer
// is nil. The returned type is nil-safe.
func (l *Layer) Metrics() *observability.Metrics {
	if l == nil {
		return nil
	}
	return l.metrics
}

// Tracer returns the tracer, or nil when tracing is disabled or the layer is
// nil. The returned type is nil-safe.
func (l *Layer) Tracer() *observability.Tracer {
	if l == nil {
		return nil
	}
	return l.tracer
}

// Enabled reports whether either metrics or tracing is active. It gates
// installation of the instrumenting decorators, which serve both subsystems.
func (l *Layer) Enabled() bool {
	return l.Metrics().Enabled() || l.Tracer().Enabled()
}

// Listener returns the /metrics HTTP listener, or nil when the layer is nil.
// The returned type is nil-safe (a disabled listener starts/stops as a no-op),
// so the platform orchestrates and wraps Start/Shutdown at its own boundary.
func (l *Layer) Listener() *observability.Listener {
	if l == nil {
		return nil
	}
	return l.listener
}
