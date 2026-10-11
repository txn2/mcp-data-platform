package exportlibrary

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/syntax"
)

// A library reads the way a refusal names it, and the run's own library is
// stated one way.
func TestLibrary(t *testing.T) {
	assert.Equal(t, "the library of the person the run acts for", Library{}.Label())
	assert.Equal(t, "the library of the person the run acts for", Library{Scope: "user"}.Label())
	assert.Equal(t, "the global library", Library{Scope: "global"}.Label())
	assert.Equal(t, "the finance persona library", Library{Scope: "persona", ScopeID: "finance"}.Label())
	assert.Equal(t, "the library of bob@example.com", Library{Scope: "user", ScopeID: "bob@example.com"}.Label())
	assert.Equal(t, Library{}, Library{Scope: "user"}.Normalized())
	assert.Equal(t, Library{Scope: "global"}, Library{Scope: "global"}.Normalized())

	scope, id := Library{}.RecordScope()
	assert.Equal(t, "user", scope)
	assert.Empty(t, id)
	scope, id = Library{Scope: "persona", ScopeID: "finance"}.RecordScope()
	assert.Equal(t, "persona", scope)
	assert.Equal(t, "finance", id)
}

// Check holds the pair to manage_resource's reading of it; CheckFor refuses
// a named library where the destination files in none.
func TestCheck(t *testing.T) {
	for _, ok := range [][2]string{{"", ""}, {"user", ""}, {"user", "b@example.com"}, {"persona", "finance"}, {"global", ""}} {
		require.NoError(t, Check(ok[0], ok[1]), ok)
	}
	for bad, want := range map[[2]string]string{
		{"", "x"}:       "only together",
		{"persona", ""}: "needs scope_id",
		{"global", "x"}: "takes no scope_id",
		{"team", ""}:    "is not one of",
	} {
		err := Check(bad[0], bad[1])
		require.Error(t, err, bad)
		assert.Contains(t, err.Error(), want, bad)
	}
	require.NoError(t, CheckFor(false, "", ""))
	require.ErrorIs(t, CheckFor(false, "global", ""), ErrNotLibrary)
	require.NoError(t, CheckFor(true, "global", ""))
	require.Error(t, CheckFor(true, "team", ""))
}

// parseCall parses one platform.export call.
func parseCall(t *testing.T, src string) *syntax.CallExpr {
	t.Helper()
	expr, err := (&syntax.FileOptions{}).ParseExpr("x.star", src, 0)
	require.NoError(t, err)
	call, ok := expr.(*syntax.CallExpr)
	require.True(t, ok)
	return call
}

// literal reads a string literal, as a module constant table would.
func literal(e syntax.Expr) (string, bool) {
	lit, ok := e.(*syntax.Literal)
	if !ok {
		return "", false
	}
	s, ok := lit.Value.(string)
	return s, ok
}

// FromCall states the library one export files in, or why it cannot.
func TestFromCall(t *testing.T) {
	cases := map[string]struct {
		src     string
		lib     *Library
		finding string
	}{
		"own":                {`export(destination="resources", key="a/b.csv")`, &Library{}, ""},
		"own named":          {`export(destination="resources", key="a/b.csv", scope="user")`, &Library{}, ""},
		"global":             {`export(destination="resources", key="a/b.csv", scope=" global ")`, &Library{Scope: "global"}, ""},
		"persona":            {`export(destination="resources", key="a/b.csv", scope="persona", scope_id="finance")`, &Library{Scope: "persona", ScopeID: "finance"}, ""},
		"portal":             {`export(rows=[])`, nil, ""},
		"computed dest":      {`export(destination=where, scope="global")`, nil, ""},
		"computed scope":     {`export(destination="resources", scope=pick())`, nil, "written out as strings"},
		"computed scope_id":  {`export(destination="resources", scope="persona", scope_id=who)`, nil, "written out as strings"},
		"scope on portal":    {`export(rows=[], scope="global")`, nil, `only the "resources" destination`},
		"no library named":   {`export(destination="resources", scope="persona")`, nil, "needs scope_id"},
		"positional ignored": {`export("a", [], 1, scope="global", destination="resources")`, &Library{Scope: "global"}, ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			lib, finding := FromCall(parseCall(t, tc.src), literal, "resources")
			assert.Equal(t, tc.lib, lib)
			if tc.finding == "" {
				assert.Nil(t, finding)
				return
			}
			require.NotNil(t, finding)
			assert.Contains(t, finding.Message, tc.finding)
		})
	}
}

// Sorted is stable and never nil.
func TestSorted(t *testing.T) {
	assert.Equal(t, []Library{}, Sorted(nil))
	assert.Equal(t, []Library{{}, {Scope: "global"}, {Scope: "persona", ScopeID: "a"}, {Scope: "persona", ScopeID: "b"}},
		Sorted(map[Library]bool{{Scope: "persona", ScopeID: "b"}: true, {}: true, {Scope: "global"}: true, {Scope: "persona", ScopeID: "a"}: true}))
}
