//go:build integration

package acceptance

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Issue #1893: every signal the platform emits carries a resource that
// identifies the deployment; metrics are pushed over OTLP beside the
// Prometheus scrape; a tools/call continues the caller's trace; the build is
// a metric.
//
// Wire forms: list_connections takes no arguments, so the only JSON the
// criteria send beyond the session handle is params._meta.traceparent, a
// string, the one form the W3C header has. The traceparent header is sent as
// an HTTP header.
//
// The dev stack (dev/start.sh) names its deployment through both channels the
// resource reads: OTEL_RESOURCE_ATTRIBUTES=deployment.environment.name=dev and
// MCP_PLATFORM_DEPLOYMENT_ID=acme-dev, and sets OTEL_METRICS_EXPORTER=both so
// the collector's metrics.jsonl (dev/otel-collector.yml) receives what
// /metrics serves.
const (
	devDeploymentEnvironment = "dev"
	devDeploymentID          = "acme-dev"
)

// assertDeploymentResource1893 asserts the four resource attributes every
// signal must carry: the two the environment set and the two the build sets.
func assertDeploymentResource1893(t *testing.T, signal string, res map[string]string) {
	t.Helper()
	if res["deployment.environment.name"] != devDeploymentEnvironment {
		t.Errorf("%s resource deployment.environment.name = %q, want %q (OTEL_RESOURCE_ATTRIBUTES)", signal, res["deployment.environment.name"], devDeploymentEnvironment)
	}
	if res["mcp_platform.deployment.id"] != devDeploymentID {
		t.Errorf("%s resource mcp_platform.deployment.id = %q, want %q (MCP_PLATFORM_DEPLOYMENT_ID)", signal, res["mcp_platform.deployment.id"], devDeploymentID)
	}
	if res["service.version"] == "" {
		t.Errorf("%s resource carries no service.version", signal)
	}
	if res["service.instance.id"] == "" {
		t.Errorf("%s resource carries no service.instance.id", signal)
	}
	if res["service.name"] == "" {
		t.Errorf("%s resource carries no service.name", signal)
	}
}

// exportedMetric is one metric from one export the collector wrote: its
// name, the resource of the process, and the attributes of each data point.
type exportedMetric struct {
	Name     string
	Resource map[string]string
	Points   []map[string]string
}

// otelSignalFile is where the dev collector writes one signal's JSON lines.
func otelSignalFile(name string) string {
	dir := os.Getenv("DEV_OTEL_DIR")
	if dir == "" {
		dir = filepath.Join("..", "..", "dev", ".otel")
	}
	return filepath.Join(dir, name)
}

// recentMetricsTail is how much of metrics.jsonl is read: every export is the
// whole cumulative state of one replica, every two seconds in the dev stack
// (OTEL_METRIC_EXPORT_INTERVAL=2000), so the newest few megabytes hold a
// complete picture of both replicas and the file need not be read from the
// start.
const recentMetricsTail = 8 << 20

// readRecentMetrics parses the exports in the tail of metrics.jsonl.
func readRecentMetrics(t *testing.T) []exportedMetric {
	t.Helper()
	f, err := os.Open(otelSignalFile("metrics.jsonl"))
	if err != nil {
		t.Fatalf("the dev collector's metrics file: %v. `make dev` starts the collector with OTEL_METRICS_EXPORTER=both", err)
	}
	defer f.Close() //nolint:errcheck // test
	if st, err := f.Stat(); err == nil && st.Size() > recentMetricsTail {
		if _, err := f.Seek(st.Size()-recentMetricsTail, io.SeekStart); err != nil {
			t.Fatalf("seek: %v", err)
		}
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	var out []exportedMetric
	first := true
	for sc.Scan() {
		if first {
			// After a seek the first read is a partial line.
			first = false
			if !strings.HasPrefix(sc.Text(), "{") {
				continue
			}
		}
		var line struct {
			ResourceMetrics []struct {
				Resource struct {
					Attributes []otlpAttr `json:"attributes"`
				} `json:"resource"`
				ScopeMetrics []struct {
					Metrics []struct {
						Name string `json:"name"`
						Sum  *struct {
							DataPoints []struct {
								Attributes []otlpAttr `json:"attributes"`
							} `json:"dataPoints"`
						} `json:"sum"`
						Gauge *struct {
							DataPoints []struct {
								Attributes []otlpAttr `json:"attributes"`
							} `json:"dataPoints"`
						} `json:"gauge"`
					} `json:"metrics"`
				} `json:"scopeMetrics"`
			} `json:"resourceMetrics"`
		}
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			continue // a partial line at the seek point
		}
		for _, rm := range line.ResourceMetrics {
			res := attrsOf(rm.Resource.Attributes)
			for _, sm := range rm.ScopeMetrics {
				for _, m := range sm.Metrics {
					em := exportedMetric{Name: m.Name, Resource: res}
					if m.Sum != nil {
						for _, dp := range m.Sum.DataPoints {
							em.Points = append(em.Points, attrsOf(dp.Attributes))
						}
					}
					if m.Gauge != nil {
						for _, dp := range m.Gauge.DataPoints {
							em.Points = append(em.Points, attrsOf(dp.Attributes))
						}
					}
					out = append(out, em)
				}
			}
		}
	}
	return out
}

// awaitExportedMetric waits for a metric by name carrying a data point with
// the given attribute, and returns that export.
func awaitExportedMetric(t *testing.T, name, attrKey, attrValue string) exportedMetric {
	t.Helper()
	deadline := time.Now().Add(spanWait)
	for {
		for _, em := range readRecentMetrics(t) {
			if em.Name != name {
				continue
			}
			for _, p := range em.Points {
				if p[attrKey] == attrValue {
					return em
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no %s export with %s=%q reached the collector within %s", name, attrKey, attrValue, spanWait)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// TestIssue1893_ExportedSpansAndMetricsCarryTheDeploymentResource: a span
// and a metric exported over OTLP both carry deployment.environment.name and
// mcp_platform.deployment.id as the environment set them, plus
// service.version and service.instance.id.
func TestIssue1893_ExportedSpansAndMetricsCarryTheDeploymentResource(t *testing.T) {
	admin := connect(t)
	admin.call("list_connections", nil)

	span := awaitSpan(t, admin.sessionID, "list_connections")
	assertDeploymentResource1893(t, "span", span.Resource)

	metric := awaitExportedMetric(t, "mcp_tool_calls", "tool", "list_connections")
	assertDeploymentResource1893(t, "metric", metric.Resource)
}

var buildInfoSeries = regexp.MustCompile(`(?m)^mcp_platform_build_info\{([^}]*)\} 1$`)

// TestIssue1893_TheSameCounterIsScrapedAndPushed: with OTEL_METRICS_EXPORTER=both
// the tool-call counter is on /metrics and at the OTLP receiver, and the
// scrape identifies the process the same way the push does, through
// target_info.
func TestIssue1893_TheSameCounterIsScrapedAndPushed(t *testing.T) {
	admin := connect(t)
	admin.call("list_connections", nil)

	scrape := scrapeRaw(t)
	if toolCallsTotal(t, "list_connections", "ok") == 0 {
		t.Errorf("mcp_tool_calls_total{tool=\"list_connections\",status_category=\"ok\"} is absent from /metrics")
	}
	if !strings.Contains(scrape, `target_info{`) || !strings.Contains(scrape, `mcp_platform_deployment_id="`+devDeploymentID+`"`) {
		t.Errorf("/metrics carries no target_info naming the deployment; the scrape and the push identify the process differently")
	}
	if !strings.Contains(scrape, `deployment_environment_name="`+devDeploymentEnvironment+`"`) {
		t.Errorf("/metrics target_info carries no deployment_environment_name")
	}

	metric := awaitExportedMetric(t, "mcp_tool_calls", "tool", "list_connections")
	found := false
	for _, p := range metric.Points {
		if p["tool"] == "list_connections" && p["status_category"] == "ok" {
			found = true
		}
	}
	if !found {
		t.Errorf("the OTLP export of mcp_tool_calls has no data point for list_connections ok: %v", metric.Points)
	}
}

// TestIssue1893_MetricsShowTheBuildInfo: /metrics carries
// mcp_platform_build_info with the version the running platform reports.
func TestIssue1893_MetricsShowTheBuildInfo(t *testing.T) {
	admin := connect(t)
	status, system := admin.rest(http.MethodGet, "/api/v1/admin/system/info", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /api/v1/admin/system/info: %d %v", status, system)
	}
	version, _ := system["version"].(string)
	commit, _ := system["commit"].(string)

	series := buildInfoSeries.FindAllStringSubmatch(scrapeRaw(t), -1)
	if len(series) == 0 {
		t.Fatalf("mcp_platform_build_info is absent from /metrics")
	}
	for _, m := range series {
		labels := m[1]
		if !strings.Contains(labels, `version="`+version+`"`) {
			t.Errorf("mcp_platform_build_info{%s}: version is not the running platform's %q", labels, version)
		}
		if !strings.Contains(labels, `commit="`+commit+`"`) {
			t.Errorf("mcp_platform_build_info{%s}: commit is not the running platform's %q", labels, commit)
		}
		if !strings.Contains(labels, `go_version="go`) {
			t.Errorf("mcp_platform_build_info{%s}: no go_version", labels)
		}
	}
}

// callerTrace is a sampled W3C traceparent a caller might send: a fresh
// trace id per test, so the span found is this run's.
func callerTrace(t *testing.T) (traceparent, traceID, spanID string) {
	t.Helper()
	var tid [16]byte
	if _, err := rand.Read(tid[:]); err != nil {
		t.Fatal(err)
	}
	traceID = hex.EncodeToString(tid[:])
	spanID = "b7ad6b7169203331"
	return "00-" + traceID + "-" + spanID + "-01", traceID, spanID
}

// traceparentRoundTripper puts the caller's traceparent on every request, as
// an instrumented HTTP client does.
type traceparentRoundTripper struct {
	traceparent string
	base        http.RoundTripper
}

func (tp traceparentRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("traceparent", tp.traceparent)
	return tp.base.RoundTrip(r)
}

func assertCallerTrace(t *testing.T, span exportedSpan, traceID, spanID string) {
	t.Helper()
	if span.TraceID != traceID {
		t.Errorf("span trace id = %s, want the caller's %s", span.TraceID, traceID)
	}
	if span.ParentSpanID != spanID {
		t.Errorf("span parent = %q, want the caller's span %s", span.ParentSpanID, spanID)
	}
	if !strings.HasPrefix(span.Name, "tools/call ") {
		t.Errorf("span name = %q, want the convention's \"tools/call {tool}\"", span.Name)
	}
}

// TestIssue1893_ATraceparentHeaderContinuesTheCallersTrace: a tools/call
// whose HTTP request carries a sampled traceparent produces a span whose
// trace id is the caller's and whose parent is the caller's span.
func TestIssue1893_ATraceparentHeaderContinuesTheCallersTrace(t *testing.T) {
	traceparent, traceID, spanID := callerTrace(t)
	c := connectVia(t, baseURL(), devAPIKey(), traceparentRoundTripper{traceparent: traceparent, base: http.DefaultTransport}, sessionTimeout)
	c.call("list_connections", nil)
	span := awaitSpan(t, c.sessionID, "list_connections")
	assertCallerTrace(t, span, traceID, spanID)
}

// TestIssue1893_ATraceparentInMetaContinuesTheCallersTrace: the same, with
// the traceparent in params._meta, where the MCP semantic conventions carry
// it; sent as a string, the one form it has.
func TestIssue1893_ATraceparentInMetaContinuesTheCallersTrace(t *testing.T) {
	traceparent, traceID, spanID := callerTrace(t)
	c := connect(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := c.session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "list_connections",
		Arguments: map[string]any{"session_id": c.sessionID},
		Meta:      mcp.Meta{"traceparent": traceparent},
	})
	if err != nil {
		t.Fatalf("list_connections: transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("list_connections: tool error: %s", firstText(res))
	}
	span := awaitSpan(t, c.sessionID, "list_connections")
	assertCallerTrace(t, span, traceID, spanID)
	if span.Attrs["gen_ai.tool.name"] != "list_connections" || span.Attrs["mcp.method.name"] != "tools/call" {
		t.Errorf("the span carries no MCP semantic convention keys: %v", span.Attrs)
	}
}
