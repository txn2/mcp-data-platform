package scriptre

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

// eval runs one expression with the module bound to re and page bound to a
// small HTML document.
func eval(t *testing.T, expr string) (starlark.Value, error) {
	t.Helper()
	thread := &starlark.Thread{Name: "test"}
	thread.SetMaxExecutionSteps(10000)
	env := starlark.StringDict{
		Name:   Module,
		"page": starlark.String(`<html><head><TITLE lang="en">Quarterly Report</TITLE><meta property="og:type" content="article"></head></html>`),
	}
	v, err := starlark.EvalOptions(&syntax.FileOptions{Set: true}, thread, "test.star", expr, env)
	if err != nil {
		return nil, fmt.Errorf("eval: %w", err)
	}
	return v, nil
}

func TestBehavesAsPythonRe(t *testing.T) {
	cases := []struct{ expr, want string }{
		// The ticket's own example.
		{`re.search("(?i)<title[^>]*>([^<]*)</title>", page).group(1)`, `"Quarterly Report"`},
		{`re.search("title", page, flags = re.I).span()`, `(13, 18)`},
		{`re.search("nope", page)`, `None`},
		{`re.match("b", "ab")`, `None`},
		{`re.match("a", "ab").group()`, `"a"`},
		{`re.fullmatch("a|ab", "ab").group()`, `"ab"`},
		{`re.fullmatch("a", "ab")`, `None`},
		{`re.findall("[0-9]+", "a1b22c333")`, `["1", "22", "333"]`},
		{`re.findall("([a-z])([0-9])?", "a1b")`, `[("a", "1"), ("b", "")]`},
		{`re.findall("([a-z])[0-9]", "a1b2")`, `["a", "b"]`},
		{`[m.start() for m in re.finditer("o", "foo")]`, `[1, 2]`},
		{`re.sub("([a-z]+)@([a-z]+)", r"\2 at \1", "joe@acme")`, `"acme at joe"`},
		{`re.sub("(?P<w>x)", r"<\g<w>\g<1>>\n", "x")`, `"<xx>\n"`},
		{`re.sub("a", "b", "aaa", count = 2)`, `"bba"`},
		{`re.sub("x*", "-", "abc")`, `"-a-b-c-"`},
		{`re.sub("[0-9]", lambda m: str(int(m.group()) * 2), "a1b4")`, `"a2b8"`},
		{`re.sub("(a)|b", r"[\1]", "ab")`, `"[a][]"`},
		{`re.split(",\\s*", "a, b,c")`, `["a", "b", "c"]`},
		{`re.split("(,)", "a,b", maxsplit = 1)`, `["a", ",", "b"]`},
		{`re.split("(x)|,", "a,b")`, `["a", None, "b"]`},
		{`re.escape("a.b*c")`, `"a\\.b\\*c"`},
		{`re.compile("(?P<k>[a-z]+)=(?P<v>[0-9]+)").search("x=1").groupdict()`, `{"k": "x", "v": "1"}`},
		{`re.compile("(a)(b)?").match("a").groups()`, `("a", None)`},
		{`re.compile("(a)(b)?").match("a").groups(default = "")`, `("a", "")`},
		{`re.compile("(a)(b)?").match("a").group(1, 2)`, `("a", None)`},
		{`re.compile("(a)(b)?").match("a").end(2)`, `-1`},
		{`re.compile("(?P<k>a)").match("a").span("k")`, `(0, 1)`},
		{`re.compile("(?P<k>a)").match("a").string`, `"a"`},
		{`re.compile("a(b)").groups`, `1`},
		{`re.compile("ab", re.I).flags`, `2`},
		{`re.compile("ab", re.I).search("xAB").group()`, `"AB"`},
		{`re.compile("ab", re.IGNORECASE | re.MULTILINE | re.DOTALL).pattern`, `"ab"`},
		{`re.compile("^b", re.M).findall("a\nb")`, `["b"]`},
		{`re.compile("a.b", re.S).fullmatch("a\nb") != None`, `True`},
		{`re.compile("a").sub("b", "aa")`, `"bb"`},
		{`re.compile("a").split("bab")`, `["b", "b"]`},
		{`str(re.compile("a"))`, `"re.compile(\"a\")"`},
		{`str(re.compile("a", re.I))`, `"re.compile(\"a\", 2)"`},
		{`str(re.search("b", "abc"))`, `"<re.Match object; span=(1, 2), match=\"b\">"`},
		{`type(re.compile("a")) + " " + type(re.search("a", "a"))`, `"re.Pattern re.Match"`},
		{`{re.compile("a"): 1}[re.compile("a")]`, `1`},
		{`re.compile("a") != re.compile("a", re.I)`, `True`},
		{`bool(re.search("", "x"))`, `True`},
		{`len(dir(re.compile("a"))) + len(dir(re.search("a", "a")))`, `17`},
		{`re.compile("a").nothing`, ``},
	}
	for _, c := range cases {
		got, err := eval(t, c.expr)
		if c.want == "" {
			if err == nil || !strings.Contains(err.Error(), "no .nothing field") {
				t.Errorf("%s: err = %v", c.expr, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.expr, err)
			continue
		}
		if got.String() != c.want {
			t.Errorf("%s = %s, want %s", c.expr, got, c.want)
		}
	}
}

func TestRefusals(t *testing.T) {
	cases := map[string]string{
		`re.search("(?=a)", "a")`:                  "no backreferences",
		`re.search("(", "a")`:                      `invalid pattern "("`,
		`re.search()`:                              "missing argument for pattern",
		`re.search(1, "a")`:                        "pattern must be a string, not int",
		`re.search("a")`:                           "missing argument for s",
		`re.search("a", "a", flags = "x")`:         "flags must be an int",
		`re.search("a", "a", flags = 1)`:           "flags=1 names a flag re does not support",
		`re.compile()`:                             "in re.compile",
		`re.compile("(")`:                          "in re.compile: invalid pattern",
		`re.escape()`:                              "in re.escape",
		`re.split("a")`:                            "missing argument for s",
		`re.sub("a")`:                              "missing argument for repl",
		`re.sub("a", 1, "a")`:                      "repl must be a string or a function, not int",
		`re.sub("a", r"\2", "a")`:                  "invalid group reference 2",
		`re.sub("a", r"\g<9>", "a")`:               "invalid group reference 9",
		`re.sub("a", r"\g<x>", "a")`:               `unknown group name "x"`,
		`re.sub("a", r"\g1", "a")`:                 "bad group reference",
		`re.sub("a", r"\d", "a")`:                  `bad escape \d`,
		`re.sub("a", "\\", "a")`:                   "bad escape (end of template)",
		`re.sub("a", lambda m: 1, "a")`:            "returned int, not a string",
		`re.sub("a", lambda m: fail("boom"), "a")`: "boom",
		`re.search("a", "a").group("x")`:           `no such group "x"`,
		`re.search("a", "a").group(3)`:             "no such group 3",
		`re.search("a", "a").group(None)`:          "group must be an int or a name",
		`re.search("a", "a").group(g = 1)`:         "takes no keyword arguments",
		`re.search("a", "a").groups(1, 2)`:         "in re.Match.groups",
		`re.search("a", "a").groupdict(1, 2)`:      "in re.Match.groupdict",
		`re.search("a", "a").start(9)`:             "no such group 9",
		`re.search("a", "a").span(1, 2)`:           "in re.Match.span",
		`re.search("a", "a").span(9)`:              "no such group 9",
		`re.compile("a") < re.compile("b")`:        "support only == and !=",
		`{re.search("a", "a"): 1}`:                 "unhashable",
	}
	for expr, want := range cases {
		_, err := eval(t, expr)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want it to contain %q", expr, err, want)
		}
	}
}

// TestCacheAndSteps pins the run's pattern cache and the step charge: a
// pattern used in a loop is compiled once, and scanning a megabyte costs a
// thousand steps rather than a million.
func TestCacheAndSteps(t *testing.T) {
	thread := &starlark.Thread{Name: "test"}
	page := strings.Repeat("x", 1<<20) + "<title>t</title>"
	env := starlark.StringDict{Name: Module, "page": starlark.String(page)}
	before := thread.ExecutionSteps()
	got, err := starlark.EvalOptions(&syntax.FileOptions{}, thread, "test.star", `[re.search("<title>([^<]*)", page).group(1) for _ in range(3)]`, env)
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != `["t", "t", "t"]` {
		t.Errorf("got %s", got)
	}
	if steps := thread.ExecutionSteps() - before; steps < 3*1024 || steps > 4*1024 {
		t.Errorf("three scans of 1 MiB cost %d steps, want about %d", steps, 3*1024)
	}
	if cache, _ := thread.Local(cacheKey).(map[string]*regexp.Regexp); len(cache) != 1 {
		t.Errorf("three uses of one pattern left %d compiled programs, want 1", len(cache))
	}
}

func TestValidate(t *testing.T) {
	if err := Validate("a+b"); err != nil {
		t.Errorf("Validate(a+b) = %v", err)
	}
	if err := Validate("(?<=a)b"); err == nil || !strings.Contains(err.Error(), "no lookaround") {
		t.Errorf("Validate(lookbehind) = %v", err)
	}
}

// TestCacheIsBounded pins the cap on one run's compiled patterns.
func TestCacheIsBounded(t *testing.T) {
	thread := &starlark.Thread{Name: "test"}
	for i := range maxCachedPatterns + 5 {
		if _, err := compiled(thread, fmt.Sprintf("a%d", i), 0); err != nil {
			t.Fatal(err)
		}
	}
	if cache, _ := thread.Local(cacheKey).(map[string]*regexp.Regexp); len(cache) != maxCachedPatterns {
		t.Errorf("cache holds %d patterns, want %d", len(cache), maxCachedPatterns)
	}
	if _, err := compiled(nil, "a", 0); err != nil {
		t.Errorf("compiling without a thread: %v", err)
	}
	charge(nil, 10)
}
