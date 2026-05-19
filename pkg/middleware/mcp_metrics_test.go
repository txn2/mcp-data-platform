package middleware_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// TestMCPMetricsMiddleware_IntegrationRecordsToolCall wires the real
// metrics middleware + tool-call middleware behind an in-memory MCP
// server, calls a tool, and asserts the scraped /metrics output
// contains the expected series with the expected bounded labels.
//
// This is the test the issue's Phase 1 acceptance criteria require:
// it proves the assembled system records metrics end-to-end, not
// just that the recorder works in isolation.
func TestMCPMetricsMiddleware_IntegrationRecordsToolCall(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true})
	if err != nil {
		t.Fatalf("observability.New: %v", err)
	}
	defer func() { _ = m.Shutdown(context.Background()) }()

	server := mcp.NewServer(&mcp.Implementation{Name: "metrics-test", Version: "v0.0.0"}, nil)
	server.AddTool(&mcp.Tool{
		Name:        "trino_query",
		Description: "test",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"sql":{"type":"string"}}}`),
	}, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
	})

	authenticator := &fakeAuthn{user: &middleware.UserInfo{UserID: "u1", Roles: []string{"analyst"}}}
	authorizer := &fakeAuthz{persona: "analyst"}
	lookup := &fakeLookup{kind: "trino", name: "prod", conn: "primary"}

	// Innermost first, outermost last. Metrics must be inner to ToolCall.
	server.AddReceivingMiddleware(middleware.MCPMetricsMiddleware(m))
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(
		authenticator, authorizer, lookup,
		middleware.ToolCallConfig{Transport: "stdio", AdminPersona: "admin"},
	))

	ctx := context.Background()
	sess := mustConnect(ctx, t, server)
	defer func() { _ = sess.Close() }()

	res, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name:      "trino_query",
		Arguments: map[string]any{"sql": "SELECT 1"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %v", res.Content)
	}

	body := scrape(t, m.Handler())
	wantSeries := []string{
		`mcp_tool_calls_total{persona="analyst",status_category="ok",tool="trino_query",toolkit_kind="trino"} 1`,
		`mcp_tool_call_duration_seconds_count{persona="analyst",status_category="ok",tool="trino_query",toolkit_kind="trino"} 1`,
		// Inflight returns to 0 after the call (1 inc + 1 dec).
		`mcp_inflight_tool_calls 0`,
	}
	for _, want := range wantSeries {
		if !strings.Contains(body, want) {
			t.Errorf("scrape missing series: %q\n--- body ---\n%s", want, body)
		}
	}
}

// TestMCPMetricsMiddleware_DisabledIsNoOp verifies that wiring the
// middleware with a nil recorder does not panic and does not interfere
// with normal tool execution. This is the path operators take by
// default (OTEL_METRICS_ENABLED unset).
func TestMCPMetricsMiddleware_DisabledIsNoOp(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "metrics-test-disabled", Version: "v0.0.0"}, nil)
	server.AddTool(&mcp.Tool{
		Name:        "trino_query",
		Description: "test",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	}, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
	})

	// Pass a nil recorder; the middleware must be safe.
	server.AddReceivingMiddleware(middleware.MCPMetricsMiddleware(nil))

	ctx := context.Background()
	sess := mustConnect(ctx, t, server)
	defer func() { _ = sess.Close() }()

	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "trino_query"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %v", res.Content)
	}
}

// TestMCPMetricsMiddleware_ToolErrorClassified verifies that a
// tool-level error (IsError=true) yields status_category=upstream_err
// when no category is attached, and the auth/authz/declined categories
// when the underlying error carries one.
func TestMCPMetricsMiddleware_ToolErrorClassified(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true})
	if err != nil {
		t.Fatalf("observability.New: %v", err)
	}
	defer func() { _ = m.Shutdown(context.Background()) }()

	server := mcp.NewServer(&mcp.Implementation{Name: "metrics-test-err", Version: "v0.0.0"}, nil)
	server.AddTool(&mcp.Tool{
		Name:        "tool_bare_err",
		Description: "returns plain tool error",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		r := &mcp.CallToolResult{}
		r.SetError(errors.New("upstream said no"))
		return r, nil
	})
	server.AddTool(&mcp.Tool{
		Name:        "tool_authz_err",
		Description: "returns categorized error",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		r := &mcp.CallToolResult{}
		r.SetError(&middleware.PlatformError{Category: middleware.ErrCategoryAuthz, Message: "nope"})
		return r, nil
	})

	authenticator := &fakeAuthn{user: &middleware.UserInfo{UserID: "u1", Roles: []string{"analyst"}}}
	authorizer := &fakeAuthz{persona: "analyst"}
	lookup := &fakeLookup{kind: "demo", name: "n", conn: "c"}

	server.AddReceivingMiddleware(middleware.MCPMetricsMiddleware(m))
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(
		authenticator, authorizer, lookup,
		middleware.ToolCallConfig{Transport: "stdio", AdminPersona: "admin"},
	))

	ctx := context.Background()
	sess := mustConnect(ctx, t, server)
	defer func() { _ = sess.Close() }()

	for _, name := range []string{"tool_bare_err", "tool_authz_err"} {
		if _, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: name}); err != nil {
			t.Fatalf("CallTool(%s): %v", name, err)
		}
	}

	body := scrape(t, m.Handler())
	wantSeries := []string{
		`mcp_tool_calls_total{persona="analyst",status_category="upstream_err",tool="tool_bare_err",toolkit_kind="demo"} 1`,
		`mcp_tool_calls_total{persona="analyst",status_category="authz_err",tool="tool_authz_err",toolkit_kind="demo"} 1`,
	}
	for _, want := range wantSeries {
		if !strings.Contains(body, want) {
			t.Errorf("scrape missing series: %q\n--- body ---\n%s", want, body)
		}
	}
}

// --- shared test helpers ---

func mustConnect(ctx context.Context, t *testing.T, server *mcp.Server) *mcp.ClientSession {
	t.Helper()
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, t1, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "tc", Version: "v0"}, nil)
	sess, err := client.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	return sess
}

func scrape(t *testing.T, h http.Handler) string {
	t.Helper()
	srv := httptest.NewServer(h)
	defer srv.Close()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, http.NoBody)
	if err != nil {
		t.Fatalf("scrape req: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("scrape: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort cleanup; body fully read above
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("scrape status %d, body=%s", resp.StatusCode, string(body))
	}
	return string(body)
}

type fakeAuthn struct{ user *middleware.UserInfo }

func (a *fakeAuthn) Authenticate(_ context.Context) (*middleware.UserInfo, error) {
	return a.user, nil
}

type fakeAuthz struct{ persona string }

func (a *fakeAuthz) IsAuthorized(_ context.Context, _ string, _ []string, _, _ string) (allowed bool, persona, reason string) {
	return true, a.persona, ""
}

type fakeLookup struct {
	kind, name, conn string
}

func (l *fakeLookup) GetToolkitForTool(_ string) registry.ToolkitMatch {
	return registry.ToolkitMatch{Kind: l.kind, Name: l.name, Connection: l.conn, Found: true}
}

var _ middleware.ToolkitLookup = (*fakeLookup)(nil)
