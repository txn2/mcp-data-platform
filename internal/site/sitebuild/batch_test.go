package sitebuild

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

func compileOne(t *testing.T, doc string, files map[string]string, opts Options) (*Result, error) {
	t.Helper()
	opts.Config = mustParse(t, doc)
	files[ControlFile] = doc
	return Compile(context.Background(), tree(files), "app.tsx", opts)
}

func outputPaths(res *Result) []string {
	out := make([]string, 0, len(res.Outputs))
	for _, o := range res.Outputs {
		out = append(out, o.Path)
	}
	return out
}

const lazyApp = `const A = () => import("./pages/A");
const B = () => import("./pages/B");
export const routes = { a: A, b: B };
`

func TestCompile_SplitsAtDynamicImports(t *testing.T) {
	files := map[string]string{"app.tsx": lazyApp, "pages/A.tsx": `export default "page A body";`, "pages/B.tsx": `export default "page B body";`}
	res, err := compileOne(t, `{"entries":["app.tsx"]}`, files, Options{})
	if err != nil {
		t.Fatal(err)
	}
	js := output(t, res, "app.js")
	if strings.Contains(js, "page A body") || strings.Contains(js, "page B body") {
		t.Fatalf("app.js inlined a lazy page:\n%s", js)
	}
	var chunks []string
	for _, p := range outputPaths(res) {
		if strings.HasPrefix(p, ChunkDir+"/") {
			chunks = append(chunks, p)
		}
	}
	if len(chunks) != 2 {
		t.Fatalf("chunks %v, want one per page", chunks)
	}
	for _, c := range chunks {
		if !strings.Contains(js, "./"+strings.TrimPrefix(c, "")) && !strings.Contains(js, c) {
			t.Errorf("app.js does not load %s:\n%s", c, js)
		}
	}
	if res.Report == nil || len(res.Report.Outputs) != 3 {
		t.Fatalf("report %+v", res.Report)
	}

	// Changing only B leaves app.js and A's chunk byte for byte.
	files2 := map[string]string{"app.tsx": lazyApp, "pages/A.tsx": `export default "page A body";`, "pages/B.tsx": `export default "page B body changed";`}
	res2, err := compileOne(t, `{"entries":["app.tsx"]}`, files2, Options{})
	if err != nil {
		t.Fatal(err)
	}
	aChunk := func(r *Result) string {
		for _, o := range r.Outputs {
			if strings.HasPrefix(o.Path, ChunkDir+"/A-") {
				return o.Path + string(o.Body)
			}
		}
		return ""
	}
	if aChunk(res) == "" || aChunk(res) != aChunk(res2) {
		t.Errorf("A's chunk changed with B")
	}
	if slices.Equal(outputPaths(res), outputPaths(res2)) {
		t.Errorf("B's chunk kept its name after its content changed: %v", outputPaths(res2))
	}
}

func TestCompile_SplittingOffAndNoDynamicImportsMatch(t *testing.T) {
	files := func() map[string]string {
		return map[string]string{"app.tsx": `import {x} from "./x"; console.log(x);`, "x.ts": `export const x = 1;`}
	}
	on, err := compileOne(t, `{"entries":["app.tsx"]}`, files(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	off, err := compileOne(t, `{"entries":["app.tsx"],"splitting":false}`, files(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if output(t, on, "app.js") != output(t, off, "app.js") || len(on.Outputs) != 1 || len(off.Outputs) != 1 {
		t.Errorf("a site with no dynamic import compiles differently with splitting: %v vs %v", outputPaths(on), outputPaths(off))
	}
	lazy, err := compileOne(t, `{"entries":["app.tsx"],"splitting":false}`,
		map[string]string{"app.tsx": lazyApp, "pages/A.tsx": `export default "A";`, "pages/B.tsx": `export default "B";`}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(lazy.Outputs) != 1 {
		t.Errorf("splitting false wrote %v", outputPaths(lazy))
	}
}

func TestCompile_DefineAndUnsetEnvRead(t *testing.T) {
	src := `if (import.meta.env.DEV) { console.log("dev only branch"); } console.log(import.meta.env.API);`
	_, err := compileOne(t, `{"entries":["app.tsx"],"define":{"import.meta.env.DEV":false}}`, map[string]string{"app.tsx": src}, Options{})
	var be *Error
	if !errors.As(err, &be) || len(be.Messages) != 1 || be.Messages[0].Line != 1 || !strings.Contains(be.Messages[0].Text, "import.meta.env.API") {
		t.Fatalf("unset read: %v", err)
	}
	res, err := compileOne(t, `{"entries":["app.tsx"],"minify":true,"define":{"import.meta.env.DEV":false,"import.meta.env.API":"/api"}}`,
		map[string]string{"app.tsx": src + "\n// import.meta.env.COMMENTED\nconst s = \"import.meta.env.QUOTED\";"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	js := output(t, res, "app.js")
	if strings.Contains(js, "dev only branch") || !strings.Contains(js, "/api") {
		t.Errorf("define not applied:\n%s", js)
	}
	_, err = compileOne(t, `{"entries":["app.tsx"]}`, map[string]string{"app.tsx": "const env = import.meta.env;\nconsole.log(`${import.meta.env.X}`);"}, Options{})
	if !errors.As(err, &be) || len(be.Messages) != 2 || be.Messages[1].Line != 2 {
		t.Fatalf("whole and template reads: %v", err)
	}
}

func TestCompile_LocalPrefix(t *testing.T) {
	res, err := compileOne(t, `{"entries":["app.tsx"],"imports":{"@/":"/src/"}}`,
		map[string]string{"app.tsx": `import { x } from "@/lib/x"; console.log(x);`, "src/lib/x.ts": `export const x = "from src lib";`}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output(t, res, "app.js"), "from src lib") || !slices.Contains(res.Inputs, "src/lib/x.ts") {
		t.Errorf("prefix not resolved locally: %v", res.Inputs)
	}
	for _, bad := range []string{`{"entries":["app.tsx"],"imports":{"@/":"/../"}}`, `{"entries":["app.tsx"],"imports":{"@/":"src/"}}`} {
		if _, err := Parse([]byte(bad)); err == nil || !strings.Contains(err.Error(), `"@/"`) {
			t.Errorf("%s: %v", bad, err)
		}
	}
}

func TestCompile_StylesheetImportedByURLBecomesAnImport(t *testing.T) {
	res, err := compileOne(t, `{"entries":["app.tsx"],"imports":{"ed/":"https://esm.sh/ed@1.2.3/"}}`,
		map[string]string{"app.tsx": `import "ed/dist/style.css"; console.log(1);`}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if js := output(t, res, "app.js"); strings.Contains(js, ".css") {
		t.Errorf("app.js imports a stylesheet:\n%s", js)
	}
	if css := output(t, res, "app.css"); !strings.Contains(css, `@import "https://esm.sh/ed@1.2.3/dist/style.css"`) {
		t.Errorf("app.css lacks the @import:\n%s", css)
	}
}

func TestCompile_SiteMetaAndPrivateMaps(t *testing.T) {
	res, err := compileOne(t, `{"entries":["app.tsx"],"sourcemap":"private"}`,
		map[string]string{"app.tsx": `console.log("r" + import.meta.site.release);`},
		Options{Site: &SiteMeta{Release: 7, PublishedAt: "2026-11-24T00:00:00Z", Site: "docs"}})
	if err != nil {
		t.Fatal(err)
	}
	if js := output(t, res, "app.js"); !strings.Contains(js, "7") || !strings.Contains(js, "sourceMappingURL=app.js.map") {
		t.Errorf("app.js:\n%s", js)
	}
	output(t, res, "app.js.map")
	if !slices.Contains(res.Inputs, SiteMetaInput) {
		t.Errorf("inputs %v lack the release marker", res.Inputs)
	}
	cfg := mustParse(t, `{"entries":["app.tsx"],"sourcemap":"private"}`)
	if cfg.Sourcemap != SourcemapPrivate {
		t.Errorf("sourcemap %q", cfg.Sourcemap)
	}
	if _, err := Parse([]byte(`{"entries":["app.tsx"],"sourcemap":"yes"}`)); err == nil {
		t.Error("sourcemap yes accepted")
	}
}

func TestReport_BudgetsAndCompare(t *testing.T) {
	res, err := compileOne(t, `{"entries":["app.tsx"],"budgets":{"app.tsx":{"js":"10B"}}}`,
		map[string]string{"app.tsx": `import {x} from "./x"; console.log(x, "padding padding padding");`, "x.ts": `export const x = 1;`}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	rep := res.Report
	if len(rep.Warnings) != 1 || !strings.Contains(rep.Warnings[0], "budget") {
		t.Errorf("warnings %v", rep.Warnings)
	}
	if len(rep.LargestInputs) == 0 || rep.LargestInputs[0].Path == "" {
		t.Errorf("largest %v", rep.LargestInputs)
	}
	rep.Compare([]Report{{Outputs: []OutputSize{{Path: "app.js", Bytes: 1}}}})
	if rep.Outputs[0].PreviousBytes == nil || !strings.Contains(rep.Outputs[0].Change, "(+") {
		t.Errorf("compare %+v", rep.Outputs[0])
	}
	if stableName("_chunks/Reports-AB12CD34.js") != "_chunks/Reports.js" || stableName("app.js") != "app.js" {
		t.Error("stableName")
	}
}

func TestOptions_Validation(t *testing.T) {
	for _, bad := range []string{
		`{"entries":["app.tsx"],"define":{"window.X":1}}`,
		`{"entries":["app.tsx"],"define":{"import.meta.env.X":{"a":1}}}`,
		`{"entries":["app.tsx"],"tailwind":"3.4.0"}`,
		`{"entries":["app.tsx"],"tailwind_input":"a.css"}`,
		`{"entries":["app.tsx"],"budgets":{"other.tsx":{"js":1}}}`,
		`{"entries":["app.tsx"],"imports_from":"yarn"}`,
		`{"entries":["app.tsx"],"singletons":["Bad Name"]}`,
		`{"entries":["app.tsx"],"exclude":["../x"]}`,
	} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	cfg := mustParse(t, `{"entries":["app.tsx"],"exclude":["e2e/**","docs/*.md"],"tailwind":"4.1.13","budgets":{"app.tsx":{"js_gzip":"300KB"}}}`)
	for name, want := range map[string]bool{"e2e/README.md": true, "e2e/a/b.ts": true, "docs/x.md": true, "docs/a/x.md": false, "src/e2e/x": false} {
		if cfg.Excluded(name) != want {
			t.Errorf("Excluded(%s) = %v", name, !want)
		}
	}
	if cfg.Budgets["app.tsx"].JSGzip != 300*1024 {
		t.Errorf("budget %d", cfg.Budgets["app.tsx"].JSGzip)
	}
	for name, want := range map[string]bool{
		"package.json": true, "pnpm-lock.yaml": true, "tsconfig.app.json": true, "vite.config.ts": true, ".eslintrc.cjs": true,
		".nvmrc": true, "src/package.json": false, "index.html": false, ".npmrc": false, "config.ts": false,
	} {
		if IsProjectFile(name) != want {
			t.Errorf("IsProjectFile(%s) = %v", name, !want)
		}
	}
}
