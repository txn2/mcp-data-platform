package s3

import (
	"testing"
	"time"
)

const (
	s3CfgTestExisting      = "existing"
	s3CfgTestString        = "string"
	s3CfgTestMissing       = "missing"
	s3CfgTestNumVal        = 123
	s3CfgTestIntVal        = 100
	s3CfgTestFloat64Val    = 200
	s3CfgTestDefaultVal    = 50
	s3CfgTestDurationInt   = 30
	s3CfgTestDurationFlt   = 60
	s3CfgTestDuration5m    = 5
	s3CfgTestDuration15s   = 15
	s3CfgTestDuration20s   = 20
	s3CfgTestDefGetMB      = 10
	s3CfgTestBytesPerMB    = 1024
	s3CfgTestTimeout45     = 45
	s3CfgTestTimeout90     = 90
	s3CfgTestGetSize1024   = 1024
	s3CfgTestPutSize2048   = 2048
	s3CfgTestGetSize512    = 512
	s3CfgTestPutSize1024   = 1024
	s3CfgTestUnexpectedErr = "unexpected error: %v"
	s3CfgTestValue         = "value"
	s3CfgTestInt           = "int"
	s3CfgTestFloat64       = "float64"
)

func TestParseConfig_ValidAllFields(t *testing.T) {
	cfg := map[string]any{
		"region":            "us-west-2",
		"endpoint":          "http://localhost:9000",
		"access_key_id":     "AKIAIOSFODNN7EXAMPLE",
		"secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		"session_token":     "token123",
		"profile":           "dev",
		"use_path_style":    true,
		"timeout":           "60s",
		"disable_ssl":       true,
		"read_only":         true,
		"max_get_size":      int64(5 * 1024 * 1024),
		"max_put_size":      int64(50 * 1024 * 1024),
		"connection_name":   "main-s3",
		"bucket_prefix":     "prod-",
	}

	result, err := ParseConfig(cfg)
	if err != nil {
		t.Fatalf(s3CfgTestUnexpectedErr, err)
	}
	assertS3ConfigAllFields(t, result)
}

func assertS3ConfigAllFields(t *testing.T, result Config) {
	t.Helper()
	if result.Region != "us-west-2" {
		t.Errorf("expected region 'us-west-2', got %q", result.Region)
	}
	if result.Endpoint != "http://localhost:9000" {
		t.Errorf("expected endpoint 'http://localhost:9000', got %q", result.Endpoint)
	}
	if result.AccessKeyID != "AKIAIOSFODNN7EXAMPLE" { //nolint:gosec // G101: test fixture, not a real credential
		t.Errorf("expected AccessKeyID, got %q", result.AccessKeyID)
	}
	if result.SecretAccessKey != "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY" {
		t.Errorf("expected SecretAccessKey, got %q", result.SecretAccessKey)
	}
	if !result.UsePathStyle {
		t.Error("expected UsePathStyle to be true")
	}
	if result.Timeout != s3CfgTestDurationFlt*time.Second {
		t.Errorf("expected Timeout 60s, got %v", result.Timeout)
	}
	if !result.DisableSSL {
		t.Error("expected DisableSSL to be true")
	}
	if !result.ReadOnly {
		t.Error("expected ReadOnly to be true")
	}
}

func TestParseConfig_DefaultsApplied(t *testing.T) {
	cfg := map[string]any{}

	result, err := ParseConfig(cfg)
	if err != nil {
		t.Fatalf(s3CfgTestUnexpectedErr, err)
	}

	if result.Region != "us-east-1" {
		t.Errorf("expected default region 'us-east-1', got %q", result.Region)
	}
	if result.Timeout != s3CfgTestDurationInt*time.Second {
		t.Errorf("expected default timeout 30s, got %v", result.Timeout)
	}
	if result.MaxGetSize != s3CfgTestDefGetMB*s3CfgTestBytesPerMB*s3CfgTestBytesPerMB {
		t.Errorf("expected default MaxGetSize 10MB, got %d", result.MaxGetSize)
	}
	if result.MaxPutSize != s3CfgTestIntVal*s3CfgTestBytesPerMB*s3CfgTestBytesPerMB {
		t.Errorf("expected default MaxPutSize 100MB, got %d", result.MaxPutSize)
	}
}

func TestParseConfig_TimeoutAsInt(t *testing.T) {
	result, err := ParseConfig(map[string]any{"timeout": s3CfgTestTimeout45})
	if err != nil {
		t.Fatalf(s3CfgTestUnexpectedErr, err)
	}
	if result.Timeout != s3CfgTestTimeout45*time.Second {
		t.Errorf("expected timeout 45s, got %v", result.Timeout)
	}
}

func TestParseConfig_TimeoutAsFloat64(t *testing.T) {
	result, err := ParseConfig(map[string]any{"timeout": float64(s3CfgTestTimeout90)})
	if err != nil {
		t.Fatalf(s3CfgTestUnexpectedErr, err)
	}
	if result.Timeout != s3CfgTestTimeout90*time.Second {
		t.Errorf("expected timeout 90s, got %v", result.Timeout)
	}
}

func TestParseConfig_SizeFieldsAsFloat64(t *testing.T) {
	result, err := ParseConfig(map[string]any{
		"max_get_size": float64(s3CfgTestGetSize1024),
		"max_put_size": float64(s3CfgTestPutSize2048),
	})
	if err != nil {
		t.Fatalf(s3CfgTestUnexpectedErr, err)
	}
	if result.MaxGetSize != s3CfgTestGetSize1024 {
		t.Errorf("expected MaxGetSize %d, got %d", s3CfgTestGetSize1024, result.MaxGetSize)
	}
	if result.MaxPutSize != s3CfgTestPutSize2048 {
		t.Errorf("expected MaxPutSize %d, got %d", s3CfgTestPutSize2048, result.MaxPutSize)
	}
}

func TestParseConfig_SizeFieldsAsInt(t *testing.T) {
	result, err := ParseConfig(map[string]any{
		"max_get_size": s3CfgTestGetSize512,
		"max_put_size": s3CfgTestPutSize1024,
	})
	if err != nil {
		t.Fatalf(s3CfgTestUnexpectedErr, err)
	}
	if result.MaxGetSize != s3CfgTestGetSize512 {
		t.Errorf("expected MaxGetSize %d, got %d", s3CfgTestGetSize512, result.MaxGetSize)
	}
	if result.MaxPutSize != s3CfgTestPutSize1024 {
		t.Errorf("expected MaxPutSize %d, got %d", s3CfgTestPutSize1024, result.MaxPutSize)
	}
}

func TestParseConfig_InvalidTimeoutDefault(t *testing.T) {
	result, err := ParseConfig(map[string]any{"timeout": "invalid"})
	if err != nil {
		t.Fatalf(s3CfgTestUnexpectedErr, err)
	}
	if result.Timeout != s3CfgTestDurationInt*time.Second {
		t.Errorf("expected default timeout 30s for invalid string, got %v", result.Timeout)
	}
}

func TestS3GetString(t *testing.T) {
	cfg := map[string]any{
		s3CfgTestExisting: s3CfgTestValue,
		"number":          s3CfgTestNumVal,
	}

	if getString(cfg, s3CfgTestExisting) != s3CfgTestValue {
		t.Error("expected 'value' for existing key")
	}
	if getString(cfg, s3CfgTestMissing) != "" {
		t.Error("expected empty string for missing key")
	}
	if getString(cfg, "number") != "" {
		t.Error("expected empty string for non-string value")
	}
}

func TestS3GetStringDefault(t *testing.T) {
	cfg := map[string]any{
		s3CfgTestExisting: s3CfgTestValue,
	}

	if getStringDefault(cfg, s3CfgTestExisting, "default") != s3CfgTestValue {
		t.Error("expected 'value' for existing key")
	}
	if getStringDefault(cfg, s3CfgTestMissing, "default") != "default" {
		t.Error("expected 'default' for missing key")
	}
}

func TestS3GetBool(t *testing.T) {
	cfg := map[string]any{
		"true":          true,
		"false":         false,
		s3CfgTestString: "true",
	}

	if !getBool(cfg, "true") {
		t.Error("expected true for true key")
	}
	if getBool(cfg, "false") {
		t.Error("expected false for false key")
	}
	if getBool(cfg, s3CfgTestMissing) {
		t.Error("expected false for missing key")
	}
	if getBool(cfg, s3CfgTestString) {
		t.Error("expected false for string value")
	}
}

func TestS3GetDuration(t *testing.T) {
	cfg := map[string]any{
		s3CfgTestString:  "5m",
		s3CfgTestInt:     s3CfgTestDurationInt,
		s3CfgTestFloat64: float64(s3CfgTestDurationFlt),
		"invalid":        "not-a-duration",
	}

	d := getDuration(cfg, s3CfgTestString, time.Second)
	if d != s3CfgTestDuration5m*time.Minute {
		t.Errorf("expected 5m, got %v", d)
	}

	d = getDuration(cfg, s3CfgTestInt, time.Second)
	if d != s3CfgTestDurationInt*time.Second {
		t.Errorf("expected 30s, got %v", d)
	}

	d = getDuration(cfg, s3CfgTestFloat64, time.Second)
	if d != s3CfgTestDurationFlt*time.Second {
		t.Errorf("expected 60s, got %v", d)
	}

	d = getDuration(cfg, s3CfgTestMissing, s3CfgTestDuration15s*time.Second)
	if d != s3CfgTestDuration15s*time.Second {
		t.Errorf("expected default 15s, got %v", d)
	}

	d = getDuration(cfg, "invalid", s3CfgTestDuration20s*time.Second)
	if d != s3CfgTestDuration20s*time.Second {
		t.Errorf("expected default 20s for invalid value, got %v", d)
	}
}

func TestS3GetInt64(t *testing.T) {
	cfg := map[string]any{
		s3CfgTestInt:     s3CfgTestIntVal,
		s3CfgTestFloat64: float64(s3CfgTestFloat64Val),
		s3CfgTestString:  "not a number",
	}

	if getInt64(cfg, s3CfgTestInt, 0) != s3CfgTestIntVal {
		t.Error("expected 100 for int key")
	}
	if getInt64(cfg, s3CfgTestFloat64, 0) != s3CfgTestFloat64Val {
		t.Error("expected 200 for float64 key")
	}
	if getInt64(cfg, s3CfgTestMissing, s3CfgTestDefaultVal) != s3CfgTestDefaultVal {
		t.Error("expected default 50 for missing key")
	}
	if getInt64(cfg, s3CfgTestString, s3CfgTestDefaultVal) != s3CfgTestDefaultVal {
		t.Error("expected default 50 for string value")
	}
}
