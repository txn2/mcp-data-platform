package stmtlookup

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/syntax"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptlib"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
)

// funcScope is a module's names and the functions it defines.
type funcScope struct {
	names
	defs map[string]*syntax.DefStmt
}

func (f funcScope) Func(name string) (*syntax.DefStmt, Resolver) { return f.defs[name], nil }

// as is v as a T, failing the test when it is not one.
func as[T any](t *testing.T, v any) T {
	t.Helper()
	got, ok := v.(T)
	require.True(t, ok, "%T is not the type wanted", v)
	return got
}

// moduleWithFuncs parses src's top-level assignments and defs.
func moduleWithFuncs(t *testing.T, src string) funcScope {
	t.Helper()
	file, err := (&syntax.FileOptions{Set: true}).Parse("m", src, 0)
	require.NoError(t, err)
	f := funcScope{names{bound: map[string]syntax.Expr{}, known: map[string]string{}}, map[string]*syntax.DefStmt{}}
	for _, s := range file.Stmts {
		switch s := s.(type) {
		case *syntax.AssignStmt:
			if id, ok := s.LHS.(*syntax.Ident); ok {
				f.bound[id.Name] = s.RHS
			}
		case *syntax.DefStmt:
			f.defs[s.Name.Name] = s
		}
	}
	return f
}

const helpers = `
A = "INSERT INTO s.a VALUES (1)"
B = "INSERT INTO s.b VALUES (1)"
SPECS = {"a": {"write": [A]}, "b": {"write": [B]}}

def rows(xs):
    """One statement per x."""
    return [{"sql": B, "params": x} for x in xs]

def grown():
    """A list built in steps."""
    out = [A]
    out.append(B)
    out.extend([A])
    out += [B]
    return out

def event(kind):
    """One history row."""
    return {"sql": A, "params": kind}

def spec_of(kind):
    """A spec from a dict the function builds."""
    specs = {"a": {"write": [A]}, "b": {"write": [B]}}
    return specs[kind]

def nothing():
    """Returns nothing."""
    pass
`

func TestStatements_FollowsHelpersAndConcatenation(t *testing.T) {
	sc := moduleWithFuncs(t, helpers)
	for src, n := range map[string]int{
		`[A] + rows(xs)`:           2,
		`grown()`:                  4,
		`[event("x"), {"sql": A}]`: 2,
		`SPECS["a"]["write"]`:      1,
		`SPECS[k]["write"]`:        2,
		`[{"sql": s} for s in SPECS["a"]["write"]]`: 1,
		`[s for s in [A, B]]`:                       2,
		`[{"sql": s} for _, s in enumerate([A])]`:   1,
		`[r for r in rows(xs)]`:                     1,
		// A helper's arguments are not bound in its body, so its lookup is
		// every value, as a key computed at run time is.
		`[{"sql": s} for s in spec_of("a")["write"]]`: 2,
	} {
		got, ok := Statements(sc, expr(t, src), nil)
		require.True(t, ok, src)
		assert.Len(t, got, n, src)
	}
	for _, src := range []string{`[A] + unknown()`, `unknown() + [A]`, `nothing()`, `[s for s in run.params["x"]]`, `[s for s, t in pairs]`, `x - y`} {
		_, ok := Statements(sc, expr(t, src), nil)
		assert.False(t, ok, src)
	}
	stmts, ok := Statements(sc, expr(t, `[event("x")]`), nil)
	require.True(t, ok)
	_, isDict := stmts[0].(*syntax.DictExpr)
	assert.True(t, isDict, "a helper's statement is what it returns")
}

func TestCandidates_ThroughContainersBuiltInSteps(t *testing.T) {
	sc := moduleWithFuncs(t, helpers)
	got, ok := Candidates(sc, expr(t, `([A] + [B])[i]`))
	require.True(t, ok)
	assert.Len(t, got, 2)
	got, ok = Candidates(sc, expr(t, `spec_of(k)["write"]`))
	require.True(t, ok)
	assert.Len(t, got, 2)
	for _, src := range []string{`(x - y)[0]`, `([A] + {"k": B})[0]`, `([A] + nope)[0]`, `nope()[0]`, `nothing()[0]`} {
		_, ok := Candidates(sc, expr(t, src))
		assert.False(t, ok, src)
	}
}

func TestListsBoundAndGrown(t *testing.T) {
	f, err := (&syntax.FileOptions{Set: true}).Parse("m", "def w(p):\n    a = [1]\n    a.append(2)\n    p.append(3)\n    p.extend(q)\n    c += [4]\n    d = [5]\n    p.pop(0)\n", 0)
	require.NoError(t, err)
	body := as[*syntax.DefStmt](t, f.Stmts[0]).Body
	all := ListsBound(body)
	assert.Len(t, all["a"], 2)
	require.Len(t, all["p"], 3, "a parameter only appended to holds its value first")
	marker, ok := all["p"][0].(*syntax.Ident)
	assert.True(t, ok && marker.Name == "p")
	grown := Grown(body)
	assert.Contains(t, grown, "a")
	assert.Contains(t, grown, "p")
	assert.Contains(t, grown, "c")
	assert.NotContains(t, grown, "d", "assigned once and never grown")
}

func TestLoopElements(t *testing.T) {
	for src, want := range map[string]string{
		`for x in L: pass`:               "x",
		`for x in D.values(): pass`:      "x",
		`for k, v in D.items(): pass`:    "v",
		`for i, v in enumerate(L): pass`: "v",
		`for x in (L): pass`:             "x",
	} {
		f, err := (&syntax.FileOptions{Set: true}).Parse("m", src, 0)
		require.NoError(t, err, src)
		loop := as[*syntax.ForStmt](t, f.Stmts[0])
		got := LoopElements(loop.Vars, loop.X)
		assert.Contains(t, got, want, src)
	}
	for _, src := range []string{`for x in {"a": 1}: pass`, `for x in D.keys(): pass`, `for x in D.items(): pass`, `for x in L.pop(): pass`, `for a, b in L: pass`} {
		f, err := (&syntax.FileOptions{Set: true}).Parse("m", src, 0)
		require.NoError(t, err, src)
		loop := as[*syntax.ForStmt](t, f.Stmts[0])
		assert.Empty(t, LoopElements(loop.Vars, loop.X), src)
	}
}

func TestSubstitute(t *testing.T) {
	sc := names{bound: map[string]syntax.Expr{"local": expr(t, `[A]`)}, known: map[string]string{"k": "a"}}
	got := Substitute(sc, expr(t, `{"x": (local), "y": [k, -k], "z": (f(k, key = local)).attr[k], "w": [v for v in local if v]}`))
	d, ok := got.(*syntax.DictExpr)
	require.True(t, ok)
	require.Len(t, d.List, 4)
	x := as[*syntax.DictEntry](t, d.List[0]).Value
	_, isList := x.(*syntax.ListExpr)
	assert.True(t, isList, "a bound local becomes its expression")
	y := as[*syntax.ListExpr](t, as[*syntax.DictEntry](t, d.List[1]).Value)
	lit, isLit := y.List[0].(*syntax.Literal)
	assert.True(t, isLit && lit.Value == "a", "a known local becomes its value")
	_, isUnary := y.List[1].(*syntax.UnaryExpr)
	assert.True(t, isUnary)
	w := as[*syntax.Comprehension](t, as[*syntax.DictEntry](t, d.List[3]).Value)
	assert.Equal(t, "v", as[*syntax.Ident](t, w.Body).Name, "a comprehension's own name stays")

	unknown := as[*syntax.Ident](t, Substitute(sc, expr(t, `mystery`)))
	assert.NotEqual(t, "mystery", unknown.Name, "an unbound local is opaque")
	assert.Equal(t, unknown, Substitute(sc, unknown), "an opaque name stays opaque")
	_, isOpaque := Substitute(sc, expr(t, `lambda: 1`)).(*syntax.Ident)
	assert.True(t, isOpaque, "an expression it does not read is opaque")
	_, ok = Statements(sc, unknown, nil)
	assert.False(t, ok, "an opaque name holds no statements")

	cyclic := names{bound: map[string]syntax.Expr{"x": expr(t, `x + [1]`)}}
	assert.NotNil(t, Substitute(cyclic, expr(t, `x`)), "a name bound to itself ends")
}

const libSource = `PREFIX = "x3ap"
ADD = "INSERT INTO " + PREFIX + ".event (k) VALUES (:k)"
VIEWS = {"a": ADD}

def rows(ks):
    """One row per k."""
    return [{"sql": ADD, "params": k} for k in ks]
`

func TestLoadLibraries(t *testing.T) {
	file, err := scriptrun.Parse("load(\"lib:ap@2\", \"ADD\", \"VIEWS\", \"rows\", \"MISSING\")\nload(\"lib:other@1\", \"X\")\nload(\"res:ignored\", \"Y\")\n\ndef main():\n    \"\"\"Runs.\"\"\"\n    pass\n")
	require.NoError(t, err)
	assert.Empty(t, LoadLibraries(file, nil))
	loaded := LoadLibraries(file, Libraries{scriptlib.Ref{Name: "ap", Version: 2}: libSource, scriptlib.Ref{Name: "other", Version: 1}: "def ("})
	v, ok := loaded["ADD"].Constant()
	assert.True(t, ok)
	assert.Equal(t, "INSERT INTO x3ap.event (k) VALUES (:k)", v)
	_, isDict := loaded["VIEWS"].Expr().(*syntax.DictExpr)
	assert.True(t, isDict)
	assert.Nil(t, loaded["MISSING"].Expr())
	def, outer := loaded["rows"].Def()
	require.NotNil(t, def)
	require.NotNil(t, outer)
	d, _ := loaded["ADD"].Def()
	assert.Nil(t, d, "a constant is no function")
	assert.NotContains(t, loaded, "X", "a library whose source does not parse binds nothing")
	var zero Loaded
	_, ok = zero.Constant()
	assert.False(t, ok)
	assert.Nil(t, zero.Expr())
	d, _ = zero.Def()
	assert.Nil(t, d)

	// A library's function, read in its scope, returns statements whose
	// constants are the library's.
	stmts, ok := returned(libCaller{outer: outer, def: def}, &syntax.CallExpr{Fn: &syntax.Ident{Name: "rows"}}, 0)
	require.True(t, ok)
	require.Len(t, stmts, 1)
	ls := as[libScope](t, outer)
	_, _, ok = ls.Global(&syntax.Ident{Name: "nope"})
	assert.False(t, ok)
	_, x, ok := ls.Global(&syntax.Ident{Name: "VIEWS"})
	assert.True(t, ok && x != nil)
	k, ok := ls.Known(&syntax.Ident{Name: "PREFIX"})
	assert.True(t, ok && k == "x3ap")
	assert.NotNil(t, ls.Bound(&syntax.Ident{Name: "VIEWS"}))
	f, _ := ls.Func("nope")
	assert.Nil(t, f)
	assert.Equal(t, map[string]string{"lib:ap@2": libSource}, Libraries{scriptlib.Ref{Name: "ap", Version: 2}: libSource}.Named())
}

// libCaller is a script scope whose one function is a library's.
type libCaller struct {
	names
	outer Resolver
	def   *syntax.DefStmt
}

func (l libCaller) Func(string) (*syntax.DefStmt, Resolver) { return l.def, l.outer }

func TestBodyScope(t *testing.T) {
	outer := moduleWithFuncs(t, helpers)
	b := bodyScope{outer: outer, assigned: map[string]syntax.Expr{"x": expr(t, `[A]`)}}
	assert.NotNil(t, b.Bound(&syntax.Ident{Name: "x"}))
	assert.NotNil(t, b.Bound(&syntax.Ident{Name: "A"}), "a name the body does not assign is the caller's module name")
	_, ok := b.Known(&syntax.Ident{Name: "x"})
	assert.False(t, ok)
	d, _ := b.Func("rows")
	assert.NotNil(t, d)
	_, _, ok = b.Global(&syntax.Ident{Name: "A"})
	assert.False(t, ok, "the script's own scope is not foreign")
	d, _ = bodyScope{outer: names{}}.Func("rows")
	assert.Nil(t, d)
}
