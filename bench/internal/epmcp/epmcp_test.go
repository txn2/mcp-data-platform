package epmcp

// The b0 server is exercised end to end: build from the generated tier-0
// spec, front the real fixture service, connect a real MCP client over
// streamable HTTP, and drive reads, writes, and error paths through the
// generated tools.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/bench/internal/apigen"
	"github.com/txn2/mcp-data-platform/bench/internal/apisvc"
)

// newSession stands up fixture service + generated b0 server + client.
func newSession(t *testing.T, tier int) *mcp.ClientSession {
	t.Helper()
	fixture := httptest.NewServer(apisvc.New(apisvc.Options{APIKey: "k"}))
	t.Cleanup(fixture.Close)
	spec, err := apigen.BuildCatalog().SpecJSON(tier)
	if err != nil {
		t.Fatal(err)
	}
	server, err := BuildServer(spec, Options{TargetBaseURL: fixture.URL, APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	t.Cleanup(func() {
		ts.CloseClientConnections()
		ts.Close()
	})
	client := mcp.NewClient(&mcp.Implementation{Name: "epmcp-test", Version: "0.0.1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: ts.URL}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// callTool invokes one tool and returns its text and error flag.
func callTool(t *testing.T, s *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	text := ""
	if len(res.Content) > 0 {
		if tc, ok := res.Content[0].(*mcp.TextContent); ok {
			text = tc.Text
		}
	}
	return text, res.IsError
}

// TestToolPerOperation asserts the tool count equals the tier's operation
// count, one tool per operation with the operationId as name.
func TestToolPerOperation(t *testing.T) {
	session := newSession(t, apigen.Tier0)
	var tools []*mcp.Tool
	for tool, err := range session.Tools(context.Background(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		tools = append(tools, tool)
	}
	want := len(apigen.BuildCatalog().TierOperations(apigen.Tier0))
	if len(tools) != want {
		t.Fatalf("b0 exposes %d tools, want %d", len(tools), want)
	}
	seen := map[string]bool{}
	for _, tool := range tools {
		seen[tool.Name] = true
	}
	for _, id := range []string{"get_customer", "list_orders", "create_order", "cancel_order", "list_billing_invoices"} {
		if !seen[id] {
			t.Errorf("tool %s missing", id)
		}
	}
}

// TestProxiedReadWrite drives a read, a parameterized read, and a
// mutation through the generated tools against the live fixture.
func TestProxiedReadWrite(t *testing.T) {
	session := newSession(t, apigen.Tier0)
	// Path-parameter read.
	text, isErr := callTool(t, session, "get_customer", map[string]any{"id": 10})
	if isErr {
		t.Fatalf("get_customer errored: %s", text)
	}
	var cust struct {
		ID   int    `json:"id"`
		Tier string `json:"tier"`
	}
	if err := json.Unmarshal([]byte(text), &cust); err != nil || cust.ID != 10 {
		t.Fatalf("get_customer returned %q", text)
	}
	// Query-parameter read.
	text, isErr = callTool(t, session, "list_orders", map[string]any{"status": "completed", "page_size": 5})
	if isErr {
		t.Fatalf("list_orders errored: %s", text)
	}
	var page struct {
		Items      []map[string]any `json:"items"`
		NextCursor string           `json:"next_cursor"`
	}
	if err := json.Unmarshal([]byte(text), &page); err != nil || len(page.Items) != 5 || page.NextCursor == "" {
		t.Fatalf("list_orders returned %q", text)
	}
	// Body mutation.
	text, isErr = callTool(t, session, "create_order", map[string]any{"customer_id": 7, "amount": 12345})
	if isErr {
		t.Fatalf("create_order errored: %s", text)
	}
	var created struct {
		Status string `json:"status"`
		Amount int64  `json:"amount"`
	}
	if err := json.Unmarshal([]byte(text), &created); err != nil || created.Status != "pending" || created.Amount != 12345 {
		t.Fatalf("create_order returned %q", text)
	}
}

// TestErrorMapping asserts upstream 4xx responses surface as tool errors
// with the status visible, and a missing path parameter fails before any
// call.
func TestErrorMapping(t *testing.T) {
	session := newSession(t, apigen.Tier0)
	text, isErr := callTool(t, session, "get_customer", map[string]any{"id": 999999})
	if !isErr || !strings.Contains(text, "HTTP 404") {
		t.Errorf("unknown id: isErr=%v text=%q", isErr, text)
	}
	text, isErr = callTool(t, session, "get_customer", map[string]any{})
	if !isErr || !strings.Contains(text, "path parameter") {
		t.Errorf("missing id: isErr=%v text=%q", isErr, text)
	}
	text, isErr = callTool(t, session, "list_customers", map[string]any{"region": "Central"})
	if !isErr || !strings.Contains(text, "HTTP 400") {
		t.Errorf("bad enum: isErr=%v text=%q", isErr, text)
	}
}

// TestBuildServerScales builds the full tier-2 server: 2,503 tools
// register without error.
func TestBuildServerScales(t *testing.T) {
	spec, err := apigen.BuildCatalog().SpecJSON(apigen.Tier2)
	if err != nil {
		t.Fatal(err)
	}
	server, err := BuildServer(spec, Options{TargetBaseURL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	if server == nil {
		t.Fatal("nil server")
	}
}
