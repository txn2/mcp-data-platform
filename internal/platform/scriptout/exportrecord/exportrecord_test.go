package exportrecord

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"
)

func TestAppendingValue(t *testing.T) {
	v := AppendingValue(Appending{Name: "pages", Destination: "resources", Format: "jsonl", Rows: 1500, Bytes: 9000})
	d, ok := v.(*starlark.Dict)
	require.True(t, ok)
	for key, want := range map[string]starlark.Value{
		"name": starlark.String("pages"), "destination": starlark.String("resources"),
		"format": starlark.String("jsonl"), "row_count": starlark.MakeInt(1500),
		"bytes": starlark.MakeInt(9000), "appending": starlark.True, "preview": starlark.False,
	} {
		got, found, err := d.Get(starlark.String(key))
		require.NoError(t, err)
		require.True(t, found, key)
		assert.Equal(t, want, got, key)
	}
	assert.Equal(t, appendingFields, d.Len())
}

// A library output's record names the library the file is in (#2080); the
// global library has no id.
func TestValueCarriesTheLibrary(t *testing.T) {
	read := func(v starlark.Value, key string) (starlark.Value, bool) {
		d, ok := v.(*starlark.Dict)
		require.True(t, ok)
		got, found, err := d.Get(starlark.String(key))
		require.NoError(t, err)
		return got, found
	}
	persona := Value(Record{Name: "o", Destination: "resources", ResourceID: "r1", Scope: "persona", ScopeID: "finance"})
	got, _ := read(persona, "scope")
	assert.Equal(t, starlark.String("persona"), got)
	got, _ = read(persona, "scope_id")
	assert.Equal(t, starlark.String("finance"), got)

	global := Value(Record{Name: "o", Destination: "resources", ResourceID: "r1", Scope: "global"})
	_, found := read(global, "scope_id")
	assert.False(t, found)
	_, found = read(Value(Record{Name: "o", Destination: "portal", AssetID: "a1"}), "scope")
	assert.False(t, found, "a portal output is in no library")
}
