package sourcecheck

import (
	"strings"
	"testing"
)

func TestCheck(t *testing.T) {
	tests := []struct {
		name, contentType, body string
		wantErr                 []string // substrings; empty means accepted
	}{
		{"other types pass untouched", "text/html", "<a href=x>", nil},
		{"dot digraph", "text/vnd.graphviz", "digraph { a -> b }", nil},
		{"dot alias", "text/x-graphviz", "graph { a -- b }", nil},
		{
			"dot with html label, comments and a layout engine", "text/vnd.graphviz",
			"strict digraph G {\n layout=neato # comment\n a [label=<<b>x</b>>] // c\n /* d */ a -> b\n}", nil,
		},
		{"dot empty", "text/vnd.graphviz", "  \n", []string{"empty"}},
		{"dot names the line", "text/vnd.graphviz", "digraph {\n a -> b\n c -> -> d\n}", []string{"does not parse", "3:7"}},
		{"dot unterminated", "text/vnd.graphviz", "digraph { a -> b", []string{"1:17", "end-of-file"}},
		{"dot no graph", "text/vnd.graphviz", "/* nothing */", []string{"does not parse", "digraph"}},
		{
			"vega-lite inline values", "application/vnd.vegalite+json",
			`{"data":{"values":[{"a":1}]},"mark":"bar","encoding":{"x":{"field":"a"}}}`, nil,
		},
		{
			"vega-lite references", "application/vnd.vegalite+json",
			`{"data":{"url":"mcp://global/data/orders.csv"},"layer":[{"mark":"line",` +
				`"transform":[{"lookup":"k","from":{"data":{"url":"mcp:asset:ast_1"},"key":"k"}}]}]}`, nil,
		},
		{"vega-lite not json", "application/vnd.vegalite+json", `{"mark":`, []string{"not valid JSON"}},
		{"vega-lite not an object", "application/vnd.vegalite+json", `[1,2]`, []string{"JSON object"}},
		{"vega-lite no view", "application/vnd.vegalite+json", `{"data":{"values":[]}}`, []string{"describes no view"}},
		{
			"vega-lite external data", "application/vnd.vegalite+json",
			`{"data":{"url":"https://example.com/d.csv"},"mark":"bar"}`,
			[]string{`"https://example.com/d.csv"`, "not a managed resource"},
		},
		{
			"vega-lite nested lookup url", "application/vnd.vegalite+json",
			`{"mark":"bar","transform":[{"lookup":"k","from":{"data":{"url":"data/x.json"}}}]}`,
			[]string{`"data/x.json"`},
		},
		{
			"vega-lite image channel constant", "application/vnd.vegalite+json",
			`{"mark":"image","encoding":{"url":{"value":"https://example.com/i.png"}}}`,
			[]string{`"https://example.com/i.png"`},
		},
		{
			"vega-lite href", "application/vnd.vegalite+json",
			`{"mark":{"type":"point","href":"javascript:alert(1)"}}`,
			[]string{"links to", "javascript:alert(1)"},
		},
		{
			"vega-lite href channel constant", "application/vnd.vegalite+json",
			`{"mark":"point","encoding":{"href":{"value":"https://example.com"}}}`,
			[]string{"links to"},
		},
		{
			"vega-lite href from a field is left to the viewer", "application/vnd.vegalite+json",
			`{"mark":"point","encoding":{"href":{"field":"link"}}}`, nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Check(tt.contentType, tt.body)
			if len(tt.wantErr) == 0 {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("accepted")
			}
			for _, want := range tt.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}
		})
	}
}

func TestNamedCapsAndDedupes(t *testing.T) {
	got := named([]string{"g", "a", "b", "a", "c", "d", "e", "f"})
	want := `"a", "b", "c", "d", "e" and 2 more`
	if got != want {
		t.Errorf("named = %s; want %s", got, want)
	}
}
