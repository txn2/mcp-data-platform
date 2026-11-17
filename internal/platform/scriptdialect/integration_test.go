package scriptdialect

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/syntax"
)

func parsed(t *testing.T, src string) *syntax.File {
	t.Helper()
	f, err := Options.Parse("s", src, 0)
	require.NoError(t, err)
	return f
}

// TestDeclaredIntegration: a top-level INTEGRATION string declares the
// integration (#2683); anything else, or none, declares nothing, and the last
// binding counts.
func TestDeclaredIntegration(t *testing.T) {
	assert.Equal(t, SiteRouting, DeclaredIntegration(parsed(t, "INTEGRATION = \"site_routing\"\n")))
	assert.Empty(t, DeclaredIntegration(parsed(t, "def main():\n    pass\n")))
	assert.Empty(t, DeclaredIntegration(parsed(t, "INTEGRATION = 3\n")), "a value that is not a string declares nothing")
	assert.Empty(t, DeclaredIntegration(parsed(t, "OTHER = \"site_routing\"\n")))
	assert.Equal(t, "b", DeclaredIntegration(parsed(t, "INTEGRATION = \"a\"\nINTEGRATION = \"b\"\n")))
	assert.Empty(t, DeclaredIntegration(parsed(t, "INTEGRATION = \"a\"\nINTEGRATION = None\n")), "a later non-string binding undoes it")
	assert.Empty(t, DeclaredIntegration(nil))
}

// TestIsEntry: main, handle, form_spec and form_submit are always entries;
// route only in a script declaring Site routing.
func TestIsEntry(t *testing.T) {
	plain := parsed(t, "def route(x):\n    return x\n")
	declared := parsed(t, "INTEGRATION = \"site_routing\"\n\ndef route(req):\n    return {}\n")
	for _, name := range []string{EntryPointName, HandlerName, FormSpecName, FormSubmitName} {
		assert.True(t, IsEntry(plain, name), name)
	}
	assert.False(t, IsEntry(plain, RouteName), "route in a script declaring nothing is a helper")
	assert.True(t, IsEntry(declared, RouteName))
	assert.False(t, IsEntry(declared, "helper"))
	assert.False(t, IsEntry(parsed(t, "INTEGRATION = \"dns\"\n"), RouteName), "an integration the platform does not have makes no entry")
}
