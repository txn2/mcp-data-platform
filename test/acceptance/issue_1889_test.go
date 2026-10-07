//go:build integration

package acceptance

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Issue #1889: every inbound HTTP request and every MCP method is measured
// under its route template with a server span; a slow request is logged.
//
// Wire forms: the criteria send one tool parameter beyond the session handle,
// mcp-test-fixture__slow's `milliseconds`, whose schema is an integer, the
// one form it has; resources/read takes `uri`, a string. The REST routes are
// reached with no body.

// metricSeries sums one series across every replica's scrape: the sample
// whose name is exactly name and whose label set contains every given label.
func metricSeries(body, name string, labels map[string]string) float64 {
	var total float64
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, name+"{") {
			continue
		}
		ok := true
		for k, v := range labels {
			if !strings.Contains(line, k+`="`+v+`"`) {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if v, err := strconv.ParseFloat(fields[len(fields)-1], 64); err == nil {
			total += v
		}
	}
	return total
}

// routeCount is http_server_request_duration_seconds_count for one route
// template across the replicas.
func routeCount(t *testing.T, route string) float64 {
	t.Helper()
	return metricSeries(scrapeRaw(t), "http_server_request_duration_seconds_count", map[string]string{"route": route})
}

// TestIssue1889_RESTRoutesReportTheirTemplate: GET /api/v1/resources through
// the running platform produces a sample under route="GET /api/v1/resources",
// never the path; an admin route and a portal route each report their own
// template rather than the /api/v1/admin/ or /api/v1/portal/ mount.
func TestIssue1889_RESTRoutesReportTheirTemplate(t *testing.T) {
	c := connect(t)
	routes := []struct{ path, template string }{
		{"/api/v1/resources?limit=5", "GET /api/v1/resources"},
		{"/api/v1/admin/personas", "GET /api/v1/admin/personas"},
		{"/api/v1/portal/me", "GET /api/v1/portal/me"},
	}
	before := map[string]float64{}
	for _, r := range routes {
		before[r.template] = routeCount(t, r.template)
	}
	for _, r := range routes {
		status, _ := c.rest(http.MethodGet, r.path, http.NoBody)
		if status != http.StatusOK {
			t.Fatalf("GET %s: HTTP %d", r.path, status)
		}
	}
	body := scrapeRaw(t)
	for _, r := range routes {
		after := metricSeries(body, "http_server_request_duration_seconds_count", map[string]string{"route": r.template, "method": "GET", "status_class": "2xx"})
		if after <= before[r.template] {
			t.Errorf("%s: no new sample under route=%q (before %v, after %v)", r.path, r.template, before[r.template], after)
		}
	}
	if strings.Contains(body, `route="/api/v1/resources?`) || strings.Contains(body, `route="GET /api/v1/resources?`) {
		t.Error("a request path reached the route label")
	}
}

// bareSession opens a streamable session as the administrator with no
// platform_info call, for the MCP methods that need none.
func bareSession(t *testing.T) *mcp.ClientSession {
	t.Helper()
	return connectBare(t, devAPIKey())
}

// mcpMethodSpan waits for the span of one MCP method made in one session.
func mcpMethodSpan(t *testing.T, sessionID, method string) exportedSpan {
	t.Helper()
	deadline := time.Now().Add(spanWait)
	for {
		for _, sp := range readSpans(t) {
			if sp.Name == method && sp.Attrs["mcp.session.id"] == sessionID {
				return sp
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no %s span for session %s reached the collector within %s", method, sessionID, spanWait)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// TestIssue1889_ToolsListAndResourcesReadAreTracedAndCounted: through the
// real client, tools/list and resources/read each produce a server span
// named by the method, with mcp.method.name and mcp.protocol.version, nested
// under the HTTP request's own span, and a count by method.
func TestIssue1889_ToolsListAndResourcesReadAreTracedAndCounted(t *testing.T) {
	s := bareSession(t)
	ctx, cancel := context.WithTimeout(context.Background(), sessionTimeout)
	defer cancel()
	body := scrapeRaw(t)
	listBefore := metricSeries(body, "mcp_requests_total", map[string]string{"method": "tools/list", "status": "ok"})
	readBefore := metricSeries(body, "mcp_requests_total", map[string]string{"method": "resources/read"})

	if _, err := s.ListTools(ctx, nil); err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	// A resources/read of a URI nothing serves: the method is observed with
	// status client_err whatever the deployment holds.
	_, readErr := s.ReadResource(ctx, &mcp.ReadResourceParams{URI: "mcp://acceptance-1889/does-not-exist"})
	if readErr == nil {
		t.Fatal("resources/read of a missing URI answered without error")
	}

	list := mcpMethodSpan(t, s.ID(), "tools/list")
	if list.Attrs["mcp.method.name"] != "tools/list" || list.Attrs["mcp.protocol.version"] == "" {
		t.Errorf("tools/list span attributes: %v", list.Attrs)
	}
	if list.ParentSpanID == "" {
		t.Error("the tools/list span has no parent; it should nest under the HTTP request's server span")
	}
	for _, sp := range readSpans(t) {
		if sp.SpanID == list.ParentSpanID && sp.Name != "POST /" {
			t.Errorf("the tools/list span's parent is %q, want the HTTP server span \"POST /\"", sp.Name)
		}
	}
	read := mcpMethodSpan(t, s.ID(), "resources/read")
	if read.Attrs["error.type"] != "client_err" || read.Status.Code != 2 {
		t.Errorf("resources/read of a missing URI: error.type=%q status=%d, want client_err and Error", read.Attrs["error.type"], read.Status.Code)
	}

	body = scrapeRaw(t)
	if after := metricSeries(body, "mcp_requests_total", map[string]string{"method": "tools/list", "status": "ok"}); after <= listBefore {
		t.Errorf("mcp_requests_total{method=tools/list} did not increase (%v -> %v)", listBefore, after)
	}
	if after := metricSeries(body, "mcp_requests_total", map[string]string{"method": "resources/read"}); after <= readBefore {
		t.Errorf("mcp_requests_total{method=resources/read} did not increase (%v -> %v)", readBefore, after)
	}
	if metricSeries(body, "mcp_request_duration_seconds_count", map[string]string{"method": "tools/list"}) == 0 {
		t.Error("no mcp_request_duration_seconds sample for tools/list")
	}
}

// stderrRecordSince returns the first JSON record in the replicas' stderr
// written at or after since that match accepts.
func stderrRecordSince(t *testing.T, since time.Time, match func(map[string]any) bool) map[string]any {
	t.Helper()
	for _, path := range stderrLogFiles(t) {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
		for sc.Scan() {
			line := sc.Bytes()
			if len(line) == 0 || line[0] != '{' {
				continue
			}
			var rec map[string]any
			if json.Unmarshal(line, &rec) != nil {
				continue
			}
			ts, _ := rec["time"].(string)
			at, err := time.Parse(time.RFC3339Nano, ts)
			if err != nil || at.Before(since) || !match(rec) {
				continue
			}
			_ = f.Close()
			return rec
		}
		_ = f.Close()
	}
	return nil
}

// TestIssue1889_ASlowRequestIsLoggedWithItsTemplate: the dev stack sets
// server.slow_request_threshold to 2s; a tools/call of the fixture's slow
// tool held for longer logs one WARN naming the template (the MCP transport's
// "/"), the method (POST), the status and the duration, with the request's
// trace id.
func TestIssue1889_ASlowRequestIsLoggedWithItsTemplate(t *testing.T) {
	c := connect(t)
	start := time.Now().Add(-time.Second)
	c.call("mcp-test-fixture__slow", map[string]any{
		"milliseconds": 2600,
		"purpose":      "Acceptance for #1889: hold one request past the slow threshold so it is logged with its template.",
	})
	deadline := time.Now().Add(spanWait)
	var rec map[string]any
	for rec == nil {
		rec = stderrRecordSince(t, start, func(r map[string]any) bool {
			ms, _ := r["duration_ms"].(float64)
			return r["msg"] == "slow HTTP request" && r["route"] == "/" && r["method"] == "POST" && ms >= 2000
		})
		if rec == nil && time.Now().After(deadline) {
			t.Fatal("no \"slow HTTP request\" line for route / method POST reached stderr")
		}
		if rec == nil {
			time.Sleep(250 * time.Millisecond)
		}
	}
	if status, _ := rec["status"].(float64); status != http.StatusOK {
		t.Errorf("status = %v, want 200", rec["status"])
	}
	traceID, _ := rec["trace_id"].(string)
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(traceID) {
		t.Errorf("trace_id = %q, want the request's trace", traceID)
	}
}

// replicaURL is one platform process: MCP_BASE_URL when set, else the first
// dev replica on DEV_API_PORT, never the proxy.
func replicaURL() string {
	if v := os.Getenv("MCP_BASE_URL"); v != "" {
		return v
	}
	port := os.Getenv("DEV_API_PORT")
	if port == "" {
		port = defaultDevPort
	}
	return "http://localhost:" + port
}

// TestIssue1889_StreamableAndSSESessionsWorkThroughTheWrapper: a streamable
// session and an SSE session each list tools unchanged with the response
// wrapper outermost; the SSE listener's stream is the one path that
// type-asserts http.Flusher.
func TestIssue1889_StreamableAndSSESessionsWorkThroughTheWrapper(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), sessionTimeout)
	defer cancel()
	s := bareSession(t)
	if tools, err := s.ListTools(ctx, nil); err != nil || len(tools.Tools) == 0 {
		t.Fatalf("streamable tools/list: %v (%d tools)", err, len(tools.Tools))
	}

	// One replica directly: the dev proxy in front of the two replicas routes
	// a streamable session by its Mcp-Session-Id header and knows nothing of
	// the ?sessionid= an SSE session's POSTs carry, so through it the second
	// request lands on the other replica. The criterion is about the
	// platform's wrapper, which every replica runs.
	httpClient := &http.Client{Transport: authRoundTripper{key: devAPIKey(), base: http.DefaultTransport}}
	sse, err := mcp.NewClient(&mcp.Implementation{Name: "acceptance-1889-sse", Version: "1.0.0"}, nil).
		Connect(ctx, &mcp.SSEClientTransport{Endpoint: replicaURL() + "/sse", HTTPClient: httpClient}, nil)
	if err != nil {
		t.Fatalf("SSE connect: %v", err)
	}
	t.Cleanup(func() { _ = sse.Close() })
	if tools, err := sse.ListTools(ctx, nil); err != nil || len(tools.Tools) == 0 {
		t.Fatalf("SSE tools/list: %v", err)
	}
	// The SSE transport's messages are POSTed to the endpoint the stream
	// announced, /sse?sessionid=..., so they are recorded under POST /sse at
	// once; the GET that holds the stream is recorded when the stream ends.
	body := scrapeRaw(t)
	if metricSeries(body, "http_server_request_duration_seconds_count", map[string]string{"route": "/sse", "method": "POST"}) == 0 {
		t.Error("the SSE message route recorded no sample under POST /sse")
	}
	if metricSeries(body, "http_server_request_duration_seconds_count", map[string]string{"route": "/", "method": "POST"}) == 0 {
		t.Error("the streamable route recorded no sample under POST /")
	}
	_ = sse.Close()
	deadline := time.Now().Add(spanWait)
	for metricSeries(scrapeRaw(t), "http_server_request_duration_seconds_count", map[string]string{"route": "/sse", "method": "GET"}) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the closed SSE stream recorded no sample under GET /sse")
		}
		time.Sleep(250 * time.Millisecond)
	}
}
