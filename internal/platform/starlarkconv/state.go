package starlarkconv

import (
	"fmt"

	"go.starlark.net/starlark"
)

// minStateEntryBytes is the fewest bytes one state entry costs serialized: two
// quotes, a colon and a separator around an empty key and a one-byte value.
const minStateEntryBytes = 5

// StateObject converts the dict a script passed to save_state or checkpoint
// into the plain object the platform stores, bounded at maxBytes serialized.
// A value that cannot cross into JSON (a function, a set) is refused naming
// its key, so the author is told which entry to fix. Moved from scriptrun for
// its size budget.
func StateObject(value *starlark.Dict, maxBytes int) (map[string]any, error) {
	if value == nil {
		return map[string]any{}, nil
	}
	// Every key costs at least its quotes, a colon and a separator once
	// serialized, so a dict with more keys than the bound can hold is refused
	// before it is walked rather than converted whole and then measured.
	if value.Len()*minStateEntryBytes > maxBytes {
		return nil, fmt.Errorf("the state has %d keys, more than the %d-byte limit can hold; state is a cursor or a summary, so keep a table as a resource instead",
			value.Len(), maxBytes)
	}
	out := make(map[string]any, value.Len())
	for _, item := range value.Items() {
		key, ok := item[0].(starlark.String)
		if !ok {
			return nil, fmt.Errorf("state keys must be strings, got %s", item[0].Type())
		}
		converted, err := FromStarlark(item[1])
		if err != nil {
			return nil, fmt.Errorf("state key %q: %w", string(key), err)
		}
		out[string(key)] = converted
	}
	return out, nil
}
