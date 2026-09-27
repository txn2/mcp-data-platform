package scriptconst

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/resolve"
	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

// parse parses and resolves src under the managed-script dialect's switches.
func parse(t *testing.T, src string) *syntax.File {
	t.Helper()
	opts := &syntax.FileOptions{Set: true, TopLevelControl: true, GlobalReassign: true}
	f, err := opts.Parse("s", src, 0)
	require.NoError(t, err)
	isUniversal := func(n string) bool { _, ok := starlark.Universe[n]; return ok }
	isPredeclared := func(n string) bool { return n == "run" || n == "platform" }
	require.NoError(t, resolve.File(f, isPredeclared, isUniversal))
	return f
}

// firstArg is the first argument of the call an expression statement makes.
func firstArg(t *testing.T, s syntax.Stmt) syntax.Expr {
	t.Helper()
	es, ok := s.(*syntax.ExprStmt)
	require.True(t, ok, "not an expression statement: %T", s)
	call, ok := es.X.(*syntax.CallExpr)
	require.True(t, ok, "not a call: %T", es.X)
	return call.Args[0]
}

// lastCallArg is the first argument of the file's last expression statement.
func lastCallArg(t *testing.T, f *syntax.File) syntax.Expr {
	t.Helper()
	return firstArg(t, f.Stmts[len(f.Stmts)-1])
}

func TestCollect(t *testing.T) {
	f := parse(t, `
A = "warehouse"
B = A + "-rw"
C = "{}.{}".format("hive", "sales")
D = "%s_%d" % ("t", 3)
E = "/".join(["a", B])
N = 10
R = "x"
R = "y"
P = run.params["x"]
L = ["a"]
for F in []:
    pass
F = "f"
if True:
    G = "g"
def H():
    pass
H = "h"
I, J = "i", "j"
[K] = ["k"]
(M) = "m"
I2 = "i2"
print(A)
`)
	tbl := Collect(f)
	want := map[string]string{"A": "warehouse", "B": "warehouse-rw", "C": "hive.sales", "D": "t_3", "E": "a/warehouse-rw", "N": "10", "I2": "i2"}
	assert.Equal(t, want, tbl.values)
	assert.Equal(t, len(want), tbl.Len())
	assert.Equal(t, Table{values: map[string]string{}}, Collect(nil))
}

func TestValueNeedsAModuleBinding(t *testing.T) {
	f := parse(t, `
A = "warehouse"
def f(A):
    return A
f(A)
`)
	tbl := Collect(f)
	arg, ok := lastCallArg(t, f).(*syntax.Ident)
	require.True(t, ok)
	v, ok := tbl.Value(arg)
	assert.True(t, ok)
	assert.Equal(t, "warehouse", v)

	def, ok := f.Stmts[1].(*syntax.DefStmt)
	require.True(t, ok)
	ret, ok := def.Body[0].(*syntax.ReturnStmt)
	require.True(t, ok)
	param, ok := ret.Result.(*syntax.Ident)
	require.True(t, ok)
	_, ok = tbl.Value(param)
	assert.False(t, ok, "the parameter shadows the constant")

	unresolved := &syntax.Ident{Name: "A"}
	_, ok = tbl.Value(unresolved)
	assert.False(t, ok)
}

func TestString(t *testing.T) {
	f := parse(t, `
A = "a"
x = run.params["x"]
print("lit")
print(A)
print((A))
print(A + "-b")
print("{}-c".format(A))
print("-".join([A, "d"]))
print(3)
print(A + x)
print([A])
print(len(A))
print(A.upper())
`)
	tbl := Collect(f)
	got := make([]string, 0, len(f.Stmts))
	oks := make([]bool, 0, len(f.Stmts))
	for _, s := range f.Stmts[2:] {
		v, ok := tbl.String(firstArg(t, s))
		got = append(got, v)
		oks = append(oks, ok)
	}
	assert.Equal(t, []bool{true, true, true, true, true, true, false, false, false, false, false}, oks)
	assert.Equal(t, []string{"lit", "a", "a", "a-b", "a-c", "a-d"}, got[:6])
}

func TestRender(t *testing.T) {
	f := parse(t, `
x = run.params["x"]
sep = run.params["s"]
y, z = 1, 2
def some_really_long_function_name_here(a, b, c):
    return a
print("SELECT * FROM " + x + " WHERE a = '%s'" % x)
print("{}.{}".format("a"))
print("a" if x else "b")
print(x.replace("a", "b"))
print("tmpl __X__".replace("__X__", x))
print("%s-%s" % x)
print(" ".join(x))
print(sep.join(["a"]))
print(some_really_long_function_name_here(x, y, z))
print(x.upper())
`)
	src := func(e syntax.Expr) string {
		start, end := e.Span()
		return "src" + string(rune('0'+int(end.Col-start.Col)%10))
	}
	cases := []struct {
		text string
		full bool
	}{
		{"SELECT * FROM {x} WHERE a = '{x}'", false},
		{"a.{?}", false},
		{"a", false},
		{"{x}", false},
		{"tmpl __X__", false},
		{"{x}-{?}", false},
		{"{src", false},
		{"a", false},
		{"{src", false},
		{"{src", false},
	}
	r := &Renderer{Source: src}
	prints := f.Stmts[4:]
	for i, s := range prints {
		v, full := r.Render(firstArg(t, s))
		assert.Contains(t, v, cases[i].text, "statement %d", i)
		assert.Equal(t, cases[i].full, full, "statement %d", i)
	}

	// No Source writes the stand-in without text; a long one is shortened.
	bare := &Renderer{}
	v, full := bare.Render(firstArg(t, prints[len(prints)-1]))
	assert.Equal(t, "{…}", v)
	assert.False(t, full)
	long := &Renderer{Source: func(syntax.Expr) string { return "abcdefghijklmnopqrstuvwxyz0123456789" }}
	v, _ = long.Render(firstArg(t, prints[8]))
	assert.Equal(t, "{abcdefghijklmnopqrstuvwxyz…}", v)

	v, full = r.Render(nil)
	assert.Empty(t, v)
	assert.True(t, full)
}

func TestNestedStopsAtItsDepth(t *testing.T) {
	var r *Renderer
	var loop syntax.Expr = &syntax.Ident{Name: "x"}
	r = &Renderer{Name: func(*syntax.Ident) (string, bool) { return r.Nested(loop) }}
	v, full := r.Render(loop)
	assert.Equal(t, "{…}", v)
	assert.False(t, full)
}

// Text is reported as known only where it is exactly what Python would
// produce. Every formatting feature the substitution does not implement makes
// the value computed rather than a wrong name.
func TestString_OnlyExactFormattingIsKnown(t *testing.T) {
	f := parse(t, `
print("%s-%d" % ("a", 3))
print("{}-{}".format("a", "b"))
print("%5s" % "a")
print("%.2f" % 1.5)
print("%r" % "a")
print("100%% %s" % "a")
print("%(x)s" % {"x": "a"})
print("{0}-{0}".format("a"))
print("{:>5}".format("a"))
print("{{}}{}".format("a"))
print("a" + 1)
`)
	tbl := Collect(f)
	want := []struct {
		text string
		ok   bool
	}{
		{"a-3", true},
		{"a-b", true},
		{"a", false},
		{"1.5", false},
		{"a", false},
		{"100%% a", false},
		{"%(x)s", false},
		{"a-{?}", false},
		{"a", false},
		{"", false},
		{"a1", false},
	}
	for i, s := range f.Stmts {
		got, ok := tbl.String(firstArg(t, s))
		assert.Equal(t, want[i].ok, ok, "statement %d: %q", i, got)
		if want[i].ok {
			assert.Equal(t, want[i].text, got, "statement %d", i)
		}
	}
}
