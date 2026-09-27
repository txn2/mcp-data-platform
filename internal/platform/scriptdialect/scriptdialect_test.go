package scriptdialect

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/resolve"
	"go.starlark.net/syntax"
)

func predeclared(name string) bool { return name == "platform" }

func TestParseResolvesAndKeepsComments(t *testing.T) {
	file, err := Parse("# the query\nX = 1\nfor i in [1]:\n    platform.query(\"SELECT 1\")\n", predeclared)
	require.NoError(t, err)
	as, ok := file.Stmts[0].(*syntax.AssignStmt)
	require.True(t, ok)
	require.NotNil(t, as.Comments())
	assert.Equal(t, "# the query", as.Comments().Before[0].Text)
	id, ok := as.LHS.(*syntax.Ident)
	require.True(t, ok)
	b, ok := id.Binding.(*resolve.Binding)
	require.True(t, ok, "the resolver set the binding")
	assert.Equal(t, resolve.Global, b.Scope, "top-level control is on, so X is a module name")
}

func TestParseRefusesWhatTheDialectRefuses(t *testing.T) {
	for name, src := range map[string]string{
		"a syntax error":    "def f(:\n",
		"a while loop":      "def f():\n    while True:\n        pass\n",
		"an undefined name": "nope()\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(src, predeclared)
			assert.Error(t, err)
		})
	}
}
