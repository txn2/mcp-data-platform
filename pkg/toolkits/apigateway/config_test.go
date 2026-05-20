package apigateway

import (
	"strings"
	"testing"
	"time"
)

func TestParseConfig_AppliesDefaults(t *testing.T) {
	c, err := ParseConfig(map[string]any{
		"base_url": "https://api.example.com",
	})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if c.AuthMode != AuthModeNone {
		t.Errorf("default AuthMode = %q; want %q", c.AuthMode, AuthModeNone)
	}
	if c.APIKeyPlacement != APIKeyPlacementHeader {
		t.Errorf("default APIKeyPlacement = %q; want %q", c.APIKeyPlacement, APIKeyPlacementHeader)
	}
	if c.APIKeyHeader != DefaultAPIKeyHeader {
		t.Errorf("default APIKeyHeader = %q; want %q", c.APIKeyHeader, DefaultAPIKeyHeader)
	}
	if c.ConnectTimeout != DefaultConnectTimeout {
		t.Errorf("default ConnectTimeout = %v; want %v", c.ConnectTimeout, DefaultConnectTimeout)
	}
	if c.CallTimeout != DefaultCallTimeout {
		t.Errorf("default CallTimeout = %v; want %v", c.CallTimeout, DefaultCallTimeout)
	}
	if c.TrustLevel != TrustLevelUntrusted {
		t.Errorf("default TrustLevel = %q; want %q", c.TrustLevel, TrustLevelUntrusted)
	}
	if c.MaxResponseBytes != DefaultMaxResponseBytes {
		t.Errorf("default MaxResponseBytes = %d; want %d", c.MaxResponseBytes, DefaultMaxResponseBytes)
	}
}

func TestParseConfig_StripsTrailingSlashes(t *testing.T) {
	c, err := ParseConfig(map[string]any{"base_url": "https://api.example.com///"})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if c.BaseURL != "https://api.example.com" {
		t.Errorf("BaseURL = %q; want %q", c.BaseURL, "https://api.example.com")
	}
}

func TestParseConfig_OAuth2ClientCredentials(t *testing.T) {
	c, err := ParseConfig(map[string]any{
		"base_url":             "https://api.example.com",
		"auth_mode":            AuthModeOAuth2ClientCredentials,
		"oauth2_token_url":     "https://idp.example/token",
		"oauth2_client_id":     "client-123",
		"oauth2_client_secret": "secret-xyz",
		"oauth2_scopes":        []any{"read:users", "write:orders"},
	})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if c.OAuth2.TokenURL != "https://idp.example/token" {
		t.Errorf("TokenURL = %q", c.OAuth2.TokenURL)
	}
	if c.OAuth2.ClientID != "client-123" {
		t.Errorf("ClientID = %q", c.OAuth2.ClientID)
	}
	if c.OAuth2.ClientSecret != "secret-xyz" {
		t.Errorf("ClientSecret stored incorrectly")
	}
	if c.OAuth2.EndpointAuthStyle != OAuth2AuthStyleHeader {
		t.Errorf("default EndpointAuthStyle = %q; want %q", c.OAuth2.EndpointAuthStyle, OAuth2AuthStyleHeader)
	}
	if len(c.OAuth2.Scopes) != 2 || c.OAuth2.Scopes[0] != "read:users" {
		t.Errorf("Scopes = %v", c.OAuth2.Scopes)
	}
}

func TestParseConfig_OAuth2AuthorizationCode(t *testing.T) {
	c, err := ParseConfig(map[string]any{
		"base_url":                 "https://api.example.com",
		"auth_mode":                AuthModeOAuth2AuthorizationCode,
		"oauth2_token_url":         "https://idp.example/token",
		"oauth2_authorization_url": "https://idp.example/auth",
		"oauth2_client_id":         "client-123",
		"oauth2_client_secret":     "secret-xyz",
		"oauth2_scopes":            []any{"openid", "profile"},
		"oauth2_prompt":            "consent",
	})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if c.AuthMode != AuthModeOAuth2AuthorizationCode {
		t.Errorf("AuthMode = %q; want %q", c.AuthMode, AuthModeOAuth2AuthorizationCode)
	}
	if c.OAuth2.AuthorizationURL != "https://idp.example/auth" {
		t.Errorf("AuthorizationURL = %q", c.OAuth2.AuthorizationURL)
	}
	if c.OAuth2.Prompt != "consent" {
		t.Errorf("Prompt = %q; want %q", c.OAuth2.Prompt, "consent")
	}
}

func TestGetStringSlice_AcceptsBothShapes(t *testing.T) {
	// Programmatic construction yields []string; YAML unmarshaling
	// yields []any. parseOAuth2Config must accept both.
	cfg := map[string]any{
		"a": []string{"x", "y"},
		"b": []any{"x", "y"},
		"c": []any{"x", 42, "y"}, // mixed: ints dropped
	}
	if got := getStringSlice(cfg, "a"); len(got) != 2 || got[0] != "x" {
		t.Errorf("[]string: got %v", got)
	}
	if got := getStringSlice(cfg, "b"); len(got) != 2 || got[0] != "x" {
		t.Errorf("[]any: got %v", got)
	}
	if got := getStringSlice(cfg, "c"); len(got) != 2 || got[0] != "x" || got[1] != "y" {
		t.Errorf("mixed: got %v", got)
	}
	if got := getStringSlice(cfg, "missing"); got != nil {
		t.Errorf("missing key: got %v; want nil", got)
	}
}

func TestParseConfig_BearerAuth(t *testing.T) {
	c, err := ParseConfig(map[string]any{
		"base_url":   "https://api.example.com",
		"auth_mode":  AuthModeBearer,
		"credential": "tok-abc",
	})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if c.Credential != "tok-abc" {
		t.Errorf("Credential = %q; want %q", c.Credential, "tok-abc")
	}
}

func TestParseConfig_APIKeyHeaderAuth(t *testing.T) {
	c, err := ParseConfig(map[string]any{
		"base_url":          "https://api.example.com",
		"auth_mode":         AuthModeAPIKey,
		"credential":        "k-1",
		"api_key_header":    "X-Custom-Key",
		"api_key_placement": APIKeyPlacementHeader,
	})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if c.APIKeyHeader != "X-Custom-Key" {
		t.Errorf("APIKeyHeader = %q; want %q", c.APIKeyHeader, "X-Custom-Key")
	}
}

func TestParseConfig_APIKeyQueryAuth(t *testing.T) {
	c, err := ParseConfig(map[string]any{
		"base_url":          "https://api.example.com",
		"auth_mode":         AuthModeAPIKey,
		"credential":        "k-1",
		"api_key_param":     "api_key",
		"api_key_placement": APIKeyPlacementQuery,
	})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if c.APIKeyPlacement != APIKeyPlacementQuery || c.APIKeyParam != "api_key" {
		t.Errorf("placement/param mismatch: %+v", c)
	}
}

// TestParseConfig_BasicAuth round-trips the new "basic" mode through
// ParseConfig. The fields land on Config exactly as set; validation
// passes; defaults for unrelated fields stay correct.
func TestParseConfig_BasicAuth(t *testing.T) {
	c, err := ParseConfig(map[string]any{
		"base_url":  "https://api.example.com",
		"auth_mode": AuthModeBasic,
		"username":  "svc-account",
		"password":  "s3cret",
	})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if c.AuthMode != AuthModeBasic {
		t.Errorf("AuthMode = %q; want %q", c.AuthMode, AuthModeBasic)
	}
	if c.Username != "svc-account" {
		t.Errorf("Username = %q; want %q", c.Username, "svc-account")
	}
	if c.Password != "s3cret" {
		t.Errorf("Password = %q; want %q", c.Password, "s3cret")
	}
}

// TestParseConfig_BasicAuthEmptyPasswordAllowed locks in the
// "token-in-userid with empty password" pattern some legacy APIs use
// (Atlassian Bitbucket app passwords, certain on-prem APIs). ParseConfig
// must accept username-only basic.
func TestParseConfig_BasicAuthEmptyPasswordAllowed(t *testing.T) {
	c, err := ParseConfig(map[string]any{
		"base_url":  "https://api.example.com",
		"auth_mode": AuthModeBasic,
		"username":  "ghp_xxx",
	})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if c.Password != "" {
		t.Errorf("Password = %q; want empty", c.Password)
	}
}

func TestParseConfig_ValidationErrors(t *testing.T) {
	cases := []struct {
		name string
		cfg  map[string]any
		want string
	}{
		{
			name: "missing base_url",
			cfg:  map[string]any{},
			want: "base_url is required",
		},
		{
			name: "bearer without credential",
			cfg: map[string]any{
				"base_url":  "https://x",
				"auth_mode": AuthModeBearer,
			},
			want: "credential is required",
		},
		{
			name: "api_key without credential",
			cfg: map[string]any{
				"base_url":  "https://x",
				"auth_mode": AuthModeAPIKey,
			},
			want: "credential is required",
		},
		{
			name: "api_key query without param",
			cfg: map[string]any{
				"base_url":          "https://x",
				"auth_mode":         AuthModeAPIKey,
				"credential":        "k",
				"api_key_placement": APIKeyPlacementQuery,
			},
			want: "api_key_param is required",
		},
		{
			name: "api_key invalid placement",
			cfg: map[string]any{
				"base_url":          "https://x",
				"auth_mode":         AuthModeAPIKey,
				"credential":        "k",
				"api_key_placement": "body",
			},
			want: "invalid api_key_placement",
		},
		{
			name: "unknown auth_mode",
			cfg: map[string]any{
				"base_url":  "https://x",
				"auth_mode": "weird",
			},
			want: "invalid auth_mode",
		},
		{
			name: "invalid trust_level",
			cfg: map[string]any{
				"base_url":    "https://x",
				"trust_level": "maybe",
			},
			want: "invalid trust_level",
		},
		{
			name: "non-positive connect_timeout",
			cfg: map[string]any{
				"base_url":        "https://x",
				"connect_timeout": "-1s",
			},
			want: "connect_timeout must be positive",
		},
		{
			name: "oauth2 missing token_url",
			cfg: map[string]any{
				"base_url":             "https://x",
				"auth_mode":            AuthModeOAuth2ClientCredentials,
				"oauth2_client_id":     "c",
				"oauth2_client_secret": "s",
			},
			want: "oauth2.token_url is required",
		},
		{
			name: "oauth2 missing client_id",
			cfg: map[string]any{
				"base_url":             "https://x",
				"auth_mode":            AuthModeOAuth2ClientCredentials,
				"oauth2_token_url":     "https://idp/token",
				"oauth2_client_secret": "s",
			},
			want: "oauth2.client_id is required",
		},
		{
			name: "oauth2 missing client_secret",
			cfg: map[string]any{
				"base_url":         "https://x",
				"auth_mode":        AuthModeOAuth2ClientCredentials,
				"oauth2_token_url": "https://idp/token",
				"oauth2_client_id": "c",
			},
			want: "oauth2.client_secret is required",
		},
		{
			name: "oauth2 invalid endpoint_auth_style",
			cfg: map[string]any{
				"base_url":                   "https://x",
				"auth_mode":                  AuthModeOAuth2ClientCredentials,
				"oauth2_token_url":           "https://idp/token",
				"oauth2_client_id":           "c",
				"oauth2_client_secret":       "s",
				"oauth2_endpoint_auth_style": "invalid",
			},
			want: "invalid oauth2.endpoint_auth_style",
		},
		{
			name: "oauth2_authorization_code missing authorization_url",
			cfg: map[string]any{
				"base_url":             "https://x",
				"auth_mode":            AuthModeOAuth2AuthorizationCode,
				"oauth2_token_url":     "https://idp/token",
				"oauth2_client_id":     "c",
				"oauth2_client_secret": "s",
			},
			want: "oauth2.authorization_url is required",
		},
		{
			name: "basic without username",
			cfg: map[string]any{
				"base_url":  "https://x",
				"auth_mode": AuthModeBasic,
				"password":  "p",
			},
			want: "username is required when auth_mode is \"basic\"",
		},
		{
			name: "basic username contains colon",
			cfg: map[string]any{
				"base_url":  "https://x",
				"auth_mode": AuthModeBasic,
				"username":  "a:b",
				"password":  "p",
			},
			want: "username must not contain",
		},
		{
			name: "basic username contains CRLF",
			cfg: map[string]any{
				"base_url":  "https://x",
				"auth_mode": AuthModeBasic,
				"username":  "alice\r\nX-Injected: 1",
				"password":  "p",
			},
			want: "username contains CR/LF/NUL",
		},
		{
			name: "basic password contains NUL",
			cfg: map[string]any{
				"base_url":  "https://x",
				"auth_mode": AuthModeBasic,
				"username":  "alice",
				"password":  "p\x00q",
			},
			want: "password contains CR/LF/NUL",
		},
		{
			// authorization_code still requires the same client
			// fields as client_credentials — verifies that the
			// authorization_code validator chains validateOAuth2.
			name: "oauth2_authorization_code missing client_secret",
			cfg: map[string]any{
				"base_url":                 "https://x",
				"auth_mode":                AuthModeOAuth2AuthorizationCode,
				"oauth2_token_url":         "https://idp/token",
				"oauth2_client_id":         "c",
				"oauth2_authorization_url": "https://idp/auth",
			},
			want: "oauth2.client_secret is required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseConfig(tc.cfg)
			if err == nil {
				t.Fatalf("ParseConfig(%+v) returned nil error; want %q", tc.cfg, tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q; want substring %q", err.Error(), tc.want)
			}
		})
	}
}

func TestParseConfig_NonPositiveCallTimeout(t *testing.T) {
	_, err := ParseConfig(map[string]any{
		"base_url":     "https://x",
		"call_timeout": "0s",
	})
	if err == nil || !strings.Contains(err.Error(), "call_timeout must be positive") {
		t.Errorf("ParseConfig: got %v; want call_timeout error", err)
	}
}

func TestParseConfig_NonPositiveMaxResponseBytes(t *testing.T) {
	_, err := ParseConfig(map[string]any{
		"base_url":           "https://x",
		"max_response_bytes": int64(-1),
	})
	if err == nil || !strings.Contains(err.Error(), "max_response_bytes must be positive") {
		t.Errorf("ParseConfig: got %v; want max_response_bytes error", err)
	}
}

func TestParseConfig_DurationFromInt(t *testing.T) {
	c, err := ParseConfig(map[string]any{
		"base_url":     "https://x",
		"call_timeout": 30,
	})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if c.CallTimeout != 30*time.Second {
		t.Errorf("CallTimeout = %v; want 30s", c.CallTimeout)
	}
}

func TestParseConfig_DurationFromFloat(t *testing.T) {
	c, err := ParseConfig(map[string]any{
		"base_url":        "https://x",
		"connect_timeout": 5.0,
	})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if c.ConnectTimeout != 5*time.Second {
		t.Errorf("ConnectTimeout = %v; want 5s", c.ConnectTimeout)
	}
}

func TestParseMultiConfig_SetsConnectionName(t *testing.T) {
	mc, err := ParseMultiConfig("default-name", map[string]map[string]any{
		"alpha": {"base_url": "https://a.example.com"},
		"beta":  {"base_url": "https://b.example.com"},
	})
	if err != nil {
		t.Fatalf("ParseMultiConfig: %v", err)
	}
	if mc.DefaultName != "default-name" {
		t.Errorf("DefaultName = %q; want %q", mc.DefaultName, "default-name")
	}
	if mc.Instances["alpha"].ConnectionName != "alpha" {
		t.Errorf("alpha.ConnectionName = %q; want %q", mc.Instances["alpha"].ConnectionName, "alpha")
	}
	if mc.Instances["beta"].ConnectionName != "beta" {
		t.Errorf("beta.ConnectionName = %q; want %q", mc.Instances["beta"].ConnectionName, "beta")
	}
}

func TestParseMultiConfig_PropagatesPerInstanceError(t *testing.T) {
	_, err := ParseMultiConfig("", map[string]map[string]any{
		"bad": {"auth_mode": AuthModeBearer}, // missing base_url + credential
	})
	if err == nil {
		t.Fatal("ParseMultiConfig: want error, got nil")
	}
	if !strings.Contains(err.Error(), "apigateway/bad") {
		t.Errorf("error = %q; want instance-prefixed error", err.Error())
	}
}

func TestParseConfig_PreservesExplicitConnectionName(t *testing.T) {
	c, err := ParseConfig(map[string]any{
		"base_url":        "https://x",
		"connection_name": "explicit",
	})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if c.ConnectionName != "explicit" {
		t.Errorf("ConnectionName = %q; want %q", c.ConnectionName, "explicit")
	}
}

func TestParseConfig_TrustLevelTrusted(t *testing.T) {
	c, err := ParseConfig(map[string]any{
		"base_url":    "https://x",
		"trust_level": TrustLevelTrusted,
	})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if c.TrustLevel != TrustLevelTrusted {
		t.Errorf("TrustLevel = %q; want %q", c.TrustLevel, TrustLevelTrusted)
	}
}

func TestParseConfig_StaticHeaders_MapStringAny(t *testing.T) {
	c, err := ParseConfig(map[string]any{
		"base_url": "https://www.googleapis.com",
		"static_headers": map[string]any{
			"X-Goog-User-Project": "quota-project-123",
			"X-Trace-Tag":         "ops",
		},
	})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if got := c.StaticHeaders["X-Goog-User-Project"]; got != "quota-project-123" {
		t.Errorf("X-Goog-User-Project = %q; want %q", got, "quota-project-123")
	}
	if got := c.StaticHeaders["X-Trace-Tag"]; got != "ops" {
		t.Errorf("X-Trace-Tag = %q; want %q", got, "ops")
	}
}

func TestParseConfig_StaticHeaders_MapStringString(t *testing.T) {
	c, err := ParseConfig(map[string]any{
		"base_url": "https://api.example.com",
		"static_headers": map[string]string{
			"X-Custom": "value",
		},
	})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if got := c.StaticHeaders["X-Custom"]; got != "value" {
		t.Errorf("X-Custom = %q; want %q", got, "value")
	}
}

func TestParseConfig_StaticHeaders_RejectsAuthorization(t *testing.T) {
	_, err := ParseConfig(map[string]any{
		"base_url":       "https://api.example.com",
		"static_headers": map[string]any{"Authorization": "Bearer leaked"},
	})
	if err == nil || !strings.Contains(err.Error(), "Authorization") {
		t.Errorf("expected Authorization rejection; got %v", err)
	}
}

func TestParseConfig_StaticHeaders_RejectsAPIKeyHeaderCollision(t *testing.T) {
	_, err := ParseConfig(map[string]any{
		"base_url":          "https://api.example.com",
		"auth_mode":         AuthModeAPIKey,
		"credential":        "ak-123",
		"api_key_header":    "X-API-Key",
		"static_headers":    map[string]any{"x-api-key": "spoof"},
		"api_key_placement": "header",
	})
	if err == nil || !strings.Contains(err.Error(), "static_headers") {
		t.Errorf("expected api_key header collision rejection; got %v", err)
	}
}

func TestParseConfig_StaticHeaders_RejectsHopByHop(t *testing.T) {
	cases := []string{"Host", "Content-Length", "Connection", "Transfer-Encoding"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseConfig(map[string]any{
				"base_url":       "https://api.example.com",
				"static_headers": map[string]any{name: "x"},
			})
			if err == nil {
				t.Errorf("hop-by-hop header %q allowed", name)
			}
		})
	}
}

func TestParseConfig_StaticHeaders_RejectsInvalidName(t *testing.T) {
	_, err := ParseConfig(map[string]any{
		"base_url":       "https://api.example.com",
		"static_headers": map[string]any{"Bad Header": "x"},
	})
	if err == nil {
		t.Error("invalid header name with space allowed")
	}
}

func TestParseConfig_StaticHeaders_RejectsCRLFInValue(t *testing.T) {
	_, err := ParseConfig(map[string]any{
		"base_url":       "https://api.example.com",
		"static_headers": map[string]any{"X-Inject": "value\r\nX-Other: evil"},
	})
	if err == nil {
		t.Error("CRLF in header value allowed (smuggling vector)")
	}
}

func TestParseConfig_StaticHeaders_EmptyMapNotPersisted(t *testing.T) {
	c, err := ParseConfig(map[string]any{
		"base_url":       "https://api.example.com",
		"static_headers": map[string]any{},
	})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if c.StaticHeaders != nil {
		t.Errorf("empty static_headers stored as %#v; want nil", c.StaticHeaders)
	}
}

func TestIsValidHeaderName(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"X-API-Key", true},
		{"X-Subscription-Key", true},
		{"x-goog-user-project", true},
		{"Content-Type", true},
		{"", false},
		{"Bad Name", false},
		{"With\rCR", false},
		{"Colon:Inside", false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := isValidHeaderName(tc.in); got != tc.want {
				t.Errorf("isValidHeaderName(%q) = %v; want %v", tc.in, got, tc.want)
			}
		})
	}
}
