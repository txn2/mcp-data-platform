package scriptalloc

import (
	"errors"
	"strings"
	"testing"

	"go.starlark.net/starlark"
	"go.starlark.net/syntax"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptguard"
)

var fileOptions = &syntax.FileOptions{TopLevelControl: true, GlobalReassign: true, While: true, Set: true}

// exec runs src rewritten (or not) with a meter of budget attached (none when
// budget is negative), returning its globals, the steps it took and its error.
func exec(t *testing.T, src string, budget int64, rewrite bool) (starlark.StringDict, uint64, error) {
	t.Helper()
	file, err := fileOptions.Parse("test.star", src, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	env := starlark.StringDict{}
	if rewrite {
		Rewrite(file)
		env = Bind(env)
	}
	prog, err := starlark.FileProgram(file, env.Has)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	thread := &starlark.Thread{Name: "test"}
	if budget >= 0 {
		scriptguard.Attach(thread, scriptguard.NewMeter(budget))
	}
	globals, err := prog.Init(thread, env)
	return globals, thread.ExecutionSteps(), err
}

func TestRewrite_KeepsTheLanguage(t *testing.T) {
	cases := map[string]string{
		`r = 2 * 3`:                                "6",
		`r = "ab" * 3`:                             `"ababab"`,
		`r = 3 * [1]`:                              "[1, 1, 1]",
		`r = (1, 2) * 2`:                           "(1, 2, 1, 2)",
		`r = 1 + 2`:                                "3",
		`r = "a" + "b"`:                            `"ab"`,
		`r = [1] + [2]`:                            "[1, 2]",
		"x = [1]\ny = x\nx += [2]\nr = y":          "[1, 2]",
		"x = [1]\nx += x\nr = x":                   "[1, 1]",
		"x = [1]\nx += (2, 3)\nr = x":              "[1, 2, 3]",
		"s = \"a\"\ns += \"b\"\nr = s":             `"ab"`,
		"n = 1\nn += 2\nr = n":                     "3",
		"m = 2\nm *= 3\nr = m":                     "6",
		"d = {\"k\": [1]}\nd[\"k\"] += [2]\nr = d": `{"k": [1, 2]}`,
		"l = [1, 2]\nl[-1] += 5\nr = l":            "[1, 7]",
		"l = [1, 2]\nl[0] *= 3\nr = l":             "[3, 2]",
		`r = "a,b".replace(",", ";")`:              `"a;b"`,
		`r = "a,a,a".replace("a", "bb", 2)`:        `"bb,bb,a"`,
		`r = ",".join(["a", "b"])`:                 `"a,b"`,
		"l = [1]\nl.extend([2])\nr = l":            "[1, 2]",
		"r = len(range(5))":                        "5",
		"def f():\n    t = 0\n    for i in range(4):\n        t += i\n    return t\nr = f()":                                   "6",
		"n = 0\ns = \"\"\nwhile n < 3:\n    n += 1\n    if n == 2:\n        s += \"b\"\n    else:\n        s *= 1\nr = (n, s)": `(3, "b")`,
		`r = list(enumerate(range(2), 1))`: "[(1, 0), (2, 1)]",
	}
	for src, want := range cases {
		globals, _, err := exec(t, src, -1, true)
		if err != nil {
			t.Errorf("%q: %v", src, err)
			continue
		}
		if got := globals["r"].String(); got != want {
			t.Errorf("%q: r = %s, want %s", src, got, want)
		}
	}
}

func TestRewrite_EvaluatesAnIndexTargetOnce(t *testing.T) {
	globals, _, err := exec(t, `calls = []
def k():
    calls.append(1)
    return "k"
d = {"k": 1}
d[k()] += 1
`, -1, true)
	if err != nil {
		t.Fatal(err)
	}
	calls, ok := globals["calls"].(*starlark.List)
	if !ok {
		t.Fatalf("calls = %v", globals["calls"])
	}
	if got := calls.Len(); got != 1 {
		t.Errorf("the index was evaluated %d times", got)
	}
	if got := globals["d"].String(); got != `{"k": 2}` {
		t.Errorf("d = %s", got)
	}
}

func TestRewrite_KeepsTheInterpretersErrors(t *testing.T) {
	cases := map[string]string{
		`r = 1 + "a"`:           "unknown binary op: int + string",
		`r = "a" * "b"`:         "unknown binary op: string * string",
		"d = {}\nd[\"x\"] += 1": `key "x" not in dict`,
		"l = []\nl[3] += 1":     "out of range",
		"t = (1,)\nt[0] += 1":   "does not support item assignment",
		"x = 1\nx.extend([1])":  "no .extend field or method",
		"x = (1).splitlines()":  "no .splitlines field or method",
		"l = [1]\nl.freeze = 1": "",
	}
	for src, want := range cases {
		if want == "" {
			continue
		}
		_, _, err := exec(t, src, -1, true)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: error %v, want it to say %q", src, err, want)
		}
	}
}

// TestRewrite_CostsTheStepsTheAuthorWrote: a run's step budget is measured on
// the code as written; the rewrite gives back the step each routed operator
// adds.
func TestRewrite_CostsTheStepsTheAuthorWrote(t *testing.T) {
	src := "t = 0\ns = \"\"\nfor i in range(200):\n    t = t + i * 2\n    s += \"x\"\n"
	_, native, err := exec(t, src, -1, false)
	if err != nil {
		t.Fatal(err)
	}
	_, rewritten, err := exec(t, src, -1, true)
	if err != nil {
		t.Fatal(err)
	}
	if rewritten != native {
		t.Errorf("rewritten code took %d steps, the code as written %d", rewritten, native)
	}
}

// TestGuards_RefuseAValueThatWouldPassTheBudget: every operation that can
// build a large value in one step is refused, with a memory failure, before
// it builds one past the run's budget.
func TestGuards_RefuseAValueThatWouldPassTheBudget(t *testing.T) {
	const budget = 1 << 20
	cases := map[string]string{
		"string repeat":       `x = "x" * (2 << 20)`,
		"list repeat":         `x = [0] * (1 << 20)`,
		"repeat by a big int": `x = "x" * (1 << 70)`,
		"doubling +":          "s = \"x\" * 1024\nfor i in range(30):\n    s = s + s",
		"doubling +=":         "s = \"x\" * 1024\nfor i in range(30):\n    s += s",
		"list +=":             "l = [0] * 1024\nfor i in range(30):\n    l += l",
		"list extend":         "l = [0] * 1024\nfor i in range(30):\n    l.extend(l)",
		"index +=":            "d = {\"k\": \"x\" * 1024}\nfor i in range(30):\n    d[\"k\"] += d[\"k\"]",
		"index *=":            "d = {\"k\": \"x\" * 1024}\nd[\"k\"] *= 4096",
		"replace":             `x = ("a" * 1000).replace("a", "b" * 10000)`,
		"replace empty":       `x = ("a" * 1000).replace("", "b" * 10000)`,
		"join":                `x = ("x" * 100000).join(["a"] * 100)`,
		"list of a range":     `x = list(range(1 << 20))`,
		"sorted range":        `x = sorted(range(1 << 20))`,
		"enumerate range":     `x = enumerate(range(1 << 20))`,
		"zip ranges":          `x = zip(range(1 << 20), range(1 << 21))`,
		"set of a range":      `x = set(range(1 << 20))`,
		"split into pieces":   `x = ("a" * 100000).split("a")`,
		"split with None":     `x = ("a " * 50000).split(None)`,
		"rsplit":              `x = ("a," * 50000).rsplit(",")`,
		"splitlines":          `x = ("\n" * 100000).splitlines()`,
		"split on whitespace": `x = ("a " * 50000).split()`,
	}
	for name, src := range cases {
		_, _, err := exec(t, src, budget, true)
		if err == nil {
			t.Errorf("%s: ran", name)
			continue
		}
		if !errors.Is(err, scriptguard.ErrMemoryBudget) && !strings.Contains(err.Error(), "memory budget") {
			t.Errorf("%s: %v, want a memory refusal", name, err)
		}
	}
}

func TestGuards_LeaveWhatFits(t *testing.T) {
	for _, src := range []string{
		`x = "x" * 1000`,
		`x = [0] * 1000`,
		`x = range(1 << 30)`,
		"for i in range(200000):\n    pass",
		`x = list(range(1000))`,
		`x = zip(range(10), range(1 << 30))`,
		`x = ("a" * 10).replace("a", "bb")`,
		`x = ",".join(["a"] * 10)`,
		`x = ("x" * 500000 + "\n" + "y" * 400000).split("\n")`,
		`x = ("a," * 50000).split(",", 3)`,
		`x = ("a," * 50000).rsplit(",", 2)`,
		`x = "a b c".split()`,
		`x = "a\nb".splitlines()`,
		`x = "x" * (2 << 20)` + "\n# no meter",
	} {
		budget := int64(1 << 20)
		if strings.Contains(src, "no meter") {
			budget = -1
		}
		if _, _, err := exec(t, src, budget, true); err != nil {
			t.Errorf("%q: %v", src, err)
		}
	}
	if _, _, err := exec(t, `x = "x" * (2 << 20)`, 0, true); err != nil {
		t.Errorf("a run with no budget was refused: %v", err)
	}
}

func TestBind_LeavesABuiltinTheEnvironmentDefines(t *testing.T) {
	own := starlark.NewBuiltin("list", func(*starlark.Thread, *starlark.Builtin, starlark.Tuple, []starlark.Tuple) (starlark.Value, error) {
		return starlark.None, nil
	})
	env := Bind(starlark.StringDict{"list": own})
	if env["list"] != own {
		t.Error("Bind replaced an environment's own list")
	}
	if _, bound := env["tuple"]; !bound {
		t.Error("Bind left tuple unguarded")
	}
}

func TestProduct(t *testing.T) {
	if product(-1, 5) != 0 || product(5, 0) != 0 {
		t.Error("a non-positive factor is not a product of zero")
	}
	if product(1<<62, 4) <= 0 {
		t.Error("product overflowed")
	}
}
