package middleware_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	apigateway "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
)

// TestPlatformAdminSelfConnection_IdentityPassthroughEndToEnd is the
// issue #543 acceptance test for caller-identity propagation. It wires
// the REAL assembled chain — a live mcp.Server with the audit and
// tool-call middleware, the real api-gateway toolkit, and a loopback
// HTTP "admin API" — then issues an api_invoke_endpoint call as an
// authenticated admin over the identity-passthrough platform-admin
// connection. It asserts two things that only hold end-to-end:
//
//  1. The loopback admin API receives the ACTING ADMIN's bearer token on
//     the Authorization header (not a shared connection credential), so a
//     self-config mutation authenticates and authorizes as the real admin.
//  2. The audit event for the call is attributed to that same admin.
//
// This is deliberately NOT a unit test with hand-built inputs: the token
// is placed on the context the way the HTTP auth path does (an outer
// middleware mirroring bridgeAuthToken), then must survive the tool-call
// middleware and be forwarded by the toolkit's invoke path through a real
// HTTP round-trip.
func TestPlatformAdminSelfConnection_IdentityPassthroughEndToEnd(t *testing.T) {
	const (
		adminToken = "admin-token-abc"
		adminEmail = "admin@example.com"
		connName   = "platform-admin"
	)

	var (
		mu      sync.Mutex
		gotAuth string
		gotPath string
	)
	adminAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"name":"analyst"}`)
	}))
	defer adminAPI.Close()

	tk := apigateway.New("api")
	if err := tk.AddConnection(connName, map[string]any{
		"base_url":             adminAPI.URL,
		"auth_mode":            apigateway.AuthModeNone,
		"identity_passthrough": true,
	}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}

	server := mcp.NewServer(&mcp.Implementation{Name: "selfconn-test", Version: "v0.0.0"}, nil)
	tk.RegisterTools(server)

	logger := &recordingAuditLogger{}
	// Innermost-first: audit reads PlatformContext set by the tool-call
	// middleware, so tool-call must be the outer of the two. The token
	// injector is added last, making it outermost — it stands in for the
	// HTTP auth path that bridges the inbound token onto the context.
	server.AddReceivingMiddleware(middleware.MCPAuditMiddleware(logger))
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(
		&fakeAuthn{user: &middleware.UserInfo{UserID: adminEmail, Email: adminEmail, Roles: []string{"admin"}}},
		&fakeAuthz{persona: "admin"},
		&fakeLookup{kind: "api", name: connName, conn: connName},
		middleware.ToolCallConfig{AdminPersona: "admin"},
	))
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			return next(middleware.WithToken(ctx, adminToken), method, req)
		}
	})

	ctx := context.Background()
	sess := mustConnect(ctx, t, server)
	defer func() { _ = sess.Close() }()

	res, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name: apigateway.ToolInvokeEndpoint,
		Arguments: map[string]any{
			"connection": connName,
			"method":     "POST",
			"path":       "/api/v1/admin/personas",
			"body":       map[string]any{"name": "analyst"},
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("api_invoke_endpoint returned an error result: %v", res.Content)
	}

	mu.Lock()
	auth, path := gotAuth, gotPath
	mu.Unlock()

	if want := "Bearer " + adminToken; auth != want {
		t.Errorf("admin API saw Authorization %q; want the acting admin's token %q (identity passthrough not applied)", auth, want)
	}
	if path != "/api/v1/admin/personas" {
		t.Errorf("admin API saw path %q; want /api/v1/admin/personas", path)
	}

	// Audit logging is fire-and-forget; poll briefly for the event.
	ev, ok := waitForAuditEvent(logger, apigateway.ToolInvokeEndpoint, time.Second)
	if !ok {
		t.Fatalf("no audit event captured for %s", apigateway.ToolInvokeEndpoint)
	}
	if ev.UserID != adminEmail {
		t.Errorf("audit event UserID = %q; want acting admin %q", ev.UserID, adminEmail)
	}
	if ev.Connection != connName {
		t.Errorf("audit event Connection = %q; want %q", ev.Connection, connName)
	}
}

// recordingAuditLogger captures audit events for assertion. The
// package-internal capturing logger lives in package middleware (not
// middleware_test), so this external test defines its own.
type recordingAuditLogger struct {
	mu     sync.Mutex
	events []middleware.AuditEvent
}

func (r *recordingAuditLogger) Log(_ context.Context, event middleware.AuditEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
	return nil
}

func (r *recordingAuditLogger) Events() []middleware.AuditEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]middleware.AuditEvent, len(r.events))
	copy(out, r.events)
	return out
}

// waitForAuditEvent polls the recording logger until an event for the
// given tool appears or the deadline passes.
func waitForAuditEvent(logger *recordingAuditLogger, tool string, within time.Duration) (middleware.AuditEvent, bool) {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		for _, e := range logger.Events() {
			if e.ToolName == tool {
				return e, true
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return middleware.AuditEvent{}, false
}
