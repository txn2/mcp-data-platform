package middleware_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// testAuditStore captures audit events for assertion.
type testAuditStore struct {
	mu     sync.Mutex
	events []middleware.AuditEvent
}

func (s *testAuditStore) Log(_ context.Context, event middleware.AuditEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return nil
}

func (s *testAuditStore) Events() []middleware.AuditEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]middleware.AuditEvent{}, s.events...)
}

// testAuthenticator returns fixed user info.
type testAuthenticator struct {
	userInfo *middleware.UserInfo
}

func (a *testAuthenticator) Authenticate(_ context.Context) (*middleware.UserInfo, error) {
	return a.userInfo, nil
}

// testAuthorizer always authorizes with a fixed persona.
type testAuthorizer struct {
	persona string
}

func (a *testAuthorizer) IsAuthorized(_ context.Context, _ string, _ []string, _ string) (bool, string, string) {
	return true, a.persona, ""
}

// testToolkitLookup returns fixed toolkit metadata.
type testToolkitLookup struct {
	tools map[string]struct{ kind, name, conn string }
}

func (l *testToolkitLookup) GetToolkitForTool(toolName string) (string, string, string, bool) {
	if info, ok := l.tools[toolName]; ok {
		return info.kind, info.name, info.conn, true
	}
	return "", "", "", false
}

// connectClientServer creates an in-memory MCP client-server pair.
func connectClientServer(ctx context.Context, server *mcp.Server) (*mcp.ClientSession, error) {
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, t1, nil); err != nil {
		return nil, err
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	return client.Connect(ctx, t2, nil)
}

// TestMiddlewareChain_AuditReceivesPlatformContext is an integration test that
// wires up MCPToolCallMiddleware and MCPAuditMiddleware through a real
// mcp.Server using AddReceivingMiddleware, then makes a tool call and verifies
// the audit store receives a complete event with all PlatformContext fields.
//
// This test exists because unit tests that manually construct PlatformContext
// cannot catch middleware ordering bugs where context.WithValue in one
// middleware is invisible to another middleware due to incorrect chaining.
func TestMiddlewareChain_AuditReceivesPlatformContext(t *testing.T) {
	auditStore := &testAuditStore{}
	authenticator := &testAuthenticator{
		userInfo: &middleware.UserInfo{
			UserID: "test-user-42",
			Email:  "test@example.com",
			Roles:  []string{"analyst"},
		},
	}
	authorizer := &testAuthorizer{persona: "data-analyst"}
	toolkitLookup := &testToolkitLookup{
		tools: map[string]struct{ kind, name, conn string }{
			"trino_query": {kind: "trino", name: "production", conn: "prod-trino"},
		},
	}

	// Create server
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test-platform",
		Version: "v0.0.1",
	}, nil)

	// Register a test tool
	server.AddTool(&mcp.Tool{
		Name:        "trino_query",
		Description: "Test tool",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"sql":{"type":"string"}}}`),
	}, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "query result"}},
		}, nil
	})

	// Add middleware in the CORRECT order (innermost first, outermost last).
	// This matches the order in platform.go's finalizeSetup().
	// MCPAuditMiddleware is added FIRST (innermost).
	// MCPToolCallMiddleware is added SECOND (outermost — runs first).
	server.AddReceivingMiddleware(middleware.MCPAuditMiddleware(auditStore))
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(authenticator, authorizer, toolkitLookup))

	// Connect client
	ctx := context.Background()
	session, err := connectClientServer(ctx, server)
	if err != nil {
		t.Fatalf("connecting client: %v", err)
	}
	defer session.Close()

	// Call the tool
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "trino_query",
		Arguments: map[string]any{"sql": "SELECT 1"},
	})
	if err != nil {
		t.Fatalf("calling tool: %v", err)
	}

	// Verify tool returned successfully
	if result.IsError {
		t.Fatalf("tool returned error: %v", result.Content)
	}

	// Wait for async audit goroutine
	deadline := time.Now().Add(2 * time.Second)
	var events []middleware.AuditEvent
	for time.Now().Before(deadline) {
		events = auditStore.Events()
		if len(events) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if len(events) == 0 {
		t.Fatal("audit store received no events — middleware chain is broken")
	}

	event := events[0]

	// Assert ALL PlatformContext fields were propagated to the audit event
	if event.UserID != "test-user-42" {
		t.Errorf("UserID = %q, want %q", event.UserID, "test-user-42")
	}
	if event.UserEmail != "test@example.com" {
		t.Errorf("UserEmail = %q, want %q", event.UserEmail, "test@example.com")
	}
	if event.Persona != "data-analyst" {
		t.Errorf("Persona = %q, want %q", event.Persona, "data-analyst")
	}
	if event.ToolName != "trino_query" {
		t.Errorf("ToolName = %q, want %q", event.ToolName, "trino_query")
	}
	if event.ToolkitKind != "trino" {
		t.Errorf("ToolkitKind = %q, want %q", event.ToolkitKind, "trino")
	}
	if event.ToolkitName != "production" {
		t.Errorf("ToolkitName = %q, want %q", event.ToolkitName, "production")
	}
	if event.Connection != "prod-trino" {
		t.Errorf("Connection = %q, want %q", event.Connection, "prod-trino")
	}
	if event.RequestID == "" {
		t.Error("RequestID is empty")
	}
	if !event.Success {
		t.Error("Success = false, want true")
	}
	if event.DurationMS < 0 {
		t.Errorf("DurationMS = %d, want >= 0", event.DurationMS)
	}
}

// TestMiddlewareChain_WrongOrder_AuditGetsNilContext proves that if middleware
// is added in the WRONG order (auth first, audit second — making audit
// outermost), the audit middleware gets nil PlatformContext and skips logging.
// This is a regression test for the bug fixed in v0.12.2.
func TestMiddlewareChain_WrongOrder_AuditGetsNilContext(t *testing.T) {
	auditStore := &testAuditStore{}
	authenticator := &testAuthenticator{
		userInfo: &middleware.UserInfo{
			UserID: "test-user",
			Roles:  []string{"analyst"},
		},
	}
	authorizer := &testAuthorizer{persona: "analyst"}
	toolkitLookup := &testToolkitLookup{
		tools: map[string]struct{ kind, name, conn string }{
			"test_tool": {kind: "test", name: "test", conn: "test"},
		},
	}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test-platform",
		Version: "v0.0.1",
	}, nil)

	server.AddTool(&mcp.Tool{
		Name:        "test_tool",
		Description: "Test tool",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "ok"}},
		}, nil
	})

	// WRONG ORDER: auth first (innermost), audit second (outermost).
	// This is what the code did before the fix.
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(authenticator, authorizer, toolkitLookup))
	server.AddReceivingMiddleware(middleware.MCPAuditMiddleware(auditStore))

	ctx := context.Background()
	session, err := connectClientServer(ctx, server)
	if err != nil {
		t.Fatalf("connecting client: %v", err)
	}
	defer session.Close()

	_, err = session.CallTool(ctx, &mcp.CallToolParams{
		Name: "test_tool",
	})
	if err != nil {
		t.Fatalf("calling tool: %v", err)
	}

	// Wait briefly for any async audit goroutine
	time.Sleep(200 * time.Millisecond)

	events := auditStore.Events()
	if len(events) != 0 {
		t.Errorf("expected 0 audit events with wrong middleware order, got %d", len(events))
	}
}
