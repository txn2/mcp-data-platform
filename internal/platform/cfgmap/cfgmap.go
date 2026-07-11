// Package cfgmap holds typed accessors for reading values out of the
// map[string]any config blobs the platform loads from YAML/JSON. YAML and JSON
// decode into map[string]any with values of varying concrete types (int vs
// float64, string durations, etc.), so these helpers centralize the type
// assertions and defaulting used across the platform's config resolution.
//
// Split out of pkg/platform to keep that package under its size budget (#756).
package cfgmap

import "time"

// String returns the string value at key, or "" if absent or not a string.
func String(cfg map[string]any, key string) string {
	if v, ok := cfg[key].(string); ok {
		return v
	}
	return ""
}

// Int returns the int value at key, accepting JSON's float64 numbers, or
// defaultVal if absent or not numeric.
func Int(cfg map[string]any, key string, defaultVal int) int {
	if v, ok := cfg[key].(int); ok {
		return v
	}
	if v, ok := cfg[key].(float64); ok {
		return int(v)
	}
	return defaultVal
}

// Bool returns the bool value at key, or false if absent or not a bool.
func Bool(cfg map[string]any, key string) bool {
	if v, ok := cfg[key].(bool); ok {
		return v
	}
	return false
}

// BoolDefault returns the bool value at key, or defaultVal if absent or not a
// bool. Use this for flags that default to true.
func BoolDefault(cfg map[string]any, key string, defaultVal bool) bool {
	if v, ok := cfg[key].(bool); ok {
		return v
	}
	return defaultVal
}

// Duration returns the duration at key. A string is parsed with
// time.ParseDuration; a bare number (int or JSON float64) is interpreted as
// seconds. Returns defaultVal if absent or unparseable.
func Duration(cfg map[string]any, key string, defaultVal time.Duration) time.Duration {
	if v, ok := cfg[key].(string); ok {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	if v, ok := cfg[key].(int); ok {
		return time.Duration(v) * time.Second
	}
	if v, ok := cfg[key].(float64); ok {
		return time.Duration(v) * time.Second
	}
	return defaultVal
}
