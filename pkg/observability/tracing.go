package observability

import (
	"context"
	"fmt"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// InstrumentationScope is the tracer name used for spans the platform
// creates directly. Call sites use otel.Tracer(InstrumentationScope) so
// every span shares one scope without threading a *Tracer through every
// constructor.
const InstrumentationScope = "github.com/txn2/mcp-data-platform"

// attrServiceName is the resource attribute key for the logical service.
// Set directly (rather than via a pinned semconv module) so the package
// is not coupled to a specific OpenTelemetry semantic-conventions
// version; collectors read the "service.name" resource key regardless.
const attrServiceName = "service.name"

// noopTracer backs Tracer.Start on a nil receiver so call sites can
// start spans unconditionally. The OTel noop span is safe to End, set
// attributes on, and record errors against — all are no-ops.
//
//nolint:gochecknoglobals // a process-wide immutable no-op tracer is the idiomatic OTel fallback.
var noopTracer = noop.NewTracerProvider().Tracer(InstrumentationScope)

// Tracer owns the OTel TracerProvider and OTLP exporter for distributed
// tracing. A nil *Tracer is a valid no-op: Start returns a no-op span
// and Shutdown is a no-op, so callers hold and use it unconditionally.
//
// Unlike Metrics (which builds an isolated Prometheus registry), Tracer
// installs the GLOBAL OTel TracerProvider and propagator in NewTracer.
// That lets any call site — middleware steps, toolkit adapters — create
// child spans with otel.Tracer(InstrumentationScope) and have them nest
// into the request's span tree via context, without injecting a *Tracer
// everywhere. When tracing is disabled the global provider stays the
// default no-op, so those call sites cost nothing.
type Tracer struct {
	cfg      TracingConfig
	provider *sdktrace.TracerProvider
	tracer   trace.Tracer

	shutdownFn   func(context.Context) error
	shutdownOnce sync.Once
	shutdownErr  error
}

// NewTracer builds a Tracer from cfg. When cfg.Enabled is false it
// returns (nil, nil) so callers receive a no-op recorder without an
// error path, mirroring metrics New.
//
// The OTLP/gRPC exporter connects lazily: NewTracer does not block or
// fail when the collector is unreachable, so an unconfigured or
// down collector never delays or breaks platform startup. Spans are
// batched and dropped if they cannot be delivered.
func NewTracer(cfg TracingConfig) (*Tracer, error) {
	if !cfg.Enabled {
		//nolint:nilnil // intentional disabled-no-op return; see Start/Shutdown.
		return nil, nil
	}

	opts := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(cfg.Endpoint)}
	if cfg.Insecure {
		opts = append(opts, otlptracegrpc.WithInsecure())
	}
	exporter, err := otlptracegrpc.New(context.Background(), opts...)
	if err != nil {
		return nil, fmt.Errorf("observability: otlp trace exporter: %w", err)
	}

	res, err := resource.New(context.Background(),
		resource.WithAttributes(attribute.String(attrServiceName, cfg.ServiceName)))
	if err != nil {
		// resource.New only errors on schema-URL conflicts, which a
		// single hand-set attribute cannot trigger; fall back to a bare
		// resource rather than failing tracing init.
		res = resource.NewSchemaless(attribute.String(attrServiceName, cfg.ServiceName))
	}

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		// ParentBased honors an upstream sampling decision (so a sampled
		// caller's whole trace is kept); root spans are sampled at the
		// configured ratio. Tail-sampling of errors/slow spans lives in
		// the collector.
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.SamplerArg))),
	)
	return NewTracerFromProvider(provider, cfg), nil
}

// NewTracerFromProvider wraps an already-constructed TracerProvider and
// installs it (plus the W3C/baggage propagator) as the OTel global. It is
// the seam NewTracer uses after building the OTLP provider, and the one
// tests use to inject an in-memory span recorder. Always returns a
// non-nil *Tracer.
func NewTracerFromProvider(provider *sdktrace.TracerProvider, cfg TracingConfig) *Tracer {
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{}))
	return &Tracer{
		cfg:        cfg,
		provider:   provider,
		tracer:     provider.Tracer(InstrumentationScope),
		shutdownFn: provider.Shutdown,
	}
}

// Enabled reports whether tracing is active. Call sites that build
// expensive span attributes can gate on this; Start itself is nil-safe.
func (t *Tracer) Enabled() bool { return t != nil }

// Start begins a span. Nil-safe: on a nil receiver it returns a no-op
// span (and the unchanged context) so call sites need no enabled check.
func (t *Tracer) Start(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	if t == nil {
		return noopTracer.Start(ctx, name, opts...)
	}
	return t.tracer.Start(ctx, name, opts...)
}

// Shutdown flushes buffered spans and stops the exporter. Safe on a nil
// receiver and idempotent: the provider rejects a second Shutdown, so
// subsequent calls return the first result.
func (t *Tracer) Shutdown(ctx context.Context) error {
	if t == nil || t.shutdownFn == nil {
		return nil
	}
	t.shutdownOnce.Do(func() {
		if err := t.shutdownFn(ctx); err != nil {
			t.shutdownErr = fmt.Errorf("observability: tracer provider shutdown: %w", err)
		}
	})
	return t.shutdownErr
}

// ChildSpan starts a span ONLY when ctx already carries an active trace
// (a valid span context from an upstream root span). When tracing is
// disabled — or this code runs outside any traced request — it returns a
// non-recording no-op span (and a context carrying it). This lets adapter
// call sites
// (Trino/DataHub/S3/OAuth/enrichment/audit) open a child span
// unconditionally with a single cheap check and never create orphan
// spans or pay span-allocation cost when tracing is off.
func ChildSpan(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	if !trace.SpanContextFromContext(ctx).IsValid() {
		return noopTracer.Start(ctx, name, opts...)
	}
	return otel.Tracer(InstrumentationScope).Start(ctx, name, opts...)
}

// SetSpanStatus records the outcome of an operation on span from the
// platform's bounded status_category plus the underlying error. It maps
// every category except StatusOK to codes.Error so error traces stand
// out in Tempo/Jaeger, and attaches the error detail (which, unlike a
// Prometheus label, is safe to carry on a span). Nil-safe span handling
// is the caller's (trace.Span is never nil from Start).
func SetSpanStatus(span trace.Span, statusCategory string, err error) {
	span.SetAttributes(attribute.String(attrStatusCategory, statusCategory))
	if err != nil {
		span.RecordError(err)
	}
	if statusCategory == StatusOK {
		span.SetStatus(codes.Ok, "")
		return
	}
	msg := statusCategory
	if err != nil {
		msg = err.Error()
	}
	span.SetStatus(codes.Error, msg)
}
