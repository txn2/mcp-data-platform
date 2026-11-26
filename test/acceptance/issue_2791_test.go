//go:build integration

package acceptance

import (
	"encoding/json"
	"strings"
	"testing"
)

// Acceptance for #2791: a release's build report describes what loads when
// the page starts, the entry and the chunks it imports statically: a URL only
// a lazily loaded page imports is not in start_imports, a URL only the shared
// chunk the entry imports statically imports is; start_bytes is the entry
// plus that chunk and a js budget is judged against it; a module reached only
// through import() is not among largest_inputs.
//
// Wire forms: manage_site takes command, site and note as strings, release as
// a number, and files as a list of {path, content} objects whose content is
// utf-8 text; _build.json's budgets is an object of objects of strings.

func project2791() []map[string]any {
	return []map[string]any{
		{"path": "_build.json", "content": `{"entries":["app.tsx"],"budgets":{"app.tsx":{"js":"10"}}}`},
		{"path": "app.tsx", "content": `import { shared } from "./shared"; const A = () => import("./pages/A"); const B = () => import("./pages/B"); export const v = [shared, A, B];`},
		{"path": "shared.ts", "content": `import y from "https://esm.sh/y@1.0.0"; export const shared = ["shared body 2791", y];`},
		{"path": "pages/A.tsx", "content": `import x from "https://esm.sh/x@1.0.0"; import { shared } from "../shared"; export default ["` + strings.Repeat("lazy page A ", 200) + `", x, shared];`},
		{"path": "pages/B.tsx", "content": `import { shared } from "../shared"; export default ["page B", shared];`},
		{"path": "index.html", "content": `<!doctype html><script type="module" src="/app.js"></script>`},
	}
}

type report2791 struct {
	Outputs []struct {
		Path  string `json:"path"`
		Bytes int64  `json:"bytes"`
		Chunk bool   `json:"chunk"`
	} `json:"outputs"`
	LargestInputs     []struct{ Path string } `json:"largest_inputs"`
	LargestInputsLazy []struct{ Path string } `json:"largest_inputs_lazy"`
	StartImports      []string                `json:"start_imports"`
	StartBytes        int64                   `json:"start_bytes"`
	StartGzipBytes    int64                   `json:"start_gzip_bytes"`
	Warnings          []string                `json:"warnings"`
}

// TestIssue2791_TheReportDescribesWhatLoadsAtStart: every criterion, read off
// manage_site build_report for the published release.
func TestIssue2791_TheReportDescribesWhatLoadsAtStart(t *testing.T) {
	c := connect(t)
	s := newSite2518(t, c)
	c.call("manage_site", map[string]any{"command": "publish", "site": s.name, "note": "start set", "files": project2791()})
	got := c.call("manage_site", map[string]any{"command": "build_report", "site": s.name, "release": 1})
	raw, _ := json.Marshal(got["builds"])
	var builds []report2791
	if err := json.Unmarshal(raw, &builds); err != nil || len(builds) != 1 {
		t.Fatalf("build_report: %v %s", err, raw)
	}
	rep := builds[0]
	if strings.Contains(strings.Join(rep.StartImports, " "), "https://esm.sh/x@1.0.0") {
		t.Errorf("a URL only a lazy page imports is a start import: %v", rep.StartImports)
	}
	if !strings.Contains(strings.Join(rep.StartImports, " "), "https://esm.sh/y@1.0.0") {
		t.Errorf("a URL the statically imported shared chunk imports is not a start import: %v", rep.StartImports)
	}
	// The entry, plus the one chunk that is neither page: the shared code.
	var entry, chunks int64
	var pageChunks int
	for _, o := range rep.Outputs {
		switch {
		case o.Path == "app.js":
			entry = o.Bytes
		case o.Chunk && strings.HasSuffix(o.Path, ".js") && (strings.Contains(o.Path, "/A-") || strings.Contains(o.Path, "/B-")):
			pageChunks++
		case o.Chunk && strings.HasSuffix(o.Path, ".js"):
			chunks += o.Bytes
		}
	}
	if pageChunks != 2 || rep.StartBytes != entry+chunks || chunks == 0 {
		t.Errorf("start_bytes %d, want app.js %d plus the shared chunk %d (outputs %+v)", rep.StartBytes, entry, chunks, rep.Outputs)
	}
	if rep.StartGzipBytes == 0 {
		t.Error("start_gzip_bytes is 0")
	}
	if len(rep.Warnings) != 1 || !strings.Contains(rep.Warnings[0], "chunk(s) it imports at start") {
		t.Errorf("the js budget is not judged against what loads at start: %v", rep.Warnings)
	}
	for _, in := range rep.LargestInputs {
		if strings.HasPrefix(in.Path, "pages/") {
			t.Errorf("a lazily loaded page is among largest_inputs: %+v", rep.LargestInputs)
		}
	}
	lazy := false
	for _, in := range rep.LargestInputsLazy {
		lazy = lazy || in.Path == "pages/A.tsx"
	}
	if !lazy {
		t.Errorf("the lazy page is not among largest_inputs_lazy: %+v", rep.LargestInputsLazy)
	}
}
