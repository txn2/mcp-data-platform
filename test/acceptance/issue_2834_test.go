//go:build integration

package acceptance

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #2834: a site type check whose compiler reported a diagnostic with no
// file location (TS18003 for a tsconfig include that matches no file, TS6053
// for a missing extends target, TS5110 for a refused option) kept none of it
// and answered failed, "the compiler exited 2 and reported nothing". Such a
// diagnostic is now one with no file, line or column, and the check answers
// errors with it; output with no diagnostic at all is quoted in the failure
// (unit-tested: the pinned compiler writes every refusal as a diagnostic).
//
// Wire forms: manage_site's command, site and note are strings, release a
// number, and files a list of objects whose path and content are strings;
// the portal typecheck route is a GET with no body; the tsconfig's include is
// a JSON array of strings. No parameter touched admits a second form.

// files2834 is a site whose tsconfig includes no file it has.
func files2834() []map[string]any {
	return []map[string]any{
		{"path": "_build.json", "content": `{"entries":["app.ts"],"typecheck":{}}`},
		{"path": "tsconfig.json", "content": `{"compilerOptions":{"strict":true,"noEmit":true,"lib":["ES2022","DOM"]},"include":["nothing-here/**/*.ts"]}`},
		{"path": "app.ts", "content": "export const total: number = 7;\n"},
		{"path": "index.html", "content": `<!doctype html><script type="module" src="/app.js"></script>`},
	}
}

// diag2834 is one diagnostic as a check reports it.
type diag2834 struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Column  int    `json:"column"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// TestIssue2834_AnIncludeMatchingNoFileReportsTS18003: the check answers
// errors with TS18003 and the compiler's own sentence, no file, line or
// column, through the portal route and manage_site build_report alike.
func TestIssue2834_AnIncludeMatchingNoFileReportsTS18003(t *testing.T) {
	// The check waits its turn and compiles the compiler afresh (the dev
	// stack keeps no compilation cache), so the connection outlives the
	// default session bound (#1738).
	c := connectFor(t, 20*time.Minute)
	site := newSite2817(t, c)
	release, queued := publish2793(t, c, site, files2834())
	if queued == nil {
		t.Fatal("the publish queued no check")
	}
	check := check2793(t, c, site, release, 0, 6*time.Minute)
	if check["status"] != "errors" {
		t.Fatalf("the check = %v; want errors", check)
	}
	raw, _ := json.Marshal(result2793(check)["diagnostics"])
	var diags []diag2834
	_ = json.Unmarshal(raw, &diags)
	if len(diags) != 1 {
		t.Fatalf("diagnostics = %+v; want TS18003 alone", diags)
	}
	d := diags[0]
	if d.Code != "TS18003" || d.File != "" || d.Line != 0 || d.Column != 0 {
		t.Errorf("diagnostic = %+v; want TS18003 with no file, line or column", d)
	}
	for _, want := range []string{"No inputs were found in config file 'tsconfig.json'", "nothing-here/**/*.ts"} {
		if !strings.Contains(d.Message, want) {
			t.Errorf("the message %q lacks %q", d.Message, want)
		}
	}
	if strings.Contains(d.Message, "/project/") {
		t.Errorf("the message names the compiler's mount point: %q", d.Message)
	}
	if msg, _ := result2793(check)["message"].(string); strings.Contains(msg, "reported nothing") {
		t.Errorf("the check still says the compiler reported nothing: %q", msg)
	}

	report := c.call("manage_site", map[string]any{"command": "build_report", "site": site, "release": release})
	if s := asJSON(report["typecheck"]); !strings.Contains(s, "TS18003") {
		t.Errorf("build_report's check lacks TS18003: %s", s)
	}
	if status, _ := c.rest(http.MethodGet, "/api/v1/portal/sites/"+site+"/typechecks", nil); status != http.StatusOK {
		t.Errorf("the summaries route answered %d", status)
	}
}
