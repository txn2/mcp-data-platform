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
