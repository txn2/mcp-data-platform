package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	connCRM       = "crm"
	connMKT       = "marketing"
	toolEcho      = "echo"
	toolBoom      = "boom"
	localCRMEcho  = connCRM + NamespaceSeparator + toolEcho
	localCRMBoom  = connCRM + NamespaceSeparator + toolBoom
	localMKTEcho  = connMKT + NamespaceSeparator + toolEcho
	testToolError = "upstream tool error"
)

// upstreamServer spins up an in-process MCP server with echo + boom tools.
func upstreamServer(t *testing.T) string {
	t.Helper()
	srv := mcp.NewServer(&mcp.Implementation{Name: "upstream", Version: "0.0.1"}, nil)

	type echoArgs struct {
		Message string `json:"message"`
	}
	mcp.AddTool(srv, &mcp.Tool{Name: toolEcho, Description: "echo"},
		func(_ context.Context, _ *mcp.CallToolRequest, a echoArgs) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "echo:" + a.Message}},
			}, nil, nil
		})
	mcp.AddTool(srv, &mcp.Tool{Name: toolBoom, Description: "always errors"},
		func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: testToolError}},
			}, nil, nil
		})

	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	ts := httptest.NewServer(handler)
	// Force-close active SSE connections before shutting down the listener
	// so the teardown doesn't block on the gateway's still-open outbound
	// stream. Cleanup order is not guaranteed to close the gateway first.
	t.Cleanup(func() {
		ts.CloseClientConnections()
		ts.Close()
	})
	return ts.URL
}

// connectionConfig builds a raw config map suitable for AddConnection.
func connectionConfig(url, connName string) map[string]any {
	return map[string]any{
		"endpoint":        url,
		"connection_name": connName,
		"connect_timeout": "3s",
		"call_timeout":    "3s",
	}
}

func TestAddConnection_DiscoversAndNamespacesTools(t *testing.T) {
	url := upstreamServer(t)
	tk := New("primary")
	t.Cleanup(func() { _ = tk.Close() })

	if err := tk.AddConnection(connCRM, connectionConfig(url, connCRM)); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}

	tools := tk.Tools()
	if !slices.Contains(tools, localCRMEcho) || !slices.Contains(tools, localCRMBoom) {
		t.Errorf("expected both namespaced tools, got %v", tools)
	}
	for _, n := range tools {
		if !strings.HasPrefix(n, connCRM+NamespaceSeparator) {
			t.Errorf("tool %q missing prefix", n)
		}
	}
}

func TestAddConnection_TwoConnectionsIsolated(t *testing.T) {
	tk := New("primary")
	t.Cleanup(func() { _ = tk.Close() })

	url1 := upstreamServer(t)
	url2 := upstreamServer(t)
	if err := tk.AddConnection(connCRM, connectionConfig(url1, connCRM)); err != nil {
		t.Fatalf("AddConnection crm: %v", err)
	}
	if err := tk.AddConnection(connMKT, connectionConfig(url2, connMKT)); err != nil {
		t.Fatalf("AddConnection marketing: %v", err)
	}

	tools := tk.Tools()
	want := []string{localCRMBoom, localCRMEcho, localMKTEcho, connMKT + NamespaceSeparator + toolBoom}
	for _, n := range want {
		if !slices.Contains(tools, n) {
			t.Errorf("missing tool %q in %v", n, tools)
		}
	}

	details := tk.ListConnections()
	if len(details) != 2 {
		t.Fatalf("ListConnections: got %d, want 2", len(details))
	}
	// Sorted by name → crm, marketing
	if details[0].Name != connCRM || details[1].Name != connMKT {
		t.Errorf("sort order: got %v", []string{details[0].Name, details[1].Name})
	}
	// DefaultName here is "primary" (from New), which matches neither
	// connection — so neither should be marked default.
	for _, d := range details {
		if d.IsDefault {
			t.Errorf("unexpected IsDefault on %s", d.Name)
		}
	}
}

func TestAddConnection_DuplicateReturnsExists(t *testing.T) {
	tk := New("primary")
	t.Cleanup(func() { _ = tk.Close() })

	url := upstreamServer(t)
	if err := tk.AddConnection(connCRM, connectionConfig(url, connCRM)); err != nil {
		t.Fatalf("first AddConnection: %v", err)
	}
	err := tk.AddConnection(connCRM, connectionConfig(url, connCRM))
	if !errors.Is(err, ErrConnectionExists) {
		t.Errorf("second AddConnection: got %v, want ErrConnectionExists", err)
	}
}

func TestAddConnection_UpstreamUnreachableReturnsError(t *testing.T) {
	tk := New("primary")
	t.Cleanup(func() { _ = tk.Close() })

	err := tk.AddConnection("broken", map[string]any{
		"endpoint":        "http://127.0.0.1:1/mcp",
		"connection_name": "broken",
		"connect_timeout": "200ms",
		"call_timeout":    "1s",
	})
	if err == nil {
		t.Fatal("expected error for unreachable upstream")
	}
	// Placeholder must NOT be stored — retry path depends on HasConnection=false.
	if tk.HasConnection("broken") {
		t.Error("expected placeholder not to be stored after dial failure")
	}
}

func TestAddConnection_BadConfigReturnsError(t *testing.T) {
	tk := New("primary")
	err := tk.AddConnection("x", map[string]any{"endpoint": ""})
	if err == nil {
		t.Error("expected config validation error")
	}
}

func TestRemoveConnection_UnregistersTools(t *testing.T) {
	tk := New("primary")
	t.Cleanup(func() { _ = tk.Close() })

	if err := tk.AddConnection(connCRM, connectionConfig(upstreamServer(t), connCRM)); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	if !tk.HasConnection(connCRM) {
		t.Fatal("expected HasConnection=true after add")
	}
	if err := tk.RemoveConnection(connCRM); err != nil {
		t.Fatalf("RemoveConnection: %v", err)
	}
	if tk.HasConnection(connCRM) {
		t.Error("expected HasConnection=false after remove")
	}
	if len(tk.Tools()) != 0 {
		t.Errorf("expected empty Tools() after remove, got %v", tk.Tools())
	}
}

func TestRemoveConnection_MissingReturnsNotFound(t *testing.T) {
	tk := New("primary")
	err := tk.RemoveConnection("nope")
	if !errors.Is(err, ErrConnectionNotFound) {
		t.Errorf("got %v, want ErrConnectionNotFound", err)
	}
}

func TestListConnections_MarksDefault(t *testing.T) {
	tk := New(connCRM) // default connection name is "crm"
	t.Cleanup(func() { _ = tk.Close() })

	url := upstreamServer(t)
	_ = tk.AddConnection(connCRM, connectionConfig(url, connCRM))
	_ = tk.AddConnection(connMKT, connectionConfig(url, connMKT))

	details := tk.ListConnections()
	defaults := 0
	for _, d := range details {
		if d.IsDefault {
			defaults++
			if d.Name != connCRM {
				t.Errorf("IsDefault on wrong connection: %s", d.Name)
			}
		}
	}
	if defaults != 1 {
		t.Errorf("expected exactly 1 default, got %d", defaults)
	}
}

func TestNewMulti_AbsorbsInitialFailures(t *testing.T) {
	cfg, err := ParseMultiConfig("primary", map[string]map[string]any{
		connCRM: {
			"endpoint":        "http://127.0.0.1:1/mcp",
			"connect_timeout": "200ms",
			"call_timeout":    "1s",
		},
	})
	if err != nil {
		t.Fatalf("ParseMultiConfig: %v", err)
	}
	tk := NewMulti(cfg)
	t.Cleanup(func() { _ = tk.Close() })

	if len(tk.Tools()) != 0 {
		t.Errorf("expected empty Tools() for failed initial, got %v", tk.Tools())
	}
	if tk.HasConnection(connCRM) {
		t.Error("failed initial connection should not be stored")
	}
}

func TestNewMulti_LoadsHealthyInstances(t *testing.T) {
	url := upstreamServer(t)
	cfg, err := ParseMultiConfig("primary", map[string]map[string]any{
		connCRM: connectionConfig(url, connCRM),
	})
	if err != nil {
		t.Fatalf("ParseMultiConfig: %v", err)
	}
	tk := NewMulti(cfg)
	t.Cleanup(func() { _ = tk.Close() })

	if !tk.HasConnection(connCRM) {
		t.Error("expected CRM connection to be live after NewMulti")
	}
}

func TestParseMultiConfig_SurfacesBadInstance(t *testing.T) {
	_, err := ParseMultiConfig("primary", map[string]map[string]any{
		connCRM: {}, // no endpoint
	})
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "gateway/crm") {
		t.Errorf("error should name the instance, got: %v", err)
	}
}

func TestToolkitMetadata(t *testing.T) {
	tk := New("primary")
	if tk.Kind() != Kind {
		t.Errorf("Kind: %q", tk.Kind())
	}
	if tk.Name() != "primary" || tk.Connection() != "primary" {
		t.Errorf("Name/Connection: %q / %q", tk.Name(), tk.Connection())
	}
}

func TestNewMulti_EmptyDefaultFallsBackToKind(t *testing.T) {
	tk := NewMulti(MultiConfig{DefaultName: "", Instances: nil})
	if tk.Name() != Kind {
		t.Errorf("Name with empty default: got %q, want %q", tk.Name(), Kind)
	}
}

func TestEndToEnd_ForwardsCallsThroughServer(t *testing.T) {
	tk := New("primary")
	t.Cleanup(func() { _ = tk.Close() })
	_ = tk.AddConnection(connCRM, connectionConfig(upstreamServer(t), connCRM))

	client := platformWithToolkit(t, tk)
	t.Cleanup(func() { _ = client.Close() })

	res, err := client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      localCRMEcho,
		Arguments: map[string]any{"message": "hi"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatal("expected success")
	}
	if tc := firstText(t, res); tc.Text != "echo:hi" {
		t.Errorf("got %q", tc.Text)
	}
}

func TestEndToEnd_HotAddAfterRegisterTools(t *testing.T) {
	tk := New("primary")
	t.Cleanup(func() { _ = tk.Close() })

	client := platformWithToolkit(t, tk)
	t.Cleanup(func() { _ = client.Close() })

	// No tools yet — tools/list returns empty.
	list, err := client.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(list.Tools) != 0 {
		t.Errorf("before Add: got %d tools, want 0", len(list.Tools))
	}

	if err := tk.AddConnection(connCRM, connectionConfig(upstreamServer(t), connCRM)); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}

	list, err = client.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools after add: %v", err)
	}
	names := make([]string, 0, len(list.Tools))
	for _, tool := range list.Tools {
		names = append(names, tool.Name)
	}
	if !slices.Contains(names, localCRMEcho) {
		t.Errorf("after Add: expected %q, got %v", localCRMEcho, names)
	}

	res, err := client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      localCRMEcho,
		Arguments: map[string]any{"message": "hot"},
	})
	if err != nil {
		t.Fatalf("CallTool after Add: %v", err)
	}
	if tc := firstText(t, res); tc.Text != "echo:hot" {
		t.Errorf("got %q", tc.Text)
	}
}

func TestEndToEnd_HotRemoveAfterRegisterTools(t *testing.T) {
	tk := New("primary")
	t.Cleanup(func() { _ = tk.Close() })

	client := platformWithToolkit(t, tk)
	t.Cleanup(func() { _ = client.Close() })

	if err := tk.AddConnection(connCRM, connectionConfig(upstreamServer(t), connCRM)); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}

	before, _ := client.ListTools(context.Background(), &mcp.ListToolsParams{})
	if len(before.Tools) == 0 {
		t.Fatal("expected tools present before remove")
	}

	if err := tk.RemoveConnection(connCRM); err != nil {
		t.Fatalf("RemoveConnection: %v", err)
	}

	after, err := client.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools after remove: %v", err)
	}
	if len(after.Tools) != 0 {
		t.Errorf("expected 0 tools after remove, got %d", len(after.Tools))
	}
}

func TestEndToEnd_UpstreamErrorResultForwarded(t *testing.T) {
	tk := New("primary")
	t.Cleanup(func() { _ = tk.Close() })
	_ = tk.AddConnection(connCRM, connectionConfig(upstreamServer(t), connCRM))

	client := platformWithToolkit(t, tk)
	t.Cleanup(func() { _ = client.Close() })

	res, err := client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      localCRMBoom,
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError=true")
	}
	if tc := firstText(t, res); tc.Text != testToolError {
		t.Errorf("got %q, want %q", tc.Text, testToolError)
	}
}

func TestEndToEnd_TransportFailureAttribution(t *testing.T) {
	tk := New("primary")
	t.Cleanup(func() { _ = tk.Close() })
	_ = tk.AddConnection(connCRM, connectionConfig(upstreamServer(t), connCRM))

	// Kill the upstream session so next call fails at transport.
	tk.mu.RLock()
	u := tk.connections[connCRM]
	tk.mu.RUnlock()
	_ = u.client.close()

	client := platformWithToolkit(t, tk)
	t.Cleanup(func() { _ = client.Close() })

	res, err := client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      localCRMEcho,
		Arguments: map[string]any{"message": "x"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError=true")
	}
	tc := firstText(t, res)
	want := "upstream:" + connCRM + ":"
	if !strings.HasPrefix(tc.Text, want) {
		t.Errorf("error %q missing prefix %q", tc.Text, want)
	}
}

func TestMakeForwarder_NilClientReturnsUnavailable(t *testing.T) {
	u := &upstream{config: Config{ConnectionName: "nilc", CallTimeout: time.Second}}
	handler := u.makeForwarder("any")
	res, err := handler(context.Background(), &mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("handler err: %v", err)
	}
	tc := firstText(t, res)
	if !strings.Contains(tc.Text, "upstream:nilc") || !strings.Contains(tc.Text, "upstream unavailable") {
		t.Errorf("unexpected error text: %q", tc.Text)
	}
}

func TestConcurrentAddConnection_Safe(t *testing.T) {
	tk := New("primary")
	t.Cleanup(func() { _ = tk.Close() })

	url := upstreamServer(t)
	const n = 8
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("c%d", i)
			errs <- tk.AddConnection(name, connectionConfig(url, name))
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Errorf("concurrent AddConnection: %v", e)
		}
	}
	if len(tk.ListConnections()) != n {
		t.Errorf("expected %d connections, got %d", n, len(tk.ListConnections()))
	}
}

func TestArgumentsFromRequest(t *testing.T) {
	if argumentsFromRequest(nil) != nil {
		t.Error("nil req")
	}
	if argumentsFromRequest(&mcp.CallToolRequest{}) != nil {
		t.Error("nil params")
	}
	empty := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Arguments: json.RawMessage{}}}
	if argumentsFromRequest(empty) != nil {
		t.Error("empty args")
	}
	raw := json.RawMessage(`{"k":"v"}`)
	got := argumentsFromRequest(&mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Arguments: raw}})
	gr, ok := got.(json.RawMessage)
	if !ok {
		t.Fatalf("unexpected type %T", got)
	}
	if string(gr) != string(raw) {
		t.Errorf("got %q, want %q", gr, raw)
	}
}

func TestUpstreamErr_Format(t *testing.T) {
	res := upstreamErr("crm", "boom")
	if !res.IsError {
		t.Fatal("expected IsError=true")
	}
	if tc := firstText(t, res); tc.Text != "upstream:crm: boom" {
		t.Errorf("got %q", tc.Text)
	}
}

func TestMakeLocalNames_SkipsInvalid(t *testing.T) {
	in := []*mcp.Tool{nil, {Name: ""}, {Name: "ok"}}
	out := makeLocalNames("c", in)
	if len(out) != 1 || out[0] != "c"+NamespaceSeparator+"ok" {
		t.Errorf("got %v", out)
	}
}

func TestSetProviders_NoOp(_ *testing.T) {
	tk := New("x")
	tk.SetSemanticProvider(nil)
	tk.SetQueryProvider(nil)
}

func TestClose_Empty(t *testing.T) {
	tk := New("x")
	if err := tk.Close(); err != nil {
		t.Error(err)
	}
}

func TestAuthRoundTripper_BearerInjectsHeader(t *testing.T) {
	got := captureAuthHeader(t, "Authorization", AuthModeBearer, "tok")
	if got != "Bearer tok" {
		t.Errorf("got %q, want %q", got, "Bearer tok")
	}
}

func TestAuthRoundTripper_APIKeyInjectsHeader(t *testing.T) {
	got := captureAuthHeader(t, "X-API-Key", AuthModeAPIKey, "k")
	if got != "k" {
		t.Errorf("got %q, want %q", got, "k")
	}
}

func TestBuildHTTPClient_NoneReturnsNil(t *testing.T) {
	if c := buildHTTPClient(Config{AuthMode: AuthModeNone}); c != nil {
		t.Errorf("expected nil client for auth_mode=none, got %v", c)
	}
}

// TestDiscover_ListToolsTimeout forces discover's post-dial error path by
// hanging the upstream's tools/list handler until the connect-scope context
// deadline elapses.
func TestDiscover_ListToolsTimeout(t *testing.T) {
	srv := mcp.NewServer(&mcp.Implementation{Name: "slow", Version: "0.0.1"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "noop"},
		func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{}, nil, nil
		})
	srv.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method == "tools/list" {
				<-ctx.Done()
				return nil, ctx.Err()
			}
			return next(ctx, method, req)
		}
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	tk := New("primary")
	t.Cleanup(func() { _ = tk.Close() })

	err := tk.AddConnection("slow", map[string]any{
		"endpoint":        ts.URL,
		"connect_timeout": "500ms",
		"call_timeout":    "500ms",
	})
	if err == nil {
		t.Fatal("expected listTools timeout error from AddConnection")
	}
	if tk.HasConnection("slow") {
		t.Error("timed-out connection should not be stored")
	}
}

// platformWithToolkit wires the toolkit into a fresh mcp.Server and returns a
// connected client session over in-memory transports.
func platformWithToolkit(t *testing.T, tk *Toolkit) *mcp.ClientSession {
	t.Helper()

	server := mcp.NewServer(&mcp.Implementation{Name: "platform", Version: "0.0.1"}, nil)
	tk.RegisterTools(server)

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ctx := context.Background()

	errCh := make(chan error, 1)
	go func() {
		ss, err := server.Connect(ctx, serverTransport, nil)
		if err != nil {
			errCh <- err
			return
		}
		errCh <- ss.Wait()
	}()

	client := mcp.NewClient(&mcp.Implementation{Name: "client", Version: "0.0.1"}, nil)
	cs, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client Connect: %v", err)
	}
	t.Cleanup(func() {
		if err := <-errCh; err != nil && !errors.Is(err, context.Canceled) {
			t.Logf("server loop exit: %v", err)
		}
	})
	return cs
}

// firstText returns the first TextContent from a CallToolResult.
func firstText(t *testing.T, res *mcp.CallToolResult) *mcp.TextContent {
	t.Helper()
	if len(res.Content) == 0 {
		t.Fatal("empty content")
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("unexpected content type %T", res.Content[0])
	}
	return tc
}

// captureAuthHeader stands up a non-MCP server, records the first inbound
// value of a given header, points the gateway at it, and returns the captured
// value. Dial fails (server isn't MCP) but the header has already been seen.
func captureAuthHeader(t *testing.T, header, authMode, credential string) string {
	t.Helper()
	seen := make(chan string, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case seen <- r.Header.Get(header):
		default:
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(ts.Close)

	tk := New("hdr")
	t.Cleanup(func() { _ = tk.Close() })
	// Error expected (non-MCP server); we only care the header was sent.
	_ = tk.AddConnection("hdr", map[string]any{
		"endpoint":        ts.URL,
		"auth_mode":       authMode,
		"credential":      credential,
		"connect_timeout": "2s",
		"call_timeout":    "2s",
	})

	select {
	case v := <-seen:
		return v
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for upstream request")
		return ""
	}
}
