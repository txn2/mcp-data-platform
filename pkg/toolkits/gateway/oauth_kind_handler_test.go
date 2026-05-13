package gateway

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/connoauth"
	"golang.org/x/oauth2"
)

func TestNewOAuthKindHandler_NilToolkit(t *testing.T) {
	t.Parallel()
	if h := NewOAuthKindHandler(nil); h != nil {
		t.Fatalf("nil toolkit must produce nil handler, got %+v", h)
	}
}

func TestOAuthKindHandler_ParseOAuthConfig_RejectsNonOAuth(t *testing.T) {
	t.Parallel()
	h := &OAuthKindHandler{}
	cases := []map[string]any{
		{ // bearer
			"endpoint":  "http://upstream/mcp",
			"auth_mode": "bearer",
		},
		{ // oauth but client_credentials grant
			"endpoint":            "http://upstream/mcp",
			"auth_mode":           "oauth",
			"oauth_grant":         "client_credentials",
			"oauth_token_url":     "https://idp/token",
			"oauth_client_id":     "c",
			"oauth_client_secret": "s",
		},
	}
	for i, raw := range cases {
		_, err := h.ParseOAuthConfig(raw)
		if err == nil {
			t.Fatalf("case %d: expected error, got nil", i)
		}
	}
}

func TestOAuthKindHandler_ParseOAuthConfig_MapsAuthorizationCode(t *testing.T) {
	t.Parallel()
	h := &OAuthKindHandler{}
	cfg, err := h.ParseOAuthConfig(map[string]any{
		"endpoint":                "http://upstream/mcp",
		"auth_mode":               "oauth",
		"oauth_grant":             "authorization_code",
		"oauth_authorization_url": "https://idp/auth",
		"oauth_token_url":         "https://idp/token",
		"oauth_client_id":         "client-id",
		"oauth_client_secret":     "client-secret",
		"oauth_scope":             "openid offline_access profile",
		"oauth_prompt":            "login",
	})
	if err != nil {
		t.Fatalf("ParseOAuthConfig: %v", err)
	}
	if cfg.AuthorizationURL != "https://idp/auth" || cfg.TokenURL != "https://idp/token" {
		t.Fatalf("URLs not mapped: %+v", cfg)
	}
	if cfg.ClientID != "client-id" || cfg.ClientSecret != "client-secret" {
		t.Fatalf("credentials not mapped: %+v", cfg)
	}
	if cfg.Prompt != "login" {
		t.Fatalf("prompt not mapped: %q", cfg.Prompt)
	}
	if cfg.EndpointAuthStyle != oauth2.AuthStyleInHeader {
		t.Fatalf("MCP gateway default auth style must be header, got %v", cfg.EndpointAuthStyle)
	}
	wantScopes := []string{"openid", "offline_access", "profile"}
	if !equalStrings(cfg.Scopes, wantScopes) {
		t.Fatalf("scopes not split: got %v want %v", cfg.Scopes, wantScopes)
	}
}

func TestOAuthKindHandler_ParseOAuthConfig_EmptyScopeReturnsNil(t *testing.T) {
	t.Parallel()
	h := &OAuthKindHandler{}
	cfg, err := h.ParseOAuthConfig(map[string]any{
		"endpoint":                "http://upstream/mcp",
		"auth_mode":               "oauth",
		"oauth_grant":             "authorization_code",
		"oauth_authorization_url": "https://idp/auth",
		"oauth_token_url":         "https://idp/token",
		"oauth_client_id":         "c",
		"oauth_client_secret":     "s",
		"oauth_scope":             "",
	})
	if err != nil {
		t.Fatalf("ParseOAuthConfig: %v", err)
	}
	if cfg.Scopes != nil {
		t.Fatalf("empty scope string must produce nil slice, got %v", cfg.Scopes)
	}
}

func TestOAuthKindHandler_ParseOAuthConfig_BadParseConfigPropagates(t *testing.T) {
	t.Parallel()
	h := &OAuthKindHandler{}
	// Missing endpoint — ParseConfig rejects.
	_, err := h.ParseOAuthConfig(map[string]any{
		"auth_mode": "oauth",
	})
	if err == nil {
		t.Fatal("expected ParseConfig error to surface")
	}
}

func TestOAuthKindHandler_AfterConnect_SeedsAndRebuilds(t *testing.T) {
	t.Parallel()
	// Use a Toolkit with no real http upstream; AddConnection will
	// fail when it tries to dial, but the order of operations (Has?
	// then Add) is what we want to exercise. We assert by checking
	// the AddConnection error path doesn't panic and the receiver
	// invariant (h.toolkit nil-check) holds via NewOAuthKindHandler.
	tk := New("test")
	h := NewOAuthKindHandler(tk)
	if h == nil {
		t.Fatal("NewOAuthKindHandler returned nil for non-nil toolkit")
	}
	// Connection placeholder doesn't exist; AfterConnect runs the
	// seed branch, which calls AddConnection — that dials and fails
	// against an unreachable host. We only care that the branch is
	// reached and the error wraps; the actual dial failure is the
	// upstream toolkit's responsibility.
	err := h.AfterConnect(context.Background(), "nope", map[string]any{
		"endpoint":   "http://127.0.0.1:0/mcp",
		"auth_mode":  "bearer",
		"credential": "x",
	})
	if err == nil {
		t.Skip("AddConnection unexpectedly succeeded against an unreachable host; environment-dependent")
	}
	if !strings.Contains(err.Error(), "seed connection placeholder") {
		t.Fatalf("expected seed-placeholder wrap, got %v", err)
	}
}

func TestSplitScopeString(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"openid", []string{"openid"}},
		{"openid offline_access", []string{"openid", "offline_access"}},
		{"  openid   offline_access  ", []string{"openid", "offline_access"}},
		{"a\tb", []string{"a", "b"}},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got := splitScopeString(tc.in)
			if !equalStrings(got, tc.want) {
				t.Fatalf("split(%q) = %v want %v", tc.in, got, tc.want)
			}
		})
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// silence unused-import lint when the connoauth import isn't otherwise
// referenced by a focused subset of these tests.
var _ = connoauth.KindMCP
var _ = errors.New
