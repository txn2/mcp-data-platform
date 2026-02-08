package trino

import (
	"testing"
	"time"
)

const (
	trinoCfgTestExisting    = "existing"
	trinoCfgTestMissing     = "missing"
	trinoCfgTestString      = "string"
	trinoCfgTestNumVal      = 123
	trinoCfgTestIntVal      = 100
	trinoCfgTestFloat64Val  = 200
	trinoCfgTestDefaultVal  = 50
	trinoCfgTestDurationInt = 30
	trinoCfgTestDurationFlt = 60
)

func TestParseConfig_ValidAllFields(t *testing.T) {
	cfg := map[string]any{
		"host":            "trino.example.com",
		"port":            trinoTestPort8080,
		"user":            "testuser",
		"password":        "secret",
		"catalog":         "hive",
		"schema":          "default",
		"ssl":             true,
		"ssl_verify":      false,
		"read_only":       true,
		"default_limit":   trinoTestDefaultLimit,
		"max_limit":       trinoTestMaxLimit,
		"timeout":         "60s",
		"connection_name": "main-trino",
	}

	result, err := ParseConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertTrinoConfigAllFields(t, result)
}

func assertTrinoConfigAllFields(t *testing.T, result Config) {
	t.Helper()
	if result.Host != "trino.example.com" {
		t.Errorf("expected host 'trino.example.com', got %q", result.Host)
	}
	if result.Port != trinoTestPort8080 {
		t.Errorf("expected port 8080, got %d", result.Port)
	}
	if !result.SSL {
		t.Error("expected SSL to be true")
	}
	if result.SSLVerify {
		t.Error("expected SSLVerify to be false")
	}
	if !result.ReadOnly {
		t.Error("expected ReadOnly to be true")
	}
	if result.DefaultLimit != trinoTestDefaultLimit {
		t.Errorf("expected DefaultLimit 500, got %d", result.DefaultLimit)
	}
	if result.MaxLimit != trinoTestMaxLimit {
		t.Errorf("expected MaxLimit 5000, got %d", result.MaxLimit)
	}
	if result.Timeout != trinoCfgTestDurationFlt*time.Second {
		t.Errorf("expected Timeout 60s, got %v", result.Timeout)
	}
	if result.ConnectionName != "main-trino" {
		t.Errorf("expected ConnectionName 'main-trino', got %q", result.ConnectionName)
	}
}

func TestParseConfig_MissingHost(t *testing.T) {
	_, err := ParseConfig(map[string]any{"user": "testuser"})
	if err == nil {
		t.Error("expected error for missing host")
	}
}

func TestParseConfig_DefaultsApplied(t *testing.T) {
	result, err := ParseConfig(map[string]any{"host": "trino.example.com"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Port != trinoTestPort8080 {
		t.Errorf("expected default port 8080, got %d", result.Port)
	}
	if result.DefaultLimit != trinoTestDefLimit {
		t.Errorf("expected default limit 1000, got %d", result.DefaultLimit)
	}
	if result.MaxLimit != trinoTestDefMaxLimit {
		t.Errorf("expected max limit 10000, got %d", result.MaxLimit)
	}
	if result.Timeout != trinoTestDefTimeoutSec*time.Second {
		t.Errorf("expected default timeout 120s, got %v", result.Timeout)
	}
	if !result.SSLVerify {
		t.Error("expected SSLVerify to default to true")
	}
}

func TestParseConfig_InvalidTimeout(t *testing.T) {
	_, err := ParseConfig(map[string]any{"host": "trino.example.com", "timeout": "invalid"})
	if err == nil {
		t.Error("expected error for invalid timeout")
	}
}

func TestParseConfig_PortAsFloat64(t *testing.T) {
	result, err := ParseConfig(map[string]any{"host": "trino.example.com", "port": float64(9090)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Port != 9090 {
		t.Errorf("expected port 9090, got %d", result.Port)
	}
}

func TestParseConfig_TimeoutAsInt(t *testing.T) {
	result, err := ParseConfig(map[string]any{"host": "trino.example.com", "timeout": trinoCfgTestDurationInt})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Timeout != trinoCfgTestDurationInt*time.Second {
		t.Errorf("expected timeout 30s, got %v", result.Timeout)
	}
}

func TestParseConfig_TimeoutAsFloat64(t *testing.T) {
	result, err := ParseConfig(map[string]any{"host": "trino.example.com", "timeout": float64(45)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Timeout != 45*time.Second {
		t.Errorf("expected timeout 45s, got %v", result.Timeout)
	}
}

func TestGetString(t *testing.T) {
	cfg := map[string]any{
		trinoCfgTestExisting: "value",
		"number":             trinoCfgTestNumVal,
	}

	if getString(cfg, trinoCfgTestExisting) != "value" {
		t.Error("expected 'value' for existing key")
	}
	if getString(cfg, trinoCfgTestMissing) != "" {
		t.Error("expected empty string for missing key")
	}
	if getString(cfg, "number") != "" {
		t.Error("expected empty string for non-string value")
	}
}

func TestGetInt(t *testing.T) {
	cfg := map[string]any{
		"int":              trinoCfgTestIntVal,
		"float64":          float64(trinoCfgTestFloat64Val),
		trinoCfgTestString: "not a number",
	}

	if getInt(cfg, "int", 0) != trinoCfgTestIntVal {
		t.Error("expected 100 for int key")
	}
	if getInt(cfg, "float64", 0) != trinoCfgTestFloat64Val {
		t.Error("expected 200 for float64 key")
	}
	if getInt(cfg, trinoCfgTestMissing, trinoCfgTestDefaultVal) != trinoCfgTestDefaultVal {
		t.Error("expected default 50 for missing key")
	}
	if getInt(cfg, trinoCfgTestString, trinoCfgTestDefaultVal) != trinoCfgTestDefaultVal {
		t.Error("expected default 50 for string value")
	}
}

func TestGetBool(t *testing.T) {
	cfg := map[string]any{
		"true":             true,
		"false":            false,
		trinoCfgTestString: "true",
	}

	if !getBool(cfg, "true") {
		t.Error("expected true for true key")
	}
	if getBool(cfg, "false") {
		t.Error("expected false for false key")
	}
	if getBool(cfg, trinoCfgTestMissing) {
		t.Error("expected false for missing key")
	}
	if getBool(cfg, trinoCfgTestString) {
		t.Error("expected false for string value")
	}
}

func TestGetBoolDefault(t *testing.T) {
	cfg := map[string]any{
		"explicit_false": false,
		"explicit_true":  true,
	}

	if !getBoolDefault(cfg, trinoCfgTestMissing, true) {
		t.Error("expected default true for missing key")
	}
	if getBoolDefault(cfg, trinoCfgTestMissing, false) {
		t.Error("expected default false for missing key")
	}
	if getBoolDefault(cfg, "explicit_false", true) {
		t.Error("expected false for explicit_false key")
	}
	if !getBoolDefault(cfg, "explicit_true", false) {
		t.Error("expected true for explicit_true key")
	}
}

func TestGetDuration(t *testing.T) {
	cfg := map[string]any{
		trinoCfgTestString: "5m",
		"int":              trinoCfgTestDurationInt,
		"float64":          float64(trinoCfgTestDurationFlt),
		"invalid":          "not-a-duration",
	}

	d, err := getDuration(cfg, trinoCfgTestString)
	if err != nil || d != 5*time.Minute {
		t.Errorf("expected 5m, got %v (err: %v)", d, err)
	}

	d, err = getDuration(cfg, "int")
	if err != nil || d != trinoCfgTestDurationInt*time.Second {
		t.Errorf("expected 30s, got %v (err: %v)", d, err)
	}

	d, err = getDuration(cfg, "float64")
	if err != nil || d != trinoCfgTestDurationFlt*time.Second {
		t.Errorf("expected 60s, got %v (err: %v)", d, err)
	}

	d, err = getDuration(cfg, trinoCfgTestMissing)
	if err != nil || d != 0 {
		t.Errorf("expected 0, got %v (err: %v)", d, err)
	}

	_, err = getDuration(cfg, "invalid")
	if err == nil {
		t.Error("expected error for invalid duration")
	}
}
