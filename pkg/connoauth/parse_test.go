package connoauth

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"golang.org/x/oauth2"
)

func TestParseConfig(t *testing.T) {
	cases := []struct {
		name       string
		cfg        map[string]any
		want       Config
		wantErr    bool
		wantLegacy bool
	}{
		{
			name: "canonical only",
			cfg: map[string]any{
				"auth_mode":                 "oauth",
				"oauth_grant":               "authorization_code",
				"oauth_token_url":           "https://idp/token",
				"oauth_authorization_url":   "https://idp/authorize",
				"oauth_client_id":           "cid",
				"oauth_client_secret":       "secret",
				"oauth_scope":               "openid offline_access",
				"oauth_prompt":              "consent",
				"oauth_endpoint_auth_style": "params",
			},
			want: Config{
				Grant:             "authorization_code",
				AuthorizationURL:  "https://idp/authorize",
				TokenURL:          "https://idp/token",
				ClientID:          "cid",
				ClientSecret:      "secret",
				Scopes:            []string{"openid", "offline_access"},
				EndpointAuthStyle: oauth2.AuthStyleInParams,
				Prompt:            "consent",
			},
			wantLegacy: false,
		},
		{
			name: "legacy only",
			cfg: map[string]any{
				"auth_mode":                  "oauth2_authorization_code",
				"oauth2_token_url":           "https://idp/token",
				"oauth2_authorization_url":   "https://idp/authorize",
				"oauth2_client_id":           "cid",
				"oauth2_client_secret":       "secret",
				"oauth2_scopes":              []any{"openid", "offline_access"},
				"oauth2_prompt":              "consent",
				"oauth2_endpoint_auth_style": "params",
			},
			want: Config{
				Grant:             "authorization_code",
				AuthorizationURL:  "https://idp/authorize",
				TokenURL:          "https://idp/token",
				ClientID:          "cid",
				ClientSecret:      "secret",
				Scopes:            []string{"openid", "offline_access"},
				EndpointAuthStyle: oauth2.AuthStyleInParams,
				Prompt:            "consent",
			},
			wantLegacy: true,
		},
		{
			name: "mixed canonical wins per field",
			cfg: map[string]any{
				"auth_mode":        "oauth2_authorization_code", // legacy auth_mode...
				"oauth_grant":      "client_credentials",        // ...but canonical grant wins
				"oauth_token_url":  "https://canonical/token",
				"oauth2_token_url": "https://legacy/token",
				"oauth_client_id":  "canonical-cid",
				"oauth2_client_id": "legacy-cid",
			},
			want: Config{
				Grant:             "client_credentials",
				TokenURL:          "https://canonical/token",
				ClientID:          "canonical-cid",
				EndpointAuthStyle: oauth2.AuthStyleInHeader,
			},
			wantLegacy: false,
		},
		{
			name: "legacy scope array of []string shape",
			cfg: map[string]any{
				"auth_mode":     "oauth2_client_credentials",
				"oauth2_scopes": []string{"a", "b"},
			},
			want: Config{
				Grant:             "client_credentials",
				Scopes:            []string{"a", "b"},
				EndpointAuthStyle: oauth2.AuthStyleInHeader,
			},
			wantLegacy: true,
		},
		{
			name: "empty config",
			cfg:  map[string]any{},
			want: Config{
				Grant:             "client_credentials", // default when no grant/auth_mode
				EndpointAuthStyle: oauth2.AuthStyleInHeader,
			},
			wantLegacy: false,
		},
		{
			name:    "malformed scope wrong type canonical",
			cfg:     map[string]any{"oauth_scope": 42},
			wantErr: true,
		},
		{
			name:    "malformed scope non-string element legacy",
			cfg:     map[string]any{"oauth2_scopes": []any{"ok", 7}},
			wantErr: true,
		},
		{
			name:    "malformed scope wrong type legacy",
			cfg:     map[string]any{"oauth2_scopes": "not-an-array"},
			wantErr: true,
		},
		{
			name:    "unknown auth_mode",
			cfg:     map[string]any{"auth_mode": "oauth2_bogus"},
			wantErr: true,
		},
		{
			name:    "unknown grant",
			cfg:     map[string]any{"oauth_grant": "device_code"},
			wantErr: true,
		},
		{
			name:    "unknown endpoint auth style canonical",
			cfg:     map[string]any{"oauth_endpoint_auth_style": "bogus"},
			wantErr: true,
		},
		{
			name:    "unknown endpoint auth style legacy",
			cfg:     map[string]any{"oauth2_endpoint_auth_style": "bogus"},
			wantErr: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseConfig("api", c.name, c.cfg)
			if (err != nil) != c.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, c.wantErr)
			}
			if c.wantErr {
				if !errors.Is(err, ErrInvalidConfig) {
					t.Errorf("err=%v want errors.Is ErrInvalidConfig", err)
				}
				return
			}
			assertConfigEqual(t, got, c.want)
		})
	}
}

func assertConfigEqual(t *testing.T, got, want Config) {
	t.Helper()
	if got.Grant != want.Grant {
		t.Errorf("Grant=%q want %q", got.Grant, want.Grant)
	}
	if got.AuthorizationURL != want.AuthorizationURL {
		t.Errorf("AuthorizationURL=%q want %q", got.AuthorizationURL, want.AuthorizationURL)
	}
	if got.TokenURL != want.TokenURL {
		t.Errorf("TokenURL=%q want %q", got.TokenURL, want.TokenURL)
	}
	if got.ClientID != want.ClientID {
		t.Errorf("ClientID=%q want %q", got.ClientID, want.ClientID)
	}
	if got.ClientSecret != want.ClientSecret {
		t.Errorf("ClientSecret=%q want %q", got.ClientSecret, want.ClientSecret)
	}
	if got.Prompt != want.Prompt {
		t.Errorf("Prompt=%q want %q", got.Prompt, want.Prompt)
	}
	if got.EndpointAuthStyle != want.EndpointAuthStyle {
		t.Errorf("EndpointAuthStyle=%v want %v", got.EndpointAuthStyle, want.EndpointAuthStyle)
	}
	if strings.Join(got.Scopes, " ") != strings.Join(want.Scopes, " ") {
		t.Errorf("Scopes=%v want %v", got.Scopes, want.Scopes)
	}
}

// TestParseConfig_NilConfig proves a nil map returns the zero Config
// without error (the caller validates required fields downstream).
func TestParseConfig_NilConfig(t *testing.T) {
	got, err := ParseConfig("mcp", "n", nil)
	if err != nil {
		t.Fatalf("ParseConfig(nil): %v", err)
	}
	assertConfigEqual(t, got, Config{})
	if got.Scopes != nil {
		t.Errorf("expected nil Scopes for nil config, got %v", got.Scopes)
	}
}

// TestParseConfig_DeprecationWarnOncePerConnection proves the legacy-key
// deprecation warning is emitted exactly once per (kind, name) per
// process lifetime, and that a distinct (kind, name) gets its own line.
func TestParseConfig_DeprecationWarnOncePerConnection(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(prev)

	legacyCfg := map[string]any{
		"auth_mode":        "oauth2_client_credentials",
		"oauth2_token_url": "https://idp/token",
	}
	// Use unique names so this test is independent of any other test
	// that may have populated the package-level dedup map.
	for range 3 {
		if _, err := ParseConfig("api", "warn-once-conn", legacyCfg); err != nil {
			t.Fatalf("ParseConfig: %v", err)
		}
	}
	if _, err := ParseConfig("api", "warn-once-other", legacyCfg); err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}

	got := strings.Count(buf.String(), "deprecated oauth2_* config keys")
	if got != 2 {
		t.Errorf("deprecation warnings=%d want 2 (one per distinct connection)", got)
	}
	if !strings.Contains(buf.String(), "name=warn-once-conn") ||
		!strings.Contains(buf.String(), "name=warn-once-other") {
		t.Errorf("expected a warning for each connection name, got: %s", buf.String())
	}
}

// TestParseConfig_CanonicalEmitsNoDeprecation proves a canonical-only
// config produces no deprecation warning.
func TestParseConfig_CanonicalEmitsNoDeprecation(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(prev)

	cfg := map[string]any{
		"auth_mode":       "oauth",
		"oauth_grant":     "client_credentials",
		"oauth_token_url": "https://idp/token",
	}
	if _, err := ParseConfig("mcp", "canonical-no-warn", cfg); err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if strings.Contains(buf.String(), "deprecated") {
		t.Errorf("canonical config should not warn, got: %s", buf.String())
	}
}
