package trino

import (
	"testing"
	"time"
)

func TestParseConfig(t *testing.T) {
	t.Run("valid config with all fields", func(t *testing.T) {
		cfg := map[string]any{
			"host":            "trino.example.com",
			"port":            8080,
			"user":            "testuser",
			"password":        "secret",
			"catalog":         "hive",
			"schema":          "default",
			"ssl":             true,
			"ssl_verify":      false,
			"read_only":       true,
			"default_limit":   500,
			"max_limit":       5000,
			"timeout":         "60s",
			"connection_name": "main-trino",
		}

		result, err := ParseConfig(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result.Host != "trino.example.com" {
			t.Errorf("expected host 'trino.example.com', got %q", result.Host)
		}
		if result.Port != 8080 {
			t.Errorf("expected port 8080, got %d", result.Port)
		}
		if result.User != "testuser" {
			t.Errorf("expected user 'testuser', got %q", result.User)
		}
		if result.Password != "secret" {
			t.Errorf("expected password 'secret', got %q", result.Password)
		}
		if result.Catalog != "hive" {
			t.Errorf("expected catalog 'hive', got %q", result.Catalog)
		}
		if result.Schema != "default" {
			t.Errorf("expected schema 'default', got %q", result.Schema)
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
		if result.DefaultLimit != 500 {
			t.Errorf("expected DefaultLimit 500, got %d", result.DefaultLimit)
		}
		if result.MaxLimit != 5000 {
			t.Errorf("expected MaxLimit 5000, got %d", result.MaxLimit)
		}
		if result.Timeout != 60*time.Second {
			t.Errorf("expected Timeout 60s, got %v", result.Timeout)
		}
		if result.ConnectionName != "main-trino" {
			t.Errorf("expected ConnectionName 'main-trino', got %q", result.ConnectionName)
		}
	})

	t.Run("missing required host", func(t *testing.T) {
		cfg := map[string]any{
			"user": "testuser",
		}

		_, err := ParseConfig(cfg)
		if err == nil {
			t.Error("expected error for missing host")
		}
	})

	t.Run("defaults applied", func(t *testing.T) {
		cfg := map[string]any{
			"host": "trino.example.com",
		}

		result, err := ParseConfig(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result.Port != 8080 {
			t.Errorf("expected default port 8080, got %d", result.Port)
		}
		if result.DefaultLimit != 1000 {
			t.Errorf("expected default limit 1000, got %d", result.DefaultLimit)
		}
		if result.MaxLimit != 10000 {
			t.Errorf("expected max limit 10000, got %d", result.MaxLimit)
		}
		if result.Timeout != 120*time.Second {
			t.Errorf("expected default timeout 120s, got %v", result.Timeout)
		}
		if !result.SSLVerify {
			t.Error("expected SSLVerify to default to true")
		}
	})

	t.Run("invalid timeout", func(t *testing.T) {
		cfg := map[string]any{
			"host":    "trino.example.com",
			"timeout": "invalid",
		}

		_, err := ParseConfig(cfg)
		if err == nil {
			t.Error("expected error for invalid timeout")
		}
	})

	t.Run("port as float64 (JSON unmarshaling)", func(t *testing.T) {
		cfg := map[string]any{
			"host": "trino.example.com",
			"port": float64(9090),
		}

		result, err := ParseConfig(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Port != 9090 {
			t.Errorf("expected port 9090, got %d", result.Port)
		}
	})

	t.Run("timeout as int (seconds)", func(t *testing.T) {
		cfg := map[string]any{
			"host":    "trino.example.com",
			"timeout": 30,
		}

		result, err := ParseConfig(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Timeout != 30*time.Second {
			t.Errorf("expected timeout 30s, got %v", result.Timeout)
		}
	})

	t.Run("timeout as float64 (seconds)", func(t *testing.T) {
		cfg := map[string]any{
			"host":    "trino.example.com",
			"timeout": float64(45),
		}

		result, err := ParseConfig(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Timeout != 45*time.Second {
			t.Errorf("expected timeout 45s, got %v", result.Timeout)
		}
	})
}

func TestGetString(t *testing.T) {
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

func TestGetInt(t *testing.T) {
	cfg := map[string]any{
		"int":     100,
		"float64": float64(200),
		"string":  "not a number",
	}

	if getInt(cfg, "int", 0) != 100 {
		t.Error("expected 100 for int key")
	}
	if getInt(cfg, "float64", 0) != 200 {
		t.Error("expected 200 for float64 key")
	}
	if getInt(cfg, "missing", 50) != 50 {
		t.Error("expected default 50 for missing key")
	}
	if getInt(cfg, "string", 50) != 50 {
		t.Error("expected default 50 for string value")
	}
}

func TestGetBool(t *testing.T) {
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

func TestGetBoolDefault(t *testing.T) {
	cfg := map[string]any{
		"explicit_false": false,
		"explicit_true":  true,
	}

	if !getBoolDefault(cfg, "missing", true) {
		t.Error("expected default true for missing key")
	}
	if getBoolDefault(cfg, "missing", false) {
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
		"string":  "5m",
		"int":     30,
		"float64": float64(60),
		"invalid": "not-a-duration",
	}

	d, err := getDuration(cfg, "string")
	if err != nil || d != 5*time.Minute {
		t.Errorf("expected 5m, got %v (err: %v)", d, err)
	}

	d, err = getDuration(cfg, "int")
	if err != nil || d != 30*time.Second {
		t.Errorf("expected 30s, got %v (err: %v)", d, err)
	}

	d, err = getDuration(cfg, "float64")
	if err != nil || d != 60*time.Second {
		t.Errorf("expected 60s, got %v (err: %v)", d, err)
	}

	d, err = getDuration(cfg, "missing")
	if err != nil || d != 0 {
		t.Errorf("expected 0, got %v (err: %v)", d, err)
	}

	_, err = getDuration(cfg, "invalid")
	if err == nil {
		t.Error("expected error for invalid duration")
	}
}
