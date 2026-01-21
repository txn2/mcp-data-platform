package s3

import (
	"testing"
	"time"
)

func TestParseConfig(t *testing.T) {
	t.Run("valid config with all fields", func(t *testing.T) {
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
			t.Fatalf("unexpected error: %v", err)
		}

		if result.Region != "us-west-2" {
			t.Errorf("expected region 'us-west-2', got %q", result.Region)
		}
		if result.Endpoint != "http://localhost:9000" {
			t.Errorf("expected endpoint 'http://localhost:9000', got %q", result.Endpoint)
		}
		if result.AccessKeyID != "AKIAIOSFODNN7EXAMPLE" {
			t.Errorf("expected AccessKeyID, got %q", result.AccessKeyID)
		}
		if result.SecretAccessKey != "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY" {
			t.Errorf("expected SecretAccessKey, got %q", result.SecretAccessKey)
		}
		if result.SessionToken != "token123" {
			t.Errorf("expected SessionToken 'token123', got %q", result.SessionToken)
		}
		if result.Profile != "dev" {
			t.Errorf("expected Profile 'dev', got %q", result.Profile)
		}
		if !result.UsePathStyle {
			t.Error("expected UsePathStyle to be true")
		}
		if result.Timeout != 60*time.Second {
			t.Errorf("expected Timeout 60s, got %v", result.Timeout)
		}
		if !result.DisableSSL {
			t.Error("expected DisableSSL to be true")
		}
		if !result.ReadOnly {
			t.Error("expected ReadOnly to be true")
		}
		if result.ConnectionName != "main-s3" {
			t.Errorf("expected ConnectionName 'main-s3', got %q", result.ConnectionName)
		}
		if result.BucketPrefix != "prod-" {
			t.Errorf("expected BucketPrefix 'prod-', got %q", result.BucketPrefix)
		}
	})

	t.Run("defaults applied", func(t *testing.T) {
		cfg := map[string]any{}

		result, err := ParseConfig(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result.Region != "us-east-1" {
			t.Errorf("expected default region 'us-east-1', got %q", result.Region)
		}
		if result.Timeout != 30*time.Second {
			t.Errorf("expected default timeout 30s, got %v", result.Timeout)
		}
		if result.MaxGetSize != 10*1024*1024 {
			t.Errorf("expected default MaxGetSize 10MB, got %d", result.MaxGetSize)
		}
		if result.MaxPutSize != 100*1024*1024 {
			t.Errorf("expected default MaxPutSize 100MB, got %d", result.MaxPutSize)
		}
		if result.UsePathStyle {
			t.Error("expected UsePathStyle to default to false")
		}
		if result.DisableSSL {
			t.Error("expected DisableSSL to default to false")
		}
		if result.ReadOnly {
			t.Error("expected ReadOnly to default to false")
		}
	})

	t.Run("timeout as int (seconds)", func(t *testing.T) {
		cfg := map[string]any{
			"timeout": 45,
		}

		result, err := ParseConfig(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Timeout != 45*time.Second {
			t.Errorf("expected timeout 45s, got %v", result.Timeout)
		}
	})

	t.Run("timeout as float64 (seconds)", func(t *testing.T) {
		cfg := map[string]any{
			"timeout": float64(90),
		}

		result, err := ParseConfig(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Timeout != 90*time.Second {
			t.Errorf("expected timeout 90s, got %v", result.Timeout)
		}
	})

	t.Run("size fields as float64 (JSON unmarshaling)", func(t *testing.T) {
		cfg := map[string]any{
			"max_get_size": float64(1024),
			"max_put_size": float64(2048),
		}

		result, err := ParseConfig(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.MaxGetSize != 1024 {
			t.Errorf("expected MaxGetSize 1024, got %d", result.MaxGetSize)
		}
		if result.MaxPutSize != 2048 {
			t.Errorf("expected MaxPutSize 2048, got %d", result.MaxPutSize)
		}
	})

	t.Run("size fields as int", func(t *testing.T) {
		cfg := map[string]any{
			"max_get_size": 512,
			"max_put_size": 1024,
		}

		result, err := ParseConfig(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.MaxGetSize != 512 {
			t.Errorf("expected MaxGetSize 512, got %d", result.MaxGetSize)
		}
		if result.MaxPutSize != 1024 {
			t.Errorf("expected MaxPutSize 1024, got %d", result.MaxPutSize)
		}
	})

	t.Run("invalid timeout string returns default", func(t *testing.T) {
		cfg := map[string]any{
			"timeout": "invalid",
		}

		result, err := ParseConfig(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Invalid duration string should fall through and use default
		if result.Timeout != 30*time.Second {
			t.Errorf("expected default timeout 30s for invalid string, got %v", result.Timeout)
		}
	})
}

func TestS3GetString(t *testing.T) {
	cfg := map[string]any{
		"existing": "value",
		"number":   123,
	}

	if getString(cfg, "existing") != "value" {
		t.Error("expected 'value' for existing key")
	}
	if getString(cfg, "missing") != "" {
		t.Error("expected empty string for missing key")
	}
	if getString(cfg, "number") != "" {
		t.Error("expected empty string for non-string value")
	}
}

func TestS3GetStringDefault(t *testing.T) {
	cfg := map[string]any{
		"existing": "value",
	}

	if getStringDefault(cfg, "existing", "default") != "value" {
		t.Error("expected 'value' for existing key")
	}
	if getStringDefault(cfg, "missing", "default") != "default" {
		t.Error("expected 'default' for missing key")
	}
}

func TestS3GetBool(t *testing.T) {
	cfg := map[string]any{
		"true":   true,
		"false":  false,
		"string": "true",
	}

	if !getBool(cfg, "true") {
		t.Error("expected true for true key")
	}
	if getBool(cfg, "false") {
		t.Error("expected false for false key")
	}
	if getBool(cfg, "missing") {
		t.Error("expected false for missing key")
	}
	if getBool(cfg, "string") {
		t.Error("expected false for string value")
	}
}

func TestS3GetDuration(t *testing.T) {
	cfg := map[string]any{
		"string":  "5m",
		"int":     30,
		"float64": float64(60),
		"invalid": "not-a-duration",
	}

	d := getDuration(cfg, "string", time.Second)
	if d != 5*time.Minute {
		t.Errorf("expected 5m, got %v", d)
	}

	d = getDuration(cfg, "int", time.Second)
	if d != 30*time.Second {
		t.Errorf("expected 30s, got %v", d)
	}

	d = getDuration(cfg, "float64", time.Second)
	if d != 60*time.Second {
		t.Errorf("expected 60s, got %v", d)
	}

	d = getDuration(cfg, "missing", 15*time.Second)
	if d != 15*time.Second {
		t.Errorf("expected default 15s, got %v", d)
	}

	d = getDuration(cfg, "invalid", 20*time.Second)
	if d != 20*time.Second {
		t.Errorf("expected default 20s for invalid value, got %v", d)
	}
}

func TestS3GetInt64(t *testing.T) {
	cfg := map[string]any{
		"int":     100,
		"float64": float64(200),
		"string":  "not a number",
	}

	if getInt64(cfg, "int", 0) != 100 {
		t.Error("expected 100 for int key")
	}
	if getInt64(cfg, "float64", 0) != 200 {
		t.Error("expected 200 for float64 key")
	}
	if getInt64(cfg, "missing", 50) != 50 {
		t.Error("expected default 50 for missing key")
	}
	if getInt64(cfg, "string", 50) != 50 {
		t.Error("expected default 50 for string value")
	}
}
