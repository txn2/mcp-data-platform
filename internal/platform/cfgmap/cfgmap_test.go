package cfgmap

import (
	"testing"
	"time"
)

func testData() map[string]any {
	return map[string]any{
		"string_key":      "value",
		"empty_string":    "",
		"int_key":         42,
		"negative_int":    -42,
		"zero":            0,
		"float_key":       3.9,
		"negative_float":  -3.9,
		"zero_float":      0.0,
		"bool_key":        true,
		"false_bool":      false,
		"duration_string": "30s",
		"duration_int":    45,
		"duration_float":  90.0,
		"invalid_dur_str": "not-a-duration",
	}
}

func TestString(t *testing.T) {
	cfg := testData()
	if v := String(cfg, "string_key"); v != "value" {
		t.Errorf("String(string_key) = %q", v)
	}
	if v := String(cfg, "empty_string"); v != "" {
		t.Errorf("String(empty_string) = %q", v)
	}
	if v := String(cfg, "missing"); v != "" {
		t.Errorf("String(missing) = %q", v)
	}
	if v := String(cfg, "int_key"); v != "" {
		t.Errorf("String(int_key) = %q (should be empty for non-string)", v)
	}
}

func TestInt(t *testing.T) {
	cfg := testData()
	if v := Int(cfg, "int_key", 0); v != 42 {
		t.Errorf("Int(int_key) = %d", v)
	}
	if v := Int(cfg, "negative_int", 0); v != -42 {
		t.Errorf("Int(negative_int) = %d", v)
	}
	if v := Int(cfg, "zero", 7); v != 0 {
		t.Errorf("Int(zero) = %d", v)
	}
	if v := Int(cfg, "float_key", 0); v != 3 { // float64 3.9 truncates to 3
		t.Errorf("Int(float_key) = %d", v)
	}
	if v := Int(cfg, "negative_float", 0); v != -3 {
		t.Errorf("Int(negative_float) = %d", v)
	}
	if v := Int(cfg, "missing", 99); v != 99 {
		t.Errorf("Int(missing) = %d", v)
	}
	if v := Int(cfg, "string_key", 99); v != 99 {
		t.Errorf("Int(string_key) = %d (non-numeric should fall back)", v)
	}
}

func TestBool(t *testing.T) {
	cfg := testData()
	if v := Bool(cfg, "bool_key"); !v {
		t.Error("Bool(bool_key) = false")
	}
	if v := Bool(cfg, "false_bool"); v {
		t.Error("Bool(false_bool) = true")
	}
	if v := Bool(cfg, "missing"); v {
		t.Error("Bool(missing) = true (should default false)")
	}
	if v := Bool(cfg, "string_key"); v {
		t.Error("Bool(string_key) = true (non-bool should be false)")
	}
}

func TestBoolDefault(t *testing.T) {
	cfg := testData()
	if v := BoolDefault(cfg, "bool_key", false); !v {
		t.Error("BoolDefault(bool_key, false) = false")
	}
	if v := BoolDefault(cfg, "false_bool", true); v {
		t.Error("BoolDefault(false_bool, true) = true")
	}
	if v := BoolDefault(cfg, "missing", true); !v {
		t.Error("BoolDefault(missing, true) = false")
	}
	if v := BoolDefault(cfg, "int_key", true); !v {
		t.Error("BoolDefault(int_key, true) = false (non-bool should use default)")
	}
}

func TestDuration(t *testing.T) {
	cfg := testData()
	if v := Duration(cfg, "duration_string", 0); v != 30*time.Second {
		t.Errorf("Duration(duration_string) = %v", v)
	}
	if v := Duration(cfg, "duration_int", 0); v != 45*time.Second {
		t.Errorf("Duration(duration_int) = %v", v)
	}
	if v := Duration(cfg, "duration_float", 0); v != 90*time.Second {
		t.Errorf("Duration(duration_float) = %v", v)
	}
	if v := Duration(cfg, "invalid_dur_str", 5*time.Second); v != 5*time.Second {
		t.Errorf("Duration(invalid_dur_str) = %v (should fall back)", v)
	}
	if v := Duration(cfg, "zero", time.Minute); v != 0 {
		t.Errorf("Duration(zero) = %v (int 0 seconds)", v)
	}
	if v := Duration(cfg, "zero_float", time.Minute); v != 0 {
		t.Errorf("Duration(zero_float) = %v (float 0 seconds)", v)
	}
	if v := Duration(cfg, "missing", 12*time.Second); v != 12*time.Second {
		t.Errorf("Duration(missing) = %v", v)
	}
	if v := Duration(cfg, "bool_key", 8*time.Second); v != 8*time.Second {
		t.Errorf("Duration(bool_key) = %v (non-duration type should fall back)", v)
	}
}
