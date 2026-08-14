package scriptrun

import (
	"fmt"
	"math"
	"sort"

	"go.starlark.net/starlark"
)

// maxConvertDepth bounds recursion through a converted value. Tool results are
// JSON and JSON nests arbitrarily; without a bound, a deeply nested result
// would recurse the Go stack rather than fail with a message.
const maxConvertDepth = 32

// exactIntFloatLimit is the magnitude past which a float64 no longer represents
// every integer exactly, so a whole-looking number above it stays a float.
const exactIntFloatLimit = 1 << 53

// toStarlark converts a decoded-JSON Go value into a Starlark value.
//
// Map keys are visited in SORTED order, which is load-bearing rather than
// cosmetic: Go map iteration is randomized, Starlark dicts preserve insertion
// order, and json.encode follows that order — so unsorted conversion would make
// a script's own output vary run to run, breaking the determinism contract at
// the one place the platform fully controls.
func toStarlark(v any) (starlark.Value, error) { return convertToStarlark(v, 0) }

// convertToStarlark is toStarlark's depth-tracking implementation.
func convertToStarlark(v any, depth int) (starlark.Value, error) {
	if depth > maxConvertDepth {
		return nil, fmt.Errorf("value nests deeper than %d levels", maxConvertDepth)
	}
	switch t := v.(type) {
	case []any:
		return convertList(t, depth)
	case map[string]any:
		return convertDict(t, depth)
	default:
		return convertScalarToStarlark(v)
	}
}

// convertScalarToStarlark converts the leaf values of a decoded JSON document.
func convertScalarToStarlark(v any) (starlark.Value, error) {
	switch t := v.(type) {
	case nil:
		return starlark.None, nil
	case bool:
		return starlark.Bool(t), nil
	case string:
		return starlark.String(t), nil
	case int:
		return starlark.MakeInt(t), nil
	case int64:
		return starlark.MakeInt64(t), nil
	case float64:
		// A JSON number that is exactly an integer arrives as float64. Row ids
		// and counts are the common case, and an author comparing one against a
		// literal should not have to know it came back as 42.0.
		if t == math.Trunc(t) && math.Abs(t) < exactIntFloatLimit {
			return starlark.MakeInt64(int64(t)), nil
		}
		return starlark.Float(t), nil
	default:
		return nil, fmt.Errorf("unsupported value of type %T", v)
	}
}

// convertList converts a JSON array.
func convertList(items []any, depth int) (starlark.Value, error) {
	out := make([]starlark.Value, 0, len(items))
	for _, item := range items {
		v, err := convertToStarlark(item, depth+1)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return starlark.NewList(out), nil
}

// convertDict converts a JSON object with its keys in sorted order.
func convertDict(m map[string]any, depth int) (starlark.Value, error) {
	d := starlark.NewDict(len(m))
	for _, k := range sortedKeys(m) {
		v, err := convertToStarlark(m[k], depth+1)
		if err != nil {
			return nil, fmt.Errorf("key %q: %w", k, err)
		}
		if err := d.SetKey(starlark.String(k), v); err != nil {
			return nil, fmt.Errorf("key %q: %w", k, err)
		}
	}
	return d, nil
}

// sortedKeys returns a map's keys in sorted order.
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// sortedSet renders a set's members in sorted order, for error messages that
// must read the same every time.
func sortedSet(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// fromStarlark converts a Starlark value back to a Go value the platform can
// serialize. Dict iteration keeps the script's own insertion order, which is
// deterministic in Starlark and is the order the author wrote.
func fromStarlark(v starlark.Value) (any, error) { return convertFromStarlark(v, 0) }

// convertFromStarlark is fromStarlark's depth-tracking implementation.
func convertFromStarlark(v starlark.Value, depth int) (any, error) {
	if depth > maxConvertDepth {
		return nil, fmt.Errorf("value nests deeper than %d levels", maxConvertDepth)
	}
	switch t := v.(type) {
	case *starlark.List:
		return listFromStarlark(t, depth)
	case *starlark.Dict:
		return dictFromStarlark(t, depth)
	default:
		return convertScalarFromStarlark(v)
	}
}

// convertScalarFromStarlark converts the leaf values a script can hand back.
// None becomes an untyped Go nil because that is what None MEANS on the way to
// JSON; the nil-error pair here is a successful conversion, not a missing
// result.
func convertScalarFromStarlark(v starlark.Value) (any, error) {
	switch t := v.(type) {
	case starlark.NoneType:
		return nil, nil //nolint:nilnil // None converts to a nil value; the nil error is success
	case starlark.Bool:
		return bool(t), nil
	case starlark.String:
		return string(t), nil
	case starlark.Int:
		i, ok := t.Int64()
		if !ok {
			return nil, fmt.Errorf("integer %s does not fit in 64 bits", t.String())
		}
		return i, nil
	case starlark.Float:
		return float64(t), nil
	default:
		return nil, fmt.Errorf("values of type %s cannot leave the script", v.Type())
	}
}

// listFromStarlark converts a Starlark list.
func listFromStarlark(l *starlark.List, depth int) (any, error) {
	out := make([]any, 0, l.Len())
	for i := range l.Len() {
		item, err := convertFromStarlark(l.Index(i), depth+1)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

// dictFromStarlark converts a Starlark dict, refusing non-string keys: the
// result is destined for JSON, where an object key is a string.
func dictFromStarlark(d *starlark.Dict, depth int) (any, error) {
	out := make(map[string]any, d.Len())
	for _, item := range d.Items() {
		key, ok := starlark.AsString(item[0])
		if !ok {
			return nil, fmt.Errorf("dict keys must be strings, got %s", item[0].Type())
		}
		val, err := convertFromStarlark(item[1], depth+1)
		if err != nil {
			return nil, fmt.Errorf("key %q: %w", key, err)
		}
		out[key] = val
	}
	return out, nil
}
