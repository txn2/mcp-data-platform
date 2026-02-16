package datahub

import (
	"testing"
	"time"
)

const (
	dhCfgTestMissing       = "missing"
	dhCfgTestNumber        = "number"
	dhCfgTestTimeoutSec    = 60
	dhCfgTestNumVal        = 123
	dhCfgTestIntVal        = 100
	dhCfgTestFloat64Val    = 200
	dhCfgTestDefaultVal    = 50
	dhCfgTestDurationInt   = 30
	dhCfgTestDurationFlt   = 60
	dhCfgTestString        = "string"
	dhCfgTestExisting      = "existing"
	dhCfgTestTimeout       = "timeout"
	dhCfgTestInt           = "int"
	dhCfgTestFloat64       = "float64"
	dhCfgTestDefaultLimit  = 20
	dhCfgTestMaxLimit      = 200
	dhCfgTestLineageDepth  = 10
	dhCfgTestLimit15       = 15
	dhCfgTestMaxLimit150   = 150
	dhCfgTestLineageDepth8 = 8
	dhCfgTestTimeout45     = 45
	dhCfgTestTimeout90     = 90
	dhCfgTestDuration5Min  = 5
	dhCfgTestExampleURL    = "http://datahub.example.com:8080"
	dhCfgTestURLKey        = "url"
	dhCfgTestUnexpectedErr = "unexpected error: %v"
)

func TestParseConfig_ValidAllFields(t *testing.T) {
	cfg := map[string]any{
		dhCfgTestURLKey:     dhCfgTestExampleURL,
		"token":             "secret-token",
		"default_limit":     dhCfgTestDefaultLimit,
		"max_limit":         dhCfgTestMaxLimit,
		"max_lineage_depth": dhCfgTestLineageDepth,
		dhCfgTestTimeout:    "60s",
		"connection_name":   "main-datahub",
	}

	result, err := ParseConfig(cfg)
	if err != nil {
		t.Fatalf(dhCfgTestUnexpectedErr, err)
	}

	assertDatahubConfigAllFields(t, result)
}

func assertDatahubConfigAllFields(t *testing.T, result Config) {
	t.Helper()
	if result.URL != dhCfgTestExampleURL {
		t.Errorf("expected URL 'http://datahub.example.com:8080', got %q", result.URL)
	}
	if result.Token != "secret-token" {
		t.Errorf("expected Token 'secret-token', got %q", result.Token)
	}
	if result.DefaultLimit != dhCfgTestDefaultLimit {
		t.Errorf("expected DefaultLimit %d, got %d", dhCfgTestDefaultLimit, result.DefaultLimit)
	}
	if result.MaxLimit != dhCfgTestMaxLimit {
		t.Errorf("expected MaxLimit %d, got %d", dhCfgTestMaxLimit, result.MaxLimit)
	}
	if result.MaxLineageDepth != dhCfgTestLineageDepth {
		t.Errorf("expected MaxLineageDepth %d, got %d", dhCfgTestLineageDepth, result.MaxLineageDepth)
	}
	if result.Timeout != dhCfgTestTimeoutSec*time.Second {
		t.Errorf("expected Timeout 60s, got %v", result.Timeout)
	}
	if result.ConnectionName != "main-datahub" {
		t.Errorf("expected ConnectionName 'main-datahub', got %q", result.ConnectionName)
	}
}

func TestParseConfig_EndpointAsURL(t *testing.T) {
	cfg := map[string]any{
		"endpoint": dhCfgTestExampleURL,
	}

	result, err := ParseConfig(cfg)
	if err != nil {
		t.Fatalf(dhCfgTestUnexpectedErr, err)
	}

	if result.URL != dhCfgTestExampleURL {
		t.Errorf("expected URL from endpoint, got %q", result.URL)
	}
}

func TestParseConfig_MissingRequiredURL(t *testing.T) {
	cfg := map[string]any{
		"token": "secret",
	}

	_, err := ParseConfig(cfg)
	if err == nil {
		t.Error("expected error for missing url")
	}
}

func TestParseConfig_DefaultsApplied(t *testing.T) {
	cfg := map[string]any{
		dhCfgTestURLKey: dhCfgTestExampleURL,
	}

	result, err := ParseConfig(cfg)
	if err != nil {
		t.Fatalf(dhCfgTestUnexpectedErr, err)
	}

	if result.Timeout != dhCfgTestDurationInt*time.Second {
		t.Errorf("expected default timeout 30s, got %v", result.Timeout)
	}
	if result.DefaultLimit != dhCfgTestLineageDepth {
		t.Errorf("expected default limit %d, got %d", dhCfgTestLineageDepth, result.DefaultLimit)
	}
	if result.MaxLimit != dhCfgTestIntVal {
		t.Errorf("expected max limit %d, got %d", dhCfgTestIntVal, result.MaxLimit)
	}
	if result.MaxLineageDepth != dhCfgTestDuration5Min {
		t.Errorf("expected max lineage depth %d, got %d", dhCfgTestDuration5Min, result.MaxLineageDepth)
	}
}

func TestParseConfig_InvalidTimeout(t *testing.T) {
	cfg := map[string]any{
		dhCfgTestURLKey:  dhCfgTestExampleURL,
		dhCfgTestTimeout: "invalid",
	}

	_, err := ParseConfig(cfg)
	if err == nil {
		t.Error("expected error for invalid timeout")
	}
}

func TestParseConfig_IntFieldsAsFloat64(t *testing.T) {
	cfg := map[string]any{
		dhCfgTestURLKey:     dhCfgTestExampleURL,
		"default_limit":     float64(dhCfgTestLimit15),
		"max_limit":         float64(dhCfgTestMaxLimit150),
		"max_lineage_depth": float64(dhCfgTestLineageDepth8),
	}

	result, err := ParseConfig(cfg)
	if err != nil {
		t.Fatalf(dhCfgTestUnexpectedErr, err)
	}
	if result.DefaultLimit != dhCfgTestLimit15 {
		t.Errorf("expected default_limit %d, got %d", dhCfgTestLimit15, result.DefaultLimit)
	}
	if result.MaxLimit != dhCfgTestMaxLimit150 {
		t.Errorf("expected max_limit %d, got %d", dhCfgTestMaxLimit150, result.MaxLimit)
	}
	if result.MaxLineageDepth != dhCfgTestLineageDepth8 {
		t.Errorf("expected max_lineage_depth %d, got %d", dhCfgTestLineageDepth8, result.MaxLineageDepth)
	}
}

func TestParseConfig_TimeoutAsInt(t *testing.T) {
	cfg := map[string]any{
		dhCfgTestURLKey:  dhCfgTestExampleURL,
		dhCfgTestTimeout: dhCfgTestTimeout45,
	}

	result, err := ParseConfig(cfg)
	if err != nil {
		t.Fatalf(dhCfgTestUnexpectedErr, err)
	}
	if result.Timeout != dhCfgTestTimeout45*time.Second {
		t.Errorf("expected timeout %ds, got %v", dhCfgTestTimeout45, result.Timeout)
	}
}

func TestParseConfig_TimeoutAsFloat64(t *testing.T) {
	cfg := map[string]any{
		dhCfgTestURLKey:  dhCfgTestExampleURL,
		dhCfgTestTimeout: float64(dhCfgTestTimeout90),
	}

	result, err := ParseConfig(cfg)
	if err != nil {
		t.Fatalf(dhCfgTestUnexpectedErr, err)
	}
	if result.Timeout != dhCfgTestTimeout90*time.Second {
		t.Errorf("expected timeout %ds, got %v", dhCfgTestTimeout90, result.Timeout)
	}
}

func TestDatahubGetString(t *testing.T) {
	cfg := map[string]any{
		dhCfgTestExisting: "value",
		dhCfgTestNumber:   dhCfgTestNumVal,
	}

	if getString(cfg, dhCfgTestExisting) != "value" {
		t.Error("expected 'value' for existing key")
	}
	if getString(cfg, dhCfgTestMissing) != "" {
		t.Error("expected empty string for missing key")
	}
	if getString(cfg, dhCfgTestNumber) != "" {
		t.Error("expected empty string for non-string value")
	}
}

func TestDatahubGetInt(t *testing.T) {
	cfg := map[string]any{
		dhCfgTestInt:     dhCfgTestIntVal,
		dhCfgTestFloat64: float64(dhCfgTestFloat64Val),
		dhCfgTestString:  "not a number",
	}

	if getInt(cfg, dhCfgTestInt, 0) != dhCfgTestIntVal {
		t.Error("expected 100 for int key")
	}
	if getInt(cfg, dhCfgTestFloat64, 0) != dhCfgTestFloat64Val {
		t.Error("expected 200 for float64 key")
	}
	if getInt(cfg, dhCfgTestMissing, dhCfgTestDefaultVal) != dhCfgTestDefaultVal {
		t.Error("expected default 50 for missing key")
	}
	if getInt(cfg, dhCfgTestString, dhCfgTestDefaultVal) != dhCfgTestDefaultVal {
		t.Error("expected default 50 for string value")
	}
}

func TestGetStringMap(t *testing.T) {
	t.Run("valid map", func(t *testing.T) {
		cfg := map[string]any{
			"descriptions": map[string]any{
				"datahub_search":     "Search the catalog",
				"datahub_get_entity": "Get entity details",
			},
		}
		result := getStringMap(cfg, "descriptions")
		if len(result) != 2 {
			t.Fatalf("expected 2 entries, got %d", len(result))
		}
		if result["datahub_search"] != "Search the catalog" {
			t.Errorf("datahub_search = %q", result["datahub_search"])
		}
		if result["datahub_get_entity"] != "Get entity details" {
			t.Errorf("datahub_get_entity = %q", result["datahub_get_entity"])
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
				"invalid": dhCfgTestNumVal,
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
		dhCfgTestURLKey: dhCfgTestExampleURL,
		"descriptions": map[string]any{
			"datahub_search": "Custom search description",
		},
	}

	result, err := ParseConfig(cfg)
	if err != nil {
		t.Fatalf(dhCfgTestUnexpectedErr, err)
	}
	if len(result.Descriptions) != 1 {
		t.Fatalf("expected 1 description, got %d", len(result.Descriptions))
	}
	if result.Descriptions["datahub_search"] != "Custom search description" {
		t.Errorf("datahub_search description = %q", result.Descriptions["datahub_search"])
	}
}

func TestParseConfig_NoDescriptions(t *testing.T) {
	cfg := map[string]any{
		dhCfgTestURLKey: dhCfgTestExampleURL,
	}

	result, err := ParseConfig(cfg)
	if err != nil {
		t.Fatalf(dhCfgTestUnexpectedErr, err)
	}
	if result.Descriptions != nil {
		t.Errorf("expected nil descriptions, got %v", result.Descriptions)
	}
}

func TestGetAnnotationsMap(t *testing.T) {
	t.Run("valid map", func(t *testing.T) {
		cfg := map[string]any{
			"annotations": map[string]any{
				"datahub_search": map[string]any{
					"read_only_hint":  true,
					"idempotent_hint": true,
					"open_world_hint": false,
				},
			},
		}
		result := getAnnotationsMap(cfg, "annotations")
		if len(result) != 1 {
			t.Fatalf("expected 1 entry, got %d", len(result))
		}
		ann := result["datahub_search"]
		if ann.ReadOnlyHint == nil || !*ann.ReadOnlyHint {
			t.Error("expected ReadOnlyHint=true")
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
			t.Fatalf("expected 1 entry, got %d", len(result))
		}
	})

	t.Run("partial fields", func(t *testing.T) {
		cfg := map[string]any{
			"annotations": map[string]any{
				"datahub_search": map[string]any{
					"read_only_hint": true,
				},
			},
		}
		result := getAnnotationsMap(cfg, "annotations")
		ann := result["datahub_search"]
		if ann.ReadOnlyHint == nil || !*ann.ReadOnlyHint {
			t.Error("expected ReadOnlyHint=true")
		}
		if ann.DestructiveHint != nil {
			t.Error("expected DestructiveHint=nil")
		}
	})
}

func TestParseConfig_WithAnnotations(t *testing.T) {
	cfg := map[string]any{
		dhCfgTestURLKey: dhCfgTestExampleURL,
		"annotations": map[string]any{
			"datahub_search": map[string]any{
				"read_only_hint": true,
			},
		},
	}

	result, err := ParseConfig(cfg)
	if err != nil {
		t.Fatalf(dhCfgTestUnexpectedErr, err)
	}
	if len(result.Annotations) != 1 {
		t.Fatalf("expected 1 annotation, got %d", len(result.Annotations))
	}
	ann := result.Annotations["datahub_search"]
	if ann.ReadOnlyHint == nil || !*ann.ReadOnlyHint {
		t.Error("expected datahub_search ReadOnlyHint=true")
	}
}

func TestParseConfig_NoAnnotations(t *testing.T) {
	cfg := map[string]any{
		dhCfgTestURLKey: dhCfgTestExampleURL,
	}

	result, err := ParseConfig(cfg)
	if err != nil {
		t.Fatalf(dhCfgTestUnexpectedErr, err)
	}
	if result.Annotations != nil {
		t.Errorf("expected nil annotations, got %v", result.Annotations)
	}
}

func TestDatahubGetDuration(t *testing.T) {
	cfg := map[string]any{
		dhCfgTestString:  "5m",
		dhCfgTestInt:     dhCfgTestDurationInt,
		dhCfgTestFloat64: float64(dhCfgTestDurationFlt),
		"invalid":        "not-a-duration",
	}

	d, err := getDuration(cfg, dhCfgTestString)
	if err != nil || d != dhCfgTestDuration5Min*time.Minute {
		t.Errorf("expected 5m, got %v (err: %v)", d, err)
	}

	d, err = getDuration(cfg, dhCfgTestInt)
	if err != nil || d != dhCfgTestDurationInt*time.Second {
		t.Errorf("expected 30s, got %v (err: %v)", d, err)
	}

	d, err = getDuration(cfg, dhCfgTestFloat64)
	if err != nil || d != dhCfgTestDurationFlt*time.Second {
		t.Errorf("expected 60s, got %v (err: %v)", d, err)
	}

	d, err = getDuration(cfg, dhCfgTestMissing)
	if err != nil || d != 0 {
		t.Errorf("expected 0, got %v (err: %v)", d, err)
	}

	_, err = getDuration(cfg, "invalid")
	if err == nil {
		t.Error("expected error for invalid duration")
	}
}

func TestDatahubGetBool(t *testing.T) {
	cfg := map[string]any{
		"enabled":       true,
		"disabled":      false,
		dhCfgTestString: "true",
		dhCfgTestNumber: 1,
	}

	if !getBool(cfg, "enabled", false) {
		t.Error("expected true for enabled key")
	}
	if getBool(cfg, "disabled", true) {
		t.Error("expected false for disabled key")
	}
	if getBool(cfg, dhCfgTestMissing, false) {
		t.Error("expected default false for missing key")
	}
	if !getBool(cfg, dhCfgTestMissing, true) {
		t.Error("expected default true for missing key")
	}
	if getBool(cfg, dhCfgTestString, false) {
		t.Error("expected default false for non-bool string value")
	}
	if getBool(cfg, dhCfgTestNumber, false) {
		t.Error("expected default false for non-bool number value")
	}
}

func TestParseConfigDebug(t *testing.T) {
	t.Run("debug enabled", func(t *testing.T) {
		cfg := map[string]any{
			dhCfgTestURLKey: dhCfgTestExampleURL,
			"debug":         true,
		}

		result, err := ParseConfig(cfg)
		if err != nil {
			t.Fatalf(dhCfgTestUnexpectedErr, err)
		}

		if !result.Debug {
			t.Error("expected Debug to be true")
		}
	})

	t.Run("debug disabled explicitly", func(t *testing.T) {
		cfg := map[string]any{
			dhCfgTestURLKey: dhCfgTestExampleURL,
			"debug":         false,
		}

		result, err := ParseConfig(cfg)
		if err != nil {
			t.Fatalf(dhCfgTestUnexpectedErr, err)
		}

		if result.Debug {
			t.Error("expected Debug to be false")
		}
	})

	t.Run("debug defaults to false", func(t *testing.T) {
		cfg := map[string]any{
			dhCfgTestURLKey: dhCfgTestExampleURL,
		}

		result, err := ParseConfig(cfg)
		if err != nil {
			t.Fatalf(dhCfgTestUnexpectedErr, err)
		}

		if result.Debug {
			t.Error("expected Debug to default to false")
		}
	})
}
