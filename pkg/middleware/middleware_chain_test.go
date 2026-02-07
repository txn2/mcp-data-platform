package middleware_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	"github.com/txn2/mcp-data-platform/pkg/storage"
	"github.com/txn2/mcp-data-platform/pkg/tuning"
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

// --- Mock providers for integration tests ---

// mockSemanticProvider returns canned semantic metadata.
type mockSemanticProvider struct {
	tableContext   *semantic.TableContext
	columnsCtx    map[string]*semantic.ColumnContext
	searchResults []semantic.TableSearchResult
}

func (m *mockSemanticProvider) Name() string { return "mock" }
func (m *mockSemanticProvider) GetTableContext(_ context.Context, _ semantic.TableIdentifier) (*semantic.TableContext, error) {
	return m.tableContext, nil
}
func (m *mockSemanticProvider) GetColumnContext(_ context.Context, _ semantic.ColumnIdentifier) (*semantic.ColumnContext, error) {
	return nil, nil
}
func (m *mockSemanticProvider) GetColumnsContext(_ context.Context, _ semantic.TableIdentifier) (map[string]*semantic.ColumnContext, error) {
	return m.columnsCtx, nil
}
func (m *mockSemanticProvider) GetLineage(_ context.Context, _ semantic.TableIdentifier, _ semantic.LineageDirection, _ int) (*semantic.LineageInfo, error) {
	return nil, nil
}
func (m *mockSemanticProvider) GetGlossaryTerm(_ context.Context, _ string) (*semantic.GlossaryTerm, error) {
	return nil, nil
}
func (m *mockSemanticProvider) SearchTables(_ context.Context, _ semantic.SearchFilter) ([]semantic.TableSearchResult, error) {
	return m.searchResults, nil
}
func (m *mockSemanticProvider) Close() error { return nil }

// mockQueryProvider returns canned query availability.
type mockQueryProvider struct {
	availability *query.TableAvailability
}

func (m *mockQueryProvider) Name() string { return "mock" }
func (m *mockQueryProvider) ResolveTable(_ context.Context, _ string) (*query.TableIdentifier, error) {
	return nil, nil
}
func (m *mockQueryProvider) GetTableAvailability(_ context.Context, _ string) (*query.TableAvailability, error) {
	return m.availability, nil
}
func (m *mockQueryProvider) GetQueryExamples(_ context.Context, _ string) ([]query.QueryExample, error) {
	return nil, nil
}
func (m *mockQueryProvider) GetExecutionContext(_ context.Context, _ []string) (*query.ExecutionContext, error) {
	return nil, nil
}
func (m *mockQueryProvider) GetTableSchema(_ context.Context, _ query.TableIdentifier) (*query.TableSchema, error) {
	return nil, nil
}
func (m *mockQueryProvider) Close() error { return nil }

// denyAuthorizer always denies access.
type denyAuthorizer struct{}

func (a *denyAuthorizer) IsAuthorized(_ context.Context, _ string, _ []string, _ string) (bool, string, string) {
	return false, "", "access denied by test policy"
}

// TestMiddlewareChain_EnrichmentAddsSemanticContext verifies that the semantic
// enrichment middleware actually appends semantic_context to Trino tool results
// when wired through a real mcp.Server. This tests Feature 1 (Semantic-First)
// and Feature 2 (Cross-Injection Trino→DataHub).
func TestMiddlewareChain_EnrichmentAddsSemanticContext(t *testing.T) {
	semProvider := &mockSemanticProvider{
		tableContext: &semantic.TableContext{
			URN:         "urn:li:dataset:(urn:li:dataPlatform:trino,catalog.schema.orders,PROD)",
			Description: "Customer order data",
			Owners: []semantic.Owner{
				{Name: "data-team", Type: semantic.OwnerTypeGroup},
			},
			Tags: []string{"pii", "production"},
			Deprecation: &semantic.Deprecation{
				Deprecated: true,
				Note:       "Use orders_v2 instead",
			},
		},
	}

	authenticator := &testAuthenticator{
		userInfo: &middleware.UserInfo{
			UserID: "test-user",
			Roles:  []string{"analyst"},
		},
	}
	authorizer := &testAuthorizer{persona: "analyst"}
	toolkitLookup := &testToolkitLookup{
		tools: map[string]struct{ kind, name, conn string }{
			"trino_describe_table": {kind: "trino", name: "prod", conn: "prod-trino"},
		},
	}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test-platform",
		Version: "v0.0.1",
	}, nil)

	server.AddTool(&mcp.Tool{
		Name:        "trino_describe_table",
		Description: "Describe a table",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"catalog":{"type":"string"},"schema":{"type":"string"},"table":{"type":"string"}}}`),
	}, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "column1 INT, column2 VARCHAR"}},
		}, nil
	})

	// Middleware order (innermost first): enrichment, then auth (outermost)
	server.AddReceivingMiddleware(middleware.MCPSemanticEnrichmentMiddleware(
		semProvider, nil, nil,
		middleware.EnrichmentConfig{EnrichTrinoResults: true},
	))
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(authenticator, authorizer, toolkitLookup))

	ctx := context.Background()
	session, err := connectClientServer(ctx, server)
	if err != nil {
		t.Fatalf("connecting client: %v", err)
	}
	defer session.Close()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "trino_describe_table",
		Arguments: map[string]any{"catalog": "catalog", "schema": "schema", "table": "orders"},
	})
	if err != nil {
		t.Fatalf("calling tool: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %v", result.Content)
	}

	// Result should have at least 2 content items: original + enrichment
	if len(result.Content) < 2 {
		t.Fatalf("expected at least 2 content items, got %d", len(result.Content))
	}

	// Find the enrichment content
	var enrichmentFound bool
	for _, content := range result.Content {
		if tc, ok := content.(*mcp.TextContent); ok {
			if strings.Contains(tc.Text, "semantic_context") {
				enrichmentFound = true

				var enrichment map[string]any
				if err := json.Unmarshal([]byte(tc.Text), &enrichment); err != nil {
					t.Fatalf("enrichment JSON parse error: %v", err)
				}

				semCtx, ok := enrichment["semantic_context"].(map[string]any)
				if !ok {
					t.Fatal("semantic_context not found or wrong type")
				}

				// Verify key fields from the mock provider
				if desc, _ := semCtx["description"].(string); desc != "Customer order data" {
					t.Errorf("description = %q, want 'Customer order data'", desc)
				}
				if owners, ok := semCtx["owners"].([]any); !ok || len(owners) == 0 {
					t.Error("owners missing or empty")
				}
				if tags, ok := semCtx["tags"].([]any); !ok || len(tags) == 0 {
					t.Error("tags missing or empty")
				}
				if dep, ok := semCtx["deprecation"].(map[string]any); !ok || dep["deprecated"] != true {
					t.Error("deprecation not propagated")
				}
			}
		}
	}

	if !enrichmentFound {
		t.Error("semantic_context enrichment not found in response")
	}
}

// TestMiddlewareChain_EnrichmentAddsQueryContext verifies that DataHub tool
// results get enriched with query_context from the QueryProvider (Trino).
// This tests Feature 2 (Cross-Injection DataHub→Trino direction).
func TestMiddlewareChain_EnrichmentAddsQueryContext(t *testing.T) {
	rowCount := int64(1500000)
	queryProv := &mockQueryProvider{
		availability: &query.TableAvailability{
			Available:     true,
			QueryTable:    "catalog.schema.orders",
			Connection:    "prod-trino",
			EstimatedRows: &rowCount,
		},
	}

	authenticator := &testAuthenticator{
		userInfo: &middleware.UserInfo{
			UserID: "test-user",
			Roles:  []string{"analyst"},
		},
	}
	authorizer := &testAuthorizer{persona: "analyst"}
	toolkitLookup := &testToolkitLookup{
		tools: map[string]struct{ kind, name, conn string }{
			"datahub_get_entity": {kind: "datahub", name: "primary", conn: "datahub-gms"},
		},
	}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test-platform",
		Version: "v0.0.1",
	}, nil)

	// The tool returns a JSON body with a URN — enrichment should pick it up
	server.AddTool(&mcp.Tool{
		Name:        "datahub_get_entity",
		Description: "Get entity details",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"urn":{"type":"string"}}}`),
	}, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		result := map[string]any{
			"urn":      "urn:li:dataset:(urn:li:dataPlatform:trino,catalog.schema.orders,PROD)",
			"name":     "orders",
			"platform": "trino",
		}
		data, _ := json.Marshal(result)
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: string(data)}},
		}, nil
	})

	// Middleware order: enrichment (innermost), auth (outermost)
	server.AddReceivingMiddleware(middleware.MCPSemanticEnrichmentMiddleware(
		nil, queryProv, nil,
		middleware.EnrichmentConfig{EnrichDataHubResults: true},
	))
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(authenticator, authorizer, toolkitLookup))

	ctx := context.Background()
	session, err := connectClientServer(ctx, server)
	if err != nil {
		t.Fatalf("connecting client: %v", err)
	}
	defer session.Close()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "datahub_get_entity",
		Arguments: map[string]any{"urn": "urn:li:dataset:(urn:li:dataPlatform:trino,catalog.schema.orders,PROD)"},
	})
	if err != nil {
		t.Fatalf("calling tool: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error")
	}

	// Look for query_context in enrichment
	var queryContextFound bool
	for _, content := range result.Content {
		if tc, ok := content.(*mcp.TextContent); ok {
			if strings.Contains(tc.Text, "query_context") {
				queryContextFound = true

				var enrichment map[string]any
				if err := json.Unmarshal([]byte(tc.Text), &enrichment); err != nil {
					t.Fatalf("enrichment JSON parse error: %v", err)
				}

				qCtx, ok := enrichment["query_context"].(map[string]any)
				if !ok {
					t.Fatal("query_context not found or wrong type")
				}

				// Should have at least one URN entry
				if len(qCtx) == 0 {
					t.Error("query_context is empty")
				}
			}
		}
	}

	if !queryContextFound {
		t.Error("query_context enrichment not found in response")
	}
}

// TestMiddlewareChain_DefaultDenyPersona verifies that when the authorizer
// denies access, the tool handler is NOT called and an error result is
// returned. This tests Feature 10 (Default-Deny).
func TestMiddlewareChain_DefaultDenyPersona(t *testing.T) {
	handlerCalled := false

	authenticator := &testAuthenticator{
		userInfo: &middleware.UserInfo{
			UserID: "unknown-user",
			Roles:  []string{"unknown-role"},
		},
	}
	authorizer := &denyAuthorizer{}
	toolkitLookup := &testToolkitLookup{
		tools: map[string]struct{ kind, name, conn string }{
			"trino_query": {kind: "trino", name: "prod", conn: "prod-trino"},
		},
	}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test-platform",
		Version: "v0.0.1",
	}, nil)

	server.AddTool(&mcp.Tool{
		Name:        "trino_query",
		Description: "Execute query",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"sql":{"type":"string"}}}`),
	}, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		handlerCalled = true
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "should not reach here"}},
		}, nil
	})

	// Only auth middleware (outermost)
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(authenticator, authorizer, toolkitLookup))

	ctx := context.Background()
	session, err := connectClientServer(ctx, server)
	if err != nil {
		t.Fatalf("connecting client: %v", err)
	}
	defer session.Close()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "trino_query",
		Arguments: map[string]any{"sql": "SELECT * FROM secrets"},
	})
	if err != nil {
		t.Fatalf("calling tool: %v", err)
	}

	// Should be an error result
	if !result.IsError {
		t.Error("expected error result for denied access, got success")
	}

	// Error message should mention authorization
	if len(result.Content) > 0 {
		if tc, ok := result.Content[0].(*mcp.TextContent); ok {
			if !strings.Contains(tc.Text, "not authorized") {
				t.Errorf("error message %q does not contain 'not authorized'", tc.Text)
			}
		}
	}

	// Handler should NOT have been called (fail-closed)
	if handlerCalled {
		t.Error("tool handler was called despite authorization denial — fail-open bug")
	}
}

// TestMiddlewareChain_FullStack verifies the complete middleware stack works
// together: enrichment + rules + audit + auth/authz. This tests Features
// 1, 2, 5, 6 together through the real wiring.
func TestMiddlewareChain_FullStack(t *testing.T) {
	semProvider := &mockSemanticProvider{
		tableContext: &semantic.TableContext{
			Description: "Test table",
			Owners:      []semantic.Owner{{Name: "team-data", Type: semantic.OwnerTypeGroup}},
			Tags:        []string{"verified"},
		},
	}

	auditStore := &testAuditStore{}
	authenticator := &testAuthenticator{
		userInfo: &middleware.UserInfo{
			UserID: "full-stack-user",
			Email:  "fullstack@example.com",
			Roles:  []string{"analyst"},
		},
	}
	authorizer := &testAuthorizer{persona: "data-analyst"}
	toolkitLookup := &testToolkitLookup{
		tools: map[string]struct{ kind, name, conn string }{
			"trino_query": {kind: "trino", name: "production", conn: "prod-trino"},
		},
	}

	ruleEngine := tuning.NewRuleEngine(&tuning.Rules{
		RequireDataHubCheck: true,
		WarnOnDeprecated:    true,
	})
	hintManager := tuning.NewHintManager()

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test-platform",
		Version: "v0.0.1",
	}, nil)

	server.AddTool(&mcp.Tool{
		Name:        "trino_query",
		Description: "Execute SQL query",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"sql":{"type":"string"}}}`),
	}, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "query result: 42"}},
		}, nil
	})

	// Add ALL middleware in correct order (innermost first):
	// 1. Enrichment (innermost)
	server.AddReceivingMiddleware(middleware.MCPSemanticEnrichmentMiddleware(
		semProvider, nil, nil,
		middleware.EnrichmentConfig{EnrichTrinoResults: true},
	))
	// 2. Rules
	server.AddReceivingMiddleware(middleware.MCPRuleEnforcementMiddleware(ruleEngine, hintManager))
	// 3. Audit
	server.AddReceivingMiddleware(middleware.MCPAuditMiddleware(auditStore))
	// 4. Auth/Authz (outermost)
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(authenticator, authorizer, toolkitLookup))

	ctx := context.Background()
	session, err := connectClientServer(ctx, server)
	if err != nil {
		t.Fatalf("connecting client: %v", err)
	}
	defer session.Close()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "trino_query",
		Arguments: map[string]any{"sql": "SELECT count(*) FROM catalog.schema.orders"},
	})
	if err != nil {
		t.Fatalf("calling tool: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error")
	}

	// Verify response has enrichment (semantic context from mock)
	var hasSemanticContext bool
	var hasOriginalResult bool
	for _, content := range result.Content {
		if tc, ok := content.(*mcp.TextContent); ok {
			if strings.Contains(tc.Text, "semantic_context") {
				hasSemanticContext = true
			}
			if strings.Contains(tc.Text, "query result: 42") {
				hasOriginalResult = true
			}
		}
	}
	if !hasOriginalResult {
		t.Error("original tool result not found in response")
	}
	if !hasSemanticContext {
		t.Error("semantic_context enrichment not found in full stack response")
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
		t.Fatal("audit store received no events in full stack test")
	}

	event := events[0]

	// Verify audit event has all fields populated through the chain
	if event.UserID != "full-stack-user" {
		t.Errorf("audit UserID = %q, want 'full-stack-user'", event.UserID)
	}
	if event.UserEmail != "fullstack@example.com" {
		t.Errorf("audit UserEmail = %q, want 'fullstack@example.com'", event.UserEmail)
	}
	if event.Persona != "data-analyst" {
		t.Errorf("audit Persona = %q, want 'data-analyst'", event.Persona)
	}
	if event.ToolName != "trino_query" {
		t.Errorf("audit ToolName = %q, want 'trino_query'", event.ToolName)
	}
	if event.ToolkitKind != "trino" {
		t.Errorf("audit ToolkitKind = %q, want 'trino'", event.ToolkitKind)
	}
	if event.ToolkitName != "production" {
		t.Errorf("audit ToolkitName = %q, want 'production'", event.ToolkitName)
	}
	if event.Connection != "prod-trino" {
		t.Errorf("audit Connection = %q, want 'prod-trino'", event.Connection)
	}
	if !event.Success {
		t.Error("audit Success = false, want true")
	}
	if event.RequestID == "" {
		t.Error("audit RequestID is empty")
	}
}

// TestMiddlewareChain_AuditResponseSize verifies that when the full middleware
// chain processes a tool call, the audit event contains ResponseChars > 0.
func TestMiddlewareChain_AuditResponseSize(t *testing.T) {
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
			"trino_query": {kind: "trino", name: "production", conn: "prod"},
		},
	}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test-platform",
		Version: "v0.0.1",
	}, nil)

	server.AddTool(&mcp.Tool{
		Name:        "trino_query",
		Description: "Execute query",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"sql":{"type":"string"}}}`),
	}, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "hello world response"}},
		}, nil
	})

	// Middleware: audit (innermost), auth (outermost)
	server.AddReceivingMiddleware(middleware.MCPAuditMiddleware(auditStore))
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(authenticator, authorizer, toolkitLookup))

	ctx := context.Background()
	session, err := connectClientServer(ctx, server)
	if err != nil {
		t.Fatalf("connecting client: %v", err)
	}
	defer session.Close()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "trino_query",
		Arguments: map[string]any{"sql": "SELECT 1"},
	})
	if err != nil {
		t.Fatalf("calling tool: %v", err)
	}
	if result.IsError {
		t.Fatal("tool returned error")
	}

	// Wait for async audit
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
		t.Fatal("audit store received no events")
	}

	event := events[0]
	// "hello world response" = 20 chars
	if event.ResponseChars != 20 {
		t.Errorf("ResponseChars = %d, want 20", event.ResponseChars)
	}
	if event.ResponseTokenEstimate != 5 {
		t.Errorf("ResponseTokenEstimate = %d, want 5", event.ResponseTokenEstimate)
	}
}

// Ensure mock types satisfy interfaces (compile-time check).
var (
	_ semantic.Provider     = (*mockSemanticProvider)(nil)
	_ query.Provider        = (*mockQueryProvider)(nil)
	_ middleware.Authorizer = (*denyAuthorizer)(nil)
)

// Suppress unused import warnings for storage (used in EnrichmentConfig).
var _ storage.Provider = nil
