// Package jsonwalk rewrites the strings of a decoded JSON value: the walk a
// script recording's arguments are renamed by on an application import and
// redacted by before they are recorded (#2475, #2476), in one copy.
package jsonwalk

import "maps"

// Strings returns v with every string in it, at any depth of maps and lists,
// passed through f, and whether any changed. v and every map and list in it
// are left as they are; a container holding a changed string is copied.
func Strings(v any, f func(string) string) (any, bool) {
	switch t := v.(type) {
	case string:
		r := f(t)
		return r, r != t
	case map[string]any:
		return inMap(t, f)
	case []any:
		return inList(t, f)
	}
	return v, false
}

func inMap(m map[string]any, f func(string) string) (any, bool) {
	var out map[string]any
	for k, inner := range m {
		r, changed := Strings(inner, f)
		if !changed {
			continue
		}
		if out == nil {
			out = maps.Clone(m)
		}
		out[k] = r
	}
	if out == nil {
		return m, false
	}
	return out, true
}

func inList(l []any, f func(string) string) (any, bool) {
	var out []any
	for i, inner := range l {
		r, changed := Strings(inner, f)
		if !changed {
			continue
		}
		if out == nil {
			out = append([]any(nil), l...)
		}
		out[i] = r
	}
	if out == nil {
		return l, false
	}
	return out, true
}
