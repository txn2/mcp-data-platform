package siterender

import (
	"context"
	"errors"
	"io/fs"
	"slices"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/site/sitebuild"
)

// TestAKeptOutputRefusesAFileAddedAtItsPath is #2777: an overlay adding app.js
// beside a kept app.tsx changes none of the entry's inputs, and is refused
// naming both instead of recording two files at one path.
func TestAKeptOutputRefusesAFileAddedAtItsPath(t *testing.T) {
	files := builtSite()
	_, idx := renderTree(t, files)
	files["app.js"] = "plain"
	_, err := Render(context.Background(), tree(files), Options{Previous: idx, Changed: []string{"app.js"}}, newCapture())
	if err == nil || !strings.Contains(err.Error(), "compiles to app.js, which the release also holds as a file") {
		t.Fatalf("err = %v", err)
	}
}

func TestWhatAReleaseNeverServes(t *testing.T) {
	files := builtSite()
	for name, body := range map[string]string{
		"_includes/README.md": "notes", "_data/x.json": "{}", "AGENTS.md": "guide", "package.json": "{}", "pnpm-lock.yaml": "x: 1",
		"tsconfig.json": "{}", "vite.config.ts": "export default {}", ".nvmrc": "22", "e2e/README.md": "e2e", "docs/a.md": "kept",
	} {
		files[name] = body //nolint:modernize // a literal map read once
	}
	files[sitebuild.ControlFile] = strings.TrimSuffix(buildFile, "}") + `,"exclude":["e2e/**"]}`
	c, _ := renderTree(t, files)
	slices.Sort(c.copies)
	if want := []string{"docs/a.md", "index.html", "style.css"}; !slices.Equal(c.copies, want) {
		t.Errorf("copied %v, want %v", c.copies, want)
	}

	plain := map[string]string{"index.html": "<p>hi</p>", "AGENTS.md": "guide", "package.json": "{}"}
	if !Active(tree(plain)) {
		t.Fatal("a tree with a guide is not rendered, so it would be served")
	}
	c, _ = renderTree(t, plain)
	slices.Sort(c.copies)
	if want := []string{"index.html", "package.json"}; !slices.Equal(c.copies, want) {
		t.Errorf("a tree without a build file copied %v, want %v: its package.json is served as before", c.copies, want)
	}
}

func TestAFileUnderChunksIsRefused(t *testing.T) {
	files := builtSite()
	files["_chunks/x.js"] = "x"
	_, err := Render(context.Background(), tree(files), Options{}, newCapture())
	if err == nil || !strings.Contains(err.Error(), "_chunks/x.js") {
		t.Fatalf("err = %v", err)
	}
	files[sitebuild.ControlFile] = strings.TrimSuffix(buildFile, "}") + `,"splitting":false}`
	renderTree(t, files)
}

func TestChunksReportsAndSharedChunks(t *testing.T) {
	lazy := `export const load = () => import("./shared/Page");`
	files := map[string]string{
		sitebuild.ControlFile: `{"entries":["a.tsx","b.tsx"],"sourcemap":"private"}`,
		"a.tsx":               lazy, "b.tsx": lazy, "shared/Page.tsx": `export default "the shared page";`,
	}
	c, idx := renderTree(t, files)
	var chunks []string
	for p := range c.files {
		if strings.HasPrefix(p, sitebuild.ChunkDir+"/") && !strings.HasSuffix(p, ".map") {
			chunks = append(chunks, p)
		}
	}
	if len(chunks) != 1 {
		t.Fatalf("chunks %v, want the one both entries share", chunks)
	}
	if len(idx.Builds) != 2 || !idx.PrivateMaps {
		t.Fatalf("builds %d private %v", len(idx.Builds), idx.PrivateMaps)
	}

	// b changes; a is kept with its chunk, and b's report compares with the
	// render before.
	files["b.tsx"] = lazy + "\nconsole.log('b grew by a line');"
	c2 := newCapture()
	idx2, err := Render(context.Background(), tree(files), Options{Previous: idx, Changed: []string{"b.tsx"}}, c2)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(c2.kept, "a.js") || !slices.Contains(c2.kept, chunks[0]) {
		t.Errorf("kept %v", c2.kept)
	}
	var changed bool
	for _, b := range idx2.Builds {
		for _, o := range b.Outputs {
			changed = changed || (b.Entry == "b.tsx" && o.Path == "b.js" && strings.Contains(o.Change, "(+"))
		}
	}
	if !changed {
		t.Errorf("b.js shows no change: %+v", idx2.Builds)
	}
}

func TestReleaseMetaRecompilesEveryRelease(t *testing.T) {
	files := map[string]string{sitebuild.ControlFile: `{"entries":["app.tsx"]}`, "app.tsx": `console.log(import.meta.site.release);`}
	c := newCapture()
	idx, err := Render(context.Background(), tree(files), Options{Site: &sitebuild.SiteMeta{Release: 3, Site: "s"}}, c)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c.files["app.js"], "3") {
		t.Fatalf("app.js %s", c.files["app.js"])
	}
	c = newCapture()
	if _, err := Render(context.Background(), tree(files), Options{Previous: idx, Site: &sitebuild.SiteMeta{Release: 4, Site: "s"}}, c); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c.files["app.js"], "4") {
		t.Errorf("app.js kept the earlier release: %v %v", c.files, c.kept)
	}
}

type fakeStyles struct{}

func (fakeStyles) CompileStylesheet(_ context.Context, _ string, body []byte) (out []byte, changed bool, err error) {
	if !strings.Contains(string(body), `@import "tailwindcss"`) {
		return body, false, nil
	}
	return []byte(".compiled{color:red}"), true, nil
}

func TestTailwindNeedsACompiler(t *testing.T) {
	files := map[string]string{
		sitebuild.ControlFile: `{"entries":["app.tsx"],"tailwind":"4.1.13"}`,
		"app.tsx":             `import "./styles.css";`, "styles.css": `@import "tailwindcss";`,
	}
	_, err := Render(context.Background(), tree(files), Options{}, newCapture())
	if err == nil || !strings.Contains(err.Error(), "cannot compile Tailwind") {
		t.Fatalf("err = %v", err)
	}
	c := newCapture()
	_, err = Render(context.Background(), tree(files), Options{Stylesheets: func(context.Context, *sitebuild.Config, fs.FS) (sitebuild.StylesheetCompiler, error) {
		return fakeStyles{}, nil
	}}, c)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c.files["app.css"], ".compiled") {
		t.Errorf("app.css %q", c.files["app.css"])
	}
	_, err = Render(context.Background(), tree(files), Options{Stylesheets: func(context.Context, *sitebuild.Config, fs.FS) (sitebuild.StylesheetCompiler, error) {
		return nil, errors.New("tailwind 4.1.13 does not exist")
	}}, newCapture())
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("err = %v", err)
	}
}

func TestSingletonsAndImportsFrom(t *testing.T) {
	files := map[string]string{
		sitebuild.ControlFile: `{"entries":["app.tsx"],"imports":{"react":"https://esm.sh/react@18.3.1","react/jsx-runtime":"https://esm.sh/react@18.2.0/jsx-runtime"}}`,
		"app.tsx":             `export const p = <p />;`,
	}
	_, err := Render(context.Background(), tree(files), Options{}, newCapture())
	if err == nil || !strings.Contains(err.Error(), "two copies of react") || !strings.Contains(err.Error(), `"react/jsx-runtime"`) {
		t.Fatalf("err = %v", err)
	}
	files = map[string]string{
		sitebuild.ControlFile: `{"entries":["app.tsx"],"imports":{"react":"https://esm.sh/react@18.3.1","react-dom/client":"https://esm.sh/react-dom@18.3.1/client","react-router-dom":"https://esm.sh/react-router-dom@6.28.0"}}`,
		"app.tsx":             `import { BrowserRouter } from "react-router-dom"; export const r = <BrowserRouter />;`,
		"package-lock.json": `{"lockfileVersion":3,"packages":{"node_modules/react":{"version":"18.3.1"},` +
			`"node_modules/react-dom":{"version":"18.3.1","peerDependencies":{"react":"^18.3.1"}},` +
			`"node_modules/react-router-dom":{"version":"6.28.0","peerDependencies":{"react":">=16.8"}}}}`,
	}
	_, err = Render(context.Background(), tree(files), Options{}, newCapture())
	if err == nil || !strings.Contains(err.Error(), "loads its own copy of react") || !strings.Contains(err.Error(), "deps=react@18.3.1") {
		t.Fatalf("err = %v", err)
	}
	files = map[string]string{
		sitebuild.ControlFile: `{"entries":["app.tsx"],"imports_from":"package.json"}`,
		"app.tsx":             `import { createRoot } from "react-dom/client"; createRoot(document.body).render(<p />);`,
		"package.json":        `{"dependencies":{"react":"^18.3.1","react-dom":"^18.3.1"}}`,
		"package-lock.json": `{"lockfileVersion":3,"packages":{"":{},"node_modules/react":{"version":"18.3.1"},` +
			`"node_modules/react-dom":{"version":"18.3.1","peerDependencies":{"react":"^18.3.1"}}}}`,
	}
	c, _ := renderTree(t, files)
	js := c.files["app.js"]
	for _, want := range []string{"https://esm.sh/react-dom@18.3.1/client?deps=react@18.3.1", "https://esm.sh/react@18.3.1/jsx-runtime"} {
		if !strings.Contains(js, want) {
			t.Errorf("app.js lacks %s:\n%s", want, js)
		}
	}
}

type registryFetch map[string]string

func (r registryFetch) Fetch(_ context.Context, u string) (body []byte, contentType string, err error) {
	if held, ok := r[u]; ok {
		return []byte(held), "application/json", nil
	}
	return nil, "", errors.New("404")
}

func TestImportsFromRefusals(t *testing.T) {
	app := `import { createRoot } from "react-dom/client"; import x from "left-pad"; createRoot(document.body).render(<p>{x}</p>);`
	for name, tc := range map[string]struct {
		files map[string]string
		want  string
	}{
		"no lockfile": {map[string]string{sitebuild.ControlFile: `{"entries":["app.tsx"],"imports_from":"package.json"}`, "app.tsx": app}, "has no package-lock.json"},
		"unlocked": {map[string]string{
			sitebuild.ControlFile: `{"entries":["app.tsx"],"imports_from":"package.json"}`, "app.tsx": app,
			"package-lock.json": `{"lockfileVersion":3,"packages":{"node_modules/react":{"version":"18.3.1"},"node_modules/react-dom":{"version":"18.3.1"}}}`,
		}, "locks no version of left-pad"},
		"bad lockfile": {map[string]string{sitebuild.ControlFile: `{"entries":["app.tsx"],"imports_from":"package.json"}`, "app.tsx": app, "package-lock.json": "{"}, "package-lock.json"},
		"bad import":   {map[string]string{sitebuild.ControlFile: `{"entries":["app.tsx"],"imports_from":"package.json"}`, "app.tsx": `import x from "../../x"; console.log(x);`, "package-lock.json": `{"lockfileVersion":3,"packages":{}}`}, "outside the site"},
	} {
		_, err := Render(context.Background(), tree(tc.files), Options{}, newCapture())
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", name, err, tc.want)
		}
	}
	// A registry that knows a package's peers judges a URL with no lockfile;
	// one that does not answer leaves them unjudged.
	files := map[string]string{
		sitebuild.ControlFile: `{"entries":["app.tsx"],"imports":{"react":"https://esm.sh/react@18.3.1","x":"https://esm.sh/x@1.0.0"}}`,
		"app.tsx":             `import x from "x"; export const r = <p>{x}</p>;`,
	}
	_, err := Render(context.Background(), tree(files), Options{Fetch: registryFetch{"https://registry.npmjs.org/x/1.0.0": `{"peerDependencies":{"react":"*"}}`}}, newCapture())
	if err == nil || !strings.Contains(err.Error(), "own copy of react") {
		t.Errorf("a registry's peers were not read: %v", err)
	}
	if _, err := Render(context.Background(), tree(files), Options{Fetch: registryFetch{}}, newCapture()); err != nil {
		t.Errorf("a registry that does not answer refused the publish: %v", err)
	}
}
