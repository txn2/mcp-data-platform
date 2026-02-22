package trino

import (
	"testing"
	"time"
)

const (
	trinoCfgTestExisting      = "existing"
	trinoCfgTestMissing       = "missing"
	trinoCfgTestString        = "string"
	trinoCfgTestNumVal        = 123
	trinoCfgTestIntVal        = 100
	trinoCfgTestFloat64Val    = 200
	trinoCfgTestDefaultVal    = 50
	trinoCfgTestDurationInt   = 30
	trinoCfgTestDurationFlt   = 60
	trinoCfgTestPort9090      = 9090
	trinoCfgTestTimeout45     = 45
	trinoCfgTestDuration5Min  = 5
	trinoCfgTestInt           = "int"
	trinoCfgTestFloat64       = "float64"
	trinoCfgTestUnexpectedErr = "unexpected error: %v"
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
		t.Fatalf(trinoCfgTestUnexpectedErr, err)
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
		t.Fatalf(trinoCfgTestUnexpectedErr, err)
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
	result, err := ParseConfig(map[string]any{"host": "trino.example.com", "port": float64(trinoCfgTestPort9090)})
	if err != nil {
		t.Fatalf(trinoCfgTestUnexpectedErr, err)
	}
	if result.Port != trinoCfgTestPort9090 {
		t.Errorf("expected port %d, got %d", trinoCfgTestPort9090, result.Port)
	}
}

func TestParseConfig_TimeoutAsInt(t *testing.T) {
	result, err := ParseConfig(map[string]any{"host": "trino.example.com", "timeout": trinoCfgTestDurationInt})
	if err != nil {
		t.Fatalf(trinoCfgTestUnexpectedErr, err)
	}
	if result.Timeout != trinoCfgTestDurationInt*time.Second {
		t.Errorf("expected timeout 30s, got %v", result.Timeout)
	}
}

func TestParseConfig_TimeoutAsFloat64(t *testing.T) {
	result, err := ParseConfig(map[string]any{"host": "trino.example.com", "timeout": float64(trinoCfgTestTimeout45)})
	if err != nil {
		t.Fatalf(trinoCfgTestUnexpectedErr, err)
	}
	if result.Timeout != trinoCfgTestTimeout45*time.Second {
		t.Errorf("expected timeout %ds, got %v", trinoCfgTestTimeout45, result.Timeout)
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
		trinoCfgTestInt:     trinoCfgTestIntVal,
		trinoCfgTestFloat64: float64(trinoCfgTestFloat64Val),
		trinoCfgTestString:  "not a number",
	}

	if getInt(cfg, trinoCfgTestInt, 0) != trinoCfgTestIntVal {
		t.Error("expected 100 for int key")
	}
	if getInt(cfg, trinoCfgTestFloat64, 0) != trinoCfgTestFloat64Val {
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

func TestGetStringMap(t *testing.T) {
	t.Run("valid map", func(t *testing.T) {
		cfg := map[string]any{
			"descriptions": map[string]any{
				"trino_query":          "Run a SQL query",
				"trino_describe_table": "Get table details",
			},
		}
		result := getStringMap(cfg, "descriptions")
		if len(result) != 2 {
			t.Fatalf("expected 2 entries, got %d", len(result))
		}
		if result["trino_query"] != "Run a SQL query" {
			t.Errorf("trino_query = %q", result["trino_query"])
		}
		if result["trino_describe_table"] != "Get table details" {
			t.Errorf("trino_describe_table = %q", result["trino_describe_table"])
		}
	})

	t.Run("missing key", func(t *testing.T) {
		cfg := map[string]any{}
		result := getStringMap(cfg, "descriptions")
		if result != nil {
			t.Errorf("expected nil for missing key, got %v", result)
		}
	})

	t.Run("wrong type", func(t *testing.T) {
		cfg := map[string]any{"descriptions": "not a map"}
		result := getStringMap(cfg, "descriptions")
		if result != nil {
			t.Errorf("expected nil for wrong type, got %v", result)
		}
	})

	t.Run("skips non-string values", func(t *testing.T) {
		cfg := map[string]any{
			"descriptions": map[string]any{
				"valid":   "a string",
				"invalid": trinoCfgTestNumVal,
			},
		}
		result := getStringMap(cfg, "descriptions")
		if len(result) != 1 {
			t.Fatalf("expected 1 entry (non-string skipped), got %d", len(result))
		}
		if result["valid"] != "a string" {
			t.Errorf("valid = %q", result["valid"])
		}
	})
}

func TestParseConfig_WithDescriptions(t *testing.T) {
	cfg := map[string]any{
		"host": "trino.example.com",
		"descriptions": map[string]any{
			"trino_query": "Custom query description",
		},
	}

	result, err := ParseConfig(cfg)
	if err != nil {
		t.Fatalf(trinoCfgTestUnexpectedErr, err)
	}
	if len(result.Descriptions) != 1 {
		t.Fatalf("expected 1 description, got %d", len(result.Descriptions))
	}
	if result.Descriptions["trino_query"] != "Custom query description" {
		t.Errorf("trino_query description = %q", result.Descriptions["trino_query"])
	}
}

func TestParseConfig_WithDescription(t *testing.T) {
	cfg := map[string]any{
		"host":        "trino.example.com",
		"description": "Production data warehouse for analytics",
	}

	result, err := ParseConfig(cfg)
	if err != nil {
		t.Fatalf(trinoCfgTestUnexpectedErr, err)
	}
	if result.Description != "Production data warehouse for analytics" {
		t.Errorf("Description = %q, want 'Production data warehouse for analytics'", result.Description)
	}
}

func TestParseConfig_NoDescription(t *testing.T) {
	cfg := map[string]any{
		"host": "trino.example.com",
	}

	result, err := ParseConfig(cfg)
	if err != nil {
		t.Fatalf(trinoCfgTestUnexpectedErr, err)
	}
	if result.Description != "" {
		t.Errorf("Description should be empty, got %q", result.Description)
	}
}

func TestParseConfig_NoDescriptions(t *testing.T) {
	cfg := map[string]any{
		"host": "trino.example.com",
	}

	result, err := ParseConfig(cfg)
	if err != nil {
		t.Fatalf(trinoCfgTestUnexpectedErr, err)
	}
	if result.Descriptions != nil {
		t.Errorf("expected nil descriptions, got %v", result.Descriptions)
	}
}

func TestGetAnnotationsMap(t *testing.T) {
	t.Run("valid map", func(t *testing.T) {
		cfg := map[string]any{
			"annotations": map[string]any{
				"trino_query": map[string]any{
					"read_only_hint":   true,
					"destructive_hint": false,
					"idempotent_hint":  true,
					"open_world_hint":  false,
				},
			},
		}
		result := getAnnotationsMap(cfg, "annotations")
		if len(result) != 1 {
			t.Fatalf("expected 1 entry, got %d", len(result))
		}
		ann := result["trino_query"]
		if ann.ReadOnlyHint == nil || !*ann.ReadOnlyHint {
			t.Error("expected ReadOnlyHint=true")
		}
		if ann.DestructiveHint == nil || *ann.DestructiveHint {
			t.Error("expected DestructiveHint=false")
		}
		if ann.IdempotentHint == nil || !*ann.IdempotentHint {
			t.Error("expected IdempotentHint=true")
		}
		if ann.OpenWorldHint == nil || *ann.OpenWorldHint {
			t.Error("expected OpenWorldHint=false")
		}
	})

	t.Run("missing key", func(t *testing.T) {
		result := getAnnotationsMap(map[string]any{}, "annotations")
		if result != nil {
			t.Errorf("expected nil, got %v", result)
		}
	})

	t.Run("wrong type", func(t *testing.T) {
		result := getAnnotationsMap(map[string]any{"annotations": "not a map"}, "annotations")
		if result != nil {
			t.Errorf("expected nil, got %v", result)
		}
	})

	t.Run("skips non-map entries", func(t *testing.T) {
		cfg := map[string]any{
			"annotations": map[string]any{
				"valid":   map[string]any{"read_only_hint": true},
				"invalid": "not a map",
			},
		}
		result := getAnnotationsMap(cfg, "annotations")
		if len(result) != 1 {
			t.Fatalf("expected 1 entry (non-map skipped), got %d", len(result))
		}
		if result["valid"].ReadOnlyHint == nil || !*result["valid"].ReadOnlyHint {
			t.Error("expected valid entry ReadOnlyHint=true")
		}
	})

	t.Run("partial fields", func(t *testing.T) {
		cfg := map[string]any{
			"annotations": map[string]any{
				"trino_query": map[string]any{
					"read_only_hint": true,
				},
			},
		}
		result := getAnnotationsMap(cfg, "annotations")
		ann := result["trino_query"]
		if ann.ReadOnlyHint == nil || !*ann.ReadOnlyHint {
			t.Error("expected ReadOnlyHint=true")
		}
		if ann.DestructiveHint != nil {
			t.Error("expected DestructiveHint=nil (unset)")
		}
		if ann.IdempotentHint != nil {
			t.Error("expected IdempotentHint=nil (unset)")
		}
		if ann.OpenWorldHint != nil {
			t.Error("expected OpenWorldHint=nil (unset)")
		}
	})
}

func TestParseConfig_WithAnnotations(t *testing.T) {
	cfg := map[string]any{
		"host": "trino.example.com",
		"annotations": map[string]any{
			"trino_query": map[string]any{
				"read_only_hint": true,
			},
		},
	}

	result, err := ParseConfig(cfg)
	if err != nil {
		t.Fatalf(trinoCfgTestUnexpectedErr, err)
	}
	if len(result.Annotations) != 1 {
		t.Fatalf("expected 1 annotation, got %d", len(result.Annotations))
	}
	ann := result.Annotations["trino_query"]
	if ann.ReadOnlyHint == nil || !*ann.ReadOnlyHint {
		t.Error("expected trino_query ReadOnlyHint=true")
	}
}

func TestParseConfig_NoAnnotations(t *testing.T) {
	cfg := map[string]any{
		"host": "trino.example.com",
	}

	result, err := ParseConfig(cfg)
	if err != nil {
		t.Fatalf(trinoCfgTestUnexpectedErr, err)
	}
	if result.Annotations != nil {
		t.Errorf("expected nil annotations, got %v", result.Annotations)
	}
}

func TestParseMultiConfig(t *testing.T) {
	t.Run("multiple instances", func(t *testing.T) {
		instances := map[string]map[string]any{
			trinoTestWarehouse: {
				"host": "warehouse.example.com",
				"user": "trino",
				"port": trinoTestPort8080,
			},
			"elasticsearch": {
				"host": "es.example.com",
				"user": "es-user",
			},
		}

		mc, err := ParseMultiConfig(trinoTestWarehouse, instances)
		if err != nil {
			t.Fatalf(trinoCfgTestUnexpectedErr, err)
		}

		if mc.DefaultConnection != trinoTestWarehouse {
			t.Errorf("DefaultConnection = %q, want %q", mc.DefaultConnection, trinoTestWarehouse)
		}
		if len(mc.Instances) != 2 {
			t.Fatalf("expected 2 instances, got %d", len(mc.Instances))
		}

		wh := mc.Instances[trinoTestWarehouse]
		if wh.Host != "warehouse.example.com" {
			t.Errorf("warehouse.Host = %q", wh.Host)
		}
		if wh.ConnectionName != trinoTestWarehouse {
			t.Errorf("warehouse.ConnectionName = %q, want %q", wh.ConnectionName, trinoTestWarehouse)
		}

		es := mc.Instances["elasticsearch"]
		if es.Host != "es.example.com" {
			t.Errorf("es.Host = %q", es.Host)
		}
		if es.ConnectionName != "elasticsearch" {
			t.Errorf("es.ConnectionName = %q, want 'elasticsearch'", es.ConnectionName)
		}
	})

	t.Run("preserves explicit connection name", func(t *testing.T) {
		instances := map[string]map[string]any{
			"main": {
				"host":            "trino.example.com",
				"user":            "trino",
				"connection_name": "custom-name",
			},
		}

		mc, err := ParseMultiConfig("main", instances)
		if err != nil {
			t.Fatalf(trinoCfgTestUnexpectedErr, err)
		}
		if mc.Instances["main"].ConnectionName != "custom-name" {
			t.Errorf("ConnectionName = %q, want 'custom-name'", mc.Instances["main"].ConnectionName)
		}
	})

	t.Run("error in instance config", func(t *testing.T) {
		instances := map[string]map[string]any{
			"good": {"host": "good.example.com"},
			"bad":  {"timeout": "not-a-duration"},
		}

		_, err := ParseMultiConfig("good", instances)
		if err == nil {
			t.Error("expected error for invalid instance config")
		}
	})

	t.Run("missing host returns error", func(t *testing.T) {
		instances := map[string]map[string]any{
			"missing-host": {"user": "testuser"},
		}

		_, err := ParseMultiConfig("missing-host", instances)
		if err == nil {
			t.Error("expected error for missing host")
		}
	})
}

func TestGetDuration(t *testing.T) {
	cfg := map[string]any{
		trinoCfgTestString:  "5m",
		trinoCfgTestInt:     trinoCfgTestDurationInt,
		trinoCfgTestFloat64: float64(trinoCfgTestDurationFlt),
		"invalid":           "not-a-duration",
	}

	d, err := getDuration(cfg, trinoCfgTestString)
	if err != nil || d != trinoCfgTestDuration5Min*time.Minute {
		t.Errorf("expected 5m, got %v (err: %v)", d, err)
	}

	d, err = getDuration(cfg, trinoCfgTestInt)
	if err != nil || d != trinoCfgTestDurationInt*time.Second {
		t.Errorf("expected 30s, got %v (err: %v)", d, err)
	}

	d, err = getDuration(cfg, trinoCfgTestFloat64)
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
