//go:build integration

package acceptance

import (
	"encoding/json"
	"strings"
	"testing"
)

// Acceptance for #2783: a publish of a site with _build.json returns each
// entry's output bytes and gzip bytes, its largest inputs and the URLs it
// loads at start; a patch adding a heavy import shows the entry's size change
// against the environment's previous release; a dry run returns the same
// report and writes no release; a budget overrun warns and does not refuse;
// and manage_site build_report reads a release's report later.
//
// Wire forms: manage_site takes command, site and note as strings, base and
// release as numbers, dry_run as a boolean, and files as a list of {path,
// content} objects whose content is utf-8 text; _build.json's budgets values
// are sizes written as strings ("1KB"). manage_asset takes action and
// asset_id as strings and edits as a list of {find, replace} objects.

const heavy2783 = `export const heavy = "` + "heavy-2783-" + `" + "x".repeat(1);
export const table = [` + `"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",` + `
  "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"];
`

// report2783 is the report of one entry in a result's builds.
func report2783(t *testing.T, got map[string]any, entry string) map[string]any {
	t.Helper()
	builds, _ := got["builds"].([]any)
	for _, b := range builds {
		if m, _ := b.(map[string]any); m["entry"] == entry {
			return m
		}
	}
	raw, _ := json.Marshal(got)
	t.Fatalf("no report for %s in %s", entry, raw)
	return nil
}

func output2783(rep map[string]any, path string) map[string]any {
	outs, _ := rep["outputs"].([]any)
	for _, o := range outs {
		if m, _ := o.(map[string]any); m["path"] == path {
			return m
		}
	}
	return nil
}

// Criteria 1, 2 and 5: the publish reports app.js's bytes and gzip bytes, its
// largest inputs and the URLs it loads at start, and a budget it exceeds as a
// warning while the release is written.
func TestIssue2783_APublishReportsWhatEachEntryCompiledTo(t *testing.T) {
	c := connect(t)
	s := newSite2518(t, c)
	budgeted := strings.Replace(buildFile2774, `"entries"`, `"budgets": {"app.tsx": {"js": "100B"}}, "entries"`, 1)
	got := c.call("manage_site", map[string]any{"command": "publish", "site": s.name, "note": "report", "files": compiled2774(budgeted)})
	rep := report2783(t, got, "app.tsx")
	js := output2783(rep, "app.js")
	if js == nil || number2588(js["bytes"]) == 0 || number2588(js["gzip_bytes"]) == 0 || number2588(js["gzip_bytes"]) >= number2588(js["bytes"]) {
		t.Errorf("app.js's sizes: %v", js)
	}
	inputs, _ := rep["largest_inputs"].([]any)
	raw, _ := json.Marshal(rep)
	if len(inputs) == 0 || !strings.Contains(string(raw), "Table.tsx") {
		t.Errorf("the largest inputs do not name Table.tsx: %s", raw)
	}
	if !strings.Contains(string(raw), "https://esm.sh/react-dom@18.3.1/client") {
		t.Errorf("the start imports do not name react-dom/client: %s", raw)
	}
	if !strings.Contains(string(raw), "over its js budget") {
		t.Errorf("the budget overrun is not a warning: %s", raw)
	}
	if rels := releases2588(t, c, s); len(rels) != 1 {
		t.Errorf("a budget overrun refused the publish: %v", rels)
	}
}

// Criteria 3, 4 and 6: a dry run adding a heavy import reports app.js larger
// than release 1 and writes nothing; the patch reports the same change; and
// build_report reads release 2's report.
func TestIssue2783_APatchShowsTheChangeAndADryRunWritesNothing(t *testing.T) {
	c := connect(t)
	s := publish2774(t, c, compiled2774(buildFile2774))
	dry := c.call("manage_site", map[string]any{"command": "publish", "site": s.name, "note": "dry", "base": 1, "dry_run": true,
		"files": []map[string]any{
			{"path": "heavy.ts", "content": heavy2783},
			{"path": "app.tsx", "content": `import { heavy, table } from "./heavy.ts";` + "\n" + app2774 + "console.log(heavy, table);\n"},
		}})
	if js := output2783(report2783(t, dry, "app.tsx"), "app.js"); js == nil || !strings.Contains(js["change"].(string), "(+") {
		t.Errorf("the dry run does not show app.js growing: %v", js)
	}
	if rels := releases2588(t, c, s); len(rels) != 1 {
		t.Errorf("a dry run wrote a release: %v", rels)
	}
	c.call("manage_site", map[string]any{"command": "publish", "site": s.name, "note": "heavy file", "base": 1, "files": []map[string]any{{"path": "heavy.ts", "content": heavy2783}}})
	patched := c.call("manage_asset", map[string]any{"action": "patch", "asset_id": s.ref("app.tsx"),
		"edits": editOf(`import { Table } from "./Table.tsx";`, `import { Table } from "./Table.tsx";`+"\n"+`import { heavy, table } from "./heavy.ts";`+"\n"+`console.log(heavy, table);`)})
	js := output2783(report2783(t, patched, "app.tsx"), "app.js")
	if js == nil || !strings.Contains(js["change"].(string), "app.js") || !strings.Contains(js["change"].(string), "(+") {
		t.Errorf("the patch does not show app.js's change: %v", js)
	}
	later := c.call("manage_site", map[string]any{"command": "build_report", "site": s.name, "release": 3})
	if number2588(later["release"]) != 3 || output2783(report2783(t, later, "app.tsx"), "app.js") == nil {
		t.Errorf("build_report does not read release 3: %v", later)
	}
}
