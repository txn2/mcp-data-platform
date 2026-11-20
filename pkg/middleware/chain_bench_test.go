package middleware_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// The tools/call chain every call crosses (#2716): the tool-call layer that
// authenticates, authorizes and builds the PlatformContext, then the audit
// layer that builds the event, each measured alone and assembled the way
// finalizeSetup orders them, over a handler that answers at once. Allocations
// per call are gated by scripts/bench-allocs.py.

type discardAudit struct{}

func (discardAudit) Log(context.Context, middleware.AuditEvent) error { return nil }

func benchToolCall() *mcp.CallToolRequest {
	args, _ := json.Marshal(map[string]any{"sql": "SELECT region, sum(amount) FROM sales.orders GROUP BY region", "limit": 100})
	return &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "trino_query", Arguments: args}}
}

func answer(context.Context, string, mcp.Request) (mcp.Result, error) {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: `{"rows":[["west",42]]}`}}}, nil
}

func toolCallLayer() mcp.Middleware {
	return middleware.MCPToolCallMiddleware(
		&fakeAuthn{user: &middleware.UserInfo{UserID: "u1", Email: "analyst@example.com", Roles: []string{"analyst"}}},
		&fakeAuthz{persona: "analyst"},
		&fakeLookup{kind: "trino", name: "warehouse", conn: "warehouse"},
		middleware.ToolCallConfig{Transport: "http", AdminPersona: "admin"},
	)
}

func runChain(b *testing.B, h mcp.MethodHandler) {
	b.Helper()
	ctx := context.Background()
	req := benchToolCall()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := h(ctx, "tools/call", req); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkToolCallLayer is authentication, authorization and the
// PlatformContext for one call.
func BenchmarkToolCallLayer(b *testing.B) {
	runChain(b, toolCallLayer()(answer))
}

// BenchmarkAuditLayer is the audit event built for one call, its logger
// discarding it.
func BenchmarkAuditLayer(b *testing.B) {
	audit := middleware.MCPAuditMiddleware(discardAudit{})(answer)
	runChain(b, toolCallLayer()(audit))
}

// BenchmarkToolCallChain is the assembled chain without enrichment: the tool
// call layer outer to audit, as finalizeSetup adds them.
func BenchmarkToolCallChain(b *testing.B) {
	h := middleware.MCPAuditMiddleware(discardAudit{})(answer)
	h = toolCallLayer()(h)
	runChain(b, h)
}
