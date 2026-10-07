package observability

import (
	"context"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricpb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/grpc"
)

// otlpReceiver is an in-process OTLP/gRPC collector for the three signals.
// It records what each export carried, so a test can prove that an
// endpoint form, a resource attribute or a trace id reached the wire,
// rather than that an exporter was constructed.
type otlpReceiver struct {
	coltracepb.UnimplementedTraceServiceServer

	addr string

	mu      sync.Mutex
	spans   []receivedSpan
	metrics []receivedMetric
	logs    []receivedLog
}

type receivedSpan struct {
	name     string
	resource map[string]string
}

type receivedMetric struct {
	name     string
	resource map[string]string
}

type receivedLog struct {
	body     string
	traceID  string
	spanID   string
	resource map[string]string
}

func startOTLPReceiver(t *testing.T) *otlpReceiver {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	r := &otlpReceiver{addr: ln.Addr().String()}
	srv := grpc.NewServer()
	coltracepb.RegisterTraceServiceServer(srv, r)
	colmetricpb.RegisterMetricsServiceServer(srv, metricsService{r: r})
	collogspb.RegisterLogsServiceServer(srv, logsService{r: r})
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(srv.Stop)
	return r
}

func protoAttrs(attrs []*commonpb.KeyValue) map[string]string {
	out := map[string]string{}
	for _, kv := range attrs {
		out[kv.GetKey()] = kv.GetValue().GetStringValue()
	}
	return out
}

func (r *otlpReceiver) Export(_ context.Context, req *coltracepb.ExportTraceServiceRequest) (*coltracepb.ExportTraceServiceResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rs := range req.GetResourceSpans() {
		res := protoAttrs(rs.GetResource().GetAttributes())
		for _, ss := range rs.GetScopeSpans() {
			for _, sp := range ss.GetSpans() {
				r.spans = append(r.spans, receivedSpan{name: sp.GetName(), resource: res})
			}
		}
	}
	return &coltracepb.ExportTraceServiceResponse{}, nil
}

func (r *otlpReceiver) exportMetrics(req *colmetricpb.ExportMetricsServiceRequest) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rm := range req.GetResourceMetrics() {
		res := protoAttrs(rm.GetResource().GetAttributes())
		for _, sm := range rm.GetScopeMetrics() {
			for _, m := range sm.GetMetrics() {
				r.metrics = append(r.metrics, receivedMetric{name: m.GetName(), resource: res})
			}
		}
	}
}

func (r *otlpReceiver) exportLogs(req *collogspb.ExportLogsServiceRequest) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rl := range req.GetResourceLogs() {
		res := protoAttrs(rl.GetResource().GetAttributes())
		for _, sl := range rl.GetScopeLogs() {
			for _, rec := range sl.GetLogRecords() {
				r.logs = append(r.logs, receivedLog{
					body:     rec.GetBody().GetStringValue(),
					traceID:  hex.EncodeToString(rec.GetTraceId()),
					spanID:   hex.EncodeToString(rec.GetSpanId()),
					resource: res,
				})
			}
		}
	}
}

// The gRPC service interfaces share the method name Export; the receiver
// satisfies the metrics and logs ones through these embedded adapters.
type metricsService struct {
	colmetricpb.UnimplementedMetricsServiceServer
	r *otlpReceiver
}

func (s metricsService) Export(_ context.Context, req *colmetricpb.ExportMetricsServiceRequest) (*colmetricpb.ExportMetricsServiceResponse, error) {
	s.r.exportMetrics(req)
	return &colmetricpb.ExportMetricsServiceResponse{}, nil
}

type logsService struct {
	collogspb.UnimplementedLogsServiceServer
	r *otlpReceiver
}

func (s logsService) Export(_ context.Context, req *collogspb.ExportLogsServiceRequest) (*collogspb.ExportLogsServiceResponse, error) {
	s.r.exportLogs(req)
	return &collogspb.ExportLogsServiceResponse{}, nil
}

// deploymentEnv sets the acceptance sentence's environment and clears the
// memoized resource so the exporters under test build from it.
func deploymentEnv(t *testing.T) {
	t.Helper()
	resetResourceForTest(t)
	t.Setenv("OTEL_SERVICE_NAME", "")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "deployment.environment.name=staging,mcp_platform.deployment.id=a")
	t.Setenv(envDeploymentEnvironment, "")
	t.Setenv(envDeploymentID, "")
}

func assertDeploymentResource(t *testing.T, res map[string]string) {
	t.Helper()
	assert.Equal(t, "staging", res["deployment.environment.name"])
	assert.Equal(t, "a", res["mcp_platform.deployment.id"])
	assert.Equal(t, "dev", res["service.version"])
	assert.NotEmpty(t, res["service.instance.id"])
	assert.Equal(t, DefaultServiceName, res["service.name"])
}

// TestNewTracer_BothEndpointFormsReachTheCollector proves the two forms the
// specification defines for OTEL_EXPORTER_OTLP_ENDPOINT, and the explicit
// OTEL_EXPORTER_OTLP_INSECURE over a URL, each deliver a span carrying the
// deployment's resource to a real OTLP receiver (#1893).
func TestNewTracer_BothEndpointFormsReachTheCollector(t *testing.T) {
	deploymentEnv(t)
	for _, tt := range []struct {
		name string
		ep   func(addr string) OTLPEndpoint
	}{
		{"host:port", func(a string) OTLPEndpoint { return OTLPEndpoint{Endpoint: a} }},
		{"http URL", func(a string) OTLPEndpoint { return OTLPEndpoint{Endpoint: "http://" + a} }},
		{"https URL with an explicit insecure", func(a string) OTLPEndpoint {
			return OTLPEndpoint{Endpoint: "https://" + a, Insecure: new(true)}
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rcv := startOTLPReceiver(t)
			tr, err := NewTracer(TracingConfig{Enabled: true, OTLP: tt.ep(rcv.addr), SamplerArg: 1})
			require.NoError(t, err)
			_, span := tr.Start(context.Background(), "probe-"+tt.name)
			span.End()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			require.NoError(t, tr.Shutdown(ctx), "shutdown flushes the batch to the receiver")

			rcv.mu.Lock()
			defer rcv.mu.Unlock()
			require.Len(t, rcv.spans, 1, "one span reached the receiver over %s", tt.name)
			assert.Equal(t, "probe-"+tt.name, rcv.spans[0].name)
			assertDeploymentResource(t, rcv.spans[0].resource)
		})
	}
}

// TestNew_MetricsReachTheScrapeAndTheCollector: with OTEL_METRICS_EXPORTER=both
// the same counter is on /metrics and at the OTLP receiver, the scrape
// carries target_info with the resource and mcp_platform_build_info with the
// build, and the OTLP metric carries the resource (#1893).
func TestNew_MetricsReachTheScrapeAndTheCollector(t *testing.T) {
	deploymentEnv(t)
	rcv := startOTLPReceiver(t)
	m, err := New(Config{Enabled: true, ListenAddr: ":0", Exporter: MetricsExporterBoth, OTLP: OTLPEndpoint{Endpoint: rcv.addr}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })

	m.RecordToolCall(context.Background(), ToolCallAttrs{Tool: "trino_query", ToolkitKind: "trino", Persona: "analyst", StatusCategory: StatusOK, Source: "mcp"}, 10*time.Millisecond)

	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", http.NoBody))
	body := rec.Body.String()
	assert.Contains(t, body, `mcp_tool_calls_total{`, "the counter is scraped")
	assert.Contains(t, body, `tool="trino_query"`)
	assert.Contains(t, body, `mcp_platform_build_info{`)
	assert.Contains(t, body, `version="dev"`)
	assert.Contains(t, body, `commit="none"`)
	assert.Contains(t, body, `go_version="go`)
	assert.Contains(t, body, `} 1`, "build info is a constant 1")
	assert.Contains(t, body, `target_info{`, "the resource is exposed to a Prometheus reader as target_info")
	assert.Contains(t, body, `mcp_platform_deployment_id="a"`)
	assert.Contains(t, body, `deployment_environment_name="staging"`)
	assert.Contains(t, body, `service_name="mcp-data-platform"`)
	assert.NotContains(t, body, "otel_scope_info", "scope info stays off the scrape")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, m.provider.ForceFlush(ctx), "the periodic reader pushes on flush")

	rcv.mu.Lock()
	defer rcv.mu.Unlock()
	names := map[string]map[string]string{}
	for _, rm := range rcv.metrics {
		names[rm.name] = rm.resource
	}
	require.Contains(t, names, "mcp_tool_calls", "the same counter reached the OTLP receiver; got %v", names)
	require.Contains(t, names, "mcp_platform_build_info")
	assertDeploymentResource(t, names["mcp_tool_calls"])
}

// TestNew_OTLPOnlyServesNoListener: OTEL_METRICS_EXPORTER=otlp pushes and
// leaves nothing for a scraper, so the /metrics listener is not started.
func TestNew_OTLPOnlyServesNoListener(t *testing.T) {
	rcv := startOTLPReceiver(t)
	m, err := New(Config{Enabled: true, ListenAddr: ":0", Exporter: MetricsExporterOTLP, OTLP: OTLPEndpoint{Endpoint: rcv.addr}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })
	assert.Nil(t, m.Handler())
	assert.Nil(t, NewListener(m))
	m.RecordToolCall(context.Background(), ToolCallAttrs{Tool: "s3_list", ToolkitKind: "s3", Persona: "analyst", StatusCategory: StatusOK, Source: "mcp"}, time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, m.provider.ForceFlush(ctx))
	rcv.mu.Lock()
	defer rcv.mu.Unlock()
	assert.NotEmpty(t, rcv.metrics, "the push is the only path and it works")
}

func TestParseMetricsExporter(t *testing.T) {
	assert.Equal(t, MetricsExporterPrometheus, parseMetricsExporter(""))
	assert.Equal(t, MetricsExporterPrometheus, parseMetricsExporter("prometheus"))
	assert.Equal(t, MetricsExporterOTLP, parseMetricsExporter(" OTLP "))
	assert.Equal(t, MetricsExporterBoth, parseMetricsExporter("both"))
	assert.Equal(t, MetricsExporterPrometheus, parseMetricsExporter("statsd"), "an unrecognized value keeps the scrape on")
	assert.True(t, MetricsExporter("").Prometheus())
	assert.False(t, MetricsExporter("").OTLP())
	assert.True(t, MetricsExporterBoth.OTLP())
	assert.False(t, MetricsExporterOTLP.Prometheus())
}

// TestNewLogProvider_RecordsCarryTheTraceAndTheResource: a record logged
// with a context carrying a span reaches the OTLP receiver with that span's
// trace and span ids, its body, and the deployment's resource (#1894).
func TestNewLogProvider_RecordsCarryTheTraceAndTheResource(t *testing.T) {
	deploymentEnv(t)
	rcv := startOTLPReceiver(t)
	lp, err := NewLogProvider(LogsConfig{Exporter: LogsExporterOTLP, OTLP: OTLPEndpoint{Endpoint: "http://" + rcv.addr}})
	require.NoError(t, err)
	require.NotNil(t, lp)
	handler := lp.Handler()
	require.NotNil(t, handler)

	tr, _ := recorderTracer(t)
	ctx, span := tr.Start(context.Background(), "parent")
	slog.New(handler).InfoContext(ctx, "hello from the platform", "k", "v")
	span.End()

	flushCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, lp.Shutdown(flushCtx))
	assert.NoError(t, lp.Shutdown(flushCtx), "idempotent")

	rcv.mu.Lock()
	defer rcv.mu.Unlock()
	require.Len(t, rcv.logs, 1)
	got := rcv.logs[0]
	assert.Equal(t, "hello from the platform", got.body)
	assert.Equal(t, span.SpanContext().TraceID().String(), got.traceID)
	assert.Equal(t, span.SpanContext().SpanID().String(), got.spanID)
	assertDeploymentResource(t, got.resource)
}

func TestNewLogProvider_OffByDefault(t *testing.T) {
	t.Setenv(envLogsExporter, "")
	cfg := LogsConfigFromEnv()
	assert.Equal(t, LogsExporterNone, cfg.Exporter)
	lp, err := NewLogProvider(cfg)
	require.NoError(t, err)
	assert.Nil(t, lp)
	assert.Nil(t, lp.Handler(), "a disabled provider offers no sink")
	assert.NoError(t, lp.Shutdown(context.Background()))

	t.Setenv(envLogsExporter, "OTLP")
	assert.Equal(t, LogsExporterOTLP, LogsConfigFromEnv().Exporter)
	t.Setenv(envLogsExporter, "console")
	assert.Equal(t, LogsExporterNone, LogsConfigFromEnv().Exporter, "an unrecognized value keeps stderr the only sink")
}
