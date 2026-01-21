package datahub

import (
	"testing"
	"time"
)

func TestParseConfig(t *testing.T) {
	t.Run("valid config with all fields", func(t *testing.T) {
		cfg := map[string]any{
			"url":               "http://datahub.example.com:8080",
			"token":             "secret-token",
			"default_limit":     20,
			"max_limit":         200,
			"max_lineage_depth": 10,
			"timeout":           "60s",
			"connection_name":   "main-datahub",
		}

		result, err := ParseConfig(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result.URL != "http://datahub.example.com:8080" {
			t.Errorf("expected URL 'http://datahub.example.com:8080', got %q", result.URL)
		}
		if result.Token != "secret-token" {
			t.Errorf("expected Token 'secret-token', got %q", result.Token)
		}
		if result.DefaultLimit != 20 {
			t.Errorf("expected DefaultLimit 20, got %d", result.DefaultLimit)
		}
		if result.MaxLimit != 200 {
			t.Errorf("expected MaxLimit 200, got %d", result.MaxLimit)
		}
		if result.MaxLineageDepth != 10 {
			t.Errorf("expected MaxLineageDepth 10, got %d", result.MaxLineageDepth)
		}
		if result.Timeout != 60*time.Second {
			t.Errorf("expected Timeout 60s, got %v", result.Timeout)
		}
		if result.ConnectionName != "main-datahub" {
			t.Errorf("expected ConnectionName 'main-datahub', got %q", result.ConnectionName)
		}
	})

	t.Run("endpoint as alternate key for url", func(t *testing.T) {
		cfg := map[string]any{
			"endpoint": "http://datahub.example.com:8080",
		}

		result, err := ParseConfig(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result.URL != "http://datahub.example.com:8080" {
			t.Errorf("expected URL from endpoint, got %q", result.URL)
		}
	})

	t.Run("missing required url", func(t *testing.T) {
		cfg := map[string]any{
			"token": "secret",
		}

		_, err := ParseConfig(cfg)
		if err == nil {
			t.Error("expected error for missing url")
		}
	})

	t.Run("defaults applied", func(t *testing.T) {
		cfg := map[string]any{
			"url": "http://datahub.example.com:8080",
		}

		result, err := ParseConfig(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result.Timeout != 30*time.Second {
			t.Errorf("expected default timeout 30s, got %v", result.Timeout)
		}
		if result.DefaultLimit != 10 {
			t.Errorf("expected default limit 10, got %d", result.DefaultLimit)
		}
		if result.MaxLimit != 100 {
			t.Errorf("expected max limit 100, got %d", result.MaxLimit)
		}
		if result.MaxLineageDepth != 5 {
			t.Errorf("expected max lineage depth 5, got %d", result.MaxLineageDepth)
		}
	})

	t.Run("invalid timeout", func(t *testing.T) {
		cfg := map[string]any{
			"url":     "http://datahub.example.com:8080",
			"timeout": "invalid",
		}

		_, err := ParseConfig(cfg)
		if err == nil {
			t.Error("expected error for invalid timeout")
		}
	})

	t.Run("int fields as float64 (JSON unmarshaling)", func(t *testing.T) {
		cfg := map[string]any{
			"url":               "http://datahub.example.com:8080",
			"default_limit":     float64(15),
			"max_limit":         float64(150),
			"max_lineage_depth": float64(8),
		}

		result, err := ParseConfig(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.DefaultLimit != 15 {
			t.Errorf("expected default_limit 15, got %d", result.DefaultLimit)
		}
		if result.MaxLimit != 150 {
			t.Errorf("expected max_limit 150, got %d", result.MaxLimit)
		}
		if result.MaxLineageDepth != 8 {
			t.Errorf("expected max_lineage_depth 8, got %d", result.MaxLineageDepth)
		}
	})

	t.Run("timeout as int (seconds)", func(t *testing.T) {
		cfg := map[string]any{
			"url":     "http://datahub.example.com:8080",
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
			"url":     "http://datahub.example.com:8080",
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
}

func TestDatahubGetString(t *testing.T) {
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

func TestDatahubGetInt(t *testing.T) {
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

func TestDatahubGetDuration(t *testing.T) {
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
