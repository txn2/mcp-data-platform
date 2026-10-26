package scriptdialect

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"
)

// A run binds a test module's name to a stub that reads as a module and
// refuses every attribute, naming where the module belongs.
func TestTestOnly(t *testing.T) {
	stub := TestOnly("assert")
	assert.Equal(t, "assert", stub.String())
	assert.Equal(t, "module", stub.Type())
	assert.Equal(t, starlark.True, stub.Truth())
	assert.Nil(t, stub.AttrNames())
	stub.Freeze()
	h, err := stub.Hash()
	require.NoError(t, err)
	want, _ := starlark.String("assert").Hash()
	assert.Equal(t, want, h)
	_, err = stub.Attr("eq")
	require.ErrorContains(t, err, "module assert.eq is available only inside a test_* function")
}
