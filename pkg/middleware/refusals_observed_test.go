package middleware_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// The values this file plants and then asserts never leave the platform on
// telemetry (#1892): a quoted literal an upstream error echoes, and the
// caller's address.
const (
	plantedLiteral = "ssn_PLANTED_LITERAL"
	plantedEmail   = "u1@planted.example.com"
)

// refusalErr is the upstream-shaped error the test tool returns: it quotes a
// value from the query and names the person, the way a Trino or S3 error can.
var errRefusal = errors.New("line 1:8: Column '" + plantedLiteral + "' cannot be resolved for " + plantedEmail)

type failingAuthn struct{ err error }

func (a *failingAuthn) Authenticate(context.Context) (*middleware.UserInfo, error) { return nil, a.err }

type denyingAuthz struct{ persona, reason string }

func (a *denyingAuthz) IsAuthorized(context.Context, string, []string, string, string) (allowed bool, persona, reason string) {
	return false, a.persona, a.reason
}

type missingLookup struct{}

func (missingLookup) GetToolkitForTool(string) registry.ToolkitMatch { return registry.ToolkitMatch{} }

// lockedBuffer is a log sink the test reads back after the calls.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p) //nolint:wrapcheck // bytes.Buffer.Write never fails
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// observedCall is what one tools/call left behind in the three observers.
type observedCall struct {
	span   sdktrace.ReadOnlySpan
	scrape string
	events []middleware.AuditEvent
	result *mcp.CallToolResult
	err    error
}

// refusalCase is one call to observe: who makes it, what admits it, and the
// gates inner to auth.
type refusalCase struct {
	authn  middleware.Authenticator
	authz  middleware.Authorizer
	lookup middleware.ToolkitLookup
	tool   string
	gates  []mcp.Middleware
}

// observe assembles tracing, metrics and audit OUTER to the auth middleware and
// the case's gates (the order pkg/platform registers, #1892), makes one call,
// and returns everything the observers recorded. Each observer is asserted to
// have seen exactly one call.
func observe(t *testing.T, c refusalCase) observedCall {
	t.Helper()
	authn, authz, lookup, tool, gates := c.authn, c.authz, c.lookup, c.tool, c.gates
	tr, sr := recorderTracer(t)
	m, err := observability.New(observability.Config{Enabled: true})
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })
	auditStore := &testAuditStore{}

	server := mcp.NewServer(&mcp.Implementation{Name: "refusals", Version: "v0"}, nil)
	server.AddTool(&mcp.Tool{Name: "trino_query", Description: "t", InputSchema: json.RawMessage(`{"type":"object","properties":{"sql":{"type":"string"}}}`)},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			r := &mcp.CallToolResult{}
			r.SetError(errRefusal)
			return r, nil
		})
	// Innermost first: the gates, then auth, then the three observers.
	for _, g := range gates {
		server.AddReceivingMiddleware(g)
	}
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(authn, authz, lookup, middleware.ToolCallConfig{Transport: "stdio", AdminPersona: "admin"}))
	server.AddReceivingMiddleware(middleware.MCPAuditMiddleware(auditStore))
	server.AddReceivingMiddleware(middleware.MCPMetricsMiddleware(m))
	server.AddReceivingMiddleware(middleware.MCPTracingMiddleware(tr))

	ctx := context.Background()
	sess := mustConnect(ctx, t, server)
	defer func() { _ = sess.Close() }()
	res, callErr := sess.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: map[string]any{"sql": "SELECT '" + plantedLiteral + "' FROM t"}})

	require.NoError(t, tr.Shutdown(ctx))
	spans := sr.Ended()
	require.Len(t, spans, 1, "exactly one tool_call span per call, refused or not")
	events := waitForAuditEvents(t, auditStore)
	require.Len(t, events, 1, "exactly one audit row per call, refused or not")
	return observedCall{span: spans[0], scrape: scrape(t, m.Handler()), events: events, result: res, err: callErr}
}

func spanAttr(s sdktrace.ReadOnlySpan, key string) string {
	for _, a := range s.Attributes() {
		if string(a.Key) == key {
			return a.Value.AsString()
		}
	}
	return ""
}

func exceptionMessages(s sdktrace.ReadOnlySpan) []string {
	var out []string
	for _, ev := range s.Events() {
		for _, a := range ev.Attributes {
			if a.Key == "exception.message" {
				out = append(out, a.Value.AsString())
			}
		}
	}
	return out
}

var (
	refusalUser   = &middleware.UserInfo{UserID: "u1", Email: plantedEmail, Roles: []string{"analyst"}, AuthType: middleware.AuthTypeAPIKey}
	refusalLookup = &fakeLookup{kind: "trino", name: "prod", conn: "primary"}
)

// TestRefusedCalls_AreCountedTracedAndAudited is the #1892 contract through
// the assembled chain: a call refused by authentication, by authorization, by
// the search-first gate or by the session gate produces one span, one
// mcp_tool_calls_total increment under its category, and one audit row naming
// the refusal. Before #1892 the observers sat inner to auth and the gates and
// a refusal reached none of them.
func TestRefusedCalls_AreCountedTracedAndAudited(t *testing.T) {
	cases := []struct {
		name     string
		authn    middleware.Authenticator
		authz    middleware.Authorizer
		gates    func() []mcp.Middleware
		status   string
		category string
		persona  string
	}{
		{
			name:     "authentication failed",
			authn:    &failingAuthn{err: errors.New("token expired: 'tok_PLANTED'")},
			authz:    &fakeAuthz{persona: "analyst"},
			status:   observability.StatusAuthErr,
			category: middleware.ErrCategoryAuth,
			persona:  "unknown",
		},
		{
			name:     "authorization denied",
			authn:    &fakeAuthn{user: refusalUser},
			authz:    &denyingAuthz{persona: "analyst", reason: "tool trino_query is not allowed for persona analyst"},
			status:   observability.StatusAuthzErr,
			category: middleware.ErrCategoryAuthz,
			persona:  "analyst",
		},
		{
			name:  "search-first gate",
			authn: &fakeAuthn{user: refusalUser},
			authz: &fakeAuthz{persona: "analyst"},
			gates: func() []mcp.Middleware {
				return []mcp.Middleware{middleware.MCPWorkflowGateMiddleware(middleware.NewSessionWorkflowTracker(nil, nil, nil, time.Minute))}
			},
			status:   observability.StatusGateErr,
			category: middleware.ErrCategorySearchRequired,
			persona:  "analyst",
		},
		{
			name:  "session gate",
			authn: &fakeAuthn{user: refusalUser},
			authz: &fakeAuthz{persona: "analyst"},
			gates: func() []mcp.Middleware {
				return []mcp.Middleware{middleware.MCPSessionGateMiddleware(middleware.NewSessionGate(middleware.SessionGateConfig{InitTool: "platform_info"}))}
			},
			status:   observability.StatusGateErr,
			category: middleware.ErrCategorySetupRequired,
			persona:  "analyst",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gates []mcp.Middleware
			if tc.gates != nil {
				gates = tc.gates()
			}
			got := observe(t, refusalCase{authn: tc.authn, authz: tc.authz, lookup: refusalLookup, tool: "trino_query", gates: gates})
			require.NoError(t, got.err)
			require.True(t, got.result.IsError, "the call is refused")

			status := spanAttr(got.span, "status_category")
			assert.Equal(t, tc.status, status, "span status_category")
			assert.Equal(t, "Error", got.span.Status().Code.String())
			assert.Equal(t, tc.status, got.span.Status().Description, "the status description is the bounded category")
			tool := spanAttr(got.span, "mcp.tool")
			assert.Equal(t, "trino_query", tool)

			want := `mcp_tool_calls_total{persona="` + tc.persona + `",source="mcp",status_category="` + tc.status + `",tool="trino_query",toolkit_kind="trino"} 1`
			assert.Contains(t, got.scrape, want)

			ev := got.events[0]
			assert.False(t, ev.Success)
			// A gate refuses a call authorization admitted; the row says so.
			assert.Equal(t, tc.status == observability.StatusGateErr, ev.Authorized)
			assert.Equal(t, tc.category, ev.ErrorCategory, "the audit row names the refusal")
			assert.Equal(t, "trino_query", ev.ToolName)
			assert.NotEmpty(t, ev.ErrorMessage)
		})
	}
}

// TestUnregisteredTool_RecordsOneLabel: a tools/call naming a tool no toolkit
// registers is counted under tool="unregistered", never under the name the
// caller sent, so a caller cannot mint a series per invented name (#1892).
func TestUnregisteredTool_RecordsOneLabel(t *testing.T) {
	got := observe(t, refusalCase{authn: &fakeAuthn{user: refusalUser}, authz: &fakeAuthz{persona: "analyst"}, lookup: missingLookup{}, tool: "made_up_tool_PLANTED"})
	assert.Contains(t, got.scrape, `tool="unregistered"`)
	assert.NotContains(t, got.scrape, "made_up_tool_PLANTED", "the caller's name is not a label value")
	assert.Equal(t, "made_up_tool_PLANTED", got.events[0].ToolName, "the audit row keeps the name")
}

// TestTelemetry_CarriesNoArgumentsSQLOrEmailByDefault is the redaction gate
// (#1892): with default settings no span attribute or event, no metric label
// and no log line carries the tool's arguments, the SQL literal the upstream
// error echoed, or the caller's address. The call here succeeds authentication
// and fails in the handler, which is the path that carries upstream text.
func TestTelemetry_CarriesNoArgumentsSQLOrEmailByDefault(t *testing.T) {
	var logs lockedBuffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	// A denied call too: its Warn log named the caller's address before #1892.
	denied := observe(t, refusalCase{authn: &fakeAuthn{user: refusalUser}, authz: &denyingAuthz{persona: "analyst", reason: "tool trino_query is not allowed for persona analyst"}, lookup: refusalLookup, tool: "trino_query"})
	got := observe(t, refusalCase{authn: &fakeAuthn{user: refusalUser}, authz: &fakeAuthz{persona: "analyst"}, lookup: refusalLookup, tool: "trino_query"})
	require.True(t, got.result.IsError)

	for _, c := range []observedCall{denied, got} {
		for _, a := range c.span.Attributes() {
			v := a.Value.AsString()
			assert.NotContains(t, v, plantedLiteral, "span attribute %s", a.Key)
			assert.NotContains(t, v, plantedEmail, "span attribute %s", a.Key)
			assert.NotEqual(t, "mcp.user_email", string(a.Key), "the address is off the span by default")
		}
		for _, msg := range exceptionMessages(c.span) {
			assert.NotContains(t, msg, plantedLiteral, "recorded error: %s", msg)
			assert.NotContains(t, msg, plantedEmail, "recorded error: %s", msg)
		}
		assert.NotContains(t, c.span.Status().Description, plantedLiteral)
		assert.NotContains(t, c.scrape, plantedLiteral)
		assert.NotContains(t, c.scrape, plantedEmail)
		assert.NotContains(t, c.scrape, "SELECT", "no tool argument reaches a label")
	}
	msgs := exceptionMessages(got.span)
	require.NotEmpty(t, msgs, "the handler error is recorded on the span, redacted")
	assert.True(t, strings.Contains(msgs[0], "'?'") && strings.Contains(msgs[0], "[email]"), "recorded as %q", msgs[0])

	logged := logs.String()
	assert.NotContains(t, logged, plantedEmail, "no log line carries the address with default settings")
	assert.NotContains(t, logged, plantedLiteral, "no log line carries the SQL literal")
	assert.Contains(t, logged, "tool call authorization denied", "the denial is still logged")
}

// The unused-import guard for tracetest, which recorderTracer in
// mcp_tracing_test.go returns.
var _ *tracetest.SpanRecorder

// TestNestedToolCall_HasItsOwnPlatformContext: a tool handler that makes a
// tools/call of its own on the same server (a managed script's host calls run
// inside manage_script this way) gets a context of its own. The outer call's
// audit row keeps its tool name and the inner call has one row under its; the
// observers seeded the outer context, and a context a call has claimed is
// never reused by a call nested inside it.
func TestNestedToolCall_HasItsOwnPlatformContext(t *testing.T) {
	tr, sr := recorderTracer(t)
	m, err := observability.New(observability.Config{Enabled: true})
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })
	auditStore := &testAuditStore{}

	server := mcp.NewServer(&mcp.Implementation{Name: "nested", Version: "v0"}, nil)
	server.AddTool(&mcp.Tool{Name: "inner_tool", Description: "i", InputSchema: json.RawMessage(`{"type":"object"}`)},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "inner"}}}, nil
		})
	server.AddTool(&mcp.Tool{Name: "outer_tool", Description: "o", InputSchema: json.RawMessage(`{"type":"object"}`)},
		func(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			// The nested call carries the outer call's context, as a script
			// run's in-process session does.
			inner := mustConnectCtx(ctx, t, server)
			defer func() { _ = inner.Close() }()
			if _, err := inner.CallTool(ctx, &mcp.CallToolParams{Name: "inner_tool"}); err != nil {
				return nil, fmt.Errorf("inner call: %w", err)
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "outer"}}}, nil
		})
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(&fakeAuthn{user: refusalUser}, &fakeAuthz{persona: "analyst"}, refusalLookup, middleware.ToolCallConfig{Transport: "stdio", AdminPersona: "admin"}))
	server.AddReceivingMiddleware(middleware.MCPAuditMiddleware(auditStore))
	server.AddReceivingMiddleware(middleware.MCPMetricsMiddleware(m))
	server.AddReceivingMiddleware(middleware.MCPTracingMiddleware(tr))

	ctx := context.Background()
	sess := mustConnect(ctx, t, server)
	defer func() { _ = sess.Close() }()
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "outer_tool"})
	require.NoError(t, err)
	require.False(t, res.IsError, "%v", res.Content)

	require.NoError(t, tr.Shutdown(ctx))
	events := waitForAuditEvents(t, auditStore)
	byTool := map[string]int{}
	for _, ev := range events {
		byTool[ev.ToolName]++
	}
	assert.Equal(t, map[string]int{"outer_tool": 1, "inner_tool": 1}, byTool, "one row per call, each under its own tool")
	spanTools := map[string]int{}
	for _, s := range sr.Ended() {
		spanTools[spanAttr(s, "mcp.tool")]++
	}
	assert.Equal(t, map[string]int{"outer_tool": 1, "inner_tool": 1}, spanTools, "one span per call, each under its own tool")
	body := scrape(t, m.Handler())
	assert.Contains(t, body, `tool="outer_tool"`)
	assert.Contains(t, body, `tool="inner_tool"`)
}

// mustConnectCtx is mustConnect over a caller-supplied context, so the server
// side of the nested session inherits the outer call's context values.
func mustConnectCtx(ctx context.Context, t *testing.T, server *mcp.Server) *mcp.ClientSession {
	t.Helper()
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, t1, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "nested", Version: "v0"}, nil)
	sess, err := client.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	return sess
}
