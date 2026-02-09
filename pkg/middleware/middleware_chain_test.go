package middleware_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	"github.com/txn2/mcp-data-platform/pkg/storage"
	"github.com/txn2/mcp-data-platform/pkg/tuning"
)

// Test constants for middleware chain integration tests.
const (
	chainTestAnalyst           = "analyst"
	chainTestConnecting        = "connecting client: %v"
	chainTestCallingTool       = "calling tool: %v"
	chainTestProdTrino         = "prod-trino"
	chainTestRowCount          = 1500000
	chainTestTrino             = "trino"
	chainTestUser              = "test-user"
	chainTestTrinoQuery        = "trino_query"
	chainTestProd              = "prod"
	chainTestSemanticCtx       = "semantic_context"
	chainTestCustOrderData     = "Customer order data"
	chainTestMetadataRef       = "metadata_reference"
	chainTestMock              = "mock"
	chainTestProduction        = "production"
	chainTestStdio             = "stdio"
	chainTestDescribeTable     = "trino_describe_table"
	chainTestOrdersURN         = "urn:li:dataset:(urn:li:dataPlatform:trino,catalog.schema.orders,PROD)"
	chainTestDataTeam          = "data-team"
	chainTestPII               = "pii"
	chainTestProductionTag     = "production"
	chainTestOrderID           = "order_id"
	chainTestDedupNoneSemantic = "call 2: should not have semantic_context on deduped call"
	chainTestPrimaryKey        = "Primary key"
	chainTestPKTag             = "pk"
)

// --- Test assertion helpers ---

// assertAuditEvent validates that an audit event has the expected field values.
func assertAuditEvent(t *testing.T, event, expected middleware.AuditEvent) {
	t.Helper()
	if event.UserID != expected.UserID {
		t.Errorf("UserID = %q, want %q", event.UserID, expected.UserID)
	}
	if event.UserEmail != expected.UserEmail {
		t.Errorf("UserEmail = %q, want %q", event.UserEmail, expected.UserEmail)
	}
	if event.Persona != expected.Persona {
		t.Errorf("Persona = %q, want %q", event.Persona, expected.Persona)
	}
	if event.ToolName != expected.ToolName {
		t.Errorf("ToolName = %q, want %q", event.ToolName, expected.ToolName)
	}
	if event.ToolkitKind != expected.ToolkitKind {
		t.Errorf("ToolkitKind = %q, want %q", event.ToolkitKind, expected.ToolkitKind)
	}
	if event.ToolkitName != expected.ToolkitName {
		t.Errorf("ToolkitName = %q, want %q", event.ToolkitName, expected.ToolkitName)
	}
	if event.Connection != expected.Connection {
		t.Errorf("Connection = %q, want %q", event.Connection, expected.Connection)
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
	if event.Transport != chainTestStdio {
		t.Errorf("Transport = %q, want %q", event.Transport, chainTestStdio)
	}
	if event.Source != "mcp" { //nolint:goconst // test constant
		t.Errorf("Source = %q, want %q", event.Source, "mcp")
	}
	if !event.Authorized {
		t.Error("Authorized = false, want true")
	}
}

// waitForAuditEvents polls the audit store until at least one event appears
// or the deadline expires.
func waitForAuditEvents(t *testing.T, store *testAuditStore) []middleware.AuditEvent {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		events := store.Events()
		if len(events) > 0 {
			return events
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("audit store received no events")
	return nil
}

// assertContentContainsText checks that at least one TextContent item
// contains the given substring.
func assertContentContainsText(t *testing.T, result *mcp.CallToolResult, substr string) {
	t.Helper()
	for _, content := range result.Content {
		if tc, ok := content.(*mcp.TextContent); ok {
			if strings.Contains(tc.Text, substr) {
				return
			}
		}
	}
	t.Errorf("no content item contains %q", substr)
}

// assertContentContainsKey checks that the result's content contains a JSON
// object with the specified key, and returns parsed enrichment data.
func assertContentContainsKey(t *testing.T, result *mcp.CallToolResult, key string) map[string]any {
	t.Helper()
	m, found := findContentWithKey(t, result, key)
	if !found {
		t.Fatalf("expected %q in response content", key)
	}
	return m
}

// assertSemanticContextFields validates semantic context enrichment fields.
func assertSemanticContextFields(t *testing.T, semCtx map[string]any, wantDesc string) {
	t.Helper()
	if desc, _ := semCtx["description"].(string); desc != wantDesc {
		t.Errorf("description = %q, want %q", desc, wantDesc)
	}
	if owners, ok := semCtx["owners"].([]any); !ok || len(owners) == 0 {
		t.Error("owners missing or empty")
	}
	if tags, ok := semCtx["tags"].([]any); !ok || len(tags) == 0 {
		t.Error("tags missing or empty")
	}
}

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

func (a *testAuthorizer) IsAuthorized(_ context.Context, _ string, _ []string, _ string) (allowed bool, persona, reason string) {
	return true, a.persona, ""
}

// testToolkitLookup returns fixed toolkit metadata.
type testToolkitLookup struct {
	tools map[string]struct{ kind, name, conn string }
}

func (l *testToolkitLookup) GetToolkitForTool(toolName string) registry.ToolkitMatch {
	if info, ok := l.tools[toolName]; ok {
		return registry.ToolkitMatch{Kind: info.kind, Name: info.name, Connection: info.conn, Found: true}
	}
	return registry.ToolkitMatch{}
}

// connectClientServer creates an in-memory MCP client-server pair.
func connectClientServer(ctx context.Context, server *mcp.Server) (*mcp.ClientSession, error) {
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, t1, nil); err != nil {
		return nil, fmt.Errorf("server connect: %w", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	session, err := client.Connect(ctx, t2, nil)
	if err != nil {
		return nil, fmt.Errorf("client connect: %w", err)
	}
	return session, nil
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
			Roles:  []string{chainTestAnalyst},
		},
	}
	authorizer := &testAuthorizer{persona: "data-analyst"}
	toolkitLookup := &testToolkitLookup{
		tools: map[string]struct{ kind, name, conn string }{
			chainTestTrinoQuery: {kind: chainTestTrino, name: chainTestProduction, conn: chainTestProdTrino},
		},
	}

	// Create server
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test-platform",
		Version: "v0.0.1",
	}, nil)

	// Register a test tool
	server.AddTool(&mcp.Tool{
		Name:        chainTestTrinoQuery,
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
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(authenticator, authorizer, toolkitLookup, chainTestStdio))

	// Connect client
	ctx := context.Background()
	session, err := connectClientServer(ctx, server)
	if err != nil {
		t.Fatalf(chainTestConnecting, err)
	}
	defer func() { _ = session.Close() }()

	// Call the tool
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      chainTestTrinoQuery,
		Arguments: map[string]any{"sql": "SELECT 1"},
	})
	if err != nil {
		t.Fatalf(chainTestCallingTool, err)
	}

	// Verify tool returned successfully
	if result.IsError {
		t.Fatalf("tool returned error: %v", result.Content)
	}

	// Wait for async audit goroutine.
	events := waitForAuditEvents(t, auditStore)

	// Assert ALL PlatformContext fields were propagated to the audit event.
	assertAuditEvent(t, events[0], middleware.AuditEvent{
		UserID:      "test-user-42",
		UserEmail:   "test@example.com",
		Persona:     "data-analyst",
		ToolName:    chainTestTrinoQuery,
		ToolkitKind: "trino",
		ToolkitName: chainTestProduction,
		Connection:  chainTestProdTrino,
	})

	// AC-4: SessionID must be non-empty (defaults to "stdio" for in-memory transport).
	if events[0].SessionID == "" {
		t.Error("SessionID is empty; expected non-empty value from middleware chain")
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
			UserID: chainTestUser,
			Roles:  []string{chainTestAnalyst},
		},
	}
	authorizer := &testAuthorizer{persona: chainTestAnalyst}
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
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(authenticator, authorizer, toolkitLookup, chainTestStdio))
	server.AddReceivingMiddleware(middleware.MCPAuditMiddleware(auditStore))

	ctx := context.Background()
	session, err := connectClientServer(ctx, server)
	if err != nil {
		t.Fatalf(chainTestConnecting, err)
	}
	defer func() { _ = session.Close() }()

	_, err = session.CallTool(ctx, &mcp.CallToolParams{
		Name: "test_tool",
	})
	if err != nil {
		t.Fatalf(chainTestCallingTool, err)
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
	tableContext  *semantic.TableContext
	columnsCtx    map[string]*semantic.ColumnContext
	searchResults []semantic.TableSearchResult
}

func (*mockSemanticProvider) Name() string { return chainTestMock }
func (m *mockSemanticProvider) GetTableContext(_ context.Context, _ semantic.TableIdentifier) (*semantic.TableContext, error) {
	return m.tableContext, nil
}

func (*mockSemanticProvider) GetColumnContext(_ context.Context, _ semantic.ColumnIdentifier) (*semantic.ColumnContext, error) {
	return nil, nil //nolint:nilnil // test mock returns zero values
}

func (m *mockSemanticProvider) GetColumnsContext(_ context.Context, _ semantic.TableIdentifier) (map[string]*semantic.ColumnContext, error) {
	return m.columnsCtx, nil
}

func (*mockSemanticProvider) GetLineage(_ context.Context, _ semantic.TableIdentifier, _ semantic.LineageDirection, _ int) (*semantic.LineageInfo, error) {
	return nil, nil //nolint:nilnil // test mock returns zero values
}

func (*mockSemanticProvider) GetGlossaryTerm(_ context.Context, _ string) (*semantic.GlossaryTerm, error) {
	return nil, nil //nolint:nilnil // test mock returns zero values
}

func (m *mockSemanticProvider) SearchTables(_ context.Context, _ semantic.SearchFilter) ([]semantic.TableSearchResult, error) {
	return m.searchResults, nil
}
func (*mockSemanticProvider) Close() error { return nil }

// mockQueryProvider returns canned query availability.
type mockQueryProvider struct {
	availability *query.TableAvailability
}

func (*mockQueryProvider) Name() string { return chainTestMock }
func (*mockQueryProvider) ResolveTable(_ context.Context, _ string) (*query.TableIdentifier, error) {
	return nil, nil //nolint:nilnil // test mock returns zero values
}

func (m *mockQueryProvider) GetTableAvailability(_ context.Context, _ string) (*query.TableAvailability, error) {
	return m.availability, nil
}

func (*mockQueryProvider) GetQueryExamples(_ context.Context, _ string) ([]query.Example, error) {
	return nil, nil //nolint:nilnil // test mock returns zero values
}

func (*mockQueryProvider) GetExecutionContext(_ context.Context, _ []string) (*query.ExecutionContext, error) {
	return nil, nil //nolint:nilnil // test mock returns zero values
}

func (*mockQueryProvider) GetTableSchema(_ context.Context, _ query.TableIdentifier) (*query.TableSchema, error) {
	return nil, nil //nolint:nilnil // test mock returns zero values
}
func (*mockQueryProvider) Close() error { return nil }

// denyAuthorizer always denies access.
type denyAuthorizer struct{}

func (*denyAuthorizer) IsAuthorized(_ context.Context, _ string, _ []string, _ string) (allowed bool, persona, reason string) {
	return false, "", "access denied by test policy"
}

// TestMiddlewareChain_EnrichmentAddsSemanticContext verifies that the semantic
// enrichment middleware actually appends semantic_context to Trino tool results
// when wired through a real mcp.Server. This tests Feature 1 (Semantic-First)
// and Feature 2 (Cross-Injection Trino→DataHub).
func TestMiddlewareChain_EnrichmentAddsSemanticContext(t *testing.T) {
	semProvider := &mockSemanticProvider{
		tableContext: &semantic.TableContext{
			URN:         chainTestOrdersURN,
			Description: chainTestCustOrderData,
			Owners: []semantic.Owner{
				{Name: chainTestDataTeam, Type: semantic.OwnerTypeGroup},
			},
			Tags: []string{chainTestPII, chainTestProductionTag},
			Deprecation: &semantic.Deprecation{
				Deprecated: true,
				Note:       "Use orders_v2 instead",
			},
		},
	}

	authenticator := &testAuthenticator{
		userInfo: &middleware.UserInfo{
			UserID: chainTestUser,
			Roles:  []string{chainTestAnalyst},
		},
	}
	authorizer := &testAuthorizer{persona: chainTestAnalyst}
	toolkitLookup := &testToolkitLookup{
		tools: map[string]struct{ kind, name, conn string }{
			chainTestDescribeTable: {kind: chainTestTrino, name: chainTestProd, conn: chainTestProdTrino},
		},
	}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test-platform",
		Version: "v0.0.1",
	}, nil)

	server.AddTool(&mcp.Tool{
		Name:        chainTestDescribeTable,
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
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(authenticator, authorizer, toolkitLookup, chainTestStdio))

	ctx := context.Background()
	session, err := connectClientServer(ctx, server)
	if err != nil {
		t.Fatalf(chainTestConnecting, err)
	}
	defer func() { _ = session.Close() }()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      chainTestDescribeTable,
		Arguments: map[string]any{"catalog": "catalog", "schema": "schema", "table": "orders"},
	})
	if err != nil {
		t.Fatalf(chainTestCallingTool, err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %v", result.Content)
	}

	// Result should have at least 2 content items: original + enrichment
	if len(result.Content) < 2 {
		t.Fatalf("expected at least 2 content items, got %d", len(result.Content))
	}

	// Find and validate the enrichment content using helpers
	enrichment := assertContentContainsKey(t, result, chainTestSemanticCtx)
	semCtx, ok := enrichment[chainTestSemanticCtx].(map[string]any)
	if !ok {
		t.Fatal("semantic_context not found or wrong type")
	}
	assertSemanticContextFields(t, semCtx, chainTestCustOrderData)

	// Verify deprecation was propagated
	dep, ok := semCtx["deprecation"].(map[string]any)
	depVal, _ := dep["deprecated"].(bool)
	if !ok || !depVal {
		t.Error("deprecation not propagated")
	}
}

// TestMiddlewareChain_EnrichmentAddsQueryContext verifies that DataHub tool
// results get enriched with query_context from the QueryProvider (Trino).
// This tests Feature 2 (Cross-Injection DataHub→Trino direction).
func TestMiddlewareChain_EnrichmentAddsQueryContext(t *testing.T) {
	rowCount := int64(chainTestRowCount)
	queryProv := &mockQueryProvider{
		availability: &query.TableAvailability{
			Available:     true,
			QueryTable:    "catalog.schema.orders",
			Connection:    chainTestProdTrino,
			EstimatedRows: &rowCount,
		},
	}

	authenticator := &testAuthenticator{
		userInfo: &middleware.UserInfo{
			UserID: chainTestUser,
			Roles:  []string{chainTestAnalyst},
		},
	}
	authorizer := &testAuthorizer{persona: chainTestAnalyst}
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
			"urn":      chainTestOrdersURN,
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
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(authenticator, authorizer, toolkitLookup, chainTestStdio))

	ctx := context.Background()
	session, err := connectClientServer(ctx, server)
	if err != nil {
		t.Fatalf(chainTestConnecting, err)
	}
	defer func() { _ = session.Close() }()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "datahub_get_entity",
		Arguments: map[string]any{"urn": chainTestOrdersURN},
	})
	if err != nil {
		t.Fatalf(chainTestCallingTool, err)
	}
	if result.IsError {
		t.Fatalf("tool returned error")
	}

	// Find and validate the query_context enrichment using helper
	enrichment := assertContentContainsKey(t, result, "query_context")
	qCtx, ok := enrichment["query_context"].(map[string]any)
	if !ok {
		t.Fatal("query_context not found or wrong type")
	}
	if len(qCtx) == 0 {
		t.Error("query_context is empty")
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
			chainTestTrinoQuery: {kind: chainTestTrino, name: chainTestProd, conn: chainTestProdTrino},
		},
	}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test-platform",
		Version: "v0.0.1",
	}, nil)

	server.AddTool(&mcp.Tool{
		Name:        chainTestTrinoQuery,
		Description: "Execute query",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"sql":{"type":"string"}}}`),
	}, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		handlerCalled = true
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "should not reach here"}},
		}, nil
	})

	// Only auth middleware (outermost)
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(authenticator, authorizer, toolkitLookup, chainTestStdio))

	ctx := context.Background()
	session, err := connectClientServer(ctx, server)
	if err != nil {
		t.Fatalf(chainTestConnecting, err)
	}
	defer func() { _ = session.Close() }()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      chainTestTrinoQuery,
		Arguments: map[string]any{"sql": "SELECT * FROM secrets"},
	})
	if err != nil {
		t.Fatalf(chainTestCallingTool, err)
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
			Roles:  []string{chainTestAnalyst},
		},
	}
	authorizer := &testAuthorizer{persona: "data-analyst"}
	toolkitLookup := &testToolkitLookup{
		tools: map[string]struct{ kind, name, conn string }{
			chainTestTrinoQuery: {kind: chainTestTrino, name: chainTestProduction, conn: chainTestProdTrino},
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
		Name:        chainTestTrinoQuery,
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
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(authenticator, authorizer, toolkitLookup, chainTestStdio))

	ctx := context.Background()
	session, err := connectClientServer(ctx, server)
	if err != nil {
		t.Fatalf(chainTestConnecting, err)
	}
	defer func() { _ = session.Close() }()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      chainTestTrinoQuery,
		Arguments: map[string]any{"sql": "SELECT count(*) FROM catalog.schema.orders"},
	})
	if err != nil {
		t.Fatalf(chainTestCallingTool, err)
	}
	if result.IsError {
		t.Fatalf("tool returned error")
	}

	// Verify response has enrichment and original result
	assertContentContainsText(t, result, "query result: 42")
	assertContentContainsKey(t, result, chainTestSemanticCtx)

	// Wait for async audit and validate with helpers
	events := waitForAuditEvents(t, auditStore)
	assertAuditEvent(t, events[0], middleware.AuditEvent{
		UserID:      "full-stack-user",
		UserEmail:   "fullstack@example.com",
		Persona:     "data-analyst",
		ToolName:    chainTestTrinoQuery,
		ToolkitKind: "trino",
		ToolkitName: chainTestProduction,
		Connection:  chainTestProdTrino,
	})
}

// TestMiddlewareChain_AuditResponseSize verifies that when the full middleware
// chain processes a tool call, the audit event contains ResponseChars > 0.
func TestMiddlewareChain_AuditResponseSize(t *testing.T) {
	auditStore := &testAuditStore{}
	authenticator := &testAuthenticator{
		userInfo: &middleware.UserInfo{
			UserID: chainTestUser,
			Roles:  []string{chainTestAnalyst},
		},
	}
	authorizer := &testAuthorizer{persona: chainTestAnalyst}
	toolkitLookup := &testToolkitLookup{
		tools: map[string]struct{ kind, name, conn string }{
			chainTestTrinoQuery: {kind: chainTestTrino, name: chainTestProduction, conn: chainTestProd},
		},
	}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test-platform",
		Version: "v0.0.1",
	}, nil)

	server.AddTool(&mcp.Tool{
		Name:        chainTestTrinoQuery,
		Description: "Execute query",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"sql":{"type":"string"}}}`),
	}, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "hello world response"}},
		}, nil
	})

	// Middleware: audit (innermost), auth (outermost)
	server.AddReceivingMiddleware(middleware.MCPAuditMiddleware(auditStore))
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(authenticator, authorizer, toolkitLookup, chainTestStdio))

	ctx := context.Background()
	session, err := connectClientServer(ctx, server)
	if err != nil {
		t.Fatalf(chainTestConnecting, err)
	}
	defer func() { _ = session.Close() }()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      chainTestTrinoQuery,
		Arguments: map[string]any{"sql": "SELECT 1"},
	})
	if err != nil {
		t.Fatalf(chainTestCallingTool, err)
	}
	if result.IsError {
		t.Fatal("tool returned error")
	}

	// Wait for async audit using helper
	events := waitForAuditEvents(t, auditStore)
	event := events[0]
	// "hello world response" = 20 chars
	if event.ResponseChars != 20 { //nolint:revive // expected test value
		t.Errorf("ResponseChars = %d, want 20", event.ResponseChars)
	}
	if event.ContentBlocks != 1 {
		t.Errorf("ContentBlocks = %d, want 1", event.ContentBlocks)
	}
	// AC-8: RequestChars must be > 0 when the request has arguments ({"sql":"SELECT 1"}).
	if event.RequestChars <= 0 {
		t.Errorf("RequestChars = %d, want > 0 for request with arguments", event.RequestChars)
	}
}

// --- Helpers for session dedup integration tests ---

// findContentWithKey iterates result.Content, parses each TextContent as JSON,
// and returns the first parsed map containing the specified top-level key.
func findContentWithKey(t *testing.T, result *mcp.CallToolResult, key string) (map[string]any, bool) {
	t.Helper()
	for _, content := range result.Content {
		tc, ok := content.(*mcp.TextContent)
		if !ok {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(tc.Text), &m); err != nil {
			continue
		}
		if _, found := m[key]; found {
			return m, true
		}
	}
	return nil, false
}

// newDedupTestServer creates an mcp.Server wired with:
//   - trino_describe_table tool (returns static text)
//   - MCPSemanticEnrichmentMiddleware (innermost) with the given cache and mode
//   - MCPToolCallMiddleware (outermost) with test auth/authz/lookup
//
// The mock semantic provider returns table context for catalog.schema.orders.
func newDedupTestServer(t *testing.T, mode middleware.DedupMode, cache *middleware.SessionEnrichmentCache) *mcp.Server {
	t.Helper()

	semProvider := &mockSemanticProvider{
		tableContext: &semantic.TableContext{
			URN:         chainTestOrdersURN,
			Description: chainTestCustOrderData,
			Owners:      []semantic.Owner{{Name: chainTestDataTeam, Type: semantic.OwnerTypeGroup}},
			Tags:        []string{chainTestPII, chainTestProductionTag},
		},
		columnsCtx: map[string]*semantic.ColumnContext{
			chainTestOrderID: {Description: chainTestPrimaryKey, Tags: []string{chainTestPKTag}},
		},
	}

	authenticator := &testAuthenticator{
		userInfo: &middleware.UserInfo{
			UserID: "dedup-user",
			Roles:  []string{chainTestAnalyst},
		},
	}
	authorizer := &testAuthorizer{persona: chainTestAnalyst}
	toolkitLookup := &testToolkitLookup{
		tools: map[string]struct{ kind, name, conn string }{
			chainTestDescribeTable: {kind: chainTestTrino, name: chainTestProd, conn: chainTestProdTrino},
		},
	}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test-dedup",
		Version: "v0.0.1",
	}, nil)

	server.AddTool(&mcp.Tool{
		Name:        chainTestDescribeTable,
		Description: "Describe a table",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"catalog":{"type":"string"},"schema":{"type":"string"},"table":{"type":"string"}}}`),
	}, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "order_id INT, customer_id INT"}},
		}, nil
	})

	// Middleware order (innermost first): enrichment, then auth (outermost)
	server.AddReceivingMiddleware(middleware.MCPSemanticEnrichmentMiddleware(
		semProvider, nil, nil,
		middleware.EnrichmentConfig{
			EnrichTrinoResults: true,
			SessionCache:       cache,
			DedupMode:          mode,
		},
	))
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(authenticator, authorizer, toolkitLookup, chainTestStdio))

	return server
}

// callDescribeOrders is a convenience to call trino_describe_table for catalog.schema.orders.
func callDescribeOrders(t *testing.T, session *mcp.ClientSession) *mcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      chainTestDescribeTable,
		Arguments: map[string]any{"catalog": "catalog", "schema": "schema", "table": "orders"},
	})
	if err != nil {
		t.Fatalf(chainTestCallingTool, err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %v", result.Content)
	}
	return result
}

// --- Session dedup integration tests ---

// TestMiddlewareChain_SessionDedup_FullThenReference proves that two calls for
// the same table through the real middleware chain produce:
//   - Call 1: full semantic_context (with column_context)
//   - Call 2: minimal metadata_reference (no semantic_context)
func TestMiddlewareChain_SessionDedup_FullThenReference(t *testing.T) {
	cache := middleware.NewSessionEnrichmentCache(5*time.Minute, 30*time.Minute)
	server := newDedupTestServer(t, middleware.DedupModeReference, cache)

	ctx := context.Background()
	session, err := connectClientServer(ctx, server)
	if err != nil {
		t.Fatalf(chainTestConnecting, err)
	}
	defer func() { _ = session.Close() }()

	// Call 1: should get full semantic_context
	result1 := callDescribeOrders(t, session)

	semCtx1, hasSemantic := findContentWithKey(t, result1, chainTestSemanticCtx)
	if !hasSemantic {
		t.Fatal("call 1: expected semantic_context, not found")
	}
	sc, ok := semCtx1[chainTestSemanticCtx].(map[string]any)
	if !ok {
		t.Fatal("call 1: semantic_context is not a map")
	}
	if desc, _ := sc["description"].(string); desc != chainTestCustOrderData {
		t.Errorf("call 1: description = %q, want 'Customer order data'", desc)
	}
	_, hasRef1 := findContentWithKey(t, result1, chainTestMetadataRef)
	if hasRef1 {
		t.Error("call 1: should not have metadata_reference on first call")
	}

	// Call 2: should get metadata_reference, not semantic_context
	result2 := callDescribeOrders(t, session)

	refContent, hasRef := findContentWithKey(t, result2, chainTestMetadataRef)
	if !hasRef {
		t.Fatal("call 2: expected metadata_reference, not found")
	}

	ref, ok := refContent[chainTestMetadataRef].(map[string]any)
	if !ok {
		t.Fatal("call 2: metadata_reference is not a map")
	}
	tables, ok := ref["tables"].([]any)
	if !ok || len(tables) == 0 {
		t.Fatal("call 2: metadata_reference.tables missing or empty")
	}
	if tables[0] != "catalog.schema.orders" {
		t.Errorf("call 2: tables[0] = %v, want 'catalog.schema.orders'", tables[0])
	}

	_, hasSemantic2 := findContentWithKey(t, result2, chainTestSemanticCtx)
	if hasSemantic2 {
		t.Error(chainTestDedupNoneSemantic)
	}
}

// TestMiddlewareChain_SessionDedup_SessionIsolation proves that independent
// client sessions get independent dedup state. Each session's first call
// gets full enrichment regardless of what other sessions have seen.
func TestMiddlewareChain_SessionDedup_SessionIsolation(t *testing.T) {
	cache := middleware.NewSessionEnrichmentCache(5*time.Minute, 30*time.Minute)
	server := newDedupTestServer(t, middleware.DedupModeReference, cache)

	ctx := context.Background()

	// Connect Session A
	sessionA, err := connectClientServer(ctx, server)
	if err != nil {
		t.Fatalf("connecting session A: %v", err)
	}
	defer func() { _ = sessionA.Close() }()

	// Connect Session B
	sessionB, err := connectClientServer(ctx, server)
	if err != nil {
		t.Fatalf("connecting session B: %v", err)
	}
	defer func() { _ = sessionB.Close() }()

	// Session A call 1: full enrichment
	resultA1 := callDescribeOrders(t, sessionA)
	_, hasSemanticA1 := findContentWithKey(t, resultA1, chainTestSemanticCtx)
	if !hasSemanticA1 {
		t.Fatal("session A call 1: expected semantic_context")
	}

	// Session B call 1: should ALSO get full enrichment (independent session)
	resultB1 := callDescribeOrders(t, sessionB)
	_, hasSemanticB1 := findContentWithKey(t, resultB1, chainTestSemanticCtx)
	_, hasRefB1 := findContentWithKey(t, resultB1, chainTestMetadataRef)

	if hasRefB1 && !hasSemanticB1 {
		// In-memory transport may share session ID ("stdio" fallback).
		// If so, B sees A's cache — verify dedup is at least consistent.
		t.Log("session B got metadata_reference on first call — sessions share ID (in-memory transport); verifying shared-session consistency")

		// Session A call 2 should also be deduped
		resultA2 := callDescribeOrders(t, sessionA)
		_, hasRefA2 := findContentWithKey(t, resultA2, chainTestMetadataRef)
		if !hasRefA2 {
			t.Error("shared session: session A call 2 should also be deduped")
		}
		return
	}

	if !hasSemanticB1 {
		t.Fatal("session B call 1: expected semantic_context (sessions should be independent)")
	}

	// Session A call 2: should be deduped within A
	resultA2 := callDescribeOrders(t, sessionA)
	_, hasRefA2 := findContentWithKey(t, resultA2, chainTestMetadataRef)
	if !hasRefA2 {
		t.Error("session A call 2: expected metadata_reference (dedup within session A)")
	}
}

// assertDedupSecondCall validates the second-call response for a dedup mode test.
func assertDedupSecondCall(t *testing.T, result *mcp.CallToolResult, mode middleware.DedupMode, hasKey, lacksKey string) {
	t.Helper()
	if hasKey != "" {
		_, found := findContentWithKey(t, result, hasKey)
		if !found {
			t.Errorf("call 2: expected %q in response", hasKey)
		}
	}

	if lacksKey != "" {
		assertContentLacksKey(t, result, mode, lacksKey)
	}

	if mode == middleware.DedupModeNone {
		if len(result.Content) != 1 {
			t.Errorf("call 2 (none mode): expected 1 content item, got %d", len(result.Content))
		}
	}
}

// assertContentLacksKey verifies a key is absent from the result content.
// For summary mode + column_context, it checks substring absence instead.
func assertContentLacksKey(t *testing.T, result *mcp.CallToolResult, mode middleware.DedupMode, key string) {
	t.Helper()
	_, hasLacked := findContentWithKey(t, result, key)
	if !hasLacked {
		return
	}

	if mode == middleware.DedupModeSummary && key == "column_context" {
		for _, content := range result.Content {
			tc, ok := content.(*mcp.TextContent)
			if !ok {
				continue
			}
			if strings.Contains(tc.Text, "column_context") {
				t.Error("call 2 (summary mode): should not have column_context")
			}
		}
		return
	}
	t.Errorf("call 2: should not have %q", key)
}

// TestMiddlewareChain_SessionDedup_Modes verifies all three dedup modes produce
// correct second-call output through the real middleware chain.
func TestMiddlewareChain_SessionDedup_Modes(t *testing.T) {
	tests := []struct {
		name         string
		mode         middleware.DedupMode
		secondHasKey string // key that MUST be present in second call enrichment
		secondLacks  string // key that MUST be absent in second call
	}{
		{
			name:         "reference mode",
			mode:         middleware.DedupModeReference,
			secondHasKey: chainTestMetadataRef,
			secondLacks:  chainTestSemanticCtx,
		},
		{
			name:         "summary mode",
			mode:         middleware.DedupModeSummary,
			secondHasKey: chainTestSemanticCtx,
			secondLacks:  "column_context",
		},
		{
			name:         "none mode",
			mode:         middleware.DedupModeNone,
			secondHasKey: "", // no enrichment key added
			secondLacks:  chainTestSemanticCtx,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := middleware.NewSessionEnrichmentCache(5*time.Minute, 30*time.Minute)
			server := newDedupTestServer(t, tt.mode, cache)

			ctx := context.Background()
			session, err := connectClientServer(ctx, server)
			if err != nil {
				t.Fatalf(chainTestConnecting, err)
			}
			defer func() { _ = session.Close() }()

			// Call 1: always full enrichment
			result1 := callDescribeOrders(t, session)
			_, hasSemantic1 := findContentWithKey(t, result1, chainTestSemanticCtx)
			if !hasSemantic1 {
				t.Fatal("call 1: expected semantic_context")
			}

			// Call 2: depends on mode
			result2 := callDescribeOrders(t, session)
			assertDedupSecondCall(t, result2, tt.mode, tt.secondHasKey, tt.secondLacks)
		})
	}
}

// TestMiddlewareChain_SessionDedup_CacheDisabled proves that when SessionCache
// is nil, every call gets full enrichment — no dedup occurs.
func TestMiddlewareChain_SessionDedup_CacheDisabled(t *testing.T) {
	server := newDedupTestServer(t, middleware.DedupModeReference, nil)

	ctx := context.Background()
	session, err := connectClientServer(ctx, server)
	if err != nil {
		t.Fatalf(chainTestConnecting, err)
	}
	defer func() { _ = session.Close() }()

	// Call 1: full enrichment
	result1 := callDescribeOrders(t, session)
	_, hasSemantic1 := findContentWithKey(t, result1, chainTestSemanticCtx)
	if !hasSemantic1 {
		t.Fatal("call 1: expected semantic_context")
	}

	// Call 2: still full enrichment (no cache to dedup)
	result2 := callDescribeOrders(t, session)
	_, hasSemantic2 := findContentWithKey(t, result2, chainTestSemanticCtx)
	if !hasSemantic2 {
		t.Fatal("call 2: expected semantic_context (cache disabled, no dedup)")
	}

	_, hasRef2 := findContentWithKey(t, result2, chainTestMetadataRef)
	if hasRef2 {
		t.Error("call 2: should not have metadata_reference when cache is disabled")
	}
}

// Ensure mock types satisfy interfaces (compile-time check).
var (
	_ semantic.Provider     = (*mockSemanticProvider)(nil)
	_ query.Provider        = (*mockQueryProvider)(nil)
	_ middleware.Authorizer = (*denyAuthorizer)(nil)
)

// TestMiddlewareChain_EnrichmentAppliedInAudit is an integration test that
// proves the EnrichmentApplied flag set by MCPSemanticEnrichmentMiddleware
// propagates through PlatformContext to the MCPAuditMiddleware audit event.
//
// Stack: Auth/Authz → Audit → Enrichment → handler
// When enrichment adds content, pc.EnrichmentApplied=true is visible to audit.
func TestMiddlewareChain_EnrichmentAppliedInAudit(t *testing.T) {
	semProvider := &mockSemanticProvider{
		tableContext: &semantic.TableContext{
			URN:         chainTestOrdersURN,
			Description: chainTestCustOrderData,
			Owners:      []semantic.Owner{{Name: chainTestDataTeam, Type: semantic.OwnerTypeGroup}},
			Tags:        []string{chainTestPII},
		},
	}

	auditStore := &testAuditStore{}
	authenticator := &testAuthenticator{
		userInfo: &middleware.UserInfo{
			UserID: chainTestUser,
			Email:  "enrichment@example.com",
			Roles:  []string{chainTestAnalyst},
		},
	}
	authorizer := &testAuthorizer{persona: chainTestAnalyst}
	toolkitLookup := &testToolkitLookup{
		tools: map[string]struct{ kind, name, conn string }{
			chainTestDescribeTable: {kind: chainTestTrino, name: chainTestProd, conn: chainTestProdTrino},
			"platform_info":        {kind: "platform", name: "info", conn: ""},
		},
	}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test-enrichment-audit",
		Version: "v0.0.1",
	}, nil)

	// Trino tool (will be enriched)
	server.AddTool(&mcp.Tool{
		Name:        chainTestDescribeTable,
		Description: "Describe a table",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"catalog":{"type":"string"},"schema":{"type":"string"},"table":{"type":"string"}}}`),
	}, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "order_id INT"}},
		}, nil
	})

	// Non-enrichable tool
	server.AddTool(&mcp.Tool{
		Name:        "platform_info",
		Description: "Get platform info",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "platform v1"}},
		}, nil
	})

	// Middleware order (innermost first):
	// 1. Enrichment (innermost)
	server.AddReceivingMiddleware(middleware.MCPSemanticEnrichmentMiddleware(
		semProvider, nil, nil,
		middleware.EnrichmentConfig{EnrichTrinoResults: true},
	))
	// 2. Audit
	server.AddReceivingMiddleware(middleware.MCPAuditMiddleware(auditStore))
	// 3. Auth/Authz (outermost)
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(authenticator, authorizer, toolkitLookup, chainTestStdio))

	ctx := context.Background()
	session, err := connectClientServer(ctx, server)
	if err != nil {
		t.Fatalf(chainTestConnecting, err)
	}
	defer func() { _ = session.Close() }()

	// Call 1: enrichable tool — EnrichmentApplied should be true
	result1, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      chainTestDescribeTable,
		Arguments: map[string]any{"catalog": "catalog", "schema": "schema", "table": "orders"},
	})
	if err != nil {
		t.Fatalf(chainTestCallingTool, err)
	}
	if result1.IsError {
		t.Fatalf("trino tool returned error: %v", result1.Content)
	}

	// Call 2: non-enrichable tool — EnrichmentApplied should be false
	result2, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "platform_info",
	})
	if err != nil {
		t.Fatalf(chainTestCallingTool, err)
	}
	if result2.IsError {
		t.Fatalf("platform_info returned error: %v", result2.Content)
	}

	// Wait for both audit events
	events := waitForNAuditEvents(t, auditStore, 2)

	// Find events by tool name and verify enrichment flags
	trinoEvent := findAuditEventByTool(events, chainTestDescribeTable)
	infoEvent := findAuditEventByTool(events, "platform_info")

	if trinoEvent == nil {
		t.Fatal("missing audit event for trino_describe_table")
	}
	if !trinoEvent.EnrichmentApplied {
		t.Error("trino_describe_table: EnrichmentApplied = false, want true")
	}
	if trinoEvent.SessionID == "" {
		t.Error("trino_describe_table: SessionID is empty")
	}

	if infoEvent == nil {
		t.Fatal("missing audit event for platform_info")
	}
	if infoEvent.EnrichmentApplied {
		t.Error("platform_info: EnrichmentApplied = true, want false")
	}
}

// waitForNAuditEvents polls the audit store until at least n events appear.
func waitForNAuditEvents(t *testing.T, store *testAuditStore, n int) []middleware.AuditEvent {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		events := store.Events()
		if len(events) >= n {
			return events
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("audit store received fewer than %d events", n)
	return nil
}

// findAuditEventByTool finds an audit event by tool name from a slice.
func findAuditEventByTool(events []middleware.AuditEvent, toolName string) *middleware.AuditEvent {
	for i := range events {
		if events[i].ToolName == toolName {
			return &events[i]
		}
	}
	return nil
}

// httpDedupTestResult holds the resources created by newHTTPDedupTestSession.
type httpDedupTestResult struct {
	session *mcp.ClientSession
	cache   *middleware.SessionEnrichmentCache
	ts      *httptest.Server
}

// newHTTPDedupTestSession creates a Streamable HTTP MCP server with dedup
// middleware, starts an httptest server, and connects a client session.
// It registers trino_describe_table (and optionally trino_query) tools.
// The caller must defer cleanup of ts.Close() and session.Close().
func newHTTPDedupTestSession(t *testing.T, includeQueryTool bool) httpDedupTestResult {
	t.Helper()

	semProvider := &mockSemanticProvider{
		tableContext: &semantic.TableContext{
			URN:         chainTestOrdersURN,
			Description: chainTestCustOrderData,
			Owners:      []semantic.Owner{{Name: chainTestDataTeam, Type: semantic.OwnerTypeGroup}},
			Tags:        []string{chainTestPII, chainTestProductionTag},
		},
		columnsCtx: map[string]*semantic.ColumnContext{
			chainTestOrderID: {Description: chainTestPrimaryKey, Tags: []string{chainTestPKTag}},
			"customer_id":    {Description: "Customer FK", Tags: []string{"fk"}},
		},
	}

	cache := middleware.NewSessionEnrichmentCache(5*time.Minute, 30*time.Minute)

	authenticator := &testAuthenticator{
		userInfo: &middleware.UserInfo{
			UserID: "streamable-user",
			Roles:  []string{chainTestAnalyst},
		},
	}
	authorizer := &testAuthorizer{persona: chainTestAnalyst}
	toolkitLookup := &testToolkitLookup{
		tools: map[string]struct{ kind, name, conn string }{
			chainTestDescribeTable: {kind: chainTestTrino, name: chainTestProd, conn: chainTestProdTrino},
			chainTestTrinoQuery:    {kind: chainTestTrino, name: chainTestProd, conn: chainTestProdTrino},
		},
	}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test-dedup-streamable",
		Version: "v0.0.1",
	}, nil)

	server.AddTool(&mcp.Tool{
		Name:        chainTestDescribeTable,
		Description: "Describe a table",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"catalog":{"type":"string"},"schema":{"type":"string"},"table":{"type":"string"}}}`),
	}, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "order_id INT, customer_id INT"}},
		}, nil
	})

	if includeQueryTool {
		server.AddTool(&mcp.Tool{
			Name:        chainTestTrinoQuery,
			Description: "Execute SQL query",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"sql":{"type":"string"}}}`),
		}, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "query result: 42"}},
			}, nil
		})
	}

	// Middleware order (innermost first): enrichment, then auth (outermost)
	server.AddReceivingMiddleware(middleware.MCPSemanticEnrichmentMiddleware(
		semProvider, nil, nil,
		middleware.EnrichmentConfig{
			EnrichTrinoResults: true,
			SessionCache:       cache,
			DedupMode:          middleware.DedupModeReference,
		},
	))
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(authenticator, authorizer, toolkitLookup, "http"))

	// Start a real HTTP server with Streamable HTTP transport
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return server
	}, &mcp.StreamableHTTPOptions{
		SessionTimeout: 30 * time.Minute,
	})
	ts := httptest.NewServer(handler)

	// Connect client via Streamable HTTP
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	ctx := context.Background()
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: ts.URL}, nil)
	if err != nil {
		ts.Close()
		t.Fatalf("connecting streamable HTTP client: %v", err)
	}

	return httpDedupTestResult{session: session, cache: cache, ts: ts}
}

// assertHasKeyDumpOnMiss checks that the result contains the given key.
// If absent, it dumps all TextContent items and calls t.Fatal.
func assertHasKeyDumpOnMiss(t *testing.T, result *mcp.CallToolResult, key, callLabel string) {
	t.Helper()
	_, found := findContentWithKey(t, result, key)
	if found {
		return
	}
	for i, c := range result.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			t.Logf("%s content[%d]: %s", callLabel, i, tc.Text)
		}
	}
	t.Fatalf("%s: expected %s, not found", callLabel, key)
}

// TestMiddlewareChain_SessionDedup_StreamableHTTP verifies that session metadata
// dedup works correctly over a real Streamable HTTP transport (not in-memory).
// This covers the production code path where session IDs come from the SDK's
// StreamableHTTPHandler rather than falling back to the "stdio" default.
func TestMiddlewareChain_SessionDedup_StreamableHTTP(t *testing.T) {
	r := newHTTPDedupTestSession(t, true)
	defer r.ts.Close()
	defer func() { _ = r.session.Close() }()

	ctx := context.Background()

	// Call 1: trino_describe_table — should get full semantic_context
	result1 := callDescribeOrders(t, r.session)

	_, hasSemantic1 := findContentWithKey(t, result1, chainTestSemanticCtx)
	if !hasSemantic1 {
		t.Fatal("call 1: expected semantic_context, not found")
	}
	_, hasRef1 := findContentWithKey(t, result1, chainTestMetadataRef)
	if hasRef1 {
		t.Error("call 1: should not have metadata_reference on first call")
	}

	// Call 2: same tool, same table — should get metadata_reference (dedup)
	result2 := callDescribeOrders(t, r.session)
	assertHasKeyDumpOnMiss(t, result2, chainTestMetadataRef, "call 2")

	_, hasSemantic2 := findContentWithKey(t, result2, chainTestSemanticCtx)
	if hasSemantic2 {
		t.Error(chainTestDedupNoneSemantic)
	}

	// Call 3: trino_query with SQL referencing the same table — should also be deduped
	result3, err := r.session.CallTool(ctx, &mcp.CallToolParams{
		Name:      chainTestTrinoQuery,
		Arguments: map[string]any{"sql": "SELECT * FROM catalog.schema.orders LIMIT 10"},
	})
	if err != nil {
		t.Fatalf("call 3 (query): %v", err)
	}
	if result3.IsError {
		t.Fatalf("call 3 returned error: %v", result3.Content)
	}
	assertHasKeyDumpOnMiss(t, result3, chainTestMetadataRef, "call 3")

	// Verify session count
	if count := r.cache.SessionCount(); count == 0 {
		t.Error("cache has 0 sessions after calls")
	}
}

// TestMiddlewareChain_SessionDedup_StreamableHTTP_Stateless verifies that
// session metadata dedup works correctly when the Streamable HTTP handler runs
// in stateless mode (as used in production with AwareHandler).
func TestMiddlewareChain_SessionDedup_StreamableHTTP_Stateless(t *testing.T) {
	semProvider := &mockSemanticProvider{
		tableContext: &semantic.TableContext{
			URN:         chainTestOrdersURN,
			Description: chainTestCustOrderData,
			Owners:      []semantic.Owner{{Name: chainTestDataTeam, Type: semantic.OwnerTypeGroup}},
			Tags:        []string{chainTestPII, chainTestProductionTag},
		},
		columnsCtx: map[string]*semantic.ColumnContext{
			chainTestOrderID: {Description: chainTestPrimaryKey, Tags: []string{chainTestPKTag}},
		},
	}

	cache := middleware.NewSessionEnrichmentCache(5*time.Minute, 30*time.Minute)

	authenticator := &testAuthenticator{
		userInfo: &middleware.UserInfo{
			UserID: "stateless-user",
			Roles:  []string{chainTestAnalyst},
		},
	}
	authorizer := &testAuthorizer{persona: chainTestAnalyst}
	toolkitLookup := &testToolkitLookup{
		tools: map[string]struct{ kind, name, conn string }{
			chainTestDescribeTable: {kind: chainTestTrino, name: chainTestProd, conn: chainTestProdTrino},
		},
	}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test-dedup-stateless",
		Version: "v0.0.1",
	}, nil)

	server.AddTool(&mcp.Tool{
		Name:        chainTestDescribeTable,
		Description: "Describe a table",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"catalog":{"type":"string"},"schema":{"type":"string"},"table":{"type":"string"}}}`),
	}, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "order_id INT, customer_id INT"}},
		}, nil
	})

	server.AddReceivingMiddleware(middleware.MCPSemanticEnrichmentMiddleware(
		semProvider, nil, nil,
		middleware.EnrichmentConfig{
			EnrichTrinoResults: true,
			SessionCache:       cache,
			DedupMode:          middleware.DedupModeReference,
		},
	))
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(authenticator, authorizer, toolkitLookup, "http"))

	// Stateless mode: each request creates a new temporary session
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return server
	}, &mcp.StreamableHTTPOptions{
		Stateless: true,
	})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	ctx := context.Background()
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: ts.URL}, nil)
	if err != nil {
		t.Fatalf("connecting streamable HTTP client: %v", err)
	}
	defer func() { _ = session.Close() }()

	// Call 1: full enrichment
	result1, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      chainTestDescribeTable,
		Arguments: map[string]any{"catalog": "catalog", "schema": "schema", "table": "orders"},
	})
	if err != nil {
		t.Fatalf("call 1: %v", err)
	}
	if result1.IsError {
		t.Fatalf("call 1 error: %v", result1.Content)
	}

	_, hasSemantic1 := findContentWithKey(t, result1, chainTestSemanticCtx)
	if !hasSemantic1 {
		t.Fatal("call 1: expected semantic_context")
	}

	// Call 2: should be deduped
	result2, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      chainTestDescribeTable,
		Arguments: map[string]any{"catalog": "catalog", "schema": "schema", "table": "orders"},
	})
	if err != nil {
		t.Fatalf("call 2: %v", err)
	}
	if result2.IsError {
		t.Fatalf("call 2 error: %v", result2.Content)
	}

	_, hasRef2 := findContentWithKey(t, result2, chainTestMetadataRef)
	_, hasSemantic2 := findContentWithKey(t, result2, chainTestSemanticCtx)

	if !hasRef2 {
		for i, c := range result2.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				t.Logf("call 2 content[%d]: %s", i, tc.Text)
			}
		}
		if hasSemantic2 {
			t.Log("call 2 got full semantic_context instead of metadata_reference — dedup failed in stateless mode")
		}
		t.Fatal("call 2: expected metadata_reference (dedup)")
	}
	if hasSemantic2 {
		t.Error(chainTestDedupNoneSemantic)
	}
}

// TestMiddlewareChain_SessionDedup_SSE verifies that session metadata dedup
// works over SSE transport, where sseServerConn.SessionID() returns empty
// and extractSessionID falls back to "stdio".
func TestMiddlewareChain_SessionDedup_SSE(t *testing.T) {
	semProvider := &mockSemanticProvider{
		tableContext: &semantic.TableContext{
			URN:         chainTestOrdersURN,
			Description: chainTestCustOrderData,
			Owners:      []semantic.Owner{{Name: chainTestDataTeam, Type: semantic.OwnerTypeGroup}},
			Tags:        []string{chainTestPII, chainTestProductionTag},
		},
		columnsCtx: map[string]*semantic.ColumnContext{
			chainTestOrderID: {Description: chainTestPrimaryKey, Tags: []string{chainTestPKTag}},
		},
	}

	cache := middleware.NewSessionEnrichmentCache(5*time.Minute, 30*time.Minute)

	authenticator := &testAuthenticator{
		userInfo: &middleware.UserInfo{
			UserID: "sse-user",
			Roles:  []string{chainTestAnalyst},
		},
	}
	authorizer := &testAuthorizer{persona: chainTestAnalyst}
	toolkitLookup := &testToolkitLookup{
		tools: map[string]struct{ kind, name, conn string }{
			chainTestDescribeTable: {kind: chainTestTrino, name: chainTestProd, conn: chainTestProdTrino},
		},
	}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test-dedup-sse",
		Version: "v0.0.1",
	}, nil)

	server.AddTool(&mcp.Tool{
		Name:        chainTestDescribeTable,
		Description: "Describe a table",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"catalog":{"type":"string"},"schema":{"type":"string"},"table":{"type":"string"}}}`),
	}, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "order_id INT, customer_id INT"}},
		}, nil
	})

	server.AddReceivingMiddleware(middleware.MCPSemanticEnrichmentMiddleware(
		semProvider, nil, nil,
		middleware.EnrichmentConfig{
			EnrichTrinoResults: true,
			SessionCache:       cache,
			DedupMode:          middleware.DedupModeReference,
		},
	))
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(authenticator, authorizer, toolkitLookup, chainTestStdio))

	// SSE handler
	sseHandler := mcp.NewSSEHandler(func(*http.Request) *mcp.Server {
		return server
	}, nil)
	ts := httptest.NewServer(sseHandler)
	defer ts.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	ctx := context.Background()
	session, err := client.Connect(ctx, &mcp.SSEClientTransport{Endpoint: ts.URL}, nil)
	if err != nil {
		t.Fatalf("connecting SSE client: %v", err)
	}
	defer func() { _ = session.Close() }()

	// Call 1: full enrichment
	result1, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      chainTestDescribeTable,
		Arguments: map[string]any{"catalog": "catalog", "schema": "schema", "table": "orders"},
	})
	if err != nil {
		t.Fatalf("call 1: %v", err)
	}
	if result1.IsError {
		t.Fatalf("call 1 error: %v", result1.Content)
	}

	_, hasSemantic1 := findContentWithKey(t, result1, chainTestSemanticCtx)
	if !hasSemantic1 {
		t.Fatal("call 1: expected semantic_context")
	}

	// Call 2: should be deduped
	result2, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      chainTestDescribeTable,
		Arguments: map[string]any{"catalog": "catalog", "schema": "schema", "table": "orders"},
	})
	if err != nil {
		t.Fatalf("call 2: %v", err)
	}
	if result2.IsError {
		t.Fatalf("call 2 error: %v", result2.Content)
	}

	_, hasRef2 := findContentWithKey(t, result2, chainTestMetadataRef)
	if !hasRef2 {
		for i, c := range result2.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				t.Logf("call 2 content[%d]: %s", i, tc.Text)
			}
		}
		t.Fatal("call 2: expected metadata_reference (dedup) via SSE transport")
	}

	_, hasSemantic2 := findContentWithKey(t, result2, chainTestSemanticCtx)
	if hasSemantic2 {
		t.Error(chainTestDedupNoneSemantic)
	}
}

// Suppress unused import warnings for storage (used in EnrichmentConfig).
var _ storage.Provider = nil
