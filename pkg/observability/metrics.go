package observability

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel/attribute"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// Instrument names follow the OpenTelemetry convention of NOT
// including the Prometheus "_total" suffix on counters or the
// "_seconds" suffix on histograms with a time unit — the Prometheus
// exporter appends those automatically based on instrument type and
// unit. The dashboard JSON and the docs reference the EXPOSED names
// (with suffixes) since that is what scrapers and PromQL queries
// observe.
//
// Exposed names:
//   - mcp_tool_calls_total
//   - mcp_tool_call_duration_seconds
//   - mcp_inflight_tool_calls
//   - apigateway_outbound_total
//   - apigateway_outbound_duration_seconds
const (
	instToolCalls            = "mcp_tool_calls"
	instToolCallDuration     = "mcp_tool_call_duration"
	instInflightToolCalls    = "mcp_inflight_tool_calls"
	instAPIGwOutbound        = "apigateway_outbound"
	instAPIGwOutboundLatency = "apigateway_outbound_duration"
)

// Histogram bucket boundaries (seconds). Tuned for tool-call and
// outbound-HTTP latencies the platform actually serves: most calls
// finish in tens to hundreds of milliseconds; the upper buckets cover
// the long-tail (DataHub fetches, large Trino queries, slow upstream
// APIs) without wasting series on sub-millisecond buckets.
//
//nolint:gochecknoglobals // OTel histogram bounds are config, not state.
var defaultDurationBuckets = []float64{
	0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60,
}

// Attribute keys. Constants so a typo can't silently create a new
// label dimension at runtime.
const (
	attrTool           = "tool"
	attrToolkitKind    = "toolkit_kind"
	attrPersona        = "persona"
	attrStatusCategory = "status_category"
	attrConnection     = "connection"
	attrHTTPStatus     = "http_status_class"
)

// ToolCallAttrs is the bounded label set for tool-call metrics. The
// metrics layer never reads request bodies, user identifiers, or
// session IDs — those are span attributes (phase 2) and audit log
// fields, not Prometheus labels.
type ToolCallAttrs struct {
	Tool           string
	ToolkitKind    string
	Persona        string
	StatusCategory string
}

// APIGatewayAttrs is the bounded label set for outbound HTTP from the
// apigateway toolkit. Connection is operator-configured (small set);
// the URL, path, query string, and raw status code are NOT recorded
// as labels — they would be cardinality bombs and live on trace
// spans instead.
type APIGatewayAttrs struct {
	Connection      string
	HTTPStatusClass string
	StatusCategory  string
}

// Metrics owns the OTel MeterProvider and the registered
// instruments. A nil *Metrics is a valid no-op recorder: every Record
// method becomes a fast nil-check, so call sites can record
// unconditionally without an enabled check.
type Metrics struct {
	cfg      Config
	provider *sdkmetric.MeterProvider
	registry *prometheus.Registry
	handler  http.Handler

	toolCallsTotal        metric.Int64Counter
	toolCallDuration      metric.Float64Histogram
	inflightToolCalls     metric.Int64UpDownCounter
	apigwOutboundTotal    metric.Int64Counter
	apigwOutboundDuration metric.Float64Histogram

	// shutdownFn calls into the underlying provider. Indirected so
	// tests can simulate a provider that returns an error on Shutdown
	// without needing the SDK to misbehave.
	shutdownFn   func(context.Context) error
	shutdownOnce sync.Once
	shutdownErr  error
}

// New builds a Metrics instance from the supplied config. When
// cfg.Enabled is false New returns (nil, nil) so callers receive a
// no-op recorder without an error path; this keeps the boot sequence
// simple in cmd/mcp-data-platform when metrics are off. The
// nil-no-op shape is intentional and documented on every Record
// method — callers can invoke them unconditionally.
//
// When enabled, New constructs a fresh prometheus.Registry (NOT the
// default registerer) so the platform's metrics are isolated from any
// other library that may publish to the default registry. The Go
// runtime and process collectors are registered explicitly so
// "go_goroutines", "process_cpu_seconds_total", and friends are
// available on the same /metrics endpoint without extra wiring.
func New(cfg Config) (*Metrics, error) {
	if !cfg.Enabled {
		// nolint:nilnil // intentional disabled-no-op return; see Record* methods.
		return nil, nil
	}

	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector())
	reg.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	exporter, err := otelprom.New(
		otelprom.WithRegisterer(reg),
		// WithoutScopeInfo / WithoutTargetInfo drop two
		// auto-emitted info metrics that aren't useful for the
		// platform's dashboards and inflate scrape size.
		otelprom.WithoutScopeInfo(),
		otelprom.WithoutTargetInfo(),
	)
	if err != nil {
		return nil, fmt.Errorf("observability: prometheus exporter: %w", err)
	}

	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(exporter),
		sdkmetric.WithView(durationHistogramView()),
	)
	meter := provider.Meter("github.com/txn2/mcp-data-platform")

	m := &Metrics{
		cfg:      cfg,
		provider: provider,
		registry: reg,
		handler: promhttp.HandlerFor(reg, promhttp.HandlerOpts{
			ErrorHandling: promhttp.HTTPErrorOnError,
			// EnableOpenMetrics off — the counter "_total"
			// suffix flag above and most Grafana queries
			// assume classic Prometheus format.
		}),
		shutdownFn: provider.Shutdown,
	}

	if err := m.registerInstruments(meter); err != nil {
		_ = provider.Shutdown(context.Background())
		return nil, err
	}
	return m, nil
}

// durationHistogramView applies the platform's bucket boundaries to
// every histogram named "*_duration_seconds" so apigateway and
// tool-call histograms share one resolution without per-instrument
// option duplication.
func durationHistogramView() sdkmetric.View {
	return sdkmetric.NewView(
		sdkmetric.Instrument{Kind: sdkmetric.InstrumentKindHistogram},
		sdkmetric.Stream{
			Aggregation: sdkmetric.AggregationExplicitBucketHistogram{
				Boundaries: defaultDurationBuckets,
			},
		},
	)
}

// instErrFmt is the wrapping format used for every instrument
// registration failure so callers can grep on a single prefix.
const instErrFmt = "observability: %s: %w"

func (m *Metrics) registerInstruments(meter metric.Meter) error {
	var err error
	m.toolCallsTotal, err = meter.Int64Counter(
		instToolCalls,
		metric.WithDescription("Total number of MCP tool calls handled by the platform, labeled by tool, toolkit_kind, persona, and status_category."),
	)
	if err != nil {
		return fmt.Errorf(instErrFmt, instToolCalls, err)
	}
	m.toolCallDuration, err = meter.Float64Histogram(
		instToolCallDuration,
		metric.WithDescription("End-to-end MCP tool call latency in seconds, measured at the tool-call middleware."),
		metric.WithUnit("s"),
	)
	if err != nil {
		return fmt.Errorf(instErrFmt, instToolCallDuration, err)
	}
	m.inflightToolCalls, err = meter.Int64UpDownCounter(
		instInflightToolCalls,
		metric.WithDescription("Number of MCP tool calls currently in flight at the tool-call middleware."),
	)
	if err != nil {
		return fmt.Errorf(instErrFmt, instInflightToolCalls, err)
	}
	m.apigwOutboundTotal, err = meter.Int64Counter(
		instAPIGwOutbound,
		metric.WithDescription("Total number of outbound HTTP calls made by the apigateway toolkit, labeled by connection, http_status_class, and status_category."),
	)
	if err != nil {
		return fmt.Errorf(instErrFmt, instAPIGwOutbound, err)
	}
	m.apigwOutboundDuration, err = meter.Float64Histogram(
		instAPIGwOutboundLatency,
		metric.WithDescription("Outbound HTTP call latency in seconds, measured at the apigateway transport."),
		metric.WithUnit("s"),
	)
	if err != nil {
		return fmt.Errorf(instErrFmt, instAPIGwOutboundLatency, err)
	}
	return nil
}

// Enabled reports whether the recorder is active. The middleware uses
// this only to skip building label sets when nothing will be
// recorded; Record methods themselves are nil-safe.
func (m *Metrics) Enabled() bool { return m != nil }

// Handler returns the /metrics HTTP handler. Returns http.NotFoundHandler
// when m is nil so cmd/main can mount the handler unconditionally.
func (m *Metrics) Handler() http.Handler {
	if m == nil {
		return http.NotFoundHandler()
	}
	return m.handler
}

// Shutdown flushes the meter provider and releases resources. Safe to
// call on a nil receiver so cmd/main's shutdown path stays branch-free.
// Idempotent: the underlying OTel meter provider rejects a second
// Shutdown with "reader is shutdown", so subsequent calls return the
// first-call result (typically nil) instead of re-invoking the
// provider.
func (m *Metrics) Shutdown(ctx context.Context) error {
	if m == nil || m.shutdownFn == nil {
		return nil
	}
	m.shutdownOnce.Do(func() {
		if err := m.shutdownFn(ctx); err != nil {
			m.shutdownErr = fmt.Errorf("observability: meter provider shutdown: %w", err)
		}
	})
	return m.shutdownErr
}

// RecordToolCall records one tool-call observation. Nil-safe.
func (m *Metrics) RecordToolCall(ctx context.Context, attrs ToolCallAttrs, duration time.Duration) {
	if m == nil {
		return
	}
	set := metric.WithAttributes(
		attribute.String(attrTool, attrs.Tool),
		attribute.String(attrToolkitKind, attrs.ToolkitKind),
		attribute.String(attrPersona, attrs.Persona),
		attribute.String(attrStatusCategory, attrs.StatusCategory),
	)
	m.toolCallsTotal.Add(ctx, 1, set)
	m.toolCallDuration.Record(ctx, duration.Seconds(), set)
}

// IncInflightToolCalls increments the in-flight gauge. Paired with
// DecInflightToolCalls in a defer at the tool-call middleware so the
// gauge cannot leak even on panic. Nil-safe.
func (m *Metrics) IncInflightToolCalls(ctx context.Context) {
	if m == nil {
		return
	}
	m.inflightToolCalls.Add(ctx, 1)
}

// DecInflightToolCalls decrements the in-flight gauge. Nil-safe.
func (m *Metrics) DecInflightToolCalls(ctx context.Context) {
	if m == nil {
		return
	}
	m.inflightToolCalls.Add(ctx, -1)
}

// RecordAPIGatewayOutbound records one outbound HTTP observation.
// Nil-safe.
func (m *Metrics) RecordAPIGatewayOutbound(ctx context.Context, attrs APIGatewayAttrs, duration time.Duration) {
	if m == nil {
		return
	}
	set := metric.WithAttributes(
		attribute.String(attrConnection, attrs.Connection),
		attribute.String(attrHTTPStatus, attrs.HTTPStatusClass),
		attribute.String(attrStatusCategory, attrs.StatusCategory),
	)
	m.apigwOutboundTotal.Add(ctx, 1, set)
	m.apigwOutboundDuration.Record(ctx, duration.Seconds(), set)
}
