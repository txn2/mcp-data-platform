package scriptdefs

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/syntax"
)

func parse(t *testing.T, src string) *syntax.File {
	t.Helper()
	f, err := (&syntax.FileOptions{}).Parse("s.star", src, 0)
	require.NoError(t, err)
	return f
}

// TestResolve is #2848's lookup: the innermost enclosing function first, then
// each one out, then the module; a nested def is not visible outside the
// function it is written in, and two nested defs of one name are two keys.
func TestResolve(t *testing.T) {
	tbl := Collect(parse(t, `
def helper():
    return 1

def outer():
    def helper():
        return 2
    def middle():
        def inner():
            return helper()
        return inner()
    return middle()

def other():
    if True:
        def helper():
            return 3
    for x in []:
        def looped():
            return x
    return helper()
`).Stmts)
	cases := []struct{ scope, name, want string }{
		{"", "helper", "helper"},
		{"outer", "helper", "outer.helper"},
		{"outer.middle.inner", "helper", "outer.helper"},
		{"outer.middle", "inner", "outer.middle.inner"},
		{"other", "helper", "other.helper"},
		{"other", "looped", "other.looped"},
		{"other", "middle", ""},
		{"", "inner", ""},
	}
	for _, tc := range cases {
		key, d := tbl.Resolve(tc.scope, tc.name)
		assert.Equal(t, tc.want, key, "%s in %q", tc.name, tc.scope)
		assert.Equal(t, tc.want != "", d != nil, "%s in %q", tc.name, tc.scope)
		if d != nil {
			assert.Equal(t, key, tbl.KeyOf[d])
		}
	}
	assert.Equal(t, "outer", tbl.ParentOf["outer.middle"])
	assert.Equal(t, "outer.middle", tbl.ParentOf["outer.middle.inner"])
	assert.Empty(t, tbl.ParentOf["outer"])
}

// TestEntryCalls is the calls the platform makes: main(), handle(req), or a
// form's form_spec(req) then form_submit(req, values); none otherwise.
func TestEntryCalls(t *testing.T) {
	names := func(src string) []string {
		calls := EntryCalls(parse(t, src))
		out := make([]string, 0, len(calls))
		for _, c := range calls {
			id, ok := c.Fn.(*syntax.Ident)
			require.True(t, ok, "an entry call names its function")
			out = append(out, id.Name)
		}
		return out
	}
	assert.Equal(t, []string{"main"}, names("def main():\n    pass\n"))
	assert.Equal(t, []string{"handle"}, names("def handle(req):\n    return {}\n"))
	assert.Equal(t, []string{"form_spec", "form_submit"},
		names("def form_submit(req, values):\n    pass\n\ndef form_spec(req):\n    return {}\n"))
	assert.Empty(t, names("def form_spec(req):\n    return {}\n"))
	assert.Empty(t, names("x = 1\n"))
	calls := EntryCalls(parse(t, "def form_spec(req):\n    pass\ndef form_submit(req, values):\n    pass\n"))
	require.Len(t, calls, 2)
	assert.Len(t, calls[1].Args, 2)
}
