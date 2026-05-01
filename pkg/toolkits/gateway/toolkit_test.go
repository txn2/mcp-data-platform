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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/toolkits/gateway/enrichment"
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

func TestConnectionForTool_ResolvesPerToolToConnection(t *testing.T) {
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

	if got := tk.ConnectionForTool(localCRMEcho); got != connCRM {
		t.Errorf("ConnectionForTool(%q) = %q, want %q", localCRMEcho, got, connCRM)
	}
	if got := tk.ConnectionForTool(localMKTEcho); got != connMKT {
		t.Errorf("ConnectionForTool(%q) = %q, want %q", localMKTEcho, got, connMKT)
	}
	if got := tk.ConnectionForTool("not_a_tool"); got != "" {
		t.Errorf("ConnectionForTool(unknown) = %q, want empty", got)
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

// engineFixture builds a minimal enrichment engine that fires one rule
// against the response and merges a hardcoded value under "enrichment".
func engineFixture(t *testing.T, connection, localTool string) *enrichment.Engine {
	t.Helper()
	store := &enrichmentStub{rules: []enrichment.Rule{{
		ID: "fixture", ConnectionName: connection, ToolName: localTool,
		EnrichAction: enrichment.Action{
			Source: enrichment.SourceTrino, Operation: "query",
		},
		MergeStrategy: enrichment.Merge{Kind: enrichment.MergePath, Path: "warehouse_signals"},
		Enabled:       true,
	}}}
	src := &fixtureSource{}
	reg := enrichment.NewSourceRegistry()
	reg.Register(src)
	return enrichment.NewEngine(store, reg)
}

type fixtureSource struct{}

func (*fixtureSource) Name() string         { return enrichment.SourceTrino }
func (*fixtureSource) Operations() []string { return []string{"query"} }
func (*fixtureSource) Execute(_ context.Context, _ string, _ map[string]any) (any, error) {
	return map[string]any{"lifetime_value": 4242}, nil
}

type enrichmentStub struct{ rules []enrichment.Rule }

func (s *enrichmentStub) List(_ context.Context, _, _ string, _ bool) ([]enrichment.Rule, error) {
	return s.rules, nil
}

func (*enrichmentStub) Get(_ context.Context, _ string) (*enrichment.Rule, error) {
	return nil, enrichment.ErrRuleNotFound
}

func (*enrichmentStub) Create(_ context.Context, r enrichment.Rule) (enrichment.Rule, error) {
	return r, nil
}

func (*enrichmentStub) Update(_ context.Context, r enrichment.Rule) (enrichment.Rule, error) {
	return r, nil
}
func (*enrichmentStub) Delete(_ context.Context, _ string) error { return nil }

func TestEndToEnd_EnrichmentRunsThroughForwarder(t *testing.T) {
	url := upstreamServer(t)
	tk := New("primary")
	t.Cleanup(func() { _ = tk.Close() })
	if err := tk.AddConnection(connCRM, connectionConfig(url, connCRM)); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	tk.SetEnrichmentEngine(engineFixture(t, connCRM, localCRMEcho))

	client := platformWithToolkit(t, tk)
	t.Cleanup(func() { _ = client.Close() })

	res, err := client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      localCRMEcho,
		Arguments: map[string]any{"message": "ignored"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	// The echo tool returns "echo:ignored" as TextContent. JSON parse
	// fails, so structuredInput returns false and enrichment is a no-op
	// here — the assertion is only that the forward+enrichment pipeline
	// did not break the call. Engine integration is exhaustively unit
	// tested in the enrichment package; this just proves the wiring.
	if res.IsError {
		t.Fatal("expected success")
	}
}

func TestProbe_DiscoversTools(t *testing.T) {
	url := upstreamServer(t)
	cfg, err := ParseConfig(map[string]any{
		"endpoint":        url,
		"connect_timeout": "3s",
		"call_timeout":    "3s",
	})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	tools, err := Probe(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(tools) == 0 {
		t.Fatal("expected probe to discover tools")
	}
}

func TestProbe_UnreachableReturnsError(t *testing.T) {
	cfg, err := ParseConfig(map[string]any{
		"endpoint":        "http://127.0.0.1:1/mcp",
		"connect_timeout": "200ms",
		"call_timeout":    "1s",
	})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if _, err := Probe(context.Background(), cfg); err == nil {
		t.Fatal("expected error for unreachable upstream")
	}
}

func TestApplyEnrichment_NoEngineIsNoOp(t *testing.T) {
	tk := New("primary")
	res := &mcp.CallToolResult{StructuredContent: map[string]any{"x": 1}}
	tk.applyEnrichment(context.Background(), "c", "c__t", &mcp.CallToolRequest{}, res)
	got, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("structured content not a map: %T", res.StructuredContent)
	}
	if got["x"] != 1 || len(got) != 1 {
		t.Errorf("unexpectedly modified: %v", got)
	}
}

func TestApplyEnrichment_AppendsWarnings(t *testing.T) {
	tk := New("primary")
	store := &enrichmentStub{rules: []enrichment.Rule{{
		ID: "r", ConnectionName: "c", ToolName: "c__t",
		EnrichAction: enrichment.Action{Source: "phantom", Operation: "x"},
		Enabled:      true,
	}}}
	tk.SetEnrichmentEngine(enrichment.NewEngine(store, enrichment.NewSourceRegistry()))

	res := &mcp.CallToolResult{StructuredContent: map[string]any{"x": 1}}
	tk.applyEnrichment(context.Background(), "c", "c__t", &mcp.CallToolRequest{}, res)

	var foundWarning bool
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok && strings.HasPrefix(tc.Text, "warning:") {
			foundWarning = true
		}
	}
	if !foundWarning {
		t.Errorf("expected at least one warning text content, got %d items", len(res.Content))
	}
}

func TestStructuredInput_PrefersStructuredContent(t *testing.T) {
	res := &mcp.CallToolResult{
		StructuredContent: map[string]any{"a": 1},
		Content:           []mcp.Content{&mcp.TextContent{Text: "ignored"}},
	}
	got, ok := structuredInput(res)
	if !ok {
		t.Fatal("expected ok")
	}
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("got %T", got)
	}
	if m["a"] != 1 {
		t.Errorf("got %v", got)
	}
}

func TestStructuredInput_FallsBackToJSONText(t *testing.T) {
	res := &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: `{"a":1}`}},
	}
	got, ok := structuredInput(res)
	if !ok {
		t.Fatal("expected ok")
	}
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("got %T", got)
	}
	if m["a"] != float64(1) {
		t.Errorf("got %v", got)
	}
}

func TestStructuredInput_NoneReturnsFalse(t *testing.T) {
	if _, ok := structuredInput(&mcp.CallToolResult{}); ok {
		t.Error("expected false on empty result")
	}
	if _, ok := structuredInput(&mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "not json"}}}); ok {
		t.Error("expected false on non-JSON text")
	}
}

func TestArgsFromRequest_ParsesJSON(t *testing.T) {
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{
		Arguments: json.RawMessage(`{"k":"v"}`),
	}}
	got := argsFromRequest(req)
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("got %T", got)
	}
	if m["k"] != "v" {
		t.Errorf("got %v", m)
	}
}

func TestArgsFromRequest_NilCases(t *testing.T) {
	if argsFromRequest(nil) != nil {
		t.Error("nil req")
	}
	if argsFromRequest(&mcp.CallToolRequest{}) != nil {
		t.Error("nil params")
	}
}

func TestArgsFromRequest_BadJSONReturnsNil(t *testing.T) {
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{
		Arguments: json.RawMessage(`{not json`),
	}}
	if argsFromRequest(req) != nil {
		t.Error("expected nil for malformed JSON")
	}
}

func TestMakeForwarder_NilClientReturnsUnavailable(t *testing.T) {
	tk := New("primary")
	u := &upstream{config: Config{ConnectionName: "nilc", CallTimeout: time.Second}}
	handler := tk.makeForwarder(u, "any", "nilc__any")
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
	if c := buildHTTPClient(Config{AuthMode: AuthModeNone}, nil); c != nil {
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

// TestStatus_DoesNotBlockDuringSlowAddConnection proves the mutex
// architecture fix: when AddConnection is performing a slow network
// handshake against an unresponsive upstream, concurrent Status()
// calls on OTHER connections must not block on the toolkit's mutex.
//
// Pre-fix: Toolkit.mu was held throughout discover() (network I/O).
// Status() acquired the same mutex and waited for the dial to finish
// or time out — typically minutes when the upstream hangs on
// notifications/initialized, which is why /portal/admin/connections
// hung whenever any connection was misbehaving.
func TestStatus_DoesNotBlockDuringSlowAddConnection(t *testing.T) {
	// hungSrv stalls long enough that the test's fast-assertion
	// window (200ms) lands while AddConnection is still in
	// discover(). The handler then writes a 500 so AddConnection
	// terminates promptly when our wait window opens — this isolates
	// the test to the mutex-contention question and prevents goroutine
	// leaks from the SDK's HTTP handling on full dial-context expiry.
	hangFor := 1500 * time.Millisecond
	hungSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(hangFor):
			http.Error(w, "simulated upstream rejection", http.StatusInternalServerError)
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(hungSrv.Close)

	healthySrv := upstreamServer(t)
	tk := New("primary")
	t.Cleanup(func() { _ = tk.Close() })

	require.NoError(t, tk.AddConnection("healthy", map[string]any{
		"endpoint":        healthySrv,
		"connection_name": "healthy",
		"connect_timeout": "3s",
		"call_timeout":    "3s",
	}))

	// Start the slow AddConnection in a goroutine.
	addDone := make(chan error, 1)
	go func() {
		addDone <- tk.AddConnection("hung", map[string]any{
			"endpoint":        hungSrv.URL,
			"connection_name": "hung",
			"connect_timeout": "5s",
			"call_timeout":    "5s",
		})
	}()
	time.Sleep(100 * time.Millisecond) // let the goroutine reach discover()

	// THE assertion: Status() on the OTHER connection must return
	// promptly while AddConnection on the hung connection is still in
	// flight. Pre-fix (mutex held through discover) this would block
	// until the hung dial returned.
	statusDone := make(chan struct{})
	go func() {
		_ = tk.Status("healthy")
		close(statusDone)
	}()
	select {
	case <-statusDone:
		// Pass.
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Status() blocked waiting for hung AddConnection — toolkit mutex held during network I/O")
	}

	// Drain the AddConnection goroutine. The hungSrv responds within
	// hangFor (~1.5s); SDK round-trip + cleanup add overhead. 30s is a
	// generous ceiling that won't mask real bugs but tolerates CI jitter.
	select {
	case <-addDone:
	case <-time.After(30 * time.Second):
		t.Fatal("AddConnection didn't return after upstream sent 500 — investigate SDK cleanup")
	}
}

// TestListConnections_DoesNotBlockDuringSlowAddConnection is the
// parallel proof for ListConnections — it's the endpoint backing the
// /portal/admin/connections page, the visible symptom that prompted
// the architectural fix.
func TestListConnections_DoesNotBlockDuringSlowAddConnection(t *testing.T) {
	hungSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(1500 * time.Millisecond):
			http.Error(w, "simulated upstream rejection", http.StatusInternalServerError)
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(hungSrv.Close)

	tk := New("primary")
	t.Cleanup(func() { _ = tk.Close() })

	addDone := make(chan error, 1)
	go func() {
		addDone <- tk.AddConnection("hung", map[string]any{
			"endpoint":        hungSrv.URL,
			"connection_name": "hung",
			"connect_timeout": "3s",
			"call_timeout":    "3s",
		})
	}()
	time.Sleep(50 * time.Millisecond)

	listDone := make(chan int, 1)
	go func() {
		listDone <- len(tk.ListConnections())
	}()
	select {
	case n := <-listDone:
		// Pass. n should be 1 (the claiming sentinel for "hung") OR 0
		// depending on test timing — either is acceptable; the point
		// is that the call returned promptly.
		_ = n
	case <-time.After(200 * time.Millisecond):
		t.Fatal("ListConnections() blocked waiting for hung AddConnection — toolkit mutex still held during network I/O")
	}

	select {
	case <-addDone:
	case <-time.After(10 * time.Second):
		t.Fatal("hung AddConnection didn't terminate")
	}
}

// TestRemoveConnection_OfDifferentName_DoesNotBlockDuringSlowAddConnection
// proves the mutex fix also unblocks RemoveConnection of OTHER
// connections — Remove of an unrelated name must complete fast.
func TestRemoveConnection_OfDifferentName_DoesNotBlockDuringSlowAddConnection(t *testing.T) {
	hungSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(1500 * time.Millisecond):
			http.Error(w, "simulated upstream rejection", http.StatusInternalServerError)
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(hungSrv.Close)
	healthySrv := upstreamServer(t)

	tk := New("primary")
	t.Cleanup(func() { _ = tk.Close() })

	require.NoError(t, tk.AddConnection("healthy", map[string]any{
		"endpoint":        healthySrv,
		"connection_name": "healthy",
		"connect_timeout": "3s",
		"call_timeout":    "3s",
	}))

	addDone := make(chan error, 1)
	go func() {
		addDone <- tk.AddConnection("hung", map[string]any{
			"endpoint":        hungSrv.URL,
			"connection_name": "hung",
			"connect_timeout": "3s",
			"call_timeout":    "3s",
		})
	}()
	time.Sleep(50 * time.Millisecond)

	removeDone := make(chan error, 1)
	go func() {
		removeDone <- tk.RemoveConnection("healthy")
	}()
	select {
	case err := <-removeDone:
		require.NoError(t, err)
	case <-time.After(200 * time.Millisecond):
		t.Fatal("RemoveConnection('healthy') blocked waiting for hung AddConnection on a DIFFERENT name — mutex contention bug")
	}

	select {
	case <-addDone:
	case <-time.After(10 * time.Second):
		t.Fatal("hung AddConnection didn't terminate")
	}
}

// TestAddConnection_SameName_OnlyOneWins proves the slot-claim
// contract under contention: two concurrent AddConnection calls for
// the same name must serialize via the claim sentinel. Exactly one
// returns nil (or an authcode-placeholder success); the other
// returns ErrConnectionExists immediately, even while the first is
// in the middle of its dial.
func TestAddConnection_SameName_OnlyOneWins(t *testing.T) {
	hangFor := 1 * time.Second
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(hangFor):
			http.Error(w, "deliberate", http.StatusInternalServerError)
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)

	tk := New("primary")
	t.Cleanup(func() { _ = tk.Close() })

	cfg := map[string]any{
		"endpoint":        srv.URL,
		"connection_name": "vendor",
		"connect_timeout": "5s",
		"call_timeout":    "5s",
	}

	results := make(chan error, 2)
	for range 2 {
		go func() { results <- tk.AddConnection("vendor", cfg) }()
	}

	got := []error{<-results, <-results}
	// One result must be ErrConnectionExists; the other is the
	// outcome of the actual dial (in this case 500 → bubbled error
	// since we're not in auth_code mode and there's no placeholder
	// path). The exact dial-outcome error is incidental — the
	// contract under test is "exactly one ErrConnectionExists."
	conflicts := 0
	for _, err := range got {
		if errors.Is(err, ErrConnectionExists) {
			conflicts++
		}
	}
	require.Equal(t, 1, conflicts,
		"exactly one of two concurrent AddConnection calls for the same name must return ErrConnectionExists; got %v", got)
}

// TestRemoveConnection_DuringSlowAdd_DiscardsResult proves the
// install-time identity check: when a connection's slot is removed
// while its dial is in flight, the dial's result is discarded
// (client closed, no entry installed) instead of being silently
// added back into the map.
func TestRemoveConnection_DuringSlowAdd_DiscardsResult(t *testing.T) {
	healthySrv := upstreamServer(t)

	tk := New("primary")
	t.Cleanup(func() { _ = tk.Close() })

	// Start a healthy AddConnection in a goroutine. The dial against
	// upstreamServer is fast (~50-100ms) so we have a narrow window —
	// but we control the race by removing the claim BEFORE the dial
	// completes from the perspective of the toolkit's mutex.
	addDone := make(chan error, 1)
	go func() {
		addDone <- tk.AddConnection("vendor", map[string]any{
			"endpoint":        healthySrv,
			"connection_name": "vendor",
			"connect_timeout": "3s",
			"call_timeout":    "3s",
		})
	}()

	// Race the goroutine to the lock. RemoveConnection acquires the
	// mutex, sees the claim sentinel, deletes it, releases. By the
	// time the goroutine's discover() finishes and tries to install,
	// the slot is gone — installDialResult takes the discard path.
	//
	// Use a tiny spin to ensure the AddConnection goroutine has
	// claimed the slot before we try to remove it. Without this,
	// RemoveConnection might run BEFORE the claim and return
	// ErrConnectionNotFound — which is also a valid outcome but
	// doesn't exercise the discard path we're testing.
	var removeErr error
	for range 100 {
		removeErr = tk.RemoveConnection("vendor")
		if removeErr == nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	require.NoError(t, removeErr, "RemoveConnection must observe the claim sentinel and remove it")

	// Wait for the AddConnection goroutine to finish — it should
	// return without error (the discard path returns nil since the
	// OAuth flow has effectively succeeded; the slot just isn't
	// ours). The connection must NOT be present afterwards.
	select {
	case <-addDone:
	case <-time.After(10 * time.Second):
		t.Fatal("AddConnection goroutine didn't terminate")
	}
	assert.Nil(t, tk.Status("vendor"),
		"after RemoveConnection-during-claim, the slot must remain empty — "+
			"the dial result was discarded, not silently re-installed")
}

// TestAddConnection_RemoveAndReAdd_DuringSlowDial_DoesNotCorruptSlot
// proves the pointer-identity check in installDialResult.
//
// Race scenario this test specifically exposes:
//
//	T1 AddConnection (slow dial 1) → claims S1 → dialing
//	RemoveConnection → deletes S1 from map
//	T2 AddConnection (slower dial 2) → claims S2 → dialing
//	T1's dial completes WHILE T2 is still claiming
//	   → installDialResult must compare *upstream pointers (S1 vs S2)
//	     and DISCARD T1's result, NOT install based on existing.claiming
//	     (which is true because S2 is still in flight).
//
// Without the identity check, T1 sees `existing.claiming == true` and
// installs its result into S2's slot, overwriting T2's claim. T2's
// own dial later finds the slot already live (claiming=false) and
// discards. Net effect: operator's most recent Connect is silently
// replaced by the previous Connect's result.
func TestAddConnection_RemoveAndReAdd_DuringSlowDial_DoesNotCorruptSlot(t *testing.T) {
	// T1's upstream: hangs ~600ms then returns 500.
	slowSrv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(600 * time.Millisecond):
			http.Error(w, "T1 deliberate", http.StatusInternalServerError)
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(slowSrv1.Close)

	// T2's upstream: hangs ~2s. T2 is STILL claiming when T1 installs.
	// T2 must outlast T1's dial so the install-time race fires.
	slowSrv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(2 * time.Second):
			http.Error(w, "T2 deliberate", http.StatusInternalServerError)
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(slowSrv2.Close)

	tk := New("primary")
	t.Cleanup(func() { _ = tk.Close() })

	// T1: claim slot, dial slowSrv1.
	t1Done := make(chan error, 1)
	go func() {
		t1Done <- tk.AddConnection("vendor", map[string]any{
			"endpoint":        slowSrv1.URL,
			"connection_name": "vendor",
			"connect_timeout": "5s",
			"call_timeout":    "5s",
		})
	}()
	time.Sleep(50 * time.Millisecond) // ensure T1 has claimed

	// Drop T1's claim from the map.
	require.NoError(t, tk.RemoveConnection("vendor"))

	// T2: claim slot, dial slowSrv2 (slower than T1).
	t2Done := make(chan error, 1)
	go func() {
		t2Done <- tk.AddConnection("vendor", map[string]any{
			"endpoint":        slowSrv2.URL,
			"connection_name": "vendor",
			"connect_timeout": "5s",
			"call_timeout":    "5s",
		})
	}()
	time.Sleep(50 * time.Millisecond) // ensure T2 has claimed and is dialing

	// At this moment: T1 is still dialing slowSrv1 (~500ms left).
	// T2 has just claimed and is dialing slowSrv2 (~2s left).
	// The slot holds S2 (claiming=true).

	// Wait for T1's dial to complete. With the pointer-identity check,
	// T1's installDialResult sees `t.connections[name] != claim_S1`
	// (it holds claim_S2) and discards. Without the check, T1 sees
	// `existing.claiming == true` and installs over S2.
	select {
	case <-t1Done:
	case <-time.After(15 * time.Second):
		t.Fatal("T1 AddConnection didn't terminate")
	}

	// Snapshot Status WHILE T2 is still claiming. After the
	// pointer-identity fix, the slot still holds T2's claim sentinel
	// (claiming=true, client=nil → Healthy=false, no toolNames). If
	// T1 had clobbered the slot with its install path, Status would
	// reflect a (transient) installed entry from T1's dial.
	mid := tk.Status("vendor")
	require.NotNil(t, mid, "T2's claim must still occupy the slot")
	assert.False(t, mid.Healthy,
		"slot must still be a claim sentinel — Healthy=true here means T1's stale dial corrupted T2's claim")
	assert.Empty(t, mid.Tools,
		"claim sentinel must have no tools — non-empty Tools means T1 installed against T2's slot")

	// Wait for T2's dial to complete (~1.5s remaining at this point).
	select {
	case <-t2Done:
	case <-time.After(15 * time.Second):
		t.Fatal("T2 AddConnection didn't terminate")
	}
}
