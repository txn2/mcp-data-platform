package graphql

import (
	"context"
	"strings"
	"testing"
)

func oauthConfig() map[string]any {
	return map[string]any{
		"endpoint_url":            "https://x.example.com/graphql",
		"auth_mode":               "oauth",
		"oauth_grant":             "authorization_code",
		"oauth_token_url":         "https://idp.example.com/token",
		"oauth_authorization_url": "https://idp.example.com/authorize",
		"oauth_client_id":         "id",
		"oauth_client_secret":     "secret",
		"oauth_scope":             "read",
	}
}

func TestParseOAuthConfigMapsTheConnectionOntoTheSharedFlow(t *testing.T) {
	h := NewOAuthKindHandler(nil)
	cfg, err := h.ParseOAuthConfig(oauthConfig())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.TokenURL != "https://idp.example.com/token" || cfg.ClientID != "id" {
		t.Errorf("cfg = %+v", cfg)
	}
	if cfg.AuthorizationURL == "" {
		t.Error("the authorization endpoint is what the browser flow starts at")
	}
}

func TestParseOAuthConfigRefusesAConnectionWithNoBrowserFlow(t *testing.T) {
	h := NewOAuthKindHandler(nil)
	if _, err := h.ParseOAuthConfig(map[string]any{"endpoint_url": "https://x"}); err == nil {
		t.Error("an unauthenticated connection was accepted for the browser flow")
	} else if !strings.Contains(err.Error(), "authorization_code") {
		t.Errorf("err = %v", err)
	}
	if _, err := h.ParseOAuthConfig(map[string]any{}); err == nil {
		t.Error("an invalid config was accepted")
	}
}

func TestAfterConnectReadsTheSchemaNowThatThereIsACredential(t *testing.T) {
	u := newUpstream(t)
	u.introspection = flatIntrospectionResult
	tk := newToolkit(t, u, "", nil)
	h := NewOAuthKindHandler(tk)

	if err := h.AfterConnect(context.Background(), "gql", nil); err != nil {
		t.Fatalf("after connect: %v", err)
	}
	info, _ := tk.SchemaInfo("gql")
	if info.OperationCount == 0 {
		t.Error("the schema was not read once the flow completed")
	}
	// A connection this toolkit does not hold, and a nil toolkit, are
	// both no-ops rather than failures: the unified handler calls every
	// registered kind.
	if err := h.AfterConnect(context.Background(), "absent", nil); err != nil {
		t.Errorf("an absent connection gave %v", err)
	}
	if err := NewOAuthKindHandler(nil).AfterConnect(context.Background(), "gql", nil); err != nil {
		t.Errorf("a nil toolkit gave %v", err)
	}
}
