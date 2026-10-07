//go:build integration

package acceptance

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Issue #1892: a refused tools/call leaves a span, a counter increment and an
// audit row like any other call; telemetry carries no personal data or raw
// upstream text by default; the inbound identity label is bounded.
//
// Wire forms: trino_query.sql is a string, the one form its schema admits;
// trino_query.purpose is a string, stated because the dev stack requires one
// on query tools. list_connections takes no arguments.
//
// The dev stack's identities (dev/platform.yaml): the inventory analyst may call
// platform_info but not list_connections, so the analyst's list_connections is
// the persona denial; the report viewer may call platform_info and trino_query
// but never search, so the viewer's trino_query is a search-first refusal every
// time, whatever an earlier run left in the gate's store.
const (
	analystKey1892  = "acme-analyst-key"
	reporterKey1892 = "acme-reporter-key"
	// spanWait bounds the wait for the platform's batch exporter (dev/start.sh
	// sets OTEL_BSP_SCHEDULE_DELAY=1000) and the collector's file write.
	spanWait = 15 * time.Second
)

// connectBare opens a session as key with no platform_info and no search, for
// an identity that may call neither, or a criterion about the call before them.
func connectBare(t *testing.T, key string) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), sessionTimeout)
	t.Cleanup(cancel)
	httpClient := &http.Client{Transport: authRoundTripper{key: key, base: http.DefaultTransport}}
	mc := mcp.NewClient(&mcp.Implementation{Name: "acceptance-1892", Version: "1.0.0"}, nil)
	session, err := mc.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: baseURL(), HTTPClient: httpClient}, nil)
	if err != nil {
		t.Fatalf("no platform answers at %s (%v); `make dev` starts one", baseURL(), err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// callBare makes one tools/call on a bare session and returns the result and
// its first text block.
func callBare(t *testing.T, s *mcp.ClientSession, name string, args map[string]any) (*mcp.CallToolResult, string) {
	t.Helper()
	if args == nil {
		args = map[string]any{}
	}
	statePurpose(name, args)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := s.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: transport error: %v", name, err)
	}
	return res, firstText(res)
}

// exportedSpan is one span as the dev collector wrote it (OTLP JSON): its
// ids (hex, as OTLP JSON writes them), the resource of the process that
// emitted it (#1893), its attributes, status and events.
type exportedSpan struct {
	Name         string
	TraceID      string
	SpanID       string
	ParentSpanID string
	Resource     map[string]string
	Attrs        map[string]string
	Status       struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	Events []map[string]string
}

type otlpAttr struct {
	Key   string `json:"key"`
	Value struct {
		String string `json:"stringValue"`
		Bool   *bool  `json:"boolValue"`
	} `json:"value"`
}

func attrsOf(raw []otlpAttr) map[string]string {
	out := map[string]string{}
	for _, a := range raw {
		if a.Value.Bool != nil {
			out[a.Key] = strconv.FormatBool(*a.Value.Bool)
			continue
		}
		out[a.Key] = a.Value.String
	}
	return out
}

// otelTracesFile is where the dev collector writes what it received
// (dev/otel-collector.yml): DEV_OTEL_DIR from dev/.dev-ports.env, which `make
// acceptance` loads, or the checkout's dev/.otel.
func otelTracesFile(t *testing.T) string {
	t.Helper()
	dir := os.Getenv("DEV_OTEL_DIR")
	if dir == "" {
		dir = filepath.Join("..", "..", "dev", ".otel")
	}
	return filepath.Join(dir, "traces.jsonl")
}

// readSpans parses every span the collector has written so far.
func readSpans(t *testing.T) []exportedSpan {
	t.Helper()
	f, err := os.Open(otelTracesFile(t))
	if err != nil {
		t.Fatalf("the dev collector's trace file: %v. `make dev` starts the collector (dev/otel-collector.yml) and points the platform at it", err)
	}
	defer f.Close() //nolint:errcheck // test
	var out []exportedSpan
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for sc.Scan() {
		var line struct {
			ResourceSpans []struct {
				Resource struct {
					Attributes []otlpAttr `json:"attributes"`
				} `json:"resource"`
				ScopeSpans []struct {
					Spans []struct {
						Name         string     `json:"name"`
						TraceID      string     `json:"traceId"`
						SpanID       string     `json:"spanId"`
						ParentSpanID string     `json:"parentSpanId"`
						Attributes   []otlpAttr `json:"attributes"`
						Status       struct {
							Code    int    `json:"code"`
							Message string `json:"message"`
						} `json:"status"`
						Events []struct {
							Attributes []otlpAttr `json:"attributes"`
						} `json:"events"`
					} `json:"spans"`
				} `json:"scopeSpans"`
			} `json:"resourceSpans"`
		}
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			t.Fatalf("collector line is not OTLP JSON: %v", err)
		}
		for _, rs := range line.ResourceSpans {
			res := attrsOf(rs.Resource.Attributes)
			for _, ss := range rs.ScopeSpans {
				for _, sp := range ss.Spans {
					es := exportedSpan{
						Name: sp.Name, TraceID: sp.TraceID, SpanID: sp.SpanID, ParentSpanID: sp.ParentSpanID,
						Resource: res, Attrs: attrsOf(sp.Attributes),
					}
					es.Status.Code, es.Status.Message = sp.Status.Code, sp.Status.Message
					for _, ev := range sp.Events {
						es.Events = append(es.Events, attrsOf(ev.Attributes))
					}
					out = append(out, es)
				}
			}
		}
	}
	return out
}

// isToolCallSpan reports whether sp is the platform's root span for a tool
// call: named "tools/call {tool}" as the MCP semantic conventions say (#1893).
func isToolCallSpan(sp exportedSpan) bool { return strings.HasPrefix(sp.Name, "tools/call ") }

// awaitSpan waits for the tool-call span of one call: the one carrying the
// session it was made in and the tool it named. The newest such span is
// returned, so a session that made the same call twice reads its latest.
func awaitSpan(t *testing.T, sessionID, tool string) exportedSpan {
	t.Helper()
	deadline := time.Now().Add(spanWait)
	for {
		var found *exportedSpan
		for _, sp := range readSpans(t) {
			if isToolCallSpan(sp) && sp.Attrs["mcp.session_id"] == sessionID && sp.Attrs["mcp.tool"] == tool {
				cp := sp
				found = &cp
			}
		}
		if found != nil {
			return *found
		}
		if time.Now().After(deadline) {
			t.Fatalf("no tools/call span for %s in session %s reached the collector within %s", tool, sessionID, spanWait)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// awaitAuditRow waits for the audit row of one call, read as the
// administrator: the row for tool, made by userID, in the transport session.
func awaitAuditRow(t *testing.T, admin *client, tool, userID, sessionID string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(spanWait)
	for {
		for _, row := range admin.list("/api/v1/admin/audit/events?tool_name=" + tool + "&user_id=" + userID + "&success=false&limit=50") {
			ev, _ := row.(map[string]any)
			if ev["session_id"] == sessionID {
				return ev
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no audit row for %s by %s in session %s within %s", tool, userID, sessionID, spanWait)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

var toolCallSeries = regexp.MustCompile(`(?m)^mcp_tool_calls_total\{([^}]*)\} (\d+)$`)

// toolCallsTotal sums mcp_tool_calls_total over every replica for one tool and
// status category.
func toolCallsTotal(t *testing.T, tool, status string) int {
	t.Helper()
	total := 0
	for _, m := range toolCallSeries.FindAllStringSubmatch(scrapeRaw(t), -1) {
		if strings.Contains(m[1], `tool="`+tool+`"`) && strings.Contains(m[1], `status_category="`+status+`"`) {
			n, _ := strconv.Atoi(m[2])
			total += n
		}
	}
	return total
}

// assertRefusalObserved asserts the three records one refused call leaves: the
// counter increment under status, the span with status as its category and as
// its error description, and the audit row naming category.
func assertRefusalObserved(t *testing.T, admin *client, sessionID, userID, tool, status, category string, before int) {
	t.Helper()
	if after := toolCallsTotal(t, tool, status); after != before+1 {
		t.Errorf("mcp_tool_calls_total{tool=%q,status_category=%q} = %d, want %d: the refusal is counted once", tool, status, after, before+1)
	}
	span := awaitSpan(t, sessionID, tool)
	if span.Attrs["status_category"] != status {
		t.Errorf("span status_category = %q, want %q", span.Attrs["status_category"], status)
	}
	if span.Status.Code != 2 || span.Status.Message != status {
		t.Errorf("span status = (%d, %q), want (ERROR, %q): the description is the bounded category", span.Status.Code, span.Status.Message, status)
	}
	row := awaitAuditRow(t, admin, tool, userID, sessionID)
	if row["error_category"] != category {
		t.Errorf("audit row error_category = %v, want %q (row %v)", row["error_category"], category, row)
	}
	if row["success"] != false {
		t.Errorf("audit row success = %v, want false", row["success"])
	}
}

// TestIssue1892_APersonaDeniedCallIsCountedTracedAndAudited: the analyst's
// persona does not allow list_connections. The refusal increments
// mcp_tool_calls_total under authz_err, produces a tool_call span with that
// category, and writes an audit row with error_category authorization_denied.
func TestIssue1892_APersonaDeniedCallIsCountedTracedAndAudited(t *testing.T) {
	admin := connect(t)
	analyst := connectBare(t, analystKey1892)
	before := toolCallsTotal(t, "list_connections", "authz_err")

	res, text := callBare(t, analyst, "list_connections", nil)
	if !res.IsError || !strings.Contains(text, "not authorized") {
		t.Fatalf("list_connections as the analyst should be refused by the persona; got error=%v %q", res.IsError, text)
	}
	assertRefusalObserved(t, admin, analyst.ID(), "apikey:analyst", "list_connections", "authz_err", "authorization_denied", before)
}

// TestIssue1892_ASearchRequiredRefusalIsCountedTracedAndAudited: the report
// viewer may run trino_query but may never call search, so the search-first
// gate refuses every trino_query. The refusal is counted under gate_err,
// traced, and audited with error_category search_required. platform_info first,
// for the session handle every gated call threads; once a handle is adopted,
// the call's session on the span and the audit row is the handle.
func TestIssue1892_ASearchRequiredRefusalIsCountedTracedAndAudited(t *testing.T) {
	admin := connect(t)
	reporter := connectBare(t, reporterKey1892)
	res, text := callBare(t, reporter, "platform_info", nil)
	if res.IsError {
		t.Fatalf("platform_info as the report viewer: %s", text)
	}
	var info map[string]any
	if err := json.Unmarshal([]byte(text), &info); err != nil {
		t.Fatalf("platform_info result: %v", err)
	}
	handle, _ := info["session_id"].(string)
	if handle == "" {
		t.Fatal("platform_info minted no session_id")
	}
	before := toolCallsTotal(t, "trino_query", "gate_err")

	res, text = callBare(t, reporter, "trino_query", map[string]any{
		"sql": "SELECT 1", "session_id": handle,
		"purpose": "The acceptance suite is exercising the search-first refusal.",
	})
	if !res.IsError || !strings.Contains(text, "SEARCH_REQUIRED") {
		t.Fatalf("trino_query before search should be refused SEARCH_REQUIRED; got error=%v %q", res.IsError, text)
	}
	assertRefusalObserved(t, admin, handle, "apikey:reporter", "trino_query", "gate_err", "search_required", before)
}

// TestIssue1892_AnUnauthenticatedCallIsRefusedAtTheHTTPGate: over HTTP a
// request with no credential is answered 401 by the HTTP auth gate before any
// JSON-RPC method is dispatched, so there is no tools/call for the tool-call
// observers to record and mcp_tool_calls_total does not move. The tools/call
// level authentication refusal exists on the stdio transport, which has no
// HTTP gate; TestEveryToolCallIsObservedOnce (pkg/platform) executes it
// through the assembled chain over the in-memory transport. The HTTP gate's
// own refusals are the inbound HTTP ticket's (#1889).
func TestIssue1892_AnUnauthenticatedCallIsRefusedAtTheHTTPGate(t *testing.T) {
	before := toolCallsTotal(t, "platform_info", "auth_err")
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"platform_info","arguments":{}}}`
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, baseURL(), strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", baseURL(), err)
	}
	defer resp.Body.Close() //nolint:errcheck // test
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("an unauthenticated tools/call over HTTP answered %d, want 401 from the HTTP auth gate", resp.StatusCode)
	}
	if resp.Header.Get("WWW-Authenticate") == "" {
		t.Error("the 401 carries no WWW-Authenticate challenge")
	}
	if after := toolCallsTotal(t, "platform_info", "auth_err"); after != before {
		t.Errorf("mcp_tool_calls_total{tool=platform_info,status_category=auth_err} moved %d -> %d; the HTTP gate refused before tools/call", before, after)
	}
}

// TestIssue1892_SpansCarryNoEmailAndNoUpstreamTextByDefault: with the dev
// stack's default settings (OTEL_TRACES_INCLUDE_USER_EMAIL unset) no exported
// span carries mcp.user_email, every error span's status description is one
// of the bounded categories, and the error a Trino failure recorded on its
// span has had the SQL's quoted names redacted.
func TestIssue1892_SpansCarryNoEmailAndNoUpstreamTextByDefault(t *testing.T) {
	admin := connect(t)
	res, text, err := admin.callRaw("trino_query", map[string]any{
		"sql":     "SELECT 'PLANTED_1892' FROM iceberg.default.no_such_table_1892",
		"purpose": "The acceptance suite is exercising what a failed query's span carries.",
	})
	if err != nil {
		t.Fatalf("trino_query: transport error: %v", err)
	}
	if res == nil || !res.IsError {
		t.Fatalf("trino_query against a missing table should fail; got %q", text)
	}
	for _, refusal := range []string{"SEARCH_REQUIRED", "SESSION_REQUIRED", "PURPOSE_REQUIRED", "RATE_LIMITED"} {
		if strings.Contains(text, refusal) {
			t.Fatalf("the query was refused before Trino (%s); the criterion needs an upstream failure: %q", refusal, text)
		}
	}
	// An administrator session threads its session handle, which is the
	// session the span and the audit row carry.
	span := awaitSpan(t, admin.sessionID, "trino_query")
	if span.Status.Code != 2 {
		t.Fatalf("the failed call's span is not an error span: %+v", span.Status)
	}
	// The Trino toolkit classifies a missing table as the caller's mistake
	// (validation_err) and a Trino outage as upstream_err (#2032); either way
	// the description is the category, never the upstream's text.
	if span.Status.Message != span.Attrs["status_category"] || (span.Status.Message != "validation_err" && span.Status.Message != "upstream_err") {
		t.Errorf("span status description = %q (status_category %q), want the bounded category, never the upstream's text", span.Status.Message, span.Attrs["status_category"])
	}
	if len(span.Events) == 0 {
		t.Error("the failed call's span recorded no error event")
	}
	for _, ev := range span.Events {
		msg := ev["exception.message"]
		t.Logf("recorded error: %q", msg)
		for _, planted := range []string{"PLANTED_1892", "no_such_table_1892"} {
			if strings.Contains(msg, planted) {
				t.Errorf("the recorded error carries %q, a name the SQL quoted: %q", planted, msg)
			}
		}
	}

	categories := map[string]bool{"auth_err": true, "authz_err": true, "gate_err": true, "declined": true, "validation_err": true, "upstream_err": true, "internal_err": true}
	spans := readSpans(t)
	if len(spans) == 0 {
		t.Fatal("the collector has received no spans")
	}
	for _, sp := range spans {
		if _, has := sp.Attrs["mcp.user_email"]; has {
			t.Fatalf("a span carries mcp.user_email with default settings: %v", sp.Attrs)
		}
		if isToolCallSpan(sp) && sp.Status.Code == 2 && !categories[sp.Status.Message] {
			t.Errorf("an error span's status description is %q, not a bounded category", sp.Status.Message)
		}
	}
	t.Logf("%d spans checked, none carries an address, every error description is a category", len(spans))
}

var identityLabel = regexp.MustCompile(`identity="([^"]*)"`)

// TestIssue1892_TheIdentityLabelCarriesNoAddress: the inbound REST gateway
// counter's identity label is the API key's name or oidc, never an address.
// The dev stack's warm-up drives the REST shim as the admin key, so the label
// is present; the criterion is that no value carries an @.
func TestIssue1892_TheIdentityLabelCarriesNoAddress(t *testing.T) {
	body := scrapeRaw(t)
	values := identityLabel.FindAllStringSubmatch(body, -1)
	if len(values) == 0 {
		t.Fatal("/metrics shows no identity label; the REST gateway counter is absent")
	}
	seen := map[string]bool{}
	for _, m := range values {
		seen[m[1]] = true
		if strings.Contains(m[1], "@") {
			t.Errorf("identity=%q carries an address", m[1])
		}
	}
	t.Logf("identity values on /metrics: %v", seen)
}
