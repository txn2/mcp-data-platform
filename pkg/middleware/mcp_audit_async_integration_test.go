package middleware_test

import (
	"context"
	"encoding/json"
	"runtime"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// blockingAuditStore is an audit.Logger whose Log blocks forever, simulating a
// wedged database. It stands in for the PostgreSQL store beneath the async
// writer so the integration test can prove that a stalled store cannot stall
// tool calls or leak goroutines.
type blockingAuditStore struct {
	release chan struct{}
}

func newBlockingAuditStore() *blockingAuditStore {
	return &blockingAuditStore{release: make(chan struct{})}
}

func (s *blockingAuditStore) Log(_ context.Context, _ audit.Event) error {
	<-s.release
	return nil
}

func (*blockingAuditStore) Query(_ context.Context, _ audit.QueryFilter) ([]audit.Event, error) {
	return nil, nil
}
func (*blockingAuditStore) Close() error { return nil }

// TestMiddlewareChain_AuditAsyncBoundedUnderStalledStore wires the real MCP
// server with MCPToolCallMiddleware and MCPAuditMiddleware, backing the audit
// logger with a bounded async writer over a store whose Log blocks forever.
// It asserts the issue #884 acceptance criterion: 100 tool calls complete
// promptly and the goroutine count does not grow by 100 — the old per-call
// detached goroutine grew without bound under exactly this condition.
func TestMiddlewareChain_AuditAsyncBoundedUnderStalledStore(t *testing.T) {
	store := newBlockingAuditStore()
	defer close(store.release)

	writer := audit.NewAsyncWriter(store, audit.WithQueueCapacity(2048))
	logger := middleware.NewAuditStoreAdapter(writer)

	authenticator := &testAuthenticator{
		userInfo: &middleware.UserInfo{
			UserID: "test-user-884",
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

	server := mcp.NewServer(&mcp.Implementation{Name: "test-platform", Version: "v0.0.1"}, nil)
	server.AddTool(&mcp.Tool{
		Name:        chainTestTrinoQuery,
		Description: "Test tool",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"sql":{"type":"string"}}}`),
	}, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "query result"}}}, nil
	})

	server.AddReceivingMiddleware(middleware.MCPAuditMiddleware(logger))
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(authenticator, authorizer, toolkitLookup,
		middleware.ToolCallConfig{Transport: chainTestStdio, AdminPersona: "admin"}))

	ctx := context.Background()
	session, err := connectClientServer(ctx, server)
	if err != nil {
		t.Fatalf(chainTestConnecting, err)
	}
	defer func() { _ = session.Close() }()

	// Baseline after the connection is established so the transport's own
	// goroutines are not counted against the audit writer.
	runtime.GC()
	before := runtime.NumGoroutine()

	const calls = 100
	start := time.Now()
	for range calls {
		result, callErr := session.CallTool(ctx, &mcp.CallToolParams{
			Name:      chainTestTrinoQuery,
			Arguments: map[string]any{"sql": "SELECT 1"},
		})
		if callErr != nil {
			t.Fatalf(chainTestCallingTool, callErr)
		}
		if result.IsError {
			t.Fatalf("tool returned error: %v", result.Content)
		}
	}
	elapsed := time.Since(start)

	// 100 calls against a wedged audit store must still finish promptly —
	// the audit enqueue is non-blocking, so the stall is invisible to callers.
	if elapsed > 10*time.Second {
		t.Errorf("100 tool calls took %v; a stalled audit store must not slow tool calls", elapsed)
	}

	runtime.GC()
	after := runtime.NumGoroutine()
	if grew := after - before; grew > 20 {
		t.Errorf("goroutine count grew by %d across %d calls; audit writes must not spawn one goroutine per call", grew, calls)
	}
}
