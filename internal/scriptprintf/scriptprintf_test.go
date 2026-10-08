package scriptprintf

import (
	"fmt"
	"strings"
	"testing"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
	"go.starlark.net/syntax"
)

// opts are the dialect's switches that matter here.
var opts = &syntax.FileOptions{Set: true, TopLevelControl: true, GlobalReassign: true}

// run executes source after Rewrite and returns its global out.
func run(t *testing.T, source string) (starlark.Value, error) {
	t.Helper()
	file, err := opts.Parse("test.star", source, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	Rewrite(file)
	date := &starlarkstruct.Module{Name: "date", Members: starlark.StringDict{
		"format": starlark.NewBuiltin("date.format", func(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
			return starlark.String("formatted " + args[0].String()), nil
		}),
	}}
	env := Bind(starlark.StringDict{"date": date})
	prog, err := starlark.FileProgram(file, env.Has)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	globals, err := prog.Init(&starlark.Thread{Name: "test"}, env)
	if err != nil {
		return nil, fmt.Errorf("run: %w", err)
	}
	return globals["out"], nil
}

// TestPercentMatchesPython holds `%` to what Python 3 writes for the same
// format and value; every want below is Python's output.
func TestPercentMatchesPython(t *testing.T) {
	cases := []struct{ expr, want string }{
		// The ticket's done-when.
		{`"%02X" % 10`, "0A"},
		{`"%5.2f" % 3.14159`, " 3.14"},
		{`"%-4s|" % "a"`, "a   |"},
		// Percent-encoding a byte, zero-padded dates, fixed decimals.
		{`"%%%02X" % 32`, "%20"},
		{`"%02d-%02d" % (3, 7)`, "03-07"},
		{`"%05.3d" % 5`, "00005"},
		{`"%-6.2f|" % 3.14159`, "3.14  |"},
		{`"%+d" % 5`, "+5"},
		{`"% d" % 5`, " 5"},
		{`"%#x" % 255`, "0xff"},
		{`"%#o" % 8`, "0o10"},
		{`"%05s" % "a"`, "    a"},
		{`"%.2s" % "abc"`, "ab"},
		{`"%5r" % "a"`, `  "a"`},
		{`"%x" % -255`, "-ff"},
		{`"%#08x" % 255`, "0x0000ff"},
		{`"%f" % float("inf")`, "inf"},
		{`"%08.2f" % -3.5`, "-0003.50"},
		{`"%g" % 1e-5`, "1e-05"},
		{`"%#g" % 0.5`, "0.500000"},
		{`"%5c" % 65`, "    A"},
		{`"%c" % "z"`, "z"},
		{`"%*d" % (5, 3)`, "    3"},
		{`"%-*d|" % (-4, 3)`, "3   |"},
		{`"%*d|" % (-4, 3)`, "3   |"},
		{`"%.*f" % (1, 2.25)`, "2.2"},
		{`"%e" % 12345.678`, "1.234568e+04"},
		{`"%010.3e" % -1.5`, "-1.500e+00"},
		{`"%G" % 1e20`, "1E+20"},
		{`"%F" % float("nan")`, "NAN"},
		{`"%05f" % float("inf")`, "  inf"},
		{`"%-05d|" % 5`, "5    |"},
		{`"%#.0f" % 2.0`, "2."},
		{`"%ld" % 7`, "7"},
		{`"%(n)03d %(s)s" % {"n": 7, "s": "x"}`, "007 x"},
		{`"%s" % {"a": 1}`, `{"a": 1}`},
		{`"%s %s" % ("a", [1])`, "a [1]"},
		{`"%d" % 3.9`, "3"},
		{`"100%%" % ()`, "100%"},
		{`"%5%" % ()`, "%"},
		// Not a string: Starlark's own operator.
		{`str(7 % 3) + str(7.5 % 2)`, "11.5"},
	}
	for _, c := range cases {
		got, err := run(t, "out = "+c.expr)
		if err != nil {
			t.Errorf("%s: %v", c.expr, err)
			continue
		}
		if s, _ := starlark.AsString(got); s != c.want {
			t.Errorf("%s = %q, want %q", c.expr, s, c.want)
		}
	}
}

// TestFormatMatchesPython holds str.format to Python 3's output.
func TestFormatMatchesPython(t *testing.T) {
	cases := []struct{ expr, want string }{
		{`"{:02X}".format(10)`, "0A"},
		{`"{:>10}".format("a")`, "         a"},
		{`"{:.2f}".format(3.14159)`, "3.14"},
		{`"{:*^9}".format("ab")`, "***ab****"},
		{`"{:,}".format(1234567)`, "1,234,567"},
		{`"{:_x}".format(65535)`, "ffff"},
		{`"{:_b}".format(255)`, "1111_1111"},
		{`"{:+08.3f}".format(2.5)`, "+002.500"},
		{`"{:=+8}".format(-5)`, "-      5"},
		{`"{:#b}".format(5)`, "0b101"},
		{`"{:%}".format(0.125)`, "12.500000%"},
		{`"{:.1%}".format(0.125)`, "12.5%"},
		{`"{:c}".format(65)`, "A"},
		{`"{:e}".format(1.0)`, "1.000000e+00"},
		{`"{:.3}".format(3.14159)`, "3.14"},
		{`"{:.3}".format("abcdef")`, "abc"},
		{`"{:10.3}".format(1.0)`, "       1.0"},
		{`"{}".format(1.0)`, "1.0"},
		{`"{:5}".format(42)`, "   42"},
		{`"{:5}".format("ab")`, "ab   "},
		{`"{:.0f}".format(2.5)`, "2"},
		{`"{:,.2f}".format(1234567.891)`, "1,234,567.89"},
		{`"{:,}".format(1234567.5)`, "1,234,567.5"},
		{`"{:10}".format(1234567.5)`, " 1234567.5"},
		{`"{:,}".format(1e20)`, "1e+20"},
		{`"{:5}".format(1e-5)`, "1e-05"},
		{`"{:5}".format(0.0001)`, "0.0001"},
		{`"{:5}".format(2.0)`, "  2.0"},
		{`"{:5}".format(float("inf"))`, "  inf"},
		{`"{:5}".format(float("nan"))`, "  nan"},
		{`"{:,}".format(-0.0)`, "-0.0"},
		{`"{:}".format(1234567.5)`, "1.2345675e+06"},
		{`"{:n}".format(1234)`, "1234"},
		{`"{:g}".format(1e16)`, "1e+16"},
		{`"{:010}".format(-3.5)`, "-0000003.5"},
		{`"{:05}".format("a")`, "a0000"},
		{`"{!r:>6}".format("a")`, `   "a"`},
		{`"{0}{1}{0}".format("a", "b")`, "aba"},
		{`"{x}-{y:03d}".format(x = "a", y = 7)`, "a-007"},
		{`"{{}} {}".format(1)`, "{} 1"},
		{`"{} {}".format(True, None)`, "True None"},
		{`"{:>4}".format([1])`, " [1]"},
		{`"{:+}".format(3)`, "+3"},
		// .format on something other than a string is that value's own.
		{`date.format("x")`, `formatted "x"`},
		// %= on a name.
		{`"%d"` + "\nout %= 4", "4"},
	}
	for _, c := range cases {
		got, err := run(t, "out = "+c.expr)
		if err != nil {
			t.Errorf("%s: %v", c.expr, err)
			continue
		}
		if s, _ := starlark.AsString(got); s != c.want {
			t.Errorf("%s = %q, want %q", c.expr, s, c.want)
		}
	}
}

func TestRefusals(t *testing.T) {
	cases := map[string]string{
		`"%q" % 1`:                  `unsupported format character 'q'`,
		`"%" % 1`:                   "incomplete format",
		`"%(a" % {}`:                "incomplete format key",
		`"%d %d" % 1`:               "not enough arguments",
		`"%d" % (1, 2)`:             "not all arguments converted",
		`"%(a)s" % 1`:               "format requires a mapping",
		`"%(a)s" % {}`:              "key not found: a",
		`"%*d" % ("x", 1)`:          "a * width or precision must be an int, not string",
		`"%.*d" % (1,)`:             "not enough arguments",
		`"%*d" % ()`:                "not enough arguments",
		`"%f" % "x"`:                "conversion f requires a float, not string",
		`"%d" % "x"`:                "conversion d requires an integer",
		`"%c" % 1.5`:                "conversion c requires an int or a single-character string",
		`"%c" % "ab"`:               "single-character string",
		`"%c" % -1`:                 "valid Unicode code point",
		`"%d" % ()`:                 "not enough arguments",
		`1 % 0`:                     "modulo by zero",
		`"{".format()`:              "unmatched '{'",
		`"}".format()`:              "single '}'",
		`"{:q}".format(1)`:          "invalid format spec",
		`"{:.}".format(1)`:          "a precision with no digits",
		`"{:{w}}".format(1, w = 2)`: "nested replacement fields",
		`"{a.b}".format(a = 1)`:     "attribute and element syntax",
		`"{!x}".format(1)`:          "unknown conversion !x",
		`"{}{0}".format(1)`:         "cannot switch from automatic",
		`"{0}{}".format(1)`:         "cannot switch from manual",
		`"{}".format()`:             "tuple index out of range",
		`"{k}".format()`:            "keyword k not found",
		`"{:d}".format(1.5)`:        "format code 'd' needs an int, not float",
		`"{:.2d}".format(1)`:        "precision is not allowed",
		`"{:f}".format("x")`:        "format code 'f' needs a number",
		`"{:c}".format([])`:         "conversion c requires an int",
		`[].format()`:               "list has no .format field or method",
		`None.format()`:             "NoneType has no .format field or method",
		`"{:d}".format(True)`:       "needs an int, not bool",
	}
	for expr, want := range cases {
		_, err := run(t, "out = "+expr)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want it to contain %q", expr, err, want)
		}
	}
}

// TestRewriteReachesEveryExpression pins the reflection walk: a `%` nested
// in a comprehension, a lambda, a default, a dict entry, a call's keyword
// argument and a subscript is routed, and `%=` on a subscript is left to
// Starlark.
func TestRewriteReachesEveryExpression(t *testing.T) {
	src := `
def f(x = "%03d" % 1):
    return x
g = lambda n: "%02d" % n
d = {"k": "%.1f" % 1.25}
l = ["%02d" % i for i in range(2) if "%d" % i]
s = ["a", "b"][7 % 2]
t = {"v": 5}
t["v"] %= 3
out = " ".join([f(), g(4), d["k"], l[1], s, str(t["v"]), "{:>3}".format(x = 1) if False else "{x:>3}".format(x = 1)])
`
	got, err := run(t, src)
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := starlark.AsString(got); s != "001 04 1.2 01 b 2   1" {
		t.Errorf("out = %q", s)
	}
}

func TestCheck(t *testing.T) {
	if CheckPercent("%%%02X %-5s %(k).2f") != nil || CheckFormat("{:02X} {k!r:>5} {{}}") != nil {
		t.Error("a supported format was refused")
	}
	if CheckPercent("%y") == nil || CheckFormat("{:y}") == nil {
		t.Error("an unsupported format was accepted")
	}
}
