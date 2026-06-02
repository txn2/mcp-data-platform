package apigateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/mcpcontext"
)

func TestParseConfig_IdentityPassthroughAndAdminOnly(t *testing.T) {
	cfg, err := ParseConfig(map[string]any{
		"base_url":             "https://x",
		"auth_mode":            AuthModeNone,
		"identity_passthrough": true,
		"admin_only":           true,
	})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if !cfg.IdentityPassthrough {
		t.Error("IdentityPassthrough = false; want true")
	}
	if !cfg.AdminOnly {
		t.Error("AdminOnly = false; want true")
	}
}

func TestParseConfig_FlagsDefaultFalse(t *testing.T) {
	cfg, err := ParseConfig(map[string]any{"base_url": "https://x"})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.IdentityPassthrough || cfg.AdminOnly {
		t.Errorf("flags should default false; got passthrough=%v admin_only=%v", cfg.IdentityPassthrough, cfg.AdminOnly)
	}
}

func TestParseConfig_StringBoolFlags(t *testing.T) {
	cfg, err := ParseConfig(map[string]any{
		"base_url":             "https://x",
		"identity_passthrough": "true",
		"admin_only":           "false",
	})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if !cfg.IdentityPassthrough {
		t.Error("string \"true\" should parse to IdentityPassthrough=true")
	}
	if cfg.AdminOnly {
		t.Error("string \"false\" should parse to AdminOnly=false")
	}
}

func TestValidate_IdentityPassthroughRequiresAuthModeNone(t *testing.T) {
	_, err := ParseConfig(map[string]any{
		"base_url":             "https://x",
		"auth_mode":            AuthModeBearer,
		"credential":           "shared-secret",
		"identity_passthrough": true,
	})
	if err == nil {
		t.Fatal("expected error: identity_passthrough with auth_mode=bearer")
	}
	if !strings.Contains(err.Error(), "identity_passthrough requires auth_mode=none") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGetBool(t *testing.T) {
	tests := []struct {
		name string
		val  any
		want bool
	}{
		{"native true", true, true},
		{"native false", false, false},
		{"string true", "true", true},
		{"string 1", "1", true},
		{"string false", "false", false},
		{"string garbage", "nope", false},
		{"absent", nil, false},
		{"wrong type", 42, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := map[string]any{}
			if tt.val != nil {
				cfg["k"] = tt.val
			}
			if got := getBool(cfg, "k"); got != tt.want {
				t.Errorf("getBool(%v) = %v; want %v", tt.val, got, tt.want)
			}
		})
	}
}

// TestHandleInvoke_IdentityPassthroughForwardsCallerToken verifies the
// invoke path replaces the (absent) shared credential with the caller's
// inbound token read from the context.
func TestHandleInvoke_IdentityPassthroughForwardsCallerToken(t *testing.T) {
	var (
		mu      sync.Mutex
		gotAuth string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotAuth = r.Header.Get("Authorization")
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	tk := New("test")
	if err := tk.AddConnection("self", map[string]any{
		"base_url":             srv.URL,
		"auth_mode":            AuthModeNone,
		"identity_passthrough": true,
	}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}

	ctx := mcpcontext.WithAuthToken(context.Background(), "caller-token-xyz")
	res, _, err := tk.handleInvoke(ctx, nil, InvokeInput{
		Connection: "self", Method: "GET", Path: "/admin/personas",
	})
	if err != nil {
		t.Fatalf("handleInvoke: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", textContent(res))
	}
	mu.Lock()
	auth := gotAuth
	mu.Unlock()
	if auth != "Bearer caller-token-xyz" {
		t.Errorf("upstream saw Authorization %q; want forwarded caller token", auth)
	}
}

// TestHandleInvoke_IdentityPassthroughRequiresToken verifies a passthrough
// call with no caller token fails rather than calling anonymously.
func TestHandleInvoke_IdentityPassthroughRequiresToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("upstream must not be contacted without a caller token")
	}))
	defer srv.Close()

	tk := New("test")
	if err := tk.AddConnection("self", map[string]any{
		"base_url":             srv.URL,
		"auth_mode":            AuthModeNone,
		"identity_passthrough": true,
	}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}

	res, _, err := tk.handleInvoke(context.Background(), nil, InvokeInput{
		Connection: "self", Method: "GET", Path: "/admin/personas",
	})
	if err != nil {
		t.Fatalf("handleInvoke unexpected go error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected an error result when no caller token is present")
	}
	if !strings.Contains(textContent(res), "identity passthrough requires an authenticated caller token") {
		t.Errorf("missing actionable error; got: %s", textContent(res))
	}
}

func TestAdminOnlyConnections(t *testing.T) {
	tk := New("test")
	mustAdd := func(name string, adminOnly bool) {
		t.Helper()
		if err := tk.AddConnection(name, map[string]any{
			"base_url":   "https://x",
			"admin_only": adminOnly,
		}); err != nil {
			t.Fatalf("AddConnection(%s): %v", name, err)
		}
	}
	mustAdd("zeta-admin", true)
	mustAdd("ordinary", false)
	mustAdd("alpha-admin", true)

	got := tk.AdminOnlyConnections()
	want := []string{"alpha-admin", "zeta-admin"} // sorted, only admin_only
	if len(got) != len(want) {
		t.Fatalf("AdminOnlyConnections() = %v; want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("AdminOnlyConnections()[%d] = %q; want %q", i, got[i], want[i])
		}
	}
}
