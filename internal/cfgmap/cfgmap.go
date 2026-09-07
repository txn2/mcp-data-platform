// Package cfgmap reads typed values out of the map[string]any form a
// connection configuration takes in the platform's connection_instances
// store.
//
// A connection is stored as JSON and reaches a toolkit as a generic map,
// so every kind that parses one needs the same handful of tolerant
// readers: a value may arrive as the Go type the author intended
// (programmatic construction, YAML with a duration string) or as
// whatever JSON round-tripping produced (a float64 for an integer, a
// string for a bool). These readers absorb that spread in one place so
// two kinds cannot disagree about what `"call_timeout": 30` means.
//
// Every reader is total: an absent key, a nil map or a value of an
// unrecognized type yields the supplied default (or the zero value)
// rather than an error. Required-field enforcement belongs to the
// caller's validation, not to reading.
package cfgmap

import (
	"maps"
	"strconv"
	"time"
)

// String reads a string value. Absent, nil or non-string yields "".
func String(cfg map[string]any, key string) string {
	if v, ok := cfg[key].(string); ok {
		return v
	}
	return ""
}

// StringDefault reads a string value, falling back to defaultVal when
// the key is absent or holds an empty string. The empty-string fallback
// matters for admin-saved connections: a form that submits every field
// writes "" for the ones the operator left blank, and those must take
// the default rather than override it with emptiness.
func StringDefault(cfg map[string]any, key, defaultVal string) string {
	if v, ok := cfg[key].(string); ok && v != "" {
		return v
	}
	return defaultVal
}

// Duration reads a duration. A string is parsed with
// time.ParseDuration ("30s", "5m"); a bare number is read as seconds,
// which is the form a JSON round-trip of an operator's "30" produces.
// An unparseable string yields defaultVal rather than an error, so one
// malformed value cannot refuse a connection that is otherwise sound.
func Duration(cfg map[string]any, key string, defaultVal time.Duration) time.Duration {
	raw, ok := cfg[key]
	if !ok {
		return defaultVal
	}
	switch v := raw.(type) {
	case string:
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	case time.Duration:
		return v
	case int:
		return time.Duration(v) * time.Second
	case int64:
		return time.Duration(v) * time.Second
	case float64:
		return time.Duration(v) * time.Second
	}
	return defaultVal
}

// Int64 reads an integer. float64 is accepted because encoding/json
// decodes every JSON number into one.
func Int64(cfg map[string]any, key string, defaultVal int64) int64 {
	raw, ok := cfg[key]
	if !ok {
		return defaultVal
	}
	switch v := raw.(type) {
	case int:
		return int64(v)
	case int64:
		return v
	case float64:
		return int64(v)
	}
	return defaultVal
}

// Bool reads a boolean flag. Absent or unrecognized values yield false.
// A string is parsed leniently (strconv.ParseBool) so YAML or JSON that
// round-trips a flag as "true"/"false" is honored alongside a native
// bool.
func Bool(cfg map[string]any, key string) bool {
	raw, ok := cfg[key]
	if !ok {
		return false
	}
	switch v := raw.(type) {
	case bool:
		return v
	case string:
		b, err := strconv.ParseBool(v)
		return err == nil && b
	}
	return false
}

// StringMap reads a map of strings. Accepts map[string]string
// (programmatic construction) or map[string]any (YAML/JSON
// unmarshaling); non-string values inside a map[string]any are skipped.
// An absent, empty or wholly non-string map yields nil, so callers can
// test the result with len() and need no separate "was it set" flag.
func StringMap(cfg map[string]any, key string) map[string]string {
	raw, ok := cfg[key]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case map[string]string:
		if len(v) == 0 {
			return nil
		}
		out := make(map[string]string, len(v))
		maps.Copy(out, v)
		return out
	case map[string]any:
		if len(v) == 0 {
			return nil
		}
		out := make(map[string]string, len(v))
		for k, val := range v {
			if s, isStr := val.(string); isStr {
				out[k] = s
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	}
	return nil
}
