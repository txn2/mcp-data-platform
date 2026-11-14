package siterender

import (
	"strings"
	"testing"
)

// TestAComputedIntegerIsALoopBound is #2633: a range bound, a limit, an offset
// and a cols taken from arithmetic render as they do from a literal.
func TestAComputedIntegerIsALoopBound(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"literal", `{% for i in (1..3) %}{{ i }}{% endfor %}`, "123"},
		{"plus", `{% assign n = 2 | plus: 1 %}{% for i in (1..n) %}{{ i }}{% endfor %}`, "123"},
		{"minus", `{% assign n = 5 | minus: 2 %}{% for i in (1..n) %}{{ i }}{% endfor %}`, "123"},
		{"times", `{% assign n = 3 | times: 1 %}{% for i in (1..n) %}{{ i }}{% endfor %}`, "123"},
		{"divided_by", `{% assign n = 7 | divided_by: 2 %}{% for i in (1..n) %}{{ i }}{% endfor %}`, "123"},
		{"modulo", `{% assign n = 7 | modulo: 4 %}{% for i in (1..n) %}{{ i }}{% endfor %}`, "123"},
		{"at_least", `{% assign n = 1 | at_least: 3 %}{% for i in (1..n) %}{{ i }}{% endfor %}`, "123"},
		{"at_most", `{% assign n = 9 | at_most: 3 %}{% for i in (1..n) %}{{ i }}{% endfor %}`, "123"},
		{"abs", `{% assign n = -3 | abs %}{% for i in (1..n) %}{{ i }}{% endfor %}`, "123"},
		{"both bounds", `{% assign a = 0 | plus: 2 %}{% assign b = a | times: 2 %}{% for i in (a..b) %}{{ i }}{% endfor %}`, "234"},
		{"chained", `{% assign n = 10 | minus: 1 | divided_by: 3 %}{% for i in (1..n) %}{{ i }}{% endfor %}`, "123"},
		{"inside a filter in the bound", `{% assign n = 2 %}{% for i in ((0 | plus: 1)..(n | plus: 1)) %}{{ i }}{% endfor %}`, "123"},
		{"floor makes a float a bound", `{% assign n = 7 | divided_by: 2.0 | floor %}{% for i in (1..n) %}{{ i }}{% endfor %}`, "123"},
		{"limit", `{% assign n = 1 | plus: 1 %}{% for i in (1..5) limit: n %}{{ i }}{% endfor %}`, "12"},
		{"offset", `{% assign n = 1 | plus: 2 %}{% for i in (1..5) offset: n %}{{ i }}{% endfor %}`, "45"},
		{"cols", `{% assign n = 1 | plus: 1 %}{% tablerow i in (1..3) cols: n %}{{ i }}{% endtablerow %}`, ""},
		{"every other row", `{% for x in (1..4) %}{% assign odd = forloop.index0 | modulo: 2 %}{% if odd == 0 %}{{ x }}{% endif %}{% endfor %}`, "13"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := renderText(t, Limits{}, c.src, map[string]any{}, memStore{})
			if err != nil {
				t.Fatalf("%s: %v", c.src, err)
			}
			if c.name == "cols" {
				if !strings.Contains(got, `<tr class="row1">`) || !strings.Contains(got, `<tr class="row2">`) {
					t.Fatalf("two columns make two rows of three cells: %q", got)
				}
				return
			}
			if got != c.want {
				t.Fatalf("%s: got %q want %q", c.src, got, c.want)
			}
		})
	}
}

// TestArithmeticPrintsAsTheEngineDoes holds the replaced filters to the
// engine's output for every operand kind they take.
func TestArithmeticPrintsAsTheEngineDoes(t *testing.T) {
	cases := []struct{ src, want string }{
		{`{{ 2 | plus: 1 }}`, "3"},
		{`{{ 2 | plus: 0.5 }}`, "2.5"},
		{`{{ "2" | plus: 1 }}`, "3"},
		{`{{ "x" | plus: 1 }}`, "1"},
		{`{{ 2 | minus: 3 }}`, "-1"},
		{`{{ 2.5 | minus: 1 }}`, "1.5"},
		{`{{ 3 | times: 4 }}`, "12"},
		{`{{ 3 | times: 1.5 }}`, "4.5"},
		{`{{ 7 | divided_by: 2 }}`, "3"},
		{`{{ 7 | divided_by: 2.0 }}`, "3.5"},
		{`{{ 7.9 | divided_by: 2 }}`, "3"},
		{`{{ 7 | modulo: 2 }}`, "1"},
		{`{{ -7 | modulo: 2 }}`, "-1"},
		{`{{ 7.5 | modulo: 2 }}`, "1.5"},
		{`{{ 7 | modulo: 0 }}`, "NaN"},
		{`{{ -3 | abs }}`, "3"},
		{`{{ -2.5 | abs }}`, "2.5"},
		{`{{ "-4" | abs }}`, "4"},
		{`{{ 1 | at_least: 3 }}`, "3"},
		{`{{ 1.5 | at_least: 1 }}`, "1.5"},
		{`{{ 9 | at_most: 3 }}`, "3"},
		{`{{ 0.5 | at_most: 1 }}`, "0.5"},
		{`{% assign f = 6 | divided_by: 4.0 %}{{ f }}`, "1.5"},
	}
	for _, c := range cases {
		got, err := renderText(t, Limits{}, c.src, map[string]any{}, memStore{})
		if err != nil {
			t.Errorf("%s: %v", c.src, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: got %q want %q", c.src, got, c.want)
		}
	}
}

// TestArithmeticRefusesWhatTheEngineRefuses keeps the engine's refusals.
func TestArithmeticRefusesWhatTheEngineRefuses(t *testing.T) {
	cases := []struct{ src, want string }{
		{`{{ 7 | divided_by: 0 }}`, "division by zero"},
		{`{{ 7 | divided_by: 0.0 }}`, "division by zero"},
		{`{{ 7 | divided_by: "2" }}`, "invalid divisor"},
		{`{{ "x" | modulo: 2 }}`, "x"},
		{`{{ 2 | modulo: "y" }}`, "y"},
		{`{{ "z" | abs }}`, "z"},
		// A float bound is refused, as Liquid itself refuses one.
		{`{% assign n = 6 | divided_by: 2.0 %}{% for i in (1..n) %}{{ i }}{% endfor %}`, "float64"},
	}
	for _, c := range cases {
		_, err := renderText(t, Limits{}, c.src, map[string]any{}, memStore{})
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want an error naming %q, got %v", c.src, c.want, err)
		}
	}
}

// TestAWholeNumberInAJSONRecordIsAnInteger is #2633 for records: a count read
// from JSON bounds a loop as one read from YAML does.
func TestAWholeNumberInAJSONRecordIsAnInteger(t *testing.T) {
	c, _ := renderTree(t, map[string]string{
		"_data/plans/a.json": `{"name":"A","seats":3,"price":2.5,"big":9007199254740993,"tags":[1,2],"nested":{"n":2}}`,
		"_data/plans/b.yaml": "name: B\nseats: 2\n",
		"plans.html": "---\n---\n{% for p in site.data.plans %}{{ p.name }}:{% for i in (1..p.seats) %}{{ i }}{% endfor %}|{% endfor %}" +
			"{% assign a = site.data.plans | first %}{% for i in (1..a.nested.n) limit: a.tags[0] %}-{% endfor %}" +
			"{{ a.price }} {{ a.big }} {{ a.seats | divided_by: 2 }} {{ a.price | times: 2 }}",
	})
	if got, want := c.files["plans.html"], "A:123|B:12|-2.5 9007199254740993 1 5"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestDecodeJSONRefusesWhatUnmarshalRefuses(t *testing.T) {
	for _, bad := range []string{`{"a":1} x`, `[1]`, `{"a":`, ``} {
		if _, err := decodeJSON([]byte(bad)); err == nil {
			t.Errorf("%q: want an error", bad)
		}
	}
	got, err := decodeJSON([]byte(" {\"a\": 1.0, \"b\": 1e3, \"c\": -2} \n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["a"].(float64); !ok {
		t.Errorf("1.0 stays a float: %T", got["a"])
	}
	if _, ok := got["b"].(float64); !ok {
		t.Errorf("1e3 stays a float: %T", got["b"])
	}
	if got["c"] != -2 {
		t.Errorf("-2 is an int: %#v", got["c"])
	}
}

func TestToFloat64ReadsEveryKind(t *testing.T) {
	cases := []struct {
		in   any
		want float64
	}{
		{int8(-2), -2},
		{int16(3), 3},
		{int32(4), 4},
		{int64(5), 5},
		{uint8(6), 6},
		{uint16(7), 7},
		{uint32(8), 8},
		{uint(9), 9},
		{uint64(10), 10},
		{float32(1.5), 1.5},
		{2.5, 2.5},
		{"3.5", 3.5},
		{"x", 0},
		{nil, 0},
		{true, 0},
		{uint64(1 << 63), 1 << 63},
		{^uint(0), float64(^uint(0))},
	}
	for _, c := range cases {
		if got := toFloat64(c.in); got != c.want {
			t.Errorf("toFloat64(%#v) = %v want %v", c.in, got, c.want)
		}
	}
	if isInteger(uint64(1<<63)) || isInteger(^uint(0)) || !isInteger(uint(1)) || !isInteger(uint64(1)) {
		t.Error("an unsigned integer is an integer only below math.MaxInt64")
	}
	if toInt64("x") != 0 || toInt64(int8(-1)) != -1 || toInt64(uint(2)) != 2 {
		t.Error("toInt64")
	}
	if toInt64(uint64(1<<63)) != 0 {
		t.Error("an unsigned value past math.MaxInt64 is not read as a negative int64")
	}
}
