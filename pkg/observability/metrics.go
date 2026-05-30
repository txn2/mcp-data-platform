package observability

import (
	"context"
	"database/sql"
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
//   - apigateway_inbound_requests_total{connection, operation_id, method, status_class, identity}
//   - apigateway_inbound_duration_seconds{connection, operation_id, method, status_class}
//
// Inbound cardinality: the inbound series count is bounded by
// connections × operation_ids × methods × status_classes × identities.
// connection is operator-configured (small); method and status_class are
// closed sets (~7 and 5); operation_id is bounded by the catalog's
// operation count and falls back to "unknown" for connections with no
// catalog or requests that match no spec path; identity is the API key
// name or OIDC subject ("unknown" when unauthenticated). identity is
// recorded ONLY on the request counter, never on the duration histogram,
// to keep the histogram's bucket series from multiplying by the identity
// dimension.
const (
	instToolCalls            = "mcp_tool_calls"
	instToolCallDuration     = "mcp_tool_call_duration"
	instInflightToolCalls    = "mcp_inflight_tool_calls"
	instAPIGwOutbound        = "apigateway_outbound"
	instAPIGwOutboundLatency = "apigateway_outbound_duration"
	instAPIGwInbound         = "apigateway_inbound_requests"
	instAPIGwInboundLatency  = "apigateway_inbound_duration"

	// Toolkit and provider instruments (issue #461). Exposed names add the
	// "_total" / "_seconds" suffixes the Prometheus exporter appends:
	//   - trino_queries_total{status, query_kind}
	//   - trino_query_duration_seconds{query_kind}
	//   - datahub_requests_total{operation, status}
	//   - datahub_request_duration_seconds{operation}
	//   - s3_operations_total{operation, status}
	//   - s3_operation_duration_seconds{operation}
	//   - oauth_token_issuance_total{grant_type, status}
	//   - oauth_token_refresh_total{status}
	//   - oauth_token_refresh_duration_seconds
	//
	// NOTE: trino_bytes_scanned_total from the original acceptance criteria is
	// NOT implemented: mcp-trino v1.3.0 QueryStats exposes row_count, duration,
	// truncated, limit_applied, and query_id but no bytes-scanned figure, so
	// there is no honest source for it. Revisit if the upstream client adds it.
	instTrinoQueries        = "trino_queries"
	instTrinoQueryDuration  = "trino_query_duration"
	instDataHubRequests     = "datahub_requests"
	instDataHubReqDuration  = "datahub_request_duration"
	instS3Operations        = "s3_operations"
	instS3OpDuration        = "s3_operation_duration"
	instOAuthIssuance       = "oauth_token_issuance"
	instOAuthRefresh        = "oauth_token_refresh"
	instOAuthRefreshLatency = "oauth_token_refresh_duration"

	// DB connection-pool instruments (issue #461), observed at scrape time
	// from (*sql.DB).Stats() via a single registered callback. Exposed names:
	//   - db_pool_open_connections{pool}
	//   - db_pool_in_use{pool}
	//   - db_pool_idle{pool}
	//   - db_pool_wait_count_total{pool}
	//   - db_pool_wait_duration_seconds_total{pool}
	instDBPoolOpen         = "db_pool_open_connections"
	instDBPoolInUse        = "db_pool_in_use"
	instDBPoolIdle         = "db_pool_idle"
	instDBPoolWaitCount    = "db_pool_wait_count"
	instDBPoolWaitDuration = "db_pool_wait_duration"
)

// unitSeconds is the OTel unit for duration histograms; the Prometheus exporter
// turns it into the "_seconds" name suffix.
const unitSeconds = "s"

// UpstreamStatus maps an error from an external dependency (Trino, DataHub, S3,
// an IdP) to a bounded status label: nil is StatusOK, anything else is
// StatusUpstreamErr. It reuses the platform's existing status taxonomy
// (see status.go) rather than introducing a parallel client_err/server_err set.
// Call sites with an HTTP status code should use HTTPStatusCategory instead.
func UpstreamStatus(err error) string {
	if err == nil {
		return StatusOK
	}
	return StatusUpstreamErr
}

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
	attrOperationID    = "operation_id"
	attrMethod         = "method"
	attrStatusClass    = "status_class"
	attrIdentity       = "identity"
	// Toolkit / provider metric attribute keys (issue #461).
	attrStatus    = "status"
	attrQueryKind = "query_kind"
	attrOperation = "operation"
	attrGrantType = "grant_type"
	attrPool      = "pool"
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

// APIGatewayInboundAttrs is the bounded label set for inbound HTTP
// requests to the apigateway REST shim. OperationID is the OpenAPI
// operationId resolved from the connection's catalog ("unknown" when
// unresolved); Identity is the API key name or OIDC subject ("unknown"
// when unauthenticated). The raw path, query string, and numeric status
// code are NOT labels; they are cardinality bombs and belong on trace
// spans. Identity is applied to the request counter only (see
// RecordAPIGatewayInbound).
type APIGatewayInboundAttrs struct {
	Connection  string
	OperationID string
	Method      string
	StatusClass string
	Identity    string
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
	apigwInboundTotal     metric.Int64Counter
	apigwInboundDuration  metric.Float64Histogram

	// Toolkit / provider instruments (issue #461).
	trinoQueriesTotal    metric.Int64Counter
	trinoQueryDuration   metric.Float64Histogram
	datahubRequestsTotal metric.Int64Counter
	datahubReqDuration   metric.Float64Histogram
	s3OperationsTotal    metric.Int64Counter
	s3OpDuration         metric.Float64Histogram
	oauthIssuanceTotal   metric.Int64Counter
	oauthRefreshTotal    metric.Int64Counter
	oauthRefreshDuration metric.Float64Histogram

	// DB connection-pool instruments, observed at scrape time from each
	// registered pool's (*sql.DB).Stats(). The five instruments and the
	// callback are registered exactly once at New(); RegisterDBPool only
	// appends to dbPools, which the callback iterates under dbMu.
	meter         metric.Meter
	dbPoolOpen    metric.Int64ObservableGauge
	dbPoolInUse   metric.Int64ObservableGauge
	dbPoolIdle    metric.Int64ObservableGauge
	dbPoolWaitCnt metric.Int64ObservableCounter
	dbPoolWaitDur metric.Float64ObservableCounter
	dbMu          sync.RWMutex
	dbPools       []registeredPool

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
		meter:    meter,
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

// wrapReg wraps an instrument-registration error with its name (or returns nil).
// Centralizing the wrap lets the registration closures return a single
// same-package value instead of a raw meter error, so there is one wrapped
// error path rather than one per instrument.
func wrapReg(name string, err error) error {
	if err != nil {
		return fmt.Errorf(instErrFmt, name, err)
	}
	return nil
}

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
		metric.WithUnit(unitSeconds),
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
		metric.WithUnit(unitSeconds),
	)
	if err != nil {
		return fmt.Errorf(instErrFmt, instAPIGwOutboundLatency, err)
	}
	m.apigwInboundTotal, err = meter.Int64Counter(
		instAPIGwInbound,
		metric.WithDescription("Total number of inbound HTTP requests to the apigateway REST shim, labeled by connection, operation_id, method, status_class, and identity."),
	)
	if err != nil {
		return fmt.Errorf(instErrFmt, instAPIGwInbound, err)
	}
	m.apigwInboundDuration, err = meter.Float64Histogram(
		instAPIGwInboundLatency,
		metric.WithDescription("Inbound HTTP request latency in seconds, measured at the apigateway REST shim, labeled by connection, operation_id, method, and status_class."),
		metric.WithUnit(unitSeconds),
	)
	if err != nil {
		return fmt.Errorf(instErrFmt, instAPIGwInboundLatency, err)
	}
	if err := m.registerToolkitInstruments(meter); err != nil {
		return err
	}
	return m.registerDBPoolInstruments(meter)
}

// registerToolkitInstruments registers the Trino/DataHub/S3/OAuth instruments
// (issue #461). Each instrument's construction is a closure; a single loop runs
// them and wraps the first failure, so there is one error path rather than one
// per instrument.
func (m *Metrics) registerToolkitInstruments(meter metric.Meter) error {
	regs := []func() error{
		func() error {
			v, err := meter.Int64Counter(instTrinoQueries,
				metric.WithDescription("Total Trino queries executed through the platform's Trino toolkit, labeled by status and query_kind."))
			m.trinoQueriesTotal = v
			return wrapReg(instTrinoQueries, err)
		},
		func() error {
			v, err := meter.Float64Histogram(instTrinoQueryDuration,
				metric.WithDescription("Trino query latency in seconds, labeled by query_kind."), metric.WithUnit(unitSeconds))
			m.trinoQueryDuration = v
			return wrapReg(instTrinoQueryDuration, err)
		},
		func() error {
			v, err := meter.Int64Counter(instDataHubRequests,
				metric.WithDescription("Total DataHub requests made by the semantic provider, labeled by operation and status."))
			m.datahubRequestsTotal = v
			return wrapReg(instDataHubRequests, err)
		},
		func() error {
			v, err := meter.Float64Histogram(instDataHubReqDuration,
				metric.WithDescription("DataHub request latency in seconds, labeled by operation."), metric.WithUnit(unitSeconds))
			m.datahubReqDuration = v
			return wrapReg(instDataHubReqDuration, err)
		},
		func() error {
			v, err := meter.Int64Counter(instS3Operations,
				metric.WithDescription("Total S3 operations performed by the S3 toolkit, labeled by operation and status."))
			m.s3OperationsTotal = v
			return wrapReg(instS3Operations, err)
		},
		func() error {
			v, err := meter.Float64Histogram(instS3OpDuration,
				metric.WithDescription("S3 operation latency in seconds, labeled by operation."), metric.WithUnit(unitSeconds))
			m.s3OpDuration = v
			return wrapReg(instS3OpDuration, err)
		},
		func() error {
			v, err := meter.Int64Counter(instOAuthIssuance,
				metric.WithDescription("Total OAuth token issuances, labeled by grant_type and status."))
			m.oauthIssuanceTotal = v
			return wrapReg(instOAuthIssuance, err)
		},
		func() error {
			v, err := meter.Int64Counter(instOAuthRefresh,
				metric.WithDescription("Total OAuth token refreshes, labeled by status."))
			m.oauthRefreshTotal = v
			return wrapReg(instOAuthRefresh, err)
		},
		func() error {
			v, err := meter.Float64Histogram(instOAuthRefreshLatency,
				metric.WithDescription("OAuth token refresh latency in seconds."), metric.WithUnit(unitSeconds))
			m.oauthRefreshDuration = v
			return wrapReg(instOAuthRefreshLatency, err)
		},
	}
	for _, fn := range regs {
		if err := fn(); err != nil {
			return err
		}
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

// RecordAPIGatewayInbound records one inbound REST-shim request
// observation. The request counter carries the identity label; the
// duration histogram deliberately omits it so the bucket series do not
// multiply by the identity dimension. Nil-safe.
func (m *Metrics) RecordAPIGatewayInbound(ctx context.Context, attrs APIGatewayInboundAttrs, duration time.Duration) {
	if m == nil {
		return
	}
	counterSet := metric.WithAttributes(
		attribute.String(attrConnection, attrs.Connection),
		attribute.String(attrOperationID, attrs.OperationID),
		attribute.String(attrMethod, attrs.Method),
		attribute.String(attrStatusClass, attrs.StatusClass),
		attribute.String(attrIdentity, attrs.Identity),
	)
	histSet := metric.WithAttributes(
		attribute.String(attrConnection, attrs.Connection),
		attribute.String(attrOperationID, attrs.OperationID),
		attribute.String(attrMethod, attrs.Method),
		attribute.String(attrStatusClass, attrs.StatusClass),
	)
	m.apigwInboundTotal.Add(ctx, 1, counterSet)
	m.apigwInboundDuration.Record(ctx, duration.Seconds(), histSet)
}

// registeredPool pairs a *sql.DB with the bounded pool label it reports under.
type registeredPool struct {
	db   *sql.DB
	name string
}

// registerDBPoolInstruments creates the five DB-pool observable instruments and
// registers a single callback that, on each scrape, reads (*sql.DB).Stats() for
// every pool registered via RegisterDBPool. The callback is registered exactly
// once here; RegisterDBPool only appends to the observed set.
func (m *Metrics) registerDBPoolInstruments(meter metric.Meter) error {
	regs := []func() error{
		func() error {
			v, err := meter.Int64ObservableGauge(instDBPoolOpen,
				metric.WithDescription("Open connections in the database pool (in use + idle), labeled by pool."))
			m.dbPoolOpen = v
			return wrapReg(instDBPoolOpen, err)
		},
		func() error {
			v, err := meter.Int64ObservableGauge(instDBPoolInUse,
				metric.WithDescription("Connections currently in use, labeled by pool."))
			m.dbPoolInUse = v
			return wrapReg(instDBPoolInUse, err)
		},
		func() error {
			v, err := meter.Int64ObservableGauge(instDBPoolIdle,
				metric.WithDescription("Idle connections in the database pool, labeled by pool."))
			m.dbPoolIdle = v
			return wrapReg(instDBPoolIdle, err)
		},
		func() error {
			v, err := meter.Int64ObservableCounter(instDBPoolWaitCount,
				metric.WithDescription("Cumulative count of waits for a database connection, labeled by pool."))
			m.dbPoolWaitCnt = v
			return wrapReg(instDBPoolWaitCount, err)
		},
		func() error {
			v, err := meter.Float64ObservableCounter(instDBPoolWaitDuration,
				metric.WithDescription("Cumulative time blocked waiting for a database connection, in seconds, labeled by pool."),
				metric.WithUnit(unitSeconds))
			m.dbPoolWaitDur = v
			return wrapReg(instDBPoolWaitDuration, err)
		},
	}
	for _, fn := range regs {
		if err := fn(); err != nil {
			return err
		}
	}
	if _, err := meter.RegisterCallback(m.observeDBPools,
		m.dbPoolOpen, m.dbPoolInUse, m.dbPoolIdle, m.dbPoolWaitCnt, m.dbPoolWaitDur); err != nil {
		return fmt.Errorf(instErrFmt, "db_pool callback", err)
	}
	return nil
}

// observeDBPools is the scrape-time callback that reports each registered
// pool's stats. Nil-safe receiver is not required (only registered when m != nil).
func (m *Metrics) observeDBPools(_ context.Context, o metric.Observer) error {
	m.dbMu.RLock()
	defer m.dbMu.RUnlock()
	for _, p := range m.dbPools {
		s := p.db.Stats()
		set := metric.WithAttributes(attribute.String(attrPool, p.name))
		o.ObserveInt64(m.dbPoolOpen, int64(s.OpenConnections), set)
		o.ObserveInt64(m.dbPoolInUse, int64(s.InUse), set)
		o.ObserveInt64(m.dbPoolIdle, int64(s.Idle), set)
		o.ObserveInt64(m.dbPoolWaitCnt, s.WaitCount, set)
		o.ObserveFloat64(m.dbPoolWaitDur, s.WaitDuration.Seconds(), set)
	}
	return nil
}

// RegisterDBPool adds a *sql.DB to the set whose pool stats are reported on each
// scrape under the given pool label. Call once per managed handle at startup.
// Nil-safe (no-op when metrics are disabled or db is nil); ignores a duplicate
// pool name so a double-registration cannot double-observe the same series.
func (m *Metrics) RegisterDBPool(db *sql.DB, name string) {
	if m == nil || db == nil {
		return
	}
	m.dbMu.Lock()
	defer m.dbMu.Unlock()
	for _, p := range m.dbPools {
		if p.name == name {
			return
		}
	}
	m.dbPools = append(m.dbPools, registeredPool{db: db, name: name})
}

// RecordTrinoQuery records one Trino query observation. status is one of the
// bounded status constants (see status.go); query_kind is a bounded label such
// as the SQL verb or the originating tool. Nil-safe.
func (m *Metrics) RecordTrinoQuery(ctx context.Context, status, queryKind string, duration time.Duration) {
	if m == nil {
		return
	}
	m.trinoQueriesTotal.Add(ctx, 1, metric.WithAttributes(
		attribute.String(attrStatus, status),
		attribute.String(attrQueryKind, queryKind),
	))
	m.trinoQueryDuration.Record(ctx, duration.Seconds(),
		metric.WithAttributes(attribute.String(attrQueryKind, queryKind)))
}

// RecordDataHubRequest records one DataHub request observation. operation is a
// bounded label (get_entity, get_schema, get_lineage, get_glossary_term,
// search_tables, ...); status is a bounded status constant. Nil-safe.
func (m *Metrics) RecordDataHubRequest(ctx context.Context, operation, status string, duration time.Duration) {
	if m == nil {
		return
	}
	m.datahubRequestsTotal.Add(ctx, 1, metric.WithAttributes(
		attribute.String(attrOperation, operation),
		attribute.String(attrStatus, status),
	))
	m.datahubReqDuration.Record(ctx, duration.Seconds(),
		metric.WithAttributes(attribute.String(attrOperation, operation)))
}

// RecordS3Operation records one S3 operation observation. operation is the S3
// tool/op name (list_buckets, get_object, ...); status is a bounded status
// constant. Nil-safe.
func (m *Metrics) RecordS3Operation(ctx context.Context, operation, status string, duration time.Duration) {
	if m == nil {
		return
	}
	m.s3OperationsTotal.Add(ctx, 1, metric.WithAttributes(
		attribute.String(attrOperation, operation),
		attribute.String(attrStatus, status),
	))
	m.s3OpDuration.Record(ctx, duration.Seconds(),
		metric.WithAttributes(attribute.String(attrOperation, operation)))
}

// RecordOAuthIssuance records one OAuth token issuance. grantType is the OAuth
// grant (authorization_code, client_credentials, refresh_token); status is a
// bounded status constant. Nil-safe.
func (m *Metrics) RecordOAuthIssuance(ctx context.Context, grantType, status string) {
	if m == nil {
		return
	}
	m.oauthIssuanceTotal.Add(ctx, 1, metric.WithAttributes(
		attribute.String(attrGrantType, grantType),
		attribute.String(attrStatus, status),
	))
}

// RecordOAuthRefresh records one OAuth token refresh outcome and its latency.
// status is a bounded status constant. Nil-safe.
func (m *Metrics) RecordOAuthRefresh(ctx context.Context, status string, duration time.Duration) {
	if m == nil {
		return
	}
	m.oauthRefreshTotal.Add(ctx, 1, metric.WithAttributes(attribute.String(attrStatus, status)))
	m.oauthRefreshDuration.Record(ctx, duration.Seconds())
}
