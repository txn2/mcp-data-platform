package stmtlookup

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/syntax"
)

// names is a Resolver over a module's top-level assignments, with the locals
// the analyzer knows the value of.
type names struct {
	bound map[string]syntax.Expr
	known map[string]string
}

func (n names) Bound(id *syntax.Ident) syntax.Expr { return n.bound[id.Name] }

func (n names) Known(id *syntax.Ident) (string, bool) {
	v, ok := n.known[id.Name]
	return v, ok
}

// module parses src's top-level assignments into a Resolver.
func module(t *testing.T, src string) names {
	t.Helper()
	f, err := (&syntax.FileOptions{Set: true}).Parse("m", src, 0)
	require.NoError(t, err)
	n := names{bound: map[string]syntax.Expr{}, known: map[string]string{}}
	for _, s := range f.Stmts {
		if a, ok := s.(*syntax.AssignStmt); ok {
			if id, ok := a.LHS.(*syntax.Ident); ok {
				n.bound[id.Name] = a.RHS
			}
		}
	}
	return n
}

func expr(t *testing.T, src string) syntax.Expr {
	t.Helper()
	e, err := (&syntax.FileOptions{Set: true}).ParseExpr("e", src, 0)
	require.NoError(t, err)
	return e
}

// raw renders what each candidate is, as written.
func raw(es []syntax.Expr) []string {
	out := make([]string, len(es))
	for i, e := range es {
		switch x := e.(type) {
		case *syntax.Ident:
			out[i] = x.Name
		case *syntax.Literal:
			out[i] = x.Raw
		default:
			out[i] = fmt.Sprintf("%T", e)
		}
	}
	return out
}

const mod = `
A = "INSERT INTO s.a VALUES (1)"
B = "INSERT INTO s.b VALUES (1)"
PAIRS = {"x": (A, B), "y": (B, A)}
SPEC = PAIRS[kind]
LIST = [A, B]
`

// TestCandidates is #2623's lookup: a constant key picks one value, a
// computed key every value, through names, nested lookups, tuples, negative
// indexes and a known local; anything else is not a lookup or says nothing.
func TestCandidates(t *testing.T) {
	r := module(t, mod)
	r.known["k"] = "y"
	for src, want := range map[string][]string{
		`PAIRS["x"][-1]`:          {"B"},
		`PAIRS[run.params][0]`:    {"A", "B"},
		`PAIRS[k][0]`:             {"B"},
		`SPEC[1]`:                 {"B", "A"},
		`LIST[0]`:                 {"A"},
		`(LIST)[1]`:               {"B"},
		`{"a": "x", "b": "y"}[z]`: {`"x"`, `"y"`},
		`A`:                       {"A"},
		`"plain"`:                 {`"plain"`},
	} {
		got, ok := Candidates(r, expr(t, src))
		require.True(t, ok, src)
		assert.Equal(t, want, raw(got), src)
	}
	for _, src := range []string{`PAIRS["z"][0]`, `LIST[5]`, `LIST["a"]`, `f()[0]`, `NOPE[0]`, `PAIRS[0]`, `{1: "x"}["a"]`} {
		_, ok := Candidates(r, expr(t, src))
		assert.False(t, ok, src)
	}
}

func TestCandidates_Bounds(t *testing.T) {
	many := "BIG = [" + strings.Repeat(`"x", `, maxCandidates+1) + "]\n"
	_, ok := Candidates(module(t, many), expr(t, "BIG[i]"))
	assert.False(t, ok, "more than maxCandidates")

	loop := names{bound: map[string]syntax.Expr{}, known: map[string]string{}}
	loop.bound["a"] = expr(t, "a[0]")
	_, ok = Candidates(loop, expr(t, "a[0]"))
	assert.False(t, ok, "a name bound to a lookup of itself ends at the depth bound")
}

// TestStatements is what a statements argument holds: a list's elements, a
// list comprehension's element, a name bound to either in the function or in
// the scope, and nothing for what is built at run time.
func TestStatements(t *testing.T) {
	r := module(t, mod+"COMP = [{\"sql\": A} for row in rows]\nSET = {A: 1 for row in rows}\n")
	body := map[string][]syntax.Expr{"local": {expr(t, `[B]`), expr(t, `[A for r in rows]`)}}
	for src, want := range map[string][]string{
		`[A, "UPDATE s.c SET n = 1"]`: {"A", `"UPDATE s.c SET n = 1"`},
		`[A for r in rows]`:           {"A"},
		`(LIST)`:                      {"A", "B"},
		`COMP`:                        {"*syntax.DictExpr"},
		`local`:                       {"B", "A"},
	} {
		got, ok := Statements(r, expr(t, src), body)
		require.True(t, ok, src)
		assert.Equal(t, want, raw(got), src)
	}
	for _, src := range []string{`SET`, `{A: 1 for r in rows}`, `run.params["stmts"]`, `NOPE`} {
		_, ok := Statements(r, expr(t, src), body)
		assert.False(t, ok, src)
	}
	broken := map[string][]syntax.Expr{"bad": {expr(t, `f()`)}}
	_, ok := Statements(r, expr(t, "bad"), broken)
	assert.False(t, ok, "a name bound to something built at run time")
}

func TestListsBound(t *testing.T) {
	f, err := (&syntax.FileOptions{Set: true}).Parse("m", "def w(tx):\n    a = [1]\n    b = [x for x in y]\n    c = {x: 1 for x in y}\n    d = f()\n    e, g = [1], [2]\n", 0)
	require.NoError(t, err)
	def, ok := f.Stmts[0].(*syntax.DefStmt)
	require.True(t, ok)
	got := ListsBound(def.Body)
	assert.Len(t, got["a"], 1)
	assert.Len(t, got["b"], 1)
	assert.NotContains(t, got, "c", "a dict comprehension is not a list")
	assert.NotContains(t, got, "d")
	assert.NotContains(t, got, "e", "an unpacking assignment binds no list by name")
}
