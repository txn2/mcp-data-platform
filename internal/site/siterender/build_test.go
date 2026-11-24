package siterender

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/site/sitebuild"
)

const buildFile = `{"entries":["app.tsx","admin/admin.tsx"],"imports":{"react":"https://esm.sh/react@18.3.1","react-dom/client":"https://esm.sh/react-dom@18.3.1/client"}}`

func builtSite() map[string]string {
	return map[string]string{
		sitebuild.ControlFile: buildFile,
		"app.tsx":             `import { Table } from "./Table"; import { createRoot } from "react-dom/client"; createRoot(document.body).render(<Table />);`,
		"Table.tsx":           `export function Table(): JSX.Element { return <table data-x="table" />; }`,
		"admin/admin.tsx":     `const n: number = 1; console.log("admin", n);`,
		"index.html":          "---\ntitle: kept as written\n---\n<script type=module src=/app.js></script>",
		"_data/x.json":        `{"a":1}`,
		"style.css":           "body{}",
	}
}

func TestABuildOnlyTreeCompilesAndServesNoSource(t *testing.T) {
	c, idx := renderTree(t, builtSite())
	for _, want := range []string{"app.js", "admin/admin.js"} {
		if _, ok := c.files[want]; !ok {
			t.Fatalf("no %s in %v", want, c.files)
		}
	}
	if !strings.Contains(c.files["app.js"], `data-x": "table"`) || !strings.Contains(c.files["app.js"], "https://esm.sh/react@18.3.1/jsx-runtime") {
		t.Errorf("app.js: %s", c.files["app.js"])
	}
	slices.Sort(c.copies)
	if want := []string{"index.html", "style.css"}; !slices.Equal(c.copies, want) {
		t.Errorf("copied %v, want %v: a tree with no layouts is not a template, and neither source nor _data is served (#2779)", c.copies, want)
	}
	for _, e := range idx.Entries {
		if e.Output == "app.js" && (e.Build != sitebuild.Version || !slices.Contains(e.Inputs, "Table.tsx") || !slices.Contains(e.Inputs, sitebuild.ControlFile)) {
			t.Errorf("app.js entry: %+v", e)
		}
	}
}

func TestATreeWithLayoutsRendersAndCompiles(t *testing.T) {
	files := builtSite()
	files["_layouts/base.html"] = "<main>{{ content }}</main>"
	files["index.html"] = "---\nlayout: base\n---\n{{ page.url }}"
	c, _ := renderTree(t, files)
	if c.files["index.html"] != "<main>/</main>" || c.files["app.js"] == "" {
		t.Fatalf("files: %v", c.files)
	}
	if slices.Contains(c.copies, "_data/x.json") {
		t.Errorf("a rendered tree served its records: %v", c.copies)
	}
}

func TestAnEntryRecompilesOnlyWhenWhatItReadChanged(t *testing.T) {
	files := builtSite()
	_, idx := renderTree(t, files)

	c := newCapture()
	if _, err := Render(context.Background(), tree(files), Options{Previous: idx, Changed: []string{"admin/admin.tsx"}}, c); err != nil {
		t.Fatal(err)
	}
	if _, rebuilt := c.files["app.js"]; rebuilt || !slices.Contains(c.kept, "app.js") {
		t.Errorf("app.js read nothing that changed: files %v kept %v", c.files, c.kept)
	}
	if _, rebuilt := c.files["admin/admin.js"]; !rebuilt {
		t.Errorf("admin.js changed and was kept")
	}

	for _, changed := range []string{"Table.tsx", sitebuild.ControlFile, "Table"} {
		c = newCapture()
		if _, err := Render(context.Background(), tree(files), Options{Previous: idx, Changed: []string{changed}}, c); err != nil {
			t.Fatal(err)
		}
		if _, rebuilt := c.files["app.js"]; !rebuilt {
			t.Errorf("%s changed and app.js was kept", changed)
		}
	}

	stale := *idx
	stale.Entries = slices.Clone(idx.Entries)
	for i := range stale.Entries {
		stale.Entries[i].Build = "older"
	}
	c = newCapture()
	if _, err := Render(context.Background(), tree(files), Options{Previous: &stale}, c); err != nil {
		t.Fatal(err)
	}
	if _, rebuilt := c.files["app.js"]; !rebuilt {
		t.Errorf("an output of another compiler was kept")
	}
}

func TestCompileFailuresAreReportedWithTheirPlace(t *testing.T) {
	files := builtSite()
	files["Table.tsx"] = "export function Table() {\n  const x = ;\n}\n"
	files["admin/admin.tsx"] = `import x from "lodash"; x();`
	_, err := Render(context.Background(), tree(files), Options{}, newCapture())
	var re *RenderError
	if !errors.As(err, &re) || len(re.Failures) < 2 {
		t.Fatalf("err = %v", err)
	}
	if f := re.Failures[0]; f.File != "Table.tsx" || f.Line != 2 || f.Column == 0 {
		t.Errorf("first failure: %+v", f)
	}
	if !strings.Contains(err.Error(), "Table.tsx:2:") || !strings.Contains(err.Error(), `"lodash"`) {
		t.Errorf("message: %v", err)
	}
}

func TestBuildRefusals(t *testing.T) {
	cases := map[string]struct {
		edit func(map[string]string)
		want string
	}{
		"bad build file":   {func(f map[string]string) { f[sitebuild.ControlFile] = `{"entries":[]}` }, sitebuild.ControlFile + ":0: entries is empty"},
		"output is a file": {func(f map[string]string) { f["app.js"] = "old" }, "compiles to app.js, which the release also holds"},
		"output is a page": {func(f map[string]string) {
			f["_layouts/b.html"] = "x"
			f["page.html"] = "---\npermalink: /admin/admin.js\n---\nx"
		}, "admin/admin.js, which page.html also produces"},
		"over a limit": {func(f map[string]string) { f["Table.tsx"] = strings.Repeat("// pad\n", 1<<20) }, "bytes in one module"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			files := builtSite()
			tc.edit(files)
			_, err := Render(context.Background(), tree(files), Options{Limits: Limits{TemplateBytes: 1 << 20}}, newCapture())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestATreeWithoutABuildFileIsUnchanged(t *testing.T) {
	files := builtSite()
	delete(files, sitebuild.ControlFile)
	delete(files, "_data/x.json")
	if Active(tree(files)) {
		t.Fatal("a tree with neither layouts nor a build file is active")
	}
	files["_data/x.json"] = "{}"
	if !Active(tree(files)) {
		t.Fatal("a tree with records is not rendered, so they would be served")
	}
	if !Active(tree(builtSite())) {
		t.Fatal("a tree with a build file is not active")
	}
	files["_layouts/base.html"] = "x"
	c, _ := renderTree(t, files)
	if !slices.Contains(c.copies, "app.tsx") || c.files["app.js"] != "" {
		t.Errorf("a rendered tree without a build file compiled: copies %v files %v", c.copies, c.files)
	}
}

func TestACompileThatRunsOutEndsTheRender(t *testing.T) {
	_, err := Render(context.Background(), tree(builtSite()), Options{Limits: Limits{TotalBytes: 10}}, newCapture())
	var be *BudgetError
	if !errors.As(err, &be) || be.Resource != "bytes of output" {
		t.Fatalf("err = %v, want the output limit", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Render(ctx, tree(builtSite()), Options{}, newCapture()); !errors.Is(err, context.Canceled) {
		t.Fatalf("a canceled render: %v", err)
	}
}
