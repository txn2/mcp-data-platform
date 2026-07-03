package platform

import (
	"errors"
	"strings"
	"testing"
)

// TestDetectUnknownFields verifies that strict decoding flags keys that do not
// map to a typed Config field, while leaving keys under untyped blocks alone.
func TestDetectUnknownFields(t *testing.T) {
	tests := []struct {
		name       string
		yaml       string
		wantSubstr string // a fragment expected in one of the reported errors, "" = expect none
	}{
		{
			name: "known keys only",
			yaml: "server:\n  name: p\naudit:\n  enabled: true\n  log_tool_calls: true\n",
		},
		{
			name:       "phantom audit key",
			yaml:       "audit:\n  enabled: true\n  log_parameters: true\n",
			wantSubstr: "log_parameters",
		},
		{
			name:       "phantom sessions key",
			yaml:       "sessions:\n  idle_timeout: 5m\n",
			wantSubstr: "idle_timeout",
		},
		{
			name:       "phantom oidc key",
			yaml:       "auth:\n  oidc:\n    enabled: true\n    clock_skew_seconds: 30\n",
			wantSubstr: "clock_skew_seconds",
		},
		{
			name:       "phantom role_mapping key",
			yaml:       "personas:\n  role_mapping:\n    user_personas:\n      alice: analyst\n",
			wantSubstr: "user_personas",
		},
		{
			// Keys under the untyped toolkits map are not inspected by
			// KnownFields; the doc round-trip test validates that shape.
			name: "arbitrary toolkit keys not flagged",
			yaml: "toolkits:\n  trino:\n    primary:\n      host: h\n",
		},
		{
			// Persona names are absorbed by the inline map, so arbitrary
			// persona keys are not flagged.
			name: "arbitrary persona names not flagged",
			yaml: "personas:\n  analyst:\n    display_name: Analyst\n",
		},
		{
			// Deliberate legacy alias must remain accepted.
			name: "injection legacy alias accepted",
			yaml: "injection:\n  trino_semantic_enrichment: true\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectUnknownFields([]byte(tt.yaml))
			if tt.wantSubstr == "" {
				if len(got) != 0 {
					t.Fatalf("expected no unknown fields, got %v", got)
				}
				return
			}
			joined := strings.Join(got, "; ")
			if !strings.Contains(joined, tt.wantSubstr) {
				t.Fatalf("expected an error mentioning %q, got %v", tt.wantSubstr, got)
			}
		})
	}
}

// TestLoadConfig_UnknownKey_WarnsByDefault verifies that in this release an
// unrecognized key does not fail the load (warn phase).
func TestLoadConfig_UnknownKey_WarnsByDefault(t *testing.T) {
	cfg, err := LoadConfigFromBytes([]byte("audit:\n  enabled: true\n  log_parameters: true\n"))
	if err != nil {
		t.Fatalf("expected warn-and-continue, got error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected config, got nil")
	}
}

// TestLoadConfig_UnknownKey_StrictErrors verifies that config.strict: true
// promotes unrecognized keys to a hard error.
func TestLoadConfig_UnknownKey_StrictErrors(t *testing.T) {
	_, err := LoadConfigFromBytes([]byte(
		"config:\n  strict: true\naudit:\n  enabled: true\n  log_parameters: true\n"))
	if err == nil {
		t.Fatal("expected strict parsing error, got nil")
	}
	if !strings.Contains(err.Error(), "log_parameters") {
		t.Fatalf("expected error to name the offending key, got: %v", err)
	}
}

// TestLoadConfig_Strict_CleanConfigLoads verifies strict mode accepts a config
// with only recognized keys.
func TestLoadConfig_Strict_CleanConfigLoads(t *testing.T) {
	cfg, err := LoadConfigFromBytes([]byte(
		"config:\n  strict: true\nserver:\n  name: p\naudit:\n  enabled: true\n"))
	if err != nil {
		t.Fatalf("clean config under strict mode should load, got: %v", err)
	}
	if cfg.Server.Name != "p" {
		t.Fatalf("expected server name p, got %q", cfg.Server.Name)
	}
}

// TestLoadConfigFromBytes_DeprecatedVersion exercises the deprecated-version
// warn branch and confirms the config still loads.
func TestLoadConfigFromBytes_DeprecatedVersion(t *testing.T) {
	reg := NewVersionRegistry()
	reg.Register(&VersionInfo{Version: "v1", Status: VersionCurrent})
	reg.Register(&VersionInfo{
		Version:            "v0",
		Status:             VersionDeprecated,
		DeprecationMessage: "upgrade to v1",
	})
	cfg, err := loadConfigWithRegistry([]byte("apiVersion: v0\nserver:\n  name: p\n"), reg)
	if err != nil {
		t.Fatalf("deprecated version should still load: %v", err)
	}
	if cfg.Server.Name != "p" {
		t.Fatalf("expected server name p, got %q", cfg.Server.Name)
	}
}

// TestLoadConfigFromBytes_Converter exercises the converter dispatch branch,
// both success and error.
func TestLoadConfigFromBytes_Converter(t *testing.T) {
	reg := NewVersionRegistry()
	reg.Register(&VersionInfo{
		Version: "vx",
		Status:  VersionCurrent,
		Converter: func([]byte) (*Config, error) {
			return &Config{Server: ServerConfig{Name: "converted"}}, nil
		},
	})
	cfg, err := loadConfigWithRegistry([]byte("apiVersion: vx\n"), reg)
	if err != nil {
		t.Fatalf("converter path should succeed: %v", err)
	}
	if cfg.Server.Name != "converted" {
		t.Fatalf("expected converter output, got %q", cfg.Server.Name)
	}

	regErr := NewVersionRegistry()
	regErr.Register(&VersionInfo{
		Version: "vy",
		Status:  VersionCurrent,
		Converter: func([]byte) (*Config, error) {
			return nil, errAssertConverter
		},
	})
	if _, err := loadConfigWithRegistry([]byte("apiVersion: vy\n"), regErr); err == nil {
		t.Fatal("converter error should propagate")
	}
}

var errAssertConverter = errors.New("converter boom")

// TestConfigMeta_IsStrict covers the tri-state of the strict escape hatch.
func TestConfigMeta_IsStrict(t *testing.T) {
	tr, fa := true, false
	cases := []struct {
		name string
		in   *bool
		want bool
	}{
		{"nil defaults to lenient", nil, false},
		{"explicit false", &fa, false},
		{"explicit true", &tr, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := (ConfigMeta{Strict: c.in}).IsStrict(); got != c.want {
				t.Fatalf("IsStrict() = %v, want %v", got, c.want)
			}
		})
	}
}
