package connsecretref

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/secretstore"
	"github.com/txn2/mcp-data-platform/internal/upstreamauth"
	"github.com/txn2/mcp-data-platform/internal/upstreamauth/sessionlogin"
)

func TestAllowed(t *testing.T) {
	cc := map[string]any{"auth_mode": "oauth", "oauth_grant": "client_credentials"}
	ac := map[string]any{"auth_mode": "oauth", "oauth_grant": "authorization_code"}
	legacy := map[string]any{"auth_mode": "oauth2_client_credentials"}
	cases := []struct {
		kind string
		cfg  map[string]any
		path string
		want bool
	}{
		{"api", nil, "credential", true},
		{"graphql", nil, "password", true},
		{"api", nil, "session_login_body", true},
		{"api", nil, "session_login_secret", true},
		{"api", nil, "static_headers.X-Sub", true},
		{"api", nil, "session_login_headers.X-Key", true},
		{"api", nil, "static_headers", false},
		{"api", nil, "static_headers.a.b", false},
		{"api", nil, "base_url", false},
		{"api", cc, "oauth_client_secret", true},
		{"api", legacy, "oauth_client_id", true},
		{"api", ac, "oauth_client_secret", false},
		{"api", map[string]any{"auth_mode": "bearer"}, "oauth_client_secret", false},
		{"mcp", nil, "credential", true},
		{"mcp", cc, "oauth_client_secret", true},
		{"mcp", nil, "static_headers.X", false},
		{"trino", nil, "password", false},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, Allowed(tc.kind, tc.cfg, tc.path), "%s %s", tc.kind, tc.path)
	}
	assert.Contains(t, Fields("api"), "static_headers")
	assert.Contains(t, Fields("mcp"), "credential, and oauth_client_id")
	assert.Equal(t, "none of a trino connection's fields", Fields("trino"))
}

// TestKeysAreTheOnesTheKindsRead pins the spelling of every key Allowed
// names to the key internal/upstreamauth parses, so a rename in one cannot
// leave the save check approving a field nothing fills.
func TestKeysAreTheOnesTheKindsRead(t *testing.T) {
	c, err := upstreamauth.Parse("api", "apigateway", "https://h.example.com", map[string]any{
		keyAuthMode: "basic", keyCredential: "c", keyUsername: "u", keyPassword: "p", keyPathSecret: "s",
		keyStaticHeaders: map[string]any{"X-A": "v"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"c", "u", "p", "s", "v"}, []string{c.Credential, c.Username, c.Password, c.PathSecret, c.StaticHeaders["X-A"]})
	s := sessionlogin.Parse("https://h.example.com", map[string]any{
		sessionlogin.ConfigKeyLoginBody: "b", sessionlogin.ConfigKeyLoginSecret: "s", sessionlogin.ConfigKeyLoginHeaders: map[string]any{"X": "h"},
	})
	assert.Equal(t, []string{"b", "s", "h"}, []string{s.LoginBody, s.Secret, s.LoginHeaders["X"]})
}

// scopes is a store that knows two secrets: one usable by connection
// "vendor", one limited to a persona.
type scopes map[string]secretstore.Secret

func (s scopes) Get(_ context.Context, name string) (secretstore.Secret, error) {
	if name == "down" {
		return secretstore.Secret{}, errors.New("database down")
	}
	sec, ok := s[name]
	if !ok {
		return secretstore.Secret{}, secretstore.ErrNotFound
	}
	return sec, nil
}

var known = scopes{
	"vendor-token": {Name: "vendor-token", AllowConnections: []string{"vendor"}},
	"finance-only": {Name: "finance-only", AllowConnections: []string{"vendor"}, AllowPersonas: []string{"finance"}},
}

func TestCheck(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, Check(ctx, known, "api", "vendor", map[string]any{"base_url": "https://x"}), "no reference, nothing to check")
	require.NoError(t, Check(ctx, known, "api", "vendor", map[string]any{
		"credential": "{{secret:vendor-token}}", "static_headers": map[string]string{"X-K": "{{secret:vendor-token}}"},
	}))
	// An mcp connection is addressed by its connection_name.
	require.NoError(t, Check(ctx, known, "mcp", "instance", map[string]any{"credential": "{{secret:vendor-token}}", "connection_name": "vendor"}))

	for name, c := range map[string]struct {
		scopes Scopes
		kind   string
		cfg    map[string]any
		want   string
	}{
		"out of scope":       {known, "mcp", map[string]any{"credential": "{{secret:vendor-token}}"}, `may not be used by connection "instance"`},
		"missing":            {known, "api", map[string]any{"credential": "{{secret:nope}}"}, `which does not exist; store it under Admin > Secrets with "instance"`},
		"personas":           {known, "mcp", map[string]any{"credential": "{{secret:finance-only}}", "connection_name": "vendor"}, "clear its allow_personas"},
		"unreadable":         {known, "api", map[string]any{"credential": "{{secret:down}}"}, "could not be read"},
		"no store":           {nil, "api", map[string]any{"credential": "{{secret:vendor-token}}"}, "not available on this deployment"},
		"malformed":          {known, "api", map[string]any{"credential": "{{secret:Bad}}"}, "not {{secret:<name>}}"},
		"one-time code":      {known, "api", map[string]any{"credential": "{{totp:vendor-token}}"}, "filled only in an api call's request"},
		"unfilled field":     {known, "api", map[string]any{"base_url": "https://x/{{secret:vendor-token}}"}, "field base_url names a stored secret"},
		"a list entry":       {known, "api", map[string]any{"scopes": []any{"{{secret:vendor-token}}"}}, "field scopes[0] names a stored secret"},
		"a trino connection": {known, "trino", map[string]any{"password": "{{secret:vendor-token}}"}, "none of a trino connection's fields"},
	} {
		err := Check(ctx, c.scopes, c.kind, "instance", c.cfg)
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), c.want, name)
	}
}

func TestDropCarriedSessionSecret(t *testing.T) {
	body := `{"t":"{{secret:pat}}"}`
	cfg := map[string]any{sessionlogin.ConfigKeyLoginSecret: "[REDACTED]", sessionlogin.ConfigKeyLoginBody: body}
	DropCarriedSessionSecret(cfg, "[REDACTED]")
	assert.NotContains(t, cfg, sessionlogin.ConfigKeyLoginSecret)

	kept := map[string]any{sessionlogin.ConfigKeyLoginSecret: "[REDACTED]", sessionlogin.ConfigKeyLoginBody: `{"t":"{{secret}}"}`}
	DropCarriedSessionSecret(kept, "[REDACTED]")
	assert.Contains(t, kept, sessionlogin.ConfigKeyLoginSecret, "a body that still writes it keeps it")

	typed := map[string]any{sessionlogin.ConfigKeyLoginSecret: "typed", sessionlogin.ConfigKeyLoginBody: body}
	DropCarriedSessionSecret(typed, "[REDACTED]")
	assert.Contains(t, typed, sessionlogin.ConfigKeyLoginSecret, "a typed value is left for validation")
}

func TestNamesStoredSecret(t *testing.T) {
	assert.True(t, NamesStoredSecret("{{secret:pat}}"))
	assert.False(t, NamesStoredSecret("Bearer {{secret:pat}}"))
	assert.False(t, NamesStoredSecret(42))
}
