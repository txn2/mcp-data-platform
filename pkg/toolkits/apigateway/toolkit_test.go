package apigateway

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/connoauth"
)

func TestNew_DefaultsToKindWhenNameEmpty(t *testing.T) {
	tk := New("")
	if tk.Name() != Kind {
		t.Errorf("Name() = %q; want %q", tk.Name(), Kind)
	}
}

func TestNew_KeepsExplicitName(t *testing.T) {
	tk := New("custom")
	if tk.Name() != "custom" {
		t.Errorf("Name() = %q; want %q", tk.Name(), "custom")
	}
}

func TestKind_IsAPI(t *testing.T) {
	tk := New("x")
	if tk.Kind() != "api" {
		t.Errorf("Kind() = %q; want %q", tk.Kind(), "api")
	}
}

func TestNewMulti_LoadsValidConnections(t *testing.T) {
	mc, err := ParseMultiConfig("default", map[string]map[string]any{
		"alpha": {"base_url": "https://a.example.com"},
		"beta":  {"base_url": "https://b.example.com", "auth_mode": AuthModeBearer, "credential": "tok"},
	})
	if err != nil {
		t.Fatalf("ParseMultiConfig: %v", err)
	}
	tk := NewMulti(mc)
	if !tk.HasConnection("alpha") || !tk.HasConnection("beta") {
		t.Errorf("connections missing: alpha=%v beta=%v", tk.HasConnection("alpha"), tk.HasConnection("beta"))
	}
	if tk.Connection() != "default" {
		t.Errorf("Connection() = %q; want %q", tk.Connection(), "default")
	}
}

func TestNewMulti_SkipsBadConnection(t *testing.T) {
	// Force a per-connection materialization failure by feeding an
	// already-validated Config that NewAuthenticator will reject. We
	// build the MultiConfig manually rather than via ParseMultiConfig
	// (which would catch this at parse time) to exercise the
	// addParsedConnection error path.
	prevLogger := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prevLogger) })
	var buf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))

	good := Config{
		BaseURL: "https://good.example.com", AuthMode: AuthModeNone,
		ConnectTimeout: time.Second, CallTimeout: time.Second, MaxResponseBytes: 1024,
		TrustLevel: TrustLevelUntrusted,
	}
	bad := Config{
		BaseURL: "https://bad.example.com", AuthMode: "weird",
		ConnectTimeout: time.Second, CallTimeout: time.Second, MaxResponseBytes: 1024,
		TrustLevel: TrustLevelUntrusted,
	}
	tk := NewMulti(MultiConfig{
		Instances: map[string]Config{"good": good, "bad": bad},
	})
	if !tk.HasConnection("good") {
		t.Error("good connection missing")
	}
	if tk.HasConnection("bad") {
		t.Error("bad connection should have been skipped")
	}
}

func TestNewMulti_DefaultNameFallsBackToKind(t *testing.T) {
	tk := NewMulti(MultiConfig{})
	if tk.Name() != Kind {
		t.Errorf("Name() = %q; want %q", tk.Name(), Kind)
	}
}

func TestAddConnection_RejectsDuplicates(t *testing.T) {
	tk := New("test")
	cfg := map[string]any{"base_url": "https://x"}
	if err := tk.AddConnection("a", cfg); err != nil {
		t.Fatalf("first AddConnection: %v", err)
	}
	if err := tk.AddConnection("a", cfg); !errors.Is(err, ErrConnectionExists) {
		t.Errorf("second AddConnection: got %v; want ErrConnectionExists", err)
	}
}

func TestAddConnection_RejectsBadConfig(t *testing.T) {
	tk := New("test")
	if err := tk.AddConnection("a", map[string]any{"auth_mode": AuthModeBearer}); err == nil {
		t.Error("bad config accepted")
	}
}

func TestAddConnection_SetsConnectionNameDefault(t *testing.T) {
	tk := New("test")
	if err := tk.AddConnection("named", map[string]any{"base_url": "https://x"}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	conns := tk.ListConnections()
	if len(conns) != 1 || conns[0].Name != "named" {
		t.Errorf("ListConnections = %#v", conns)
	}
}

func TestRemoveConnection(t *testing.T) {
	tk := New("test")
	_ = tk.AddConnection("a", map[string]any{"base_url": "https://x"})
	if err := tk.RemoveConnection("a"); err != nil {
		t.Fatalf("RemoveConnection: %v", err)
	}
	if tk.HasConnection("a") {
		t.Error("HasConnection still true after Remove")
	}
	if err := tk.RemoveConnection("ghost"); !errors.Is(err, ErrConnectionNotFound) {
		t.Errorf("RemoveConnection(ghost) = %v; want ErrConnectionNotFound", err)
	}
}

func TestListConnections_SortedByName(t *testing.T) {
	tk := New("test")
	for _, name := range []string{"zeta", "alpha", "mu"} {
		if err := tk.AddConnection(name, map[string]any{"base_url": "https://x"}); err != nil {
			t.Fatalf("AddConnection(%s): %v", name, err)
		}
	}
	conns := tk.ListConnections()
	if len(conns) != 3 {
		t.Fatalf("len = %d", len(conns))
	}
	if conns[0].Name != "alpha" || conns[1].Name != "mu" || conns[2].Name != "zeta" {
		t.Errorf("not sorted: %+v", conns)
	}
}

func TestListConnections_FlagsDefault(t *testing.T) {
	tk := NewMulti(MultiConfig{
		DefaultName: "primary",
		Instances: map[string]Config{
			"primary": {BaseURL: "https://x", AuthMode: AuthModeNone, ConnectTimeout: time.Second, CallTimeout: time.Second, MaxResponseBytes: 1024, TrustLevel: TrustLevelUntrusted, ConnectionName: "primary"},
			"backup":  {BaseURL: "https://y", AuthMode: AuthModeNone, ConnectTimeout: time.Second, CallTimeout: time.Second, MaxResponseBytes: 1024, TrustLevel: TrustLevelUntrusted, ConnectionName: "backup"},
		},
	})
	conns := tk.ListConnections()
	for _, c := range conns {
		if c.Name == "primary" && !c.IsDefault {
			t.Error("primary not flagged as default")
		}
		if c.Name == "backup" && c.IsDefault {
			t.Error("backup flagged as default")
		}
	}
}

func TestSetSemanticAndQueryProvider_NoOp(_ *testing.T) {
	tk := New("test")
	// v1 stores both providers without consuming them; nil is the only
	// case worth exercising here. Real provider integration is covered
	// once response-shaping (issue #373) lands.
	tk.SetSemanticProvider(nil)
	tk.SetQueryProvider(nil)
}

func TestClose_TolerantOfClosedConnections(t *testing.T) {
	tk := New("test")
	if err := tk.AddConnection("a", map[string]any{"base_url": "https://x"}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	if err := tk.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestTools_NamesInvokeAndListEndpoints(t *testing.T) {
	tk := New("test")
	tools := tk.Tools()
	want := []string{ToolInvokeEndpoint, ToolListEndpoints, ToolGetEndpointSchema}
	if len(tools) != len(want) {
		t.Fatalf("Tools() = %v; want %v", tools, want)
	}
	for i, got := range tools {
		if got != want[i] {
			t.Errorf("Tools()[%d] = %q; want %q", i, got, want[i])
		}
	}
}

// handleInvoke is the only entry point exercised by the MCP server.
// The tests here exercise it directly because spinning up a full
// mcp.Server inside a unit test is heavy; the integration test in
// TestRegisterTools_AgainstRealServer covers the wire-up path.
func TestHandleInvoke_RejectsMissingConnectionArg(t *testing.T) {
	tk := New("test")
	res, _, err := tk.handleInvoke(context.Background(), nil, InvokeInput{})
	if err != nil {
		t.Fatalf("handleInvoke: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError=true for missing connection")
	}
}

func TestHandleInvoke_ReportsUnknownConnection(t *testing.T) {
	tk := New("test")
	res, _, err := tk.handleInvoke(context.Background(), nil, InvokeInput{
		Connection: "ghost", Method: "GET", Path: "/",
	})
	if err != nil {
		t.Fatalf("handleInvoke: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError=true for unknown connection")
	}
}

func TestHandleInvoke_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()

	tk := New("test")
	if err := tk.AddConnection("c1", map[string]any{"base_url": srv.URL}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	res, out, err := tk.handleInvoke(context.Background(), nil, InvokeInput{
		Connection: "c1", Method: "GET", Path: "/",
	})
	if err != nil {
		t.Fatalf("handleInvoke: %v", err)
	}
	if res.IsError {
		t.Errorf("IsError=true: %s", textContent(res))
	}
	o, ok := out.(InvokeOutput)
	if !ok {
		t.Fatalf("out type = %T", out)
	}
	if o.Status != 200 {
		t.Errorf("status = %d", o.Status)
	}
}

func TestHandleInvoke_BadInputProducesToolError(t *testing.T) {
	tk := New("test")
	if err := tk.AddConnection("c1", map[string]any{"base_url": "https://x"}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	res, _, err := tk.handleInvoke(context.Background(), nil, InvokeInput{
		Connection: "c1", Method: "PURGE", Path: "/",
	})
	if err != nil {
		t.Fatalf("handleInvoke unexpected go error: %v", err)
	}
	if !res.IsError {
		t.Error("invalid method did not produce IsError=true")
	}
}

// stubRoutePolicy lets tests assert handleInvoke's gating without
// depending on the persona package.
type stubRoutePolicy struct {
	calls   int
	allowed bool
	reason  string
	gotConn string
	gotMeth string
	gotPath string
}

func (s *stubRoutePolicy) Allow(_ context.Context, conn, method, path string) (allowed bool, reason string) {
	s.calls++
	s.gotConn = conn
	s.gotMeth = method
	s.gotPath = path
	return s.allowed, s.reason
}

func TestHandleInvoke_RoutePolicyDeniesBeforeInvoke(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("upstream was contacted but route policy should have denied the call")
	}))
	defer srv.Close()

	tk := New("test")
	if err := tk.AddConnection("c1", map[string]any{"base_url": srv.URL}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	pol := &stubRoutePolicy{allowed: false, reason: "DELETE not allowed"}
	tk.SetRoutePolicy(pol)

	res, _, err := tk.handleInvoke(context.Background(), nil, InvokeInput{
		Connection: "c1", Method: "DELETE", Path: "/v1/users/123",
	})
	if err != nil {
		t.Fatalf("handleInvoke unexpected go error: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected IsError=true on policy denial; got %s", textContent(res))
	}
	if pol.calls != 1 {
		t.Errorf("policy called %d times; want 1", pol.calls)
	}
	if pol.gotMeth != "DELETE" || pol.gotConn != "c1" || pol.gotPath != "/v1/users/123" {
		t.Errorf("policy received wrong args: conn=%q method=%q path=%q", pol.gotConn, pol.gotMeth, pol.gotPath)
	}
	if !strings.Contains(textContent(res), "DELETE not allowed") {
		t.Errorf("denial reason missing from result: %s", textContent(res))
	}
}

func TestHandleInvoke_RoutePolicyAllowsThenInvokes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()

	tk := New("test")
	if err := tk.AddConnection("c1", map[string]any{"base_url": srv.URL}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	pol := &stubRoutePolicy{allowed: true}
	tk.SetRoutePolicy(pol)

	res, _, err := tk.handleInvoke(context.Background(), nil, InvokeInput{
		Connection: "c1", Method: "GET", Path: "/v1/users",
	})
	if err != nil {
		t.Fatalf("handleInvoke: %v", err)
	}
	if res.IsError {
		t.Errorf("policy allowed but result is error: %s", textContent(res))
	}
	if pol.calls != 1 {
		t.Errorf("policy not consulted; calls=%d", pol.calls)
	}
}

// Methods are normalized (uppercased) BEFORE the policy sees them so
// persona configs can use the conventional uppercase form regardless
// of how the model formats the input.
func TestHandleInvoke_RoutePolicyReceivesNormalizedMethod(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tk := New("test")
	if err := tk.AddConnection("c1", map[string]any{"base_url": srv.URL}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	pol := &stubRoutePolicy{allowed: true}
	tk.SetRoutePolicy(pol)

	_, _, err := tk.handleInvoke(context.Background(), nil, InvokeInput{
		Connection: "c1", Method: "get", Path: "/v1/users",
	})
	if err != nil {
		t.Fatalf("handleInvoke: %v", err)
	}
	if pol.gotMeth != "GET" {
		t.Errorf("policy saw method=%q; want %q (normalized)", pol.gotMeth, "GET")
	}
}

func TestHandleInvoke_NoPolicyMeansPassthrough(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tk := New("test")
	if err := tk.AddConnection("c1", map[string]any{"base_url": srv.URL}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	// no SetRoutePolicy → policy is nil, the call should proceed.

	res, _, err := tk.handleInvoke(context.Background(), nil, InvokeInput{
		Connection: "c1", Method: "GET", Path: "/v1/users",
	})
	if err != nil {
		t.Fatalf("handleInvoke: %v", err)
	}
	if res.IsError {
		t.Errorf("nil policy should not block call: %s", textContent(res))
	}
}

// Validation errors (unsupported method, malformed path) surface BEFORE
// the policy is consulted, so the persona doesn't have to validate
// input shape.
func TestHandleInvoke_RoutePolicyNotConsultedOnInvalidMethod(t *testing.T) {
	tk := New("test")
	if err := tk.AddConnection("c1", map[string]any{"base_url": "https://x"}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	pol := &stubRoutePolicy{allowed: true}
	tk.SetRoutePolicy(pol)

	res, _, err := tk.handleInvoke(context.Background(), nil, InvokeInput{
		Connection: "c1", Method: "PURGE", Path: "/foo",
	})
	if err != nil {
		t.Fatalf("handleInvoke unexpected go error: %v", err)
	}
	if !res.IsError {
		t.Error("invalid method should produce IsError")
	}
	if pol.calls != 0 {
		t.Errorf("policy was consulted on invalid method (calls=%d)", pol.calls)
	}
}

// Path validation runs before the policy is consulted (mirrors the
// invalid-method case). Path with a forbidden character ("@" triggers
// the SSRF guard) should error without invoking the policy.
func TestHandleInvoke_RoutePolicyNotConsultedOnInvalidPath(t *testing.T) {
	tk := New("test")
	if err := tk.AddConnection("c1", map[string]any{"base_url": "https://x"}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	pol := &stubRoutePolicy{allowed: true}
	tk.SetRoutePolicy(pol)

	res, _, err := tk.handleInvoke(context.Background(), nil, InvokeInput{
		Connection: "c1", Method: "GET", Path: "/@evil/foo",
	})
	if err != nil {
		t.Fatalf("handleInvoke unexpected go error: %v", err)
	}
	if !res.IsError {
		t.Error("invalid path should produce IsError")
	}
	if pol.calls != 0 {
		t.Errorf("policy was consulted on invalid path (calls=%d)", pol.calls)
	}
}

func TestHandleListEndpoints_RejectsMissingConnection(t *testing.T) {
	tk := New("test")
	res, _, err := tk.handleListEndpoints(context.Background(), nil, ListEndpointsInput{})
	if err != nil {
		t.Fatalf("handleListEndpoints: %v", err)
	}
	if !res.IsError {
		t.Error("missing connection should produce IsError")
	}
}

func TestHandleListEndpoints_UnknownConnection(t *testing.T) {
	tk := New("test")
	res, _, err := tk.handleListEndpoints(context.Background(), nil, ListEndpointsInput{
		Connection: "ghost",
	})
	if err != nil {
		t.Fatalf("handleListEndpoints: %v", err)
	}
	if !res.IsError {
		t.Error("unknown connection should produce IsError")
	}
}

func TestHandleListEndpoints_NoSpec_ReturnsEmptyWithNote(t *testing.T) {
	tk := New("test")
	if err := tk.AddConnection("c1", map[string]any{"base_url": "https://x"}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	res, out, err := tk.handleListEndpoints(context.Background(), nil, ListEndpointsInput{
		Connection: "c1",
	})
	if err != nil {
		t.Fatalf("handleListEndpoints: %v", err)
	}
	if res.IsError {
		t.Errorf("connection without spec should NOT be an error: %s", textContent(res))
	}
	o, ok := out.(ListEndpointsOutput)
	if !ok {
		t.Fatalf("out type %T", out)
	}
	if len(o.Operations) != 0 {
		t.Errorf("expected empty operations, got %v", o.Operations)
	}
	if o.Note == "" {
		t.Error("expected non-empty Note for spec-less connection")
	}
}

func TestHandleListEndpoints_ReturnsOperations(t *testing.T) {
	tk := New("test")
	setupCatalogWithSpec(t, tk, "petstore", "default", validMinimalSpec)
	if err := tk.AddConnection("c1", map[string]any{
		"base_url":   "https://x",
		"catalog_id": "petstore",
	}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	res, out, err := tk.handleListEndpoints(context.Background(), nil, ListEndpointsInput{
		Connection: "c1",
	})
	if err != nil {
		t.Fatalf("handleListEndpoints: %v", err)
	}
	if res.IsError {
		t.Errorf("expected success, got error: %s", textContent(res))
	}
	o, _ := out.(ListEndpointsOutput)
	if len(o.Operations) != 5 {
		t.Errorf("expected 5 operations, got %d", len(o.Operations))
	}
}

func TestHandleListEndpoints_FiltersByQuery(t *testing.T) {
	tk := New("test")
	setupCatalogWithSpec(t, tk, "petstore", "default", validMinimalSpec)
	if err := tk.AddConnection("c1", map[string]any{
		"base_url":   "https://x",
		"catalog_id": "petstore",
	}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	_, out, _ := tk.handleListEndpoints(context.Background(), nil, ListEndpointsInput{
		Connection: "c1",
		Query:      "orders",
	})
	o, _ := out.(ListEndpointsOutput)
	if len(o.Operations) != 1 {
		t.Errorf("expected 1 match for 'orders', got %d", len(o.Operations))
	}
}

// TestHandleListEndpoints_FiltersByRoutePolicy proves the model
// only sees operations it could actually invoke. Without this
// filter the catalog leaks the existence of denied endpoints —
// information disclosure plus a wasted-turn UX.
func TestHandleListEndpoints_FiltersByRoutePolicy(t *testing.T) {
	tk := New("test")
	setupCatalogWithSpec(t, tk, "petstore", "default", validMinimalSpec)
	if err := tk.AddConnection("c1", map[string]any{
		"base_url":   "https://x",
		"catalog_id": "petstore",
	}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	// Policy: only GET allowed; DELETE/POST denied.
	tk.SetRoutePolicy(routePolicyFunc(func(_ context.Context, _, method, _ string) (bool, string) {
		if method == "GET" {
			return true, ""
		}
		return false, "method not allowed"
	}))
	_, out, err := tk.handleListEndpoints(context.Background(), nil, ListEndpointsInput{
		Connection: "c1",
	})
	if err != nil {
		t.Fatalf("handleListEndpoints: %v", err)
	}
	o, _ := out.(ListEndpointsOutput)
	for _, op := range o.Operations {
		if op.Method != "GET" {
			t.Errorf("policy-filtered list returned %s %s; only GET should appear", op.Method, op.Path)
		}
	}
	// /v1/orders GET, /v1/users GET, /v1/users/{id} GET = 3 ops.
	if len(o.Operations) != 3 {
		t.Errorf("expected 3 GET operations, got %d", len(o.Operations))
	}
}

// routePolicyFunc adapts an inline closure to the RoutePolicy
// interface for tests that don't need the full stubRoutePolicy
// observation surface.
type routePolicyFunc func(ctx context.Context, conn, method, path string) (bool, string)

func (f routePolicyFunc) Allow(ctx context.Context, conn, method, path string) (allowed bool, reason string) {
	return f(ctx, conn, method, path)
}

func TestHandleListEndpoints_HonorsLimit(t *testing.T) {
	tk := New("test")
	setupCatalogWithSpec(t, tk, "petstore", "default", validMinimalSpec)
	if err := tk.AddConnection("c1", map[string]any{
		"base_url":   "https://x",
		"catalog_id": "petstore",
	}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	_, out, _ := tk.handleListEndpoints(context.Background(), nil, ListEndpointsInput{
		Connection: "c1",
		Limit:      2,
	})
	o, _ := out.(ListEndpointsOutput)
	if len(o.Operations) != 2 {
		t.Errorf("limit=2: got %d", len(o.Operations))
	}
}

func TestRegisterTools_AgainstRealServer(_ *testing.T) {
	srv := mcp.NewServer(&mcp.Implementation{Name: "test"}, nil)
	tk := New("api")
	tk.RegisterTools(srv)
	// No assertion API on mcp.Server for tool counts; the act of
	// registering without panicking is the assertion. A future SDK
	// upgrade that breaks the AddTool signature would fail to compile.
}

func textContent(res *mcp.CallToolResult) string {
	if len(res.Content) == 0 {
		return ""
	}
	tc, _ := res.Content[0].(*mcp.TextContent)
	if tc == nil {
		return ""
	}
	return tc.Text
}

func TestNewHTTPClient_BlocksRedirects(t *testing.T) {
	cfg := Config{CallTimeout: time.Second}
	c := newHTTPClient(cfg)
	req := &http.Request{}
	via := []*http.Request{}
	if err := c.CheckRedirect(req, via); err == nil {
		t.Error("CheckRedirect should signal use-last-response")
	}
}

func TestNewHTTPTransport_AppliesConnectTimeout(t *testing.T) {
	cfg := Config{ConnectTimeout: 1500 * time.Millisecond, CallTimeout: 30 * time.Second}
	tr := newHTTPTransport(cfg)
	if tr.TLSHandshakeTimeout != cfg.ConnectTimeout {
		t.Errorf("TLSHandshakeTimeout = %v; want %v", tr.TLSHandshakeTimeout, cfg.ConnectTimeout)
	}
	if tr.DialContext == nil {
		t.Fatal("DialContext is nil; ConnectTimeout cannot be enforced")
	}
}

// TestNewHTTPClient_HasTransport prevents a regression where the
// transport falls back to http.DefaultTransport (which would silently
// drop cfg.ConnectTimeout). A nil Transport on the returned Client
// would mean "use http.DefaultTransport" — and the default has no
// per-connection timeouts derived from our Config.
func TestNewHTTPClient_HasTransport(t *testing.T) {
	c := newHTTPClient(Config{ConnectTimeout: time.Second, CallTimeout: time.Second})
	if c.Transport == nil {
		t.Fatal("Client.Transport is nil; cfg.ConnectTimeout would be dropped")
	}
	if _, ok := c.Transport.(*http.Transport); !ok {
		t.Errorf("Client.Transport = %T; want *http.Transport", c.Transport)
	}
}

// TestInvoke_ConnectTimeout_FiresFast proves that a connection to an
// address that accepts neither connections nor reset packets fails
// within ConnectTimeout, not the larger CallTimeout. Uses a
// closed-but-listening test server; we close the listener so connect
// would block forever waiting for accept, and we expect ConnectTimeout
// to fire.
//
// Note: this test cannot use httptest.NewServer (which would actually
// accept). We construct a listener, immediately close it, and dial the
// stale port — the kernel responds with RST or the dial blocks until
// the timeout. RST is fast, but on systems where it isn't, the
// ConnectTimeout still bounds the wait. The 200ms ConnectTimeout vs
// 5s CallTimeout gap makes the bound observable.
func TestInvoke_ConnectTimeout_FiresFast(t *testing.T) {
	// Use a TEST-NET-1 address (RFC 5737, guaranteed unreachable).
	// Routing this address discards or blackholes — the dial cannot
	// complete. ConnectTimeout must terminate it.
	cfg := Config{
		BaseURL:          "http://192.0.2.1:80",
		AuthMode:         AuthModeNone,
		ConnectTimeout:   200 * time.Millisecond,
		CallTimeout:      10 * time.Second,
		MaxResponseBytes: DefaultMaxResponseBytes,
	}
	auth, _ := NewAuthenticator(cfg)
	start := time.Now()
	out, err := invoke(context.Background(), invocation{cfg: cfg, auth: auth, client: newHTTPClient(cfg)}, InvokeInput{
		Connection: "x", Method: "GET", Path: "/",
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("invoke unexpected error: %v", err)
	}
	if out.Status != 0 || out.Error == "" {
		t.Errorf("got status=%d error=%q; want 0 + error", out.Status, out.Error)
	}
	// Allow generous slop for slow CI; the assertion is "much faster
	// than CallTimeout", not "exactly ConnectTimeout".
	if elapsed > 3*time.Second {
		t.Errorf("invoke took %v; ConnectTimeout (%v) was not honored — appears to be using CallTimeout (%v)",
			elapsed, cfg.ConnectTimeout, cfg.CallTimeout)
	}
}

func TestErrorResult_ProducesIsError(t *testing.T) {
	r := errorResult("bad thing")
	if !r.IsError {
		t.Error("IsError = false")
	}
	if !strings.Contains(textContent(r), "bad thing") {
		t.Errorf("error message missing: %s", textContent(r))
	}
}

func TestJSONResult_EmbedsPayload(t *testing.T) {
	r := jsonResult(map[string]any{"x": 1})
	if r.IsError {
		t.Error("jsonResult set IsError")
	}
	if !strings.Contains(textContent(r), `"x": 1`) {
		t.Errorf("payload missing: %s", textContent(r))
	}
}

// TestToolkit_SetTokenStore_RethreadsAuthorizationCodeAuth proves the
// wiring contract that platform.WireAPIGatewayTokenStore depends on:
// when SetTokenStore is called AFTER addParsedConnection has already
// materialized an authorization_code Authenticator, the new store
// must reach the existing Authenticator (not just future ones).
//
// Without this re-thread, an api gateway connection registered before
// the platform's WireAPIGatewayTokenStore call would silently come
// up with no persistence — the failure mode that v1.57.1 surfaced on
// the MCP gateway side.
func TestToolkit_SetConnOAuthStore_RethreadsAuthorizationCodeAuth(t *testing.T) {
	tk := New("primary")
	t.Cleanup(func() { _ = tk.Close() })

	if tk.ConnOAuthStore() != nil {
		t.Fatal("freshly built toolkit unexpectedly has a ConnOAuthStore")
	}

	err := tk.AddConnection("acme", map[string]any{
		"base_url":                 "https://api.example.com",
		"auth_mode":                AuthModeOAuth2AuthorizationCode,
		"oauth2_token_url":         "https://idp.example/token",
		"oauth2_authorization_url": "https://idp.example/auth",
		"oauth2_client_id":         "id",
		"oauth2_client_secret":     "sec",
	})
	if err != nil {
		t.Fatalf("AddConnection: %v", err)
	}

	want := connoauth.NewMemoryStore()
	tk.SetConnOAuthStore(want)

	if got := tk.ConnOAuthStore(); got != want {
		t.Errorf("ConnOAuthStore after SetConnOAuthStore = %v; want %v", got, want)
	}

	tk.mu.RLock()
	c := tk.connections["acme"]
	tk.mu.RUnlock()
	if c == nil {
		t.Fatal("connection vanished")
	}
	ac, ok := c.auth.(*oauth2AuthorizationCodeAuth)
	if !ok {
		t.Fatalf("expected *oauth2AuthorizationCodeAuth, got %T", c.auth)
	}
	if ac.store != want {
		t.Error("re-thread did not deliver the new store to the existing Authenticator")
	}
}
