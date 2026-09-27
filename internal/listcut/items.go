package listcut

import "maps"

// A list response is cut on its items, never on its bytes (#1915): what a
// caller is handed stays valid JSON, and it can be told how many items it
// holds of how many, which is what lets it ask for the rest. A byte prefix
// of the same body ends partway through a record and says neither.

// maxItemsDepth bounds how far below the top level the list is looked for.
// A GraphQL answer puts it two objects down (data.users.edges); anything
// deeper is not a list response in a shape the cut can name.
const maxItemsDepth = 3

// Items locates the one list a JSON value carries.
type Items struct {
	// Path is the keys walked from the value to the list; empty when the
	// value is itself the list.
	Path []string
	// Total is the number of items in the list.
	Total int
}

// FindItems locates the list v is cut on: v itself when it is an array, or
// the one array an object carries, found through objects that carry one
// object and no array (data.users.edges). An object with two arrays, or
// with no array within reach, has no list the cut could name, and neither
// has a scalar.
func FindItems(v any) (Items, bool) {
	path, list, ok := findList(v, 0)
	if !ok {
		return Items{}, false
	}
	return Items{Path: path, Total: len(list)}, true
}

func findList(v any, depth int) (path []string, list []any, ok bool) {
	switch t := v.(type) {
	case []any:
		return nil, t, true
	case map[string]any:
		return findInObject(t, depth)
	}
	return nil, nil, false
}

// findInObject finds the list an object carries: its one array member, or
// the list below its one object member when it has no array.
func findInObject(obj map[string]any, depth int) (path []string, list []any, ok bool) {
	var arrays, objects []string
	for k, m := range obj {
		switch m.(type) {
		case []any:
			arrays = append(arrays, k)
		case map[string]any:
			objects = append(objects, k)
		}
	}
	switch {
	case len(arrays) == 1:
		list, _ = obj[arrays[0]].([]any)
		return arrays, list, true
	case len(arrays) == 0 && len(objects) == 1 && depth < maxItemsDepth:
		if path, list, ok = findList(obj[objects[0]], depth+1); ok {
			return append(objects, path...), list, true
		}
	}
	return nil, nil, false
}

// Keep returns v with the list at path cut to its first n items. v is not
// changed: the objects on the path are copied, so the caller's decoded body
// stays whole for the next attempt.
func Keep(v any, path []string, n int) any {
	if len(path) == 0 {
		list, ok := v.([]any)
		if !ok {
			return v
		}
		return list[:min(n, len(list))]
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return v
	}
	key, rest := path[0], path[1:]
	out := maps.Clone(obj)
	out[key] = Keep(obj[key], rest, n)
	return out
}

// FitItems finds the most items of a list of total that a result renders
// within budget with. try renders the result holding the first n items and
// reports whether it fits. The count returned is below total -- a result
// that fits whole is not cut -- and at least one: a result that cannot hold
// even one item has no cut that hands the caller anything, and false is
// returned.
func FitItems(total int, try func(n int) bool) (int, bool) {
	lo, hi, best := 1, total-1, 0
	for lo <= hi {
		mid := lo + (hi-lo)/2
		if try(mid) {
			best, lo = mid, mid+1
		} else {
			hi = mid - 1
		}
	}
	return best, best > 0
}
