//go:build integration

package acceptance

import (
	"encoding/json"
	"testing"
	"time"
)

// Issue #2830: a site type check crashed the compiler ("wasm error:
// unreachable") whenever the tsconfig's include named a file rather than a
// glob, and the report kept only the runtime's anonymous stack. The compiler
// panicked with "path must not be empty": the release was mounted at /, and
// the directory of /app.ts is empty. It is now mounted at /project, and a
// crash is reported with the compiler's own panic text (unit-tested; no input
// crashes the compiler now).
//
// Wire forms: manage_site's command, site and note are strings and files a
// list of objects whose path and content are strings; the portal typecheck
// route is a GET with no body; the tsconfig's include is a JSON array of
// strings, sent naming a file, a file with ./, two files, and a directory.

// files2830 is a site whose tsconfig includes what include names, with one
// type error in each of two files.
func files2830(include string) []map[string]any {
	return []map[string]any{
		{"path": "_build.json", "content": `{"entries":["app.ts"],"typecheck":{}}`},
		{"path": "tsconfig.json", "content": `{"compilerOptions":{"target":"ES2022","module":"ESNext","moduleResolution":"bundler","strict":true,"noEmit":true,"lib":["ES2022","DOM"]},"include":` + include + `}`},
		{"path": "app.ts", "content": "export const total: number = \"seven\";\n"},
		{"path": "src/more.ts", "content": "export const count: number = \"eight\";\n"},
		{"path": "index.html", "content": `<!doctype html><script type="module" src="/app.js"></script>`},
	}
}

// TestIssue2830_AnIncludeNamingFilesChecksLikeAGlob: each include form checks
// and reports the error in each file it names, at its line and column.
func TestIssue2830_AnIncludeNamingFilesChecksLikeAGlob(t *testing.T) {
	// Each check waits its turn and compiles the compiler afresh (no
	// compilation cache is kept on disk, #2828), so the connection outlives the
	// default session bound (#1738).
	c := connectFor(t, 20*time.Minute)
	for include, want := range map[string][]string{
		`["app.ts"]`:               {"app.ts"},
		`["./app.ts"]`:             {"app.ts"},
		`["app.ts","src/more.ts"]`: {"app.ts", "src/more.ts"},
		`["src"]`:                  {"src/more.ts"},
	} {
		site := newSite2817(t, c)
		release, _ := publish2793(t, c, site, files2830(include))
		check := check2793(t, c, site, release, 0, 6*time.Minute)
		if check["status"] != "errors" {
			t.Errorf("include %s: the check = %v", include, check)
			continue
		}
		raw, _ := json.Marshal(result2793(check)["diagnostics"])
		var diags []struct {
			File   string `json:"file"`
			Line   int    `json:"line"`
			Column int    `json:"column"`
			Code   string `json:"code"`
		}
		_ = json.Unmarshal(raw, &diags)
		got := map[string]bool{}
		for _, d := range diags {
			if d.Code != "TS2322" || d.Line != 1 || d.Column != 14 {
				t.Errorf("include %s: diagnostic %+v, want TS2322 at 1:14", include, d)
			}
			got[d.File] = true
		}
		if len(got) != len(want) {
			t.Errorf("include %s: errors in %v, want %v", include, got, want)
		}
		for _, f := range want {
			if !got[f] {
				t.Errorf("include %s: no error reported in %s: %v", include, f, diags)
			}
		}
	}
}
