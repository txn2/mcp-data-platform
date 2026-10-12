package middleware_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/httpserver/httpauth"
	"github.com/txn2/mcp-data-platform/pkg/admin"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	apigateway "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
)

// tokenAuthn admits the one token it holds, the way the platform's
// authenticator chain admits an API key.
type tokenAuthn struct {
	token string
	user  *middleware.UserInfo
}

func (a tokenAuthn) Authenticate(ctx context.Context) (*middleware.UserInfo, error) {
	if middleware.GetToken(ctx) == a.token {
		return a.user, nil
	}
	return nil, nil //nolint:nilnil // no credential admitted
}

// selfDispatchFixture is the platform-admin connection over the REAL admin
// authenticator: the same handler is the loopback server a token is sent to
// and the in-process handler a token-less call is served by, so the draft and
// the saved run cross one authorization rule.
type selfDispatchFixture struct {
	server *mcp.Server
	logger *recordingAuditLogger

	mu       sync.Mutex
	served   []string // the admin user each request was authorized as
	overWire int
}

func newSelfDispatchFixture(t *testing.T, caller *middleware.UserInfo, source, token string) *selfDispatchFixture {
	t.Helper()
	const adminToken = "admin-token"
	f := &selfDispatchFixture{logger: &recordingAuditLogger{}}

	reg := persona.NewRegistry()
	for _, p := range []*persona.Persona{
		{Name: "admin", Roles: []string{"admin"}},
		{Name: "analyst", Roles: []string{"analyst"}},
	} {
		if err := reg.Register(p); err != nil {
			t.Fatalf("Register: %v", err)
		}
	}
	authn := tokenAuthn{token: adminToken, user: &middleware.UserInfo{UserID: "admin@example.com", Email: "admin@example.com", Roles: []string{"admin"}}}
	adminAPI := admin.RequirePersona(httpauth.AdminSelfCalls(admin.NewPlatformAuthenticator(authn, "admin", reg), reg, "admin"))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.served = append(f.served, admin.GetUser(r.Context()).UserID)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"user": admin.GetUser(r.Context()).UserID})
	}))
	loopback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.overWire++
		f.mu.Unlock()
		adminAPI.ServeHTTP(w, r)
	}))
	t.Cleanup(loopback.Close)

	tk := apigateway.New("api")
	tk.SetSelfHandler(adminAPI)
	if err := tk.AddConnection("platform-admin", map[string]any{
		"base_url":             loopback.URL,
		"auth_mode":            apigateway.AuthModeNone,
		"identity_passthrough": true,
		"self_dispatch":        true,
	}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	f.server = mcp.NewServer(&mcp.Implementation{Name: "selfdispatch-test", Version: "v0.0.0"}, nil)
	tk.RegisterTools(f.server)
	f.server.AddReceivingMiddleware(middleware.MCPAuditMiddleware(f.logger))
	f.server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(
		&fakeAuthn{user: caller}, &fakeAuthz{persona: "admin"},
		&fakeLookup{kind: "api", name: "api", conn: "platform-admin"},
		middleware.ToolCallConfig{AdminPersona: "admin"},
	))
	// Outermost: the identity a run or a draft hands its in-memory session.
	f.server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			ctx = middleware.WithSource(ctx, source)
			if token == "" {
				ctx = middleware.WithPreAuthenticatedUser(ctx, caller)
			} else {
				ctx = middleware.WithToken(ctx, token)
			}
			return next(ctx, method, req)
		}
	})
	return f
}

// invokeResult is the part of api_invoke_endpoint's result these tests read.
type invokeResult struct {
	Status int            `json:"status"`
	Body   map[string]any `json:"body"`
	text   string
}

func (f *selfDispatchFixture) call(t *testing.T) invokeResult {
	t.Helper()
	ctx := context.Background()
	sess := mustConnect(ctx, t, f.server)
	defer func() { _ = sess.Close() }()
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name:      apigateway.ToolInvokeEndpoint,
		Arguments: map[string]any{"connection": "platform-admin", "method": "GET", "path": "/api/v1/admin/personas"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			_, _ = b.WriteString(tc.Text)
		}
	}
	out := invokeResult{text: b.String()}
	_ = json.Unmarshal([]byte(out.text), &out)
	return out
}

// TestPlatformAdminSelfConnection_DraftAndSavedRunAgree is #2086: the same
// call on the platform-admin connection succeeds from a draft (the caller's
// token, over the loopback) and from a saved script's run (no token, served
// in this process as the script's identity), each authorized by the admin
// API's own rule and audited as the caller; a saved run whose owner's roles
// do not reach the admin persona is refused by that rule, naming it.
func TestPlatformAdminSelfConnection_DraftAndSavedRunAgree(t *testing.T) {
	scriptAdmin := &middleware.UserInfo{
		UserID: "script:refs-monitor", Email: "owner@example.com", Roles: []string{"admin"},
		AuthType: middleware.AuthTypeScript, OnBehalfOf: "owner@example.com",
	}
	scriptAnalyst := &middleware.UserInfo{
		UserID: "script:refs-monitor", Email: "owner@example.com", Roles: []string{"analyst"},
		AuthType: middleware.AuthTypeScript, OnBehalfOf: "owner@example.com",
	}
	signedIn := &middleware.UserInfo{UserID: "admin@example.com", Email: "admin@example.com", Roles: []string{"admin"}}

	t.Run("a draft forwards the caller's token over the loopback", func(t *testing.T) {
		f := newSelfDispatchFixture(t, signedIn, middleware.SourceScript, "admin-token")
		out := f.call(t)
		if out.Body["user"] != "admin@example.com" || f.overWire != 1 {
			t.Fatalf("draft call: served %v over the wire %d times, result %s", f.served, f.overWire, out.text)
		}
	})

	t.Run("a saved run is served in process as the script", func(t *testing.T) {
		f := newSelfDispatchFixture(t, scriptAdmin, middleware.SourceScript, "")
		out := f.call(t)
		if out.Status != http.StatusOK || out.Body["user"] != "script:refs-monitor" {
			t.Fatalf("saved run result %s; want the admin API to have served script:refs-monitor", out.text)
		}
		if f.overWire != 0 {
			t.Errorf("a token-less call went over the loopback %d times; it is served in process", f.overWire)
		}
		ev, ok := waitForAuditEvent(f.logger, apigateway.ToolInvokeEndpoint, time.Second)
		if !ok || ev.UserID != "script:refs-monitor" {
			t.Errorf("audit event %+v; want the call attributed to script:refs-monitor", ev)
		}
	})

	t.Run("a saved run whose owner is not an admin is refused naming the persona", func(t *testing.T) {
		f := newSelfDispatchFixture(t, scriptAnalyst, middleware.SourceScript, "")
		out := f.call(t)
		if out.Status != http.StatusForbidden {
			t.Errorf("status %d; want 403", out.Status)
		}
		for _, want := range []string{`the admin API requires the persona \"admin\"`, "[admin]", "[analyst]"} {
			if !strings.Contains(out.text, want) {
				t.Errorf("refusal %s does not contain %q", out.text, want)
			}
		}
		if len(f.served) != 0 {
			t.Errorf("the admin API served %v; want nothing", f.served)
		}
	})
}
