package sitebuild

import (
	"context"
	"errors"
	"runtime/debug"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

const reactURL = "https://esm.sh/react@18.3.1"

func tree(files map[string]string) fstest.MapFS {
	fsys := fstest.MapFS{}
	for name, body := range files {
		fsys[name] = &fstest.MapFile{Data: []byte(body)}
	}
	return fsys
}

func mustParse(t *testing.T, doc string) *Config {
	t.Helper()
	cfg, err := Parse([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func output(t *testing.T, res *Result, name string) string {
	t.Helper()
	for _, o := range res.Outputs {
		if o.Path == name {
			return string(o.Body)
		}
	}
	t.Fatalf("no output %s in %v", name, res.Outputs)
	return ""
}

const app = `import { Table } from "./components/Table";
import { createRoot } from "react-dom/client";
const rows: number[] = [1, 2];
createRoot(document.getElementById("root")!).render(<Table rows={rows} />);
`

const table = `export function Table({ rows }: { rows: number[] }) {
  return <table>{rows.map((r) => <tr key={r}><td>{r}</td></tr>)}</table>;
}
`

func TestCompile_BundlesLocalModulesAndLeavesURLsExternal(t *testing.T) {
	cfg := mustParse(t, `{"entries":["app.tsx"],"imports":{"react":"`+reactURL+`","react-dom/client":"https://esm.sh/react-dom@18.3.1/client"}}`)
	src := tree(map[string]string{"app.tsx": app, "components/Table.tsx": table, ControlFile: "{}"})
	res, err := Compile(context.Background(), src, "app.tsx", Options{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	js := output(t, res, "app.js")
	for _, want := range []string{`from "https://esm.sh/react-dom@18.3.1/client"`, `from "https://esm.sh/react@18.3.1/jsx-runtime"`, "function Table("} {
		if !strings.Contains(js, want) {
			t.Errorf("app.js lacks %s:\n%s", want, js)
		}
	}
	if strings.Contains(js, "number[]") {
		t.Errorf("app.js kept a type:\n%s", js)
	}
	for _, in := range []string{"app.tsx", "components/Table.tsx", ControlFile, "components/Table"} {
		if !slices.Contains(res.Inputs, in) {
			t.Errorf("inputs %v lack %s", res.Inputs, in)
		}
	}
}

func TestCompile_SourcemapAndCSSSidecars(t *testing.T) {
	cfg := mustParse(t, `{"entries":["admin/main.jsx"],"sourcemap":true,"minify":true}`)
	src := tree(map[string]string{
		"admin/main.jsx": `import "./main.css"; import data from "../data.json"; document.title = data.name;`,
		"admin/main.css": `@import "https://fonts.example/css?family=Inter"; @import "./more.css"; body { color: red; background: url(/img/logo.png); }`,
		"admin/more.css": `p { margin: 0; }`,
		"data.json":      `{"name":"x"}`,
	})
	res, err := Compile(context.Background(), src, "admin/main.jsx", Options{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	js := output(t, res, "admin/main.js")
	if !strings.Contains(js, "sourceMappingURL=main.js.map") {
		t.Errorf("no source map link: %s", js)
	}
	output(t, res, "admin/main.js.map")
	css := output(t, res, "admin/main.css")
	for _, want := range []string{"red", "url(/img/logo.png)", "https://fonts.example/css?family=Inter", "margin"} {
		if !strings.Contains(css, want) {
			t.Errorf("css lacks %s: %s", want, css)
		}
	}
}

func TestCompile_SyntaxErrorNamesFileLineAndColumn(t *testing.T) {
	cfg := mustParse(t, `{"entries":["app.tsx"]}`)
	src := tree(map[string]string{"app.tsx": `import { T } from "./Table"; T();`, "Table.tsx": "export function T() {\n  const x = ;\n  return x;\n}\n"})
	_, err := Compile(context.Background(), src, "app.tsx", Options{Config: cfg})
	var be *Error
	if !errors.As(err, &be) || len(be.Messages) == 0 {
		t.Fatalf("err = %v", err)
	}
	m := be.Messages[0]
	if m.File != "Table.tsx" || m.Line != 2 || m.Column == 0 {
		t.Fatalf("message = %+v", m)
	}
}

func TestCompile_Refusals(t *testing.T) {
	cases := map[string]struct {
		files map[string]string
		cfg   string
		want  string
	}{
		"unmapped bare":    {map[string]string{"app.tsx": `import x from "lodash"; x();`}, `{"entries":["app.tsx"]}`, `imports "lodash"`},
		"climbs out":       {map[string]string{"app.tsx": `import x from "../../etc/passwd"; x();`}, `{"entries":["app.tsx"]}`, "outside the site"},
		"climbs from dir":  {map[string]string{"a/app.tsx": `import x from "../../x"; x();`, "x.ts": "export default 1"}, `{"entries":["a/app.tsx"]}`, "outside the site"},
		"unpinned url":     {map[string]string{"app.tsx": `import x from "https://esm.sh/react@18"; x();`}, `{"entries":["app.tsx"]}`, "exact version"},
		"http url":         {map[string]string{"app.tsx": `import x from "http://esm.sh/react@18.3.1"; x();`}, `{"entries":["app.tsx"]}`, "by its https URL"},
		"missing file":     {map[string]string{"app.tsx": `import x from "./nope"; x();`}, `{"entries":["app.tsx"]}`, "no file nope"},
		"missing entry":    {map[string]string{}, `{"entries":["app.tsx"]}`, "no file app.tsx"},
		"unloadable":       {map[string]string{"app.tsx": `import x from "./a.png"; x();`, "a.png": "x"}, `{"entries":["app.tsx"]}`, "cannot be imported"},
		"bundle no fetch":  {map[string]string{"app.tsx": `import x from "react"; x();`}, `{"entries":["app.tsx"],"remote":"bundle","imports":{"react":"` + reactURL + `"}}`, "cannot fetch"},
		"absolute outside": {map[string]string{"app.tsx": `import x from "/etc/passwd"; x();`}, `{"entries":["app.tsx"]}`, "no file etc/passwd"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			entry := mustParse(t, tc.cfg).Entries[0]
			_, err := Compile(context.Background(), tree(tc.files), entry, Options{Config: mustParse(t, tc.cfg)})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestCompile_Limits(t *testing.T) {
	cfg := mustParse(t, `{"entries":["app.ts"]}`)
	src := tree(map[string]string{"app.ts": `import "./a"; import "./b";`, "a.ts": "console.log(1);", "b.ts": "console.log(2);"})
	cases := map[string]Limits{"modules": {Modules: 2}, "bytes in one module": {FileBytes: 5}, "bytes of modules": {InputBytes: 30}, "bytes of output": {OutputBytes: 1}}
	for resource, limits := range cases {
		t.Run(resource, func(t *testing.T) {
			_, err := Compile(context.Background(), src, "app.ts", Options{Config: cfg, Limits: limits, Fetch: nil})
			var le *LimitError
			if !errors.As(err, &le) || le.Resource != resource {
				t.Fatalf("err = %v, want the %s limit", err, resource)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Compile(ctx, src, "app.ts", Options{Config: cfg}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled: %v", err)
	}
}

type fakeFetch map[string]string

func (f fakeFetch) Fetch(_ context.Context, u string) (body []byte, contentType string, err error) {
	text, ok := f[u]
	if !ok {
		return nil, "", errors.New("not found")
	}
	return []byte(text), "text/javascript", nil
}

func TestCompile_BundleFetchesURLModules(t *testing.T) {
	cfg := mustParse(t, `{"entries":["app.ts"],"remote":"bundle","imports":{"lib":"https://cdn.example/lib@1.0.0"}}`)
	fetch := fakeFetch{
		"https://cdn.example/lib@1.0.0":            `export * from "/lib@1.0.0/es/lib.mjs";`,
		"https://cdn.example/lib@1.0.0/es/lib.mjs": `export const greet = () => "from-the-cdn";`,
	}
	res, err := Compile(context.Background(), tree(map[string]string{"app.ts": `import { greet } from "lib"; console.log(greet());`}), "app.ts", Options{Config: cfg, Limits: Limits{}, Fetch: fetch})
	if err != nil {
		t.Fatal(err)
	}
	js := output(t, res, "app.js")
	if !strings.Contains(js, "from-the-cdn") || strings.Contains(js, `from "https`) {
		t.Fatalf("app.js did not bundle the module:\n%s", js)
	}
}

func TestTheCompilerIsTheReviewedVersion(t *testing.T) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		t.Skip("no build info")
	}
	for _, dep := range info.Deps {
		if dep.Path == "github.com/evanw/esbuild" && dep.Version != reviewedCompiler {
			t.Fatalf("esbuild is %s and the build options were written against %s", dep.Version, reviewedCompiler)
		}
	}
}

type typedFetch map[string][2]string

func (f typedFetch) Fetch(_ context.Context, u string) (body []byte, contentType string, err error) {
	got, ok := f[u]
	if !ok {
		return nil, "", errors.New("gone")
	}
	return []byte(got[0]), got[1], nil
}

func TestCompile_URLModuleImports(t *testing.T) {
	cfg := mustParse(t, `{"entries":["app.ts"],"remote":"bundle","imports":{"lib":"https://cdn.example/lib@1.0.0"}}`)
	src := tree(map[string]string{"app.ts": `import "lib";`})
	cases := map[string]struct {
		fetch typedFetch
		want  string
	}{
		"bare import in a module": {typedFetch{"https://cdn.example/lib@1.0.0": {`import "other";`, "text/javascript"}}, "neither a URL nor a path"},
		"http import in a module": {typedFetch{"https://cdn.example/lib@1.0.0": {`import "http://cdn.example/x.js";`, "text/javascript"}}, "neither a URL nor a path"},
		"module gone":             {typedFetch{}, "fetching https://cdn.example/lib@1.0.0: gone"},
		"stylesheet module":       {typedFetch{"https://cdn.example/lib@1.0.0": {`.x{color:blue}`, "text/css"}}, ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			res, err := Compile(context.Background(), src, "app.ts", Options{Config: cfg, Limits: Limits{}, Fetch: tc.fetch})
			if tc.want == "" {
				if err != nil || !strings.Contains(output(t, res, "app.css"), "blue") {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}
