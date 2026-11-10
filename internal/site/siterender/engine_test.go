package siterender

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/osteele/liquid/render"
)

type memStore map[string]string

func (s memStore) ReadTemplate(name string) ([]byte, error) {
	if v, ok := s[name]; ok {
		return []byte(v), nil
	}
	return nil, errors.New("no template " + name)
}

func renderText(t *testing.T, limits Limits, source string, vars map[string]any, store memStore) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	m := newMeter(ctx, limits.withDefaults())
	e, err := newEngine(m, store)
	if err != nil {
		t.Fatal(err)
	}
	root, err := e.parse("t.html", source)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if rerr := render.Render(root, meterWriter{w: &buf, m: m}, vars, e.cfg); rerr != nil {
		if m.exceeded != nil {
			return buf.String(), m.exceeded
		}
		return buf.String(), rerr
	}
	return buf.String(), nil
}

func wantBudget(t *testing.T, err error) {
	t.Helper()
	_ = budget(t, err)
}

func budget(t *testing.T, err error) *BudgetError {
	t.Helper()
	var b *BudgetError
	if !errors.As(err, &b) {
		t.Fatalf("want a budget error, got %v", err)
	}
	return b
}

func TestStockBehaviourSurvivesTheMeter(t *testing.T) {
	cases := []struct{ src, want string }{
		{`{% for i in (1..3) %}{{ i }}{% endfor %}`, "123"},
		{`{% for i in (1..5) %}{% if i == 2 %}{% continue %}{% endif %}{% if i == 4 %}{% break %}{% endif %}{{ i }}{% endfor %}`, "13"},
		{`{% for i in (1..3) %}{{ forloop.index0 }}{{ forloop.last }}{% endfor %}`, "0false1false2true"},
		{`{% assign e = "" | split: "," %}{% for i in e %}x{% else %}none{% endfor %}`, "none"},
		{`{% for i in (1..5) limit:2 offset:1 %}{{ i }}{% endfor %}`, "23"},
		{`{% for i in (1..3) reversed %}{{ i }}{% endfor %}`, "321"},
		{`{% capture x %}a{% for i in (1..2) %}b{% endfor %}c{% endcapture %}[{{ x }}]`, "[abbc]"},
		{`{% assign s = "a" | append: "b" %}{{ s }}`, "ab"},
		{`{{ "aXb" | replace: "X", "-" }}`, "a-b"},
		{`{{ "a,b" | split: "," | join: "+" }}`, "a+b"},
		{`{% include "inc.html" %}`, "included"},
		{`{% render "inc.html" %}`, "included"},
		{`{% assign v = "seen" %}{% include "var.html" %}`, "seen"},
		{`{% assign v = "seen" %}{% render "var.html" %}`, ""},
		{`{% include "arg.html", a: "b,c", n: 2 %}`, "b,c2"},
		{`{% assign f = "inc.html" %}{% include f %}`, "included"},
		{`{{ "2026-01-02" | date: "%Y" }}`, "2026"},
		{`{{ "a..b" }}`, "a..b"},
		{`{{ 'it"s..x' }}`, `it"s..x`},
	}
	for _, c := range cases {
		got, err := renderText(t, Limits{}, c.src, map[string]any{}, memStore{"_includes/inc.html": "included", "_includes/var.html": "{{ v }}", "_includes/arg.html": "{{ a }}{{ n }}"})
		if err != nil {
			t.Errorf("%s: %v", c.src, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: got %q want %q", c.src, got, c.want)
		}
	}
}

func TestLimitsStopWhatATemplateCanSpend(t *testing.T) {
	t.Run("a silent loop", func(t *testing.T) {
		_, err := renderText(t, Limits{Steps: 1000}, `{% for i in (1..2000000000) %}{% endfor %}`, nil, nil)
		if b := budget(t, err); b.Resource != "steps" {
			t.Fatal(b)
		}
	})
	t.Run("nested loops", func(t *testing.T) {
		_, err := renderText(t, Limits{Steps: 1000}, `{% for i in (1..1000) %}{% for j in (1..1000) %}{% for k in (1..1000) %}{% endfor %}{% endfor %}{% endfor %}`, nil, nil)
		wantBudget(t, err)
	})
	t.Run("tablerow", func(t *testing.T) {
		_, err := renderText(t, Limits{Steps: 1000}, `{% tablerow i in (1..2000000000) %}{% endtablerow %}`, nil, nil)
		wantBudget(t, err)
	})
	t.Run("output flood", func(t *testing.T) {
		_, err := renderText(t, Limits{PageBytes: 1 << 16}, `{% for i in (1..2000000000) %}xxxxxxxxxxxxxxxx{% endfor %}`, nil, nil)
		if b := budget(t, err); !strings.Contains(b.Resource, "page") {
			t.Fatal(b)
		}
	})
	t.Run("a capture that never reaches the page", func(t *testing.T) {
		_, err := renderText(t, Limits{PageBytes: 1 << 16}, `{% capture x %}{% for i in (1..2000000000) %}xxxxxxxxxxxxxxxx{% endfor %}{% endcapture %}`, nil, nil)
		wantBudget(t, err)
	})
	t.Run("a variable doubling in a loop", func(t *testing.T) {
		_, err := renderText(t, Limits{PageBytes: 1 << 20}, `{% assign s = "xxxxxxxx" %}{% for i in (1..64) %}{% assign s = s | append: s %}{% endfor %}`, nil, nil)
		wantBudget(t, err)
	})
	t.Run("a list doubling in a loop", func(t *testing.T) {
		_, err := renderText(t, Limits{PageBytes: 1 << 20}, `{% assign a = "x,y" | split: "," %}{% for i in (1..64) %}{% assign a = a | concat: a %}{% endfor %}`, nil, nil)
		wantBudget(t, err)
	})
	t.Run("one replace squaring a string", func(t *testing.T) {
		_, err := renderText(t, Limits{PageBytes: 1 << 16}, `{% assign s = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" %}{% for i in (1..12) %}{% assign s = s | replace: "a", s %}{% endfor %}`, nil, nil)
		wantBudget(t, err)
	})
	t.Run("a self include", func(t *testing.T) {
		_, err := renderText(t, Limits{}, `{% include "self.html" %}`, nil, memStore{"_includes/self.html": `{% include "self.html" %}`})
		if b := budget(t, err); !strings.Contains(b.Resource, "include") {
			t.Fatal(b)
		}
	})
	t.Run("includes multiplying without a cycle", func(t *testing.T) {
		store := memStore{"_includes/e.html": "x"}
		for _, p := range [][2]string{{"a", "b"}, {"b", "c"}, {"c", "d"}, {"d", "e"}} {
			store["_includes/"+p[0]+".html"] = strings.Repeat(`{% include "`+p[1]+`.html" %}`, 100)
		}
		_, err := renderText(t, Limits{Steps: 5000}, `{% include "a.html" %}`, nil, store)
		wantBudget(t, err)
	})
	t.Run("a loop inside an include", func(t *testing.T) {
		_, err := renderText(t, Limits{Steps: 1000}, `{% include "loop.html" %}`, nil, memStore{"_includes/loop.html": `{% for i in (1..2000000000) %}{% endfor %}`})
		wantBudget(t, err)
	})
	t.Run("a range inside an include", func(t *testing.T) {
		_, err := renderText(t, Limits{}, `{% include "r.html" %}`, nil, memStore{"_includes/r.html": `{{ (1..2000000000) | join: "," }}`})
		if err == nil || !strings.Contains(err.Error(), "range") {
			t.Fatal(err)
		}
	})
	t.Run("an include outside the folder", func(t *testing.T) {
		_, err := renderText(t, Limits{}, `{% include "../secret.html" %}`, nil, memStore{"secret.html": "x"})
		if err == nil || !strings.Contains(err.Error(), "outside") {
			t.Fatal(err)
		}
	})
	t.Run("a canceled render", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		m := newMeter(ctx, Limits{}.withDefaults())
		e, _ := newEngine(m, memStore{})
		root, _ := e.parse("t", `{% for i in (1..10) %}{% endfor %}`)
		var buf bytes.Buffer
		if err := render.Render(root, &buf, nil, e.cfg); !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
	})
}

func TestStaticChecks(t *testing.T) {
	for _, src := range []string{
		`{{ (1..2000000000) | join: "," }}`,
		`{% assign r = (1..2000000000) %}`,
		`{% if (1..3) %}{% endif %}`,
		`{% case (1..3) %}{% when 1 %}{% endcase %}`,
	} {
		if _, err := renderText(t, Limits{}, src, nil, nil); err == nil || !strings.Contains(err.Error(), "range") {
			t.Errorf("%s: %v", src, err)
		}
	}
	deep := strings.Repeat("{% if 1 %}", 100) + strings.Repeat("{% endif %}", 100)
	if _, err := renderText(t, Limits{Nesting: 32}, deep, nil, nil); err == nil || !strings.Contains(err.Error(), "nest") {
		t.Errorf("deep nesting: %v", err)
	}
	for _, src := range []string{`{{ "now" | date: "%Y" }}`, `{% assign n = "no" | append: "w" %}{{ n | date: "%Y" }}`} {
		if _, err := renderText(t, Limits{}, src, nil, nil); err == nil || !strings.Contains(err.Error(), "now") {
			t.Errorf("%s: %v", src, err)
		}
	}
}

func TestErrorsNameTheFileAndLine(t *testing.T) {
	_, err := renderText(t, Limits{}, "line one\n{% if %}", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "t.html") {
		t.Fatalf("got %v", err)
	}
}

func TestAWrapperThatCannotCompileIsReported(t *testing.T) {
	bare := &engine{cfg: render.NewConfig()}
	if err := bare.wrapBody(&render.BlockNode{}); err == nil {
		t.Fatal("a config with no metered block must refuse")
	}
	if firstBlock(&render.TextNode{}) != nil {
		t.Fatal("text holds no block")
	}
}
