package platform

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/txn2/mcp-data-platform/pkg/auth"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// countingAuditLogger records every audit event the assembled chain hands it.
type countingAuditLogger struct {
	mu     sync.Mutex
	events []middleware.AuditEvent
}

func (l *countingAuditLogger) Log(_ context.Context, ev middleware.AuditEvent) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, ev)
	return nil
}

func (l *countingAuditLogger) byTool(tool string) []middleware.AuditEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []middleware.AuditEvent
	for _, ev := range l.events {
		if ev.ToolName == tool {
			out = append(out, ev)
		}
	}
	return out
}

const (
	coverageAdminKey  = "coverage-admin-key"
	coverageNobodyKey = "coverage-nobody-key"
)

// coverageToolkit is a mockToolkit whose RegisterTools adds real handlers.
type coverageToolkit struct{ mockToolkit }

func (*coverageToolkit) RegisterTools(s *mcp.Server) {
	s.AddTool(&mcp.Tool{Name: "coverage_ok", Description: "answers", InputSchema: json.RawMessage(`{"type":"object"}`)},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
		})
	s.AddTool(&mcp.Tool{Name: "coverage_fail", Description: "fails", InputSchema: json.RawMessage(`{"type":"object"}`)},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			r := &mcp.CallToolResult{}
			r.SetError(errors.New("upstream said no to 'x'"))
			return r, nil
		})
}

// observedPlatform is a real platform with metrics and tracing on, an
// in-memory span recorder on the tracer the platform installed, and an audit
// logger that keeps what it is handed.
type observedPlatform struct {
	p     *Platform
	spans *tracetest.SpanRecorder
	audit *countingAuditLogger
}

// newObservedPlatform assembles the platform the way cmd does, from the
// environment, with API-key auth (so a missing or wrong key is refused), the
// session gate, the search-first gate and a rate limit of rpm requests a
// minute. It is not parallel-safe: the tracer is the process global.
func newObservedPlatform(t *testing.T, rpm int) *observedPlatform {
	t.Helper()
	t.Setenv("OTEL_METRICS_ENABLED", "true")
	t.Setenv("OTEL_METRICS_ADDR", "127.0.0.1:0")
	t.Setenv("OTEL_TRACES_ENABLED", "true")
	// A closed port: the exporter connects lazily and drops what it cannot
	// deliver, and the recorder below is what the test reads.
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "127.0.0.1:1")
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "1")

	on := true
	cfg := &Config{
		Server:   ServerConfig{Name: "observed"},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
		Auth: AuthConfig{APIKeys: APIKeyAuthConfig{Enabled: true, Keys: []APIKeyDef{
			{Key: coverageAdminKey, Name: "admin", Roles: []string{testRoleAdmin}},
			{Key: coverageNobodyKey, Name: "nobody", Roles: []string{"nobody"}},
		}}},
		Personas: PersonasConfig{Definitions: map[string]PersonaDef{
			"admin": {DisplayName: "Admin", Roles: []string{testRoleAdmin}, Tools: ToolRulesDef{Allow: []string{"*"}}, Connections: ConnectionRulesDef{Allow: []string{"*"}}},
		}},
		Audit:       AuditConfig{Enabled: &on, LogToolCalls: &on},
		SessionGate: SessionGateConfig{Enabled: true, InitTool: "platform_info"},
		RateLimit:   RateLimitConfig{RequestsPerMinute: rpm, Burst: rpm},
	}
	// A toolkit with handlers of its own, so the sweep below covers tools the
	// registry knows beside the platform's three, one that succeeds and one
	// that fails in its handler.
	reg := registry.NewRegistry()
	require.NoError(t, reg.Register(&coverageToolkit{mockToolkit{kind: "coverage", name: "cov", connection: "c", tools: []string{"coverage_ok", "coverage_fail"}}}))
	audit := &countingAuditLogger{}
	p, err := New(WithConfig(cfg), WithAuditLogger(audit), WithToolkitRegistry(reg))
	require.NoError(t, err)
	tp, ok := otel.GetTracerProvider().(*sdktrace.TracerProvider)
	require.True(t, ok, "the platform installed the SDK tracer provider as the global")
	sr := tracetest.NewSpanRecorder()
	tp.RegisterSpanProcessor(sr)
	require.NoError(t, p.Start(context.Background()))
	t.Cleanup(func() { _ = p.Stop(context.Background()) })
	return &observedPlatform{p: p, spans: sr, audit: audit}
}

// session opens an in-memory client session carrying key as its credential
// (an empty key carries none).
func (o *observedPlatform) session(t *testing.T, key string) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	if key != "" {
		ctx = auth.WithToken(ctx, key)
	}
	t1, t2 := mcp.NewInMemoryTransports()
	ss, err := o.p.MCPServer().Connect(ctx, t1, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v0"}, nil).Connect(ctx, t2, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// call makes one tools/call and returns its result; a protocol-level error is
// returned as a nil result. Bounded, so a tool that waits on something the
// test platform does not have cannot hang the gate.
func call(t *testing.T, cs *mcp.ClientSession, tool string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if args == nil {
		args = map[string]any{}
	}
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Logf("%s: protocol error: %v", tool, err)
		return nil
	}
	return res
}

func (o *observedPlatform) spansFor(tool string) []sdktrace.ReadOnlySpan {
	var out []sdktrace.ReadOnlySpan
	for _, s := range o.spans.Ended() {
		for _, a := range s.Attributes() {
			if string(a.Key) == "mcp.tool" && a.Value.AsString() == tool {
				out = append(out, s)
			}
		}
	}
	return out
}

func spanStatusCategory(s sdktrace.ReadOnlySpan) string {
	for _, a := range s.Attributes() {
		if string(a.Key) == "status_category" {
			return a.Value.AsString()
		}
	}
	return ""
}

var seriesLine = regexp.MustCompile(`^mcp_tool_calls_total\{([^}]*)\} (\d+)$`)

// toolCallCounts scrapes /metrics and returns the mcp_tool_calls_total value
// per (tool, status_category).
func (o *observedPlatform) toolCallCounts(t *testing.T) map[string]map[string]int {
	t.Helper()
	srv := httptest.NewServer(o.p.Metrics().Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL) //nolint:noctx // test scrape
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck // test
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	out := map[string]map[string]int{}
	for line := range strings.SplitSeq(string(body), "\n") {
		m := seriesLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		var tool, status string
		for kv := range strings.SplitSeq(m[1], ",") {
			k, v, _ := strings.Cut(kv, "=")
			v = strings.Trim(v, `"`)
			switch k {
			case "tool":
				tool = v
			case "status_category":
				status = v
			}
		}
		if out[tool] == nil {
			out[tool] = map[string]int{}
		}
		n := 0
		for _, c := range m[2] {
			n = n*10 + int(c-'0')
		}
		out[tool][status] += n
	}
	return out
}

// assertObservedOnce asserts the one call of tool that ended under status
// left exactly one span, one counter increment and one audit row, the row's
// error category being category (empty for a success). A bare platform has
// three tools of its own, so one tool serves more than one scenario here.
func (o *observedPlatform) assertObservedOnce(t *testing.T, tool, status, category string) {
	t.Helper()
	var spans []sdktrace.ReadOnlySpan
	for _, s := range o.spansFor(tool) {
		if spanStatusCategory(s) == status {
			spans = append(spans, s)
		}
	}
	require.Len(t, spans, 1, "%s: one tool_call span under %s", tool, status)
	counts := o.toolCallCounts(t)[tool]
	assert.Equal(t, 1, counts[status], "%s: one increment under %s, got %v", tool, status, counts)
	var events []middleware.AuditEvent
	for _, ev := range o.audit.byTool(tool) {
		if ev.ErrorCategory == category {
			events = append(events, ev)
		}
	}
	require.Len(t, events, 1, "%s: one audit row with error_category %q", tool, category)
	assert.Equal(t, status == observability.StatusOK, events[0].Success, "%s: audit success", tool)
}

// TestEveryToolCallIsObservedOnce is the #1892 tool-coverage gate through the
// real assembled platform: every tool the platform registers is called once
// through the full receiving-middleware chain, and so is one refusal of each
// kind (no credential, a persona that allows nothing, a missing session
// handle, the search-first gate; the session gate and the rate limit are the
// tests beside this one and in pkg/middleware). Each leaves exactly one tool_call span, exactly one
// mcp_tool_calls_total increment under its status category, and exactly one
// audit row. Before #1892 a refused call left none of the three.
func TestEveryToolCallIsObservedOnce(t *testing.T) {
	o := newObservedPlatform(t, 1000)

	// A gated tool before platform_info, on a session holding no session
	// handle: the session-handle resolver refuses it (SESSION_REQUIRED), the
	// first of the gates a call meets.
	gated := o.session(t, coverageAdminKey)
	res := call(t, gated, "list_connections", nil)
	require.NotNil(t, res)
	require.True(t, res.IsError, "list_connections before platform_info is refused")
	o.assertObservedOnce(t, "list_connections", observability.StatusGateErr, middleware.ErrCategorySessionRequired)

	// No credential at all.
	anon := o.session(t, "")
	res = call(t, anon, "platform_find_tools", nil)
	require.NotNil(t, res)
	require.True(t, res.IsError)
	o.assertObservedOnce(t, "platform_find_tools", observability.StatusAuthErr, middleware.ErrCategoryAuth)

	// A credential no persona admits.
	nobody := o.session(t, coverageNobodyKey)
	res = call(t, nobody, "platform_info", nil)
	require.NotNil(t, res)
	require.True(t, res.IsError)
	o.assertObservedOnce(t, "platform_info", observability.StatusAuthzErr, middleware.ErrCategoryAuthz)

	// The administrator: platform_info opens the session gate, then a query
	// tool before search is refused by the search-first gate. trino_query is
	// gated by name and no Trino toolkit is configured, so the refusal is the
	// gate's, not a handler's.
	admin := o.session(t, coverageAdminKey)
	res = call(t, admin, "platform_info", nil)
	require.NotNil(t, res)
	require.False(t, res.IsError, "platform_info: %v", res.Content)
	o.assertObservedOnce(t, "platform_info", observability.StatusOK, "")
	res = call(t, admin, "trino_query", map[string]any{"sql": "SELECT 1"})
	require.NotNil(t, res)
	require.True(t, res.IsError)
	// No Trino toolkit is configured, so the name is one nothing registers:
	// the span carries it, the counter records the one unregistered label.
	spans := o.spansFor("trino_query")
	require.Len(t, spans, 1)
	assert.Equal(t, observability.StatusGateErr, spanStatusCategory(spans[0]))
	assert.Equal(t, 1, o.toolCallCounts(t)[observability.ToolLabelUnregistered][observability.StatusGateErr])
	require.Len(t, o.audit.byTool("trino_query"), 1)
	assert.Equal(t, middleware.ErrCategorySearchRequired, o.audit.byTool("trino_query")[0].ErrorCategory)

	// Every registered tool, once, as the administrator. The outcome is the
	// tool's business (most refuse empty arguments or a missing database); the
	// gate is that each one leaves exactly one of each record.
	listed, err := admin.ListTools(context.Background(), &mcp.ListToolsParams{})
	require.NoError(t, err)
	require.NotEmpty(t, listed.Tools)
	already := map[string]bool{"list_connections": true, "platform_find_tools": true, "platform_info": true, "trino_query": true}
	called := 0
	for _, tool := range listed.Tools {
		if already[tool.Name] {
			continue
		}
		call(t, admin, tool.Name, nil)
		called++
		spans := o.spansFor(tool.Name)
		require.Len(t, spans, 1, "%s: one tool_call span", tool.Name)
		counts := o.toolCallCounts(t)[tool.Name]
		total := 0
		for _, n := range counts {
			total += n
		}
		assert.Equal(t, 1, total, "%s: one mcp_tool_calls_total increment, got %v", tool.Name, counts)
		assert.Equal(t, spanStatusCategory(spans[0]), singleKey(counts), "%s: span and counter agree on the category", tool.Name)
		require.Len(t, o.audit.byTool(tool.Name), 1, "%s: one audit row", tool.Name)
	}
	require.GreaterOrEqual(t, called, 2, "the sweep covered the coverage toolkit's tools beside the platform's own")
	assert.Equal(t, observability.StatusOK, spanStatusCategory(o.spansFor("coverage_ok")[0]))
	assert.Equal(t, observability.StatusUpstreamErr, spanStatusCategory(o.spansFor("coverage_fail")[0]))
	t.Logf("called %d registered tools", called+len(already))
}

// TestRateLimitedCallIsObserved: the per-user rate limiter's refusal is
// counted, traced and audited like every other refusal (#1892). A platform of
// its own, with a limit of one call a minute. platform_info is exempt from the
// limit (it opens the session gate), so list_connections spends the one token
// and platform_find_tools is the call the limiter refuses.
func TestRateLimitedCallIsObserved(t *testing.T) {
	o := newObservedPlatform(t, 1)
	admin := o.session(t, coverageAdminKey)
	res := call(t, admin, "platform_info", nil)
	require.NotNil(t, res)
	require.False(t, res.IsError, "platform_info: %v", res.Content)
	res = call(t, admin, "list_connections", nil)
	require.NotNil(t, res)
	require.False(t, res.IsError, "the first metered call is admitted: %v", res.Content)
	res = call(t, admin, "platform_find_tools", nil)
	require.NotNil(t, res)
	require.True(t, res.IsError, "the second metered call within the minute is refused")
	o.assertObservedOnce(t, "platform_find_tools", observability.StatusGateErr, observability.CategoryRateLimited)
}

func singleKey(m map[string]int) string {
	for k := range m {
		return k
	}
	return ""
}
