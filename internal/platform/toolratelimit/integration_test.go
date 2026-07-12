package toolratelimit_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/platform/toolratelimit"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/registry"
)

const (
	itToolTrinoQuery = "trino_query"
	itAnalyst        = "analyst"
	itStdio          = "stdio"
)

// --- minimal fakes for the real assembled middleware chain ---

type fakeAuthenticator struct{ info *middleware.UserInfo }

func (a *fakeAuthenticator) Authenticate(_ context.Context) (*middleware.UserInfo, error) {
	return a.info, nil
}

type fakeAuthorizer struct{ persona string }

func (a *fakeAuthorizer) IsAuthorized(_ context.Context, _ string, _ []string, _, _ string) (authorized bool, persona, reason string) {
	return true, a.persona, ""
}

type fakeToolkitLookup struct{}

func (fakeToolkitLookup) GetToolkitForTool(tool string) registry.ToolkitMatch {
	if tool == itToolTrinoQuery {
		return registry.ToolkitMatch{Kind: "trino", Name: "production", Connection: "prod-trino", Found: true}
	}
	return registry.ToolkitMatch{}
}

// countingAuditStore records every audit event it receives; it stands in for the
// audit pipeline so the test can assert refused calls never reach it.
type countingAuditStore struct {
	mu     sync.Mutex
	events int
}

func (s *countingAuditStore) Log(_ context.Context, _ middleware.AuditEvent) error {
	s.mu.Lock()
	s.events++
	s.mu.Unlock()
	return nil
}

func (s *countingAuditStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.events
}

func connect(t *testing.T, server *mcp.Server) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, t1, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	session, err := client.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	return session
}

func addTrinoTool(server *mcp.Server, onCall func()) {
	server.AddTool(&mcp.Tool{
		Name:        itToolTrinoQuery,
		Description: "Test tool",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"sql":{"type":"string"}}}`),
	}, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if onCall != nil {
			onCall()
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "query result"}}}, nil
	})
}

// TestChain_RateLimitBoundsRunawayLoop is the issue #929 acceptance test. It
// wires the real MCP server with the chain arranged as it ships —
// MCPToolCallMiddleware (outermost, writes PlatformContext) -> rate limiter ->
// MCPAuditMiddleware -> handler — then drives one authenticated user hammering
// an expensive tool far past its burst. It proves: over-limit calls are refused
// with the RATE_LIMITED in-band contract; the handler runs at most `burst`
// times; and the audit store receives no more events than there were allowed
// calls (refused calls short-circuit outer to audit, so DB pressure stays
// bounded).
func TestChain_RateLimitBoundsRunawayLoop(t *testing.T) {
	const (
		burst      = 3
		totalCalls = 30
	)

	var handlerRuns atomic.Int64
	auditStore := &countingAuditStore{}
	authenticator := &fakeAuthenticator{info: &middleware.UserInfo{
		UserID: "runaway-user-929", Email: "loop@example.com", Roles: []string{itAnalyst}, AuthType: middleware.AuthTypeOIDC,
	}}

	// rpm=6 => 1 token per 10s, so no meaningful refill during the test loop and
	// exactly `burst` calls are admitted (no flake even on a slow CI).
	h := toolratelimit.New(6, burst, nil, nil)
	defer h.Close()

	server := mcp.NewServer(&mcp.Implementation{Name: "test-platform", Version: "v0.0.1"}, nil)
	addTrinoTool(server, func() { handlerRuns.Add(1) })

	// Innermost-first: Audit -> rate limit -> ToolCall (outermost).
	server.AddReceivingMiddleware(middleware.MCPAuditMiddleware(auditStore))
	server.AddReceivingMiddleware(h.Middleware())
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(authenticator, &fakeAuthorizer{persona: "data-analyst"},
		fakeToolkitLookup{}, middleware.ToolCallConfig{Transport: itStdio, AdminPersona: "admin"}))

	session := connect(t, server)
	defer func() { _ = session.Close() }()

	var allowed, refused int
	for range totalCalls {
		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      itToolTrinoQuery,
			Arguments: map[string]any{"sql": "SELECT 1"},
		})
		if err != nil {
			t.Fatalf("calling tool: %v", err)
		}
		if result.IsError {
			refused++
			if !strings.Contains(resultText(result), "RATE_LIMITED") {
				t.Errorf("refused call is not a RATE_LIMITED error: %s", resultText(result))
			}
		} else {
			allowed++
		}
	}

	if allowed != burst {
		t.Errorf("allowed = %d, want exactly burst = %d", allowed, burst)
	}
	if refused != totalCalls-burst {
		t.Errorf("refused = %d, want %d", refused, totalCalls-burst)
	}
	if got := handlerRuns.Load(); got != int64(burst) {
		t.Errorf("handler ran %d times; the runaway loop must be bounded to burst = %d", got, burst)
	}

	events := waitForAuditCount(t, auditStore, burst)
	if events != burst {
		t.Errorf("audit store received %d events; refused calls must not produce audit rows (want %d)", events, burst)
	}
}

// TestChain_RateLimitPerUserIsolation proves that through the real assembled
// chain two distinct authenticated users do not share a bucket.
func TestChain_RateLimitPerUserIsolation(t *testing.T) {
	const burst = 2

	h := toolratelimit.New(6, burst, nil, nil)
	defer h.Close()

	run := func(userID string) (allowed, refused int) {
		authenticator := &fakeAuthenticator{info: &middleware.UserInfo{
			UserID: userID, Email: userID + "@example.com", Roles: []string{itAnalyst}, AuthType: middleware.AuthTypeOIDC,
		}}
		server := mcp.NewServer(&mcp.Implementation{Name: "test-platform", Version: "v0.0.1"}, nil)
		addTrinoTool(server, nil)
		// One shared limiter across both users' servers: the buckets are keyed on
		// identity, not on which server processed the call.
		server.AddReceivingMiddleware(h.Middleware())
		server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(authenticator, &fakeAuthorizer{persona: "data-analyst"},
			fakeToolkitLookup{}, middleware.ToolCallConfig{Transport: itStdio, AdminPersona: "admin"}))

		session := connect(t, server)
		defer func() { _ = session.Close() }()

		for range burst + 2 {
			result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
				Name:      itToolTrinoQuery,
				Arguments: map[string]any{"sql": "SELECT 1"},
			})
			if err != nil {
				t.Fatalf("calling tool: %v", err)
			}
			if result.IsError {
				refused++
			} else {
				allowed++
			}
		}
		return allowed, refused
	}

	aliceAllowed, aliceRefused := run("alice-929")
	if aliceAllowed != burst || aliceRefused == 0 {
		t.Errorf("alice allowed=%d refused=%d; want allowed=%d and some refused", aliceAllowed, aliceRefused, burst)
	}
	bobAllowed, bobRefused := run("bob-929")
	if bobAllowed != burst || bobRefused == 0 {
		t.Errorf("bob allowed=%d refused=%d; bob must not share alice's bucket (want allowed=%d)", bobAllowed, bobRefused, burst)
	}
}

func resultText(r *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range r.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// waitForAuditCount waits for the async audit writer to record at least `want`
// events, then returns the final count.
func waitForAuditCount(t *testing.T, store *countingAuditStore, want int) int {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if store.count() >= want {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	// A brief settle so a stray extra event (if any) would surface.
	time.Sleep(50 * time.Millisecond)
	return store.count()
}
