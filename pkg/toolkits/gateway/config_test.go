package gateway

import (
	"strings"
	"testing"
	"time"
)

func TestParseConfig_Defaults(t *testing.T) {
	cfg, err := ParseConfig(map[string]any{
		"endpoint": "https://upstream.example.com/mcp",
	})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.Endpoint != "https://upstream.example.com/mcp" {
		t.Errorf("endpoint: got %q", cfg.Endpoint)
	}
	if cfg.AuthMode != AuthModeNone {
		t.Errorf("auth_mode default: got %q, want %q", cfg.AuthMode, AuthModeNone)
	}
	if cfg.TrustLevel != TrustLevelUntrusted {
		t.Errorf("trust_level default: got %q, want %q", cfg.TrustLevel, TrustLevelUntrusted)
	}
	if cfg.ConnectTimeout != DefaultConnectTimeout {
		t.Errorf("connect_timeout default: got %v, want %v", cfg.ConnectTimeout, DefaultConnectTimeout)
	}
	if cfg.CallTimeout != DefaultCallTimeout {
		t.Errorf("call_timeout default: got %v, want %v", cfg.CallTimeout, DefaultCallTimeout)
	}
}

func TestParseConfig_AllFields(t *testing.T) {
	cfg, err := ParseConfig(map[string]any{
		"endpoint":        "https://upstream.example.com/mcp",
		"auth_mode":       AuthModeBearer,
		"credential":      "secret-token",
		"connection_name": "crm",
		"connect_timeout": "5s",
		"call_timeout":    "2m",
		"trust_level":     TrustLevelTrusted,
	})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.AuthMode != AuthModeBearer || cfg.Credential != "secret-token" {
		t.Errorf("auth fields: got mode=%q cred=%q", cfg.AuthMode, cfg.Credential)
	}
	if cfg.ConnectionName != "crm" {
		t.Errorf("connection_name: got %q", cfg.ConnectionName)
	}
	if cfg.ConnectTimeout != 5*time.Second || cfg.CallTimeout != 2*time.Minute {
		t.Errorf("timeouts: got connect=%v call=%v", cfg.ConnectTimeout, cfg.CallTimeout)
	}
	if cfg.TrustLevel != TrustLevelTrusted {
		t.Errorf("trust_level: got %q", cfg.TrustLevel)
	}
}

func TestParseConfig_NumericTimeouts(t *testing.T) {
	cases := []struct {
		name string
		raw  any
		want time.Duration
	}{
		{"int", 15, 15 * time.Second},
		{"int64", int64(30), 30 * time.Second},
		{"float", 1.5, 1 * time.Second},
		{"duration", 7 * time.Second, 7 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := ParseConfig(map[string]any{
				"endpoint":        "https://u.example.com",
				"connect_timeout": tc.raw,
			})
			if err != nil {
				t.Fatalf("ParseConfig: %v", err)
			}
			if cfg.ConnectTimeout != tc.want {
				t.Errorf("got %v, want %v", cfg.ConnectTimeout, tc.want)
			}
		})
	}
}

func TestParseConfig_UnparseableStringTimeoutFallsBackToDefault(t *testing.T) {
	cfg, err := ParseConfig(map[string]any{
		"endpoint":     "https://u.example.com",
		"call_timeout": "not-a-duration",
	})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.CallTimeout != DefaultCallTimeout {
		t.Errorf("unparseable timeout should fall back to default, got %v", cfg.CallTimeout)
	}
}

func TestParseConfig_Errors(t *testing.T) {
	cases := []struct {
		name    string
		cfg     map[string]any
		wantMsg string
	}{
		{
			name:    "missing endpoint",
			cfg:     map[string]any{},
			wantMsg: "endpoint is required",
		},
		{
			name: "invalid auth_mode",
			cfg: map[string]any{
				"endpoint":  "https://u.example.com",
				"auth_mode": "kerberos",
			},
			wantMsg: "invalid auth_mode",
		},
		{
			name: "bearer without credential",
			cfg: map[string]any{
				"endpoint":  "https://u.example.com",
				"auth_mode": AuthModeBearer,
			},
			wantMsg: "credential is required",
		},
		{
			name: "api_key without credential",
			cfg: map[string]any{
				"endpoint":  "https://u.example.com",
				"auth_mode": AuthModeAPIKey,
			},
			wantMsg: "credential is required",
		},
		{
			name: "invalid trust_level",
			cfg: map[string]any{
				"endpoint":    "https://u.example.com",
				"trust_level": "maybe",
			},
			wantMsg: "invalid trust_level",
		},
		{
			name: "zero connect_timeout",
			cfg: map[string]any{
				"endpoint":        "https://u.example.com",
				"connect_timeout": "0s",
			},
			wantMsg: "connect_timeout must be positive",
		},
		{
			name: "zero call_timeout",
			cfg: map[string]any{
				"endpoint":     "https://u.example.com",
				"call_timeout": "0s",
			},
			wantMsg: "call_timeout must be positive",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseConfig(tc.cfg)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantMsg)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantMsg)
			}
		})
	}
}

func TestGetStringDefault_EmptyFallsBack(t *testing.T) {
	// Empty string in config should not overwrite the default.
	got := getStringDefault(map[string]any{"k": ""}, "k", "fallback")
	if got != "fallback" {
		t.Errorf("got %q, want %q", got, "fallback")
	}
}

func TestEnsureOfflineAccessScope(t *testing.T) {
	cases := []struct {
		name  string
		grant string
		scope string
		want  string
	}{
		{
			name:  "client_credentials passes scope through unchanged",
			grant: OAuthGrantClientCredentials,
			scope: "read write",
			want:  "read write",
		},
		{
			name:  "client_credentials with empty scope stays empty",
			grant: OAuthGrantClientCredentials,
			scope: "",
			want:  "",
		},
		{
			name:  "authorization_code with empty scope gets default scope",
			grant: OAuthGrantAuthorizationCode,
			scope: "",
			want:  defaultAuthCodeScope,
		},
		{
			name:  "authorization_code preserves scope already containing offline_access",
			grant: OAuthGrantAuthorizationCode,
			scope: "openid profile email offline_access",
			want:  "openid profile email offline_access",
		},
		{
			name:  "authorization_code with custom scope adds offline_access",
			grant: OAuthGrantAuthorizationCode,
			scope: "openid profile",
			want:  "openid profile offline_access",
		},
		{
			name:  "authorization_code does not duplicate when offline_access already present in middle",
			grant: OAuthGrantAuthorizationCode,
			scope: "openid offline_access email",
			want:  "openid offline_access email",
		},
		{
			name:  "match is case-sensitive per RFC 6749 §3.3",
			grant: OAuthGrantAuthorizationCode,
			scope: "openid Offline_Access",
			want:  "openid Offline_Access offline_access",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ensureOfflineAccessScope(tc.grant, tc.scope)
			if got != tc.want {
				t.Errorf("ensureOfflineAccessScope(%q, %q) = %q, want %q",
					tc.grant, tc.scope, got, tc.want)
			}
		})
	}
}

func TestParseConfig_OfflineAccessDefault(t *testing.T) {
	cases := []struct {
		name string
		cfg  map[string]any
		want string
	}{
		{
			name: "nested authorization_code with no scope -> default scope",
			cfg: map[string]any{
				"endpoint":  "https://u.example.com",
				"auth_mode": AuthModeOAuth,
				"oauth": map[string]any{
					"grant":             OAuthGrantAuthorizationCode,
					"token_url":         "https://idp/token",
					"authorization_url": "https://idp/authorize",
					"client_id":         "cid",
					"client_secret":     "csec",
				},
			},
			want: defaultAuthCodeScope,
		},
		{
			name: "flat authorization_code with explicit scope -> offline_access appended",
			cfg: map[string]any{
				"endpoint":                "https://u.example.com",
				"auth_mode":               AuthModeOAuth,
				"oauth_grant":             OAuthGrantAuthorizationCode,
				"oauth_token_url":         "https://idp/token",
				"oauth_authorization_url": "https://idp/authorize",
				"oauth_client_id":         "cid",
				"oauth_client_secret":     "csec",
				"oauth_scope":             "openid profile",
			},
			want: "openid profile offline_access",
		},
		{
			name: "client_credentials leaves scope untouched",
			cfg: map[string]any{
				"endpoint":  "https://u.example.com",
				"auth_mode": AuthModeOAuth,
				"oauth": map[string]any{
					"grant":         OAuthGrantClientCredentials,
					"token_url":     "https://idp/token",
					"client_id":     "cid",
					"client_secret": "csec",
					"scope":         "read",
				},
			},
			want: "read",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := ParseConfig(tc.cfg)
			if err != nil {
				t.Fatalf("ParseConfig: %v", err)
			}
			if cfg.OAuth.Scope != tc.want {
				t.Errorf("OAuth.Scope = %q, want %q", cfg.OAuth.Scope, tc.want)
			}
		})
	}
}
