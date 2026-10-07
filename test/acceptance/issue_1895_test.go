//go:build integration

package acceptance

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Issue #1895: every outbound HTTP call goes through one chain that opens a
// client span, carries the caller's trace to the upstream unless the
// connection turns that off, and counts the call by kind; the MCP gateway
// records its forwarded calls; a blocked egress and an embedding call are
// counted.
//
// Wire forms: api_invoke_endpoint's `body` admits an object and a string of
// JSON; the egress criterion sends the fetch body as an object, as #1544
// does, and the propagation criterion sends none. `trace_propagation` is
// written on the connection as a boolean, the one form the schema admits.

// outboundSpan waits for a client span of one kind in one trace.
func outboundSpan(t *testing.T, traceID, name string) exportedSpan {
	t.Helper()
	deadline := time.Now().Add(spanWait)
	for {
		for _, sp := range readSpans(t) {
			if sp.TraceID == traceID && sp.Name == name {
				return sp
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no %q span in trace %s reached the collector within %s", name, traceID, spanWait)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// TestIssue1895_AnAPICallCarriesTheTraceToTheUpstreamUnlessTurnedOff: a
// tools/call of an api connection with a sampled trace produces a client span
// under the tools/call span, and the api-test fixture receives a traceparent
// with that trace id; with trace_propagation false on the connection, the
// fixture receives none and the client span is recorded all the same.
func TestIssue1895_AnAPICallCarriesTheTraceToTheUpstreamUnlessTurnedOff(t *testing.T) {
	admin := connect(t)
	propagating := issue1647Connect(t, admin, "trace-on", map[string]any{"auth_mode": "none"})
	silent := issue1647Connect(t, admin, "trace-off", map[string]any{"auth_mode": "none", "trace_propagation": false})

	traceparent, traceID, _ := callerTrace(t)
	c := connectVia(t, baseURL(), devAPIKey(), traceparentRoundTripper{traceparent: traceparent, base: http.DefaultTransport}, sessionTimeout)

	body := issue1647Echo(t, c, propagating, nil)
	got := issue1647Header(body, "Traceparent")
	if len(got) != 1 || !strings.Contains(got[0], traceID) {
		t.Fatalf("the fixture received traceparent %v, want the caller's trace %s", got, traceID)
	}
	toolCall := awaitSpan(t, c.sessionID, issue1647Tool)
	client := outboundSpan(t, traceID, "api GET")
	if client.ParentSpanID != toolCall.SpanID {
		t.Errorf("the client span's parent is %s, want the tools/call span %s", client.ParentSpanID, toolCall.SpanID)
	}
	if client.Attrs["upstream.kind"] != "api" || client.Attrs["mcp.connection"] != propagating {
		t.Errorf("client span attributes: %v", client.Attrs)
	}
	if client.Attrs["http.response.status_code"] != "200" {
		t.Errorf("client span status code = %q", client.Attrs["http.response.status_code"])
	}

	body = issue1647Echo(t, c, silent, nil)
	if got := issue1647Header(body, "Traceparent"); got != nil {
		t.Errorf("trace_propagation: false, yet the fixture received traceparent %v", got)
	}
	if got := issue1647Header(body, "Tracestate"); got != nil {
		t.Errorf("trace_propagation: false, yet the fixture received tracestate %v", got)
	}
	deadline := time.Now().Add(spanWait)
	for {
		var spans int
		for _, sp := range readSpans(t) {
			if sp.TraceID == traceID && sp.Name == "api GET" && sp.Attrs["mcp.connection"] == silent {
				spans++
			}
		}
		if spans > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the silent connection's call has no client span; propagation off must not switch the span off")
		}
		time.Sleep(250 * time.Millisecond)
	}
	scrape := scrapeRaw(t)
	for _, conn := range []string{propagating, silent} {
		if metricSeries(scrape, "http_client_requests_total", map[string]string{"kind": "api", "connection": conn, "status_class": "2xx"}) == 0 {
			t.Errorf("no http_client_requests_total sample for kind=api connection=%s", conn)
		}
	}
}

// TestIssue1895_AnMCPGatewayCallIsRecordedUnderItsKind: a proxied tool call
// through the real client records the upstream HTTP call under kind="mcp"
// and the forwarded call under its connection and outcome.
func TestIssue1895_AnMCPGatewayCallIsRecordedUnderItsKind(t *testing.T) {
	c := connect(t)
	labels := map[string]string{"kind": "mcp", "connection": "mcp-test-fixture", "status_class": "2xx"}
	before := scrapeRaw(t)
	httpBefore := metricSeries(before, "http_client_requests_total", labels)
	callsBefore := metricSeries(before, "gateway_upstream_calls_total", map[string]string{"connection": "mcp-test-fixture", "outcome": "ok"})

	c.call("mcp-test-fixture__whoami", map[string]any{
		"purpose": "Acceptance for #1895: one forwarded call so the gateway's upstream request is counted under its kind.",
	})

	after := scrapeRaw(t)
	if got := metricSeries(after, "http_client_requests_total", labels); got <= httpBefore {
		t.Errorf("http_client_requests_total{kind=mcp} did not increase (%v -> %v)", httpBefore, got)
	}
	if got := metricSeries(after, "gateway_upstream_calls_total", map[string]string{"connection": "mcp-test-fixture", "outcome": "ok"}); got <= callsBefore {
		t.Errorf("gateway_upstream_calls_total{outcome=ok} did not increase (%v -> %v)", callsBefore, got)
	}
	if metricSeries(after, "gateway_upstream_call_duration_seconds_count", map[string]string{"connection": "mcp-test-fixture"}) == 0 {
		t.Error("no gateway_upstream_call_duration_seconds sample")
	}
}

// TestIssue1895_AUtilFetchOfAPrivateAddressIsCountedAsBlocked: fetch_url of
// an address inside the private range is refused with 403, and the refusal
// increments egress_blocked_total{reason="private"}.
func TestIssue1895_AUtilFetchOfAPrivateAddressIsCountedAsBlocked(t *testing.T) {
	c := connect(t)
	before := metricSeries(scrapeRaw(t), "egress_blocked_total", map[string]string{"reason": "private"})
	res, text, err := c.callRaw("api_invoke_endpoint", map[string]any{
		"connection": "util",
		"method":     "POST",
		"path":       "/util/fetch",
		"body":       map[string]any{"url": "http://10.255.255.1/"},
		"purpose":    "Acceptance for #1895: a fetch of a private address is refused and counted.",
	})
	if err != nil {
		t.Fatalf("api_invoke_endpoint: transport error: %v", err)
	}
	if !strings.Contains(text, "403") && !strings.Contains(text, "refused") && !res.IsError {
		t.Fatalf("the fetch was not refused: %s", text)
	}
	deadline := time.Now().Add(spanWait)
	for {
		after := metricSeries(scrapeRaw(t), "egress_blocked_total", map[string]string{"reason": "private"})
		if after > before {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("egress_blocked_total{reason=private} did not increase (%v)", before)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// TestIssue1895_ASearchThatEmbedsIsCountedUnderItsModel: a query-time search
// embeds its query; the call is counted under the deployment's model.
func TestIssue1895_ASearchThatEmbedsIsCountedUnderItsModel(t *testing.T) {
	c := connect(t)
	const model = "nomic-embed-text"
	before := metricSeries(scrapeRaw(t), "embedding_calls_total", map[string]string{"model": model})
	c.call("search", map[string]any{
		"intent":  "customer orders by region for the quarterly review",
		"purpose": "Acceptance for #1895: one query-time search so its embedding call is counted under the model.",
	})
	after := metricSeries(scrapeRaw(t), "embedding_calls_total", map[string]string{"model": model})
	if after <= before {
		t.Fatalf("embedding_calls_total{model=%s} did not increase (%v -> %v)", model, before, after)
	}
	if metricSeries(scrapeRaw(t), "embedding_call_duration_seconds_count", map[string]string{"model": model}) == 0 {
		t.Error("no embedding_call_duration_seconds sample")
	}
}

// TestIssue1895_TheSemgrepRuleRefusesAClientOffTheChain:
// .semgrep/go-outbound-client.yml fails on a client literal, the
// process-wide client and the package-level helpers in non-test Go outside
// internal/outbound, and passes a client built on the chain, a test file and
// the chain itself.
func TestIssue1895_TheSemgrepRuleRefusesAClientOffTheChain(t *testing.T) {
	semgrep, err := exec.LookPath("semgrep")
	if err != nil {
		t.Fatalf("semgrep is not installed; make verify's semgrep gate needs it and so does this criterion")
	}
	rule, err := filepath.Abs(filepath.Join("..", "..", ".semgrep", "go-outbound-client.yml"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	run := func(rel, src string) (int, string) {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Remove(path) })
		cmd := exec.Command(semgrep, "scan", "--config", rule, "--error", "--quiet", "--json", ".") //nolint:gosec // test runs the gate's tool on a fixture
		cmd.Dir = root
		out, err := cmd.Output()
		code := 0
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		} else if err != nil {
			t.Fatalf("semgrep: %v", err)
		}
		var report struct {
			Results []struct {
				Start struct {
					Line int `json:"line"`
				} `json:"start"`
			} `json:"results"`
		}
		if err := json.Unmarshal(out, &report); err != nil {
			t.Fatalf("semgrep output: %v: %s", err, out)
		}
		// A line two patterns match (&http.Client{} is also an http.Client{})
		// is one finding to a reader.
		seen := map[int]bool{}
		lines := make([]string, 0, len(report.Results))
		for _, r := range report.Results {
			if seen[r.Start.Line] {
				continue
			}
			seen[r.Start.Line] = true
			lines = append(lines, strconv.Itoa(r.Start.Line))
		}
		return code, strings.Join(lines, ",")
	}

	offending := `package sample

import "net/http"

func a() *http.Client   { return &http.Client{} }
func b() http.Client    { return http.Client{} }
func c() *http.Client   { return http.DefaultClient }
func d() (*http.Response, error) { return http.Get("https://example.com") }
func e() (*http.Response, error) { return http.Post("https://example.com", "text/plain", nil) }
`
	code, lines := run("pkg/sample/sample.go", offending)
	if code == 0 || lines != "5,6,7,8,9" {
		t.Errorf("the rule accepted a client off the chain: exit %d, findings at lines %q, want 5,6,7,8,9", code, lines)
	}
	_ = os.Remove(filepath.Join(root, "pkg", "sample", "sample.go"))

	clean := `package sample

import (
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/outbound"
)

func a() *http.Client { return outbound.NewClient(outbound.Options{Kind: outbound.KindOIDC}) }
func b(c *http.Client) (*http.Response, error) { return c.Get("https://example.com") }
`
	if code, lines := run("pkg/sample/sample.go", clean); code != 0 {
		t.Errorf("the rule refused a client on the chain: exit %d at lines %q", code, lines)
	}
	_ = os.Remove(filepath.Join(root, "pkg", "sample", "sample.go"))
	if code, lines := run("pkg/sample/sample_test.go", offending); code != 0 {
		t.Errorf("the rule reached a test file: exit %d at lines %q", code, lines)
	}
	_ = os.Remove(filepath.Join(root, "pkg", "sample", "sample_test.go"))
	if code, lines := run("internal/outbound/chain.go", offending); code != 0 {
		t.Errorf("the rule reached the chain itself: exit %d at lines %q", code, lines)
	}
}
