package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	testConnection = "test-gateway"
	testToolName   = "echo"
	testLocalName  = testConnection + NamespaceSeparator + testToolName
	testToolError  = "upstream tool error"
)

// upstreamServer spins up an in-process MCP server exposed over streamable
// HTTP, returning its URL and a shutdown function. The server advertises two
// tools:
//   - "echo": returns "echo:<msg>" as text content
//   - "boom": returns IsError=true with a known message, so we can verify
//     the gateway forwards error-results verbatim.
func upstreamServer(t *testing.T) string {
	t.Helper()

	srv := mcp.NewServer(&mcp.Implementation{Name: "upstream", Version: "0.0.1"}, nil)

	type echoArgs struct {
		Message string `json:"message"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        testToolName,
		Description: "echo back a message",
	}, func(_ context.Context, _ *mcp.CallToolRequest, args echoArgs) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "echo:" + args.Message}},
		}, nil, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "boom",
		Description: "always returns an error",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{&mcp.TextContent{Text: testToolError}},
		}, nil, nil
	})

	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts.URL
}

// gatewayAgainst builds a Toolkit pointing at the given upstream URL.
func gatewayAgainst(t *testing.T, url string) *Toolkit {
	t.Helper()
	cfg, err := ParseConfig(map[string]any{
		"endpoint":        url,
		"connection_name": testConnection,
		"connect_timeout": "3s",
		"call_timeout":    "3s",
	})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	return New("primary", cfg)
}

func TestNew_DiscoversAndNamespacesTools(t *testing.T) {
	tk := gatewayAgainst(t, upstreamServer(t))
	t.Cleanup(func() { _ = tk.Close() })

	names := tk.Tools()
	if !slices.Contains(names, testLocalName) {
		t.Errorf("expected %q in Tools(), got %v", testLocalName, names)
	}
	if !slices.Contains(names, testConnection+NamespaceSeparator+"boom") {
		t.Errorf("expected boom tool to be discovered, got %v", names)
	}
	for _, n := range names {
		if !strings.HasPrefix(n, testConnection+NamespaceSeparator) {
			t.Errorf("tool %q missing connection-name prefix", n)
		}
	}
}

func TestToolkitMetadata(t *testing.T) {
	tk := gatewayAgainst(t, upstreamServer(t))
	t.Cleanup(func() { _ = tk.Close() })

	if tk.Kind() != Kind {
		t.Errorf("Kind: got %q, want %q", tk.Kind(), Kind)
	}
	if tk.Name() != "primary" {
		t.Errorf("Name: got %q", tk.Name())
	}
	if tk.Connection() != testConnection {
		t.Errorf("Connection: got %q", tk.Connection())
	}
}

func TestNew_DefaultsConnectionNameToInstance(t *testing.T) {
	url := upstreamServer(t)
	cfg, err := ParseConfig(map[string]any{
		"endpoint":        url,
		"connect_timeout": "3s",
		"call_timeout":    "3s",
	})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	tk := New("crm", cfg)
	t.Cleanup(func() { _ = tk.Close() })
	if tk.Connection() != "crm" {
		t.Errorf("expected connection_name to default to instance name, got %q", tk.Connection())
	}
}

func TestNew_UpstreamUnreachable_ReturnsEmptyToolkit(t *testing.T) {
	cfg, err := ParseConfig(map[string]any{
		// Unreachable endpoint; a low connect timeout keeps this fast.
		"endpoint":        "http://127.0.0.1:1/mcp",
		"connection_name": "broken",
		"connect_timeout": "500ms",
		"call_timeout":    "1s",
	})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	tk := New("unreach", cfg)
	t.Cleanup(func() { _ = tk.Close() })

	if len(tk.Tools()) != 0 {
		t.Errorf("expected empty Tools() on unreachable upstream, got %v", tk.Tools())
	}
	if tk.Connection() != "broken" {
		t.Errorf("Connection still exposed: got %q", tk.Connection())
	}
}

func TestForwarder_ReturnsUpstreamResultVerbatim(t *testing.T) {
	tk := gatewayAgainst(t, upstreamServer(t))
	t.Cleanup(func() { _ = tk.Close() })

	client := platformWithGateway(t, tk)
	t.Cleanup(func() { _ = client.Close() })

	res, err := client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      testLocalName,
		Arguments: map[string]any{"message": "hi"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatal("expected success, got IsError=true")
	}
	tc := firstText(t, res)
	if tc.Text != "echo:hi" {
		t.Errorf("got %q, want %q", tc.Text, "echo:hi")
	}
}

func TestForwarder_UpstreamErrorResultIsForwarded(t *testing.T) {
	tk := gatewayAgainst(t, upstreamServer(t))
	t.Cleanup(func() { _ = tk.Close() })

	client := platformWithGateway(t, tk)
	t.Cleanup(func() { _ = client.Close() })

	res, err := client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      testConnection + NamespaceSeparator + "boom",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError=true from upstream tool error")
	}
	tc := firstText(t, res)
	// Upstream's error message is forwarded unchanged — no "upstream:" prefix,
	// because this is a TOOL error (IsError in result), not a TRANSPORT error.
	if tc.Text != testToolError {
		t.Errorf("got %q, want %q", tc.Text, testToolError)
	}
}

func TestForwarder_TransportFailureIsAttributed(t *testing.T) {
	// Stand up an upstream, connect, then close it so the next call fails at
	// the transport layer. The forwarder must emit an "upstream:<conn>:" error.
	url := upstreamServer(t)
	tk := gatewayAgainst(t, url)

	if tk.client == nil {
		t.Fatal("expected successful dial before shutdown")
	}
	// Close the upstream session so subsequent calls fail at the transport.
	_ = tk.client.close()

	client := platformWithGateway(t, tk)
	t.Cleanup(func() { _ = client.Close() })

	res, err := client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      testLocalName,
		Arguments: map[string]any{"message": "x"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError=true from closed upstream")
	}
	tc := firstText(t, res)
	wantPrefix := "upstream:" + testConnection + ":"
	if !strings.HasPrefix(tc.Text, wantPrefix) {
		t.Errorf("error %q missing prefix %q", tc.Text, wantPrefix)
	}
}

func TestMakeForwarder_NilClientReturnsUnavailable(t *testing.T) {
	tk := &Toolkit{config: Config{ConnectionName: "nilc", CallTimeout: time.Second}}
	handler := tk.makeForwarder("any")
	res, err := handler(context.Background(), &mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("handler err: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError=true")
	}
	tc := firstText(t, res)
	if !strings.Contains(tc.Text, "upstream:nilc") || !strings.Contains(tc.Text, "upstream unavailable") {
		t.Errorf("unexpected error text: %q", tc.Text)
	}
}

func TestArgumentsFromRequest(t *testing.T) {
	if got := argumentsFromRequest(nil); got != nil {
		t.Errorf("nil req: got %v, want nil", got)
	}
	if got := argumentsFromRequest(&mcp.CallToolRequest{}); got != nil {
		t.Errorf("nil params: got %v, want nil", got)
	}
	emptyArgs := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Arguments: json.RawMessage{}}}
	if got := argumentsFromRequest(emptyArgs); got != nil {
		t.Errorf("empty args: got %v, want nil", got)
	}
	raw := json.RawMessage(`{"k":"v"}`)
	populated := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Arguments: raw}}
	got := argumentsFromRequest(populated)
	gotRaw, ok := got.(json.RawMessage)
	if !ok {
		t.Fatalf("expected json.RawMessage, got %T", got)
	}
	if !bytesEqual(gotRaw, raw) {
		t.Errorf("populated: got %v, want %v", got, raw)
	}
}

func TestNamespaceTools_SkipsInvalid(t *testing.T) {
	in := []*mcp.Tool{
		nil,
		{Name: ""},
		{Name: "ok"},
	}
	out := namespaceTools("c", in)
	if len(out) != 1 {
		t.Fatalf("got %d forwarded tools, want 1", len(out))
	}
	if out[0].localName != "c"+NamespaceSeparator+"ok" {
		t.Errorf("unexpected localName %q", out[0].localName)
	}
}

func TestUpstreamErr_Format(t *testing.T) {
	res := upstreamErr("crm", "boom")
	if !res.IsError {
		t.Fatal("expected IsError=true")
	}
	tc := firstText(t, res)
	if tc.Text != "upstream:crm: boom" {
		t.Errorf("got %q", tc.Text)
	}
}

func TestSetProviders_NoOp(_ *testing.T) {
	tk := &Toolkit{}
	tk.SetSemanticProvider(nil)
	tk.SetQueryProvider(nil)
	// No panic = pass; also exercises the methods for coverage.
}

func TestClose_NilClient(t *testing.T) {
	tk := &Toolkit{}
	if err := tk.Close(); err != nil {
		t.Errorf("Close() on nil client: %v", err)
	}
}

func TestUpstreamClient_CloseIdempotent(t *testing.T) {
	tk := gatewayAgainst(t, upstreamServer(t))
	if tk.client == nil {
		t.Fatal("expected dial to succeed")
	}
	if err := tk.client.close(); err != nil {
		t.Errorf("first close: %v", err)
	}
	// nil-session variant — second invocation: session is gone, ensure we
	// don't panic and we pass through cleanly.
	var u *upstreamClient
	if err := u.close(); err != nil {
		t.Errorf("nil-receiver close: %v", err)
	}
}

func TestUpstreamClient_ListTools_FailsAfterClose(t *testing.T) {
	url := upstreamServer(t)
	cfg, err := ParseConfig(map[string]any{
		"endpoint":        url,
		"connect_timeout": "3s",
		"call_timeout":    "1s",
	})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	client, err := dial(context.Background(), cfg)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	_ = client.close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := client.listTools(ctx); err == nil {
		t.Error("expected error calling listTools on closed session")
	}
}

func TestAuthRoundTripper_BearerInjectsHeader(t *testing.T) {
	got := captureAuthHeader(t, "Authorization", AuthModeBearer, "tok-123")
	want := "Bearer tok-123"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestAuthRoundTripper_APIKeyInjectsHeader(t *testing.T) {
	got := captureAuthHeader(t, "X-API-Key", AuthModeAPIKey, "key-456")
	want := "key-456"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildHTTPClient_NoneReturnsNil(t *testing.T) {
	if c := buildHTTPClient(Config{AuthMode: AuthModeNone}); c != nil {
		t.Errorf("expected nil client for auth_mode=none, got %v", c)
	}
}

// TestDiscover_ListToolsTimeout exercises the dial-success / listTools-failure
// branch of discover by hanging the upstream's tools/list handler until the
// connect-scope context expires.
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

	cfg, err := ParseConfig(map[string]any{
		"endpoint":        ts.URL,
		"connection_name": "slow",
		"connect_timeout": "500ms",
		"call_timeout":    "500ms",
	})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	tk := New("slow", cfg)
	t.Cleanup(func() { _ = tk.Close() })

	if got := len(tk.Tools()); got != 0 {
		t.Errorf("expected empty Tools() when listTools times out, got %d: %v", got, tk.Tools())
	}
}

// captureAuthHeader stands up a server that records the first inbound request's
// value of a given header, points the gateway at it, and returns the captured
// value. The dial will fail (server isn't MCP) but the header is observed.
func captureAuthHeader(t *testing.T, header, authMode, credential string) string {
	t.Helper()
	seen := make(chan string, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case seen <- r.Header.Get(header):
		default:
		}
		// Respond with a body the MCP client will reject so dial ends fast.
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(ts.Close)

	cfg, err := ParseConfig(map[string]any{
		"endpoint":        ts.URL,
		"auth_mode":       authMode,
		"credential":      credential,
		"connect_timeout": "2s",
		"call_timeout":    "2s",
	})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	// New is expected to fail to discover tools (server isn't MCP), but
	// authRoundTripper runs on the first HTTP request regardless.
	tk := New("hdr", cfg)
	t.Cleanup(func() { _ = tk.Close() })

	select {
	case v := <-seen:
		return v
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for upstream request")
		return ""
	}
}

// platformWithGateway wires the toolkit into a fresh mcp.Server and returns a
// connected client session via in-memory transports.
func platformWithGateway(t *testing.T, tk *Toolkit) *mcp.ClientSession {
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

// firstText returns the first TextContent from a CallToolResult, failing the
// test if the shape is unexpected.
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

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
