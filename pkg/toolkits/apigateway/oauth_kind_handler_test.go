package apigateway

import (
	"context"
	"testing"

	"golang.org/x/oauth2"
)

func TestOAuthKindHandler_ParseOAuthConfig_RejectsNonAuthCode(t *testing.T) {
	t.Parallel()
	h := &OAuthKindHandler{}
	// api_key mode — not OAuth at all.
	_, err := h.ParseOAuthConfig(map[string]any{
		"base_url":          "https://api.example/",
		"auth_mode":         "api_key",
		"api_key_placement": "header",
		"api_key_header":    "X-API-Key",
		"credential":        "k",
	})
	if err == nil {
		t.Fatal("expected error for non-authorization_code mode")
	}
}

func TestOAuthKindHandler_ParseOAuthConfig_MapsAllFields(t *testing.T) {
	t.Parallel()
	h := &OAuthKindHandler{}
	cfg, err := h.ParseOAuthConfig(map[string]any{
		"base_url":                   "https://api.example/",
		"auth_mode":                  "oauth2_authorization_code",
		"oauth2_authorization_url":   "https://idp/auth",
		"oauth2_token_url":           "https://idp/token",
		"oauth2_client_id":           "client-id",
		"oauth2_client_secret":       "client-secret",
		"oauth2_scopes":              []any{"openid", "offline_access"},
		"oauth2_endpoint_auth_style": "params",
		"oauth2_prompt":              "consent",
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
	if cfg.Prompt != "consent" {
		t.Fatalf("prompt not mapped: %q", cfg.Prompt)
	}
	if cfg.EndpointAuthStyle != oauth2.AuthStyleInParams {
		t.Fatalf("params auth style not mapped: got %v", cfg.EndpointAuthStyle)
	}
	if len(cfg.Scopes) != 2 || cfg.Scopes[0] != "openid" || cfg.Scopes[1] != "offline_access" {
		t.Fatalf("scopes not mapped: %v", cfg.Scopes)
	}
}

// TestOAuthKindHandler_ParseOAuthConfig_PropagatesCABundle pins the
// initial code-exchange path's CA-bundle propagation. The doc claims
// that `tls_ca_bundle_pem` is honored on both the token-exchange and
// refresh paths against private-CA IdPs; without this propagation the
// very first Connect attempt would fall back to system trust and the
// TLS handshake would fail before the refresh path ever ran. A
// regression that re-broke the mapping (e.g., by re-introducing the
// hand-rolled struct literal here) would silently re-create that
// production bug.
func TestOAuthKindHandler_ParseOAuthConfig_PropagatesCABundle(t *testing.T) {
	t.Parallel()
	// A short throwaway PEM is enough; ParseConfig validates parseability
	// via validateCABundle (covered by the apigateway tls_test.go suite),
	// so this test asserts only the propagation through the kind handler.
	caCert, _, _ := generateCertPair(t, keyRSA2048)
	h := &OAuthKindHandler{}
	cfg, err := h.ParseOAuthConfig(map[string]any{
		"base_url":                 "https://api.example/",
		"auth_mode":                "oauth2_authorization_code",
		"oauth2_authorization_url": "https://idp/auth",
		"oauth2_token_url":         "https://idp/token",
		"oauth2_client_id":         "c",
		"oauth2_client_secret":     "s",
		"tls_ca_bundle_pem":        caCert,
	})
	if err != nil {
		t.Fatalf("ParseOAuthConfig: %v", err)
	}
	if cfg.CABundlePEM != caCert {
		t.Fatalf("CABundlePEM not propagated: want %q got %q", caCert, cfg.CABundlePEM)
	}
}

func TestOAuthKindHandler_ParseOAuthConfig_DefaultsToHeaderAuthStyle(t *testing.T) {
	t.Parallel()
	h := &OAuthKindHandler{}
	cfg, err := h.ParseOAuthConfig(map[string]any{
		"base_url":                 "https://api.example/",
		"auth_mode":                "oauth2_authorization_code",
		"oauth2_authorization_url": "https://idp/auth",
		"oauth2_token_url":         "https://idp/token",
		"oauth2_client_id":         "c",
		"oauth2_client_secret":     "s",
	})
	if err != nil {
		t.Fatalf("ParseOAuthConfig: %v", err)
	}
	if cfg.EndpointAuthStyle != oauth2.AuthStyleInHeader {
		t.Fatalf("default auth style must be header (Basic), got %v", cfg.EndpointAuthStyle)
	}
}

func TestOAuthKindHandler_AfterConnect_IsNoOp(t *testing.T) {
	t.Parallel()
	h := &OAuthKindHandler{}
	if err := h.AfterConnect(context.Background(), "any-name", nil); err != nil {
		t.Fatalf("API gateway AfterConnect must be a no-op, got %v", err)
	}
}

func TestNewOAuthKindHandler_AcceptsNilToolkit(t *testing.T) {
	t.Parallel()
	if h := NewOAuthKindHandler(nil); h == nil {
		t.Fatal("API kind handler must be constructable without a toolkit reference")
	}
}
