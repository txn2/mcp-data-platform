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
