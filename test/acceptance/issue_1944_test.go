//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Issue #1944: a script created from this release on keeps its work in
// main(), which the platform calls after the module loads; its top level
// declares. A script saved before it runs as it did. These criteria go through
// manage_script, run_script and the scheduler on the running platform.
//
// Wire forms: manage_script's command, name, description, source, cron and
// timezone and run_script's name are typed strings, and run_script's
// wait_seconds an integer, so each admits one JSON form and is sent as a
// literal tools/call parameter of it.

// topLevel1944 is a report written the old way: its work at the top level.
const topLevel1944 = `rows = [{"region": "west", "total": 3}, {"region": "east", "total": 4}]
platform.export(name = "regions", rows = rows, format = "csv")
platform.result({"regions": len(rows), "total": sum([r["total"] for r in rows])})
`

// inMain1944 is the same work in main().
const inMain1944 = `ROWS = [{"region": "west", "total": 3}, {"region": "east", "total": 4}]

def main():
    """Exports the regions and reports their total."""
    platform.export(name = "regions", rows = ROWS, format = "csv")
    platform.result({"regions": len(ROWS), "total": sum([r["total"] for r in ROWS])})
`

func name1944(kind string) string { return fmt.Sprintf("acc-1944-%s-%d", kind, time.Now().UnixNano()) }

// findings1944 reads the findings of a refused save or a validate.
func findings1944(out map[string]any) []map[string]any {
	list, _ := out["findings"].([]any)
	found := make([]map[string]any, 0, len(list))
	for _, f := range list {
		if m, ok := f.(map[string]any); ok {
			found = append(found, m)
		}
	}
	return found
}

func hasLibraryEffect1944(found []map[string]any) bool {
	for _, f := range found {
		if hint, _ := f["hint"].(string); f["rule"] == "library-effect" && strings.Contains(hint, "def main():") {
			return true
		}
	}
	return false
}

func hasFinding1944(found []map[string]any, rule string, line float64) bool {
	for _, f := range found {
		if f["rule"] == rule && f["line"] == line && f["hint"] != "" {
			return true
		}
	}
	return false
}

// TestIssue1944_TopLevelWorkIsRefusedAndMainRunsTheSame: a new script with work
// at the top level is refused naming the line; the same work in main() saves,
// and run_script on it hands back the result the top-level version's draft
// run did.
func TestIssue1944_TopLevelWorkIsRefusedAndMainRunsTheSame(t *testing.T) {
	c := connect(t)
	name := name1944("main")
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })

	refused := c.call("manage_script", map[string]any{
		"command": "create", "name": name, "source": topLevel1944,
		"description": "Acceptance #1944: work at the top level.",
	})
	if refused["status"] != "invalid" {
		t.Fatalf("a new script with top-level work saved: %v", refused)
	}
	// Line 1 binds a literal list, which the top level may; lines 2 and 3 work.
	found := findings1944(refused)
	for _, line := range []float64{2, 3} {
		if !hasFinding1944(found, "top-level-work", line) {
			t.Errorf("no top-level-work finding with a hint on line %v: %v", line, found)
		}
	}
	// A source with no main() is a library since #1941, and a library may not
	// name platform: that finding's hint is where the author is told to put
	// the work in main().
	if hasFinding1944(found, "top-level-work", 1) || !hasLibraryEffect1944(found) {
		t.Errorf("want the missing main() reported and no work on line 1: %v", found)
	}

	draft := c.call("manage_script", map[string]any{"command": "run_draft", "name": name, "source": topLevel1944})
	created := c.saveScript(map[string]any{
		"command": "create", "name": name, "source": inMain1944,
		"description": "Acceptance #1944: the same work in main().",
	}, nil)
	if created["status"] != "created" {
		t.Fatalf("the script in main() did not save: %v", created)
	}
	ran := c.call("run_script", map[string]any{"name": name, "wait_seconds": 120})
	if ran["status"] != "succeeded" {
		t.Fatalf("run_script: %v", ran)
	}
	want, _ := json.Marshal(draft["result"])
	got, _ := json.Marshal(ran["result"])
	if string(want) != string(got) || !strings.Contains(string(got), `"total":7`) {
		t.Errorf("run_script result %s; the top-level draft handed back %s", got, want)
	}
	if !strings.Contains(fmt.Sprint(ran["outputs"]), "regions") {
		t.Errorf("the run wrote no regions output: %v", ran["outputs"])
	}
}

// TestIssue1944_ValidateReportsWithoutSaving: validate names the top-level
// statement and saves nothing.
func TestIssue1944_ValidateReportsWithoutSaving(t *testing.T) {
	c := connect(t)
	name := name1944("validate")
	out := c.call("manage_script", map[string]any{"command": "validate", "name": name, "source": topLevel1944})
	if out["ok"] != false || !hasFinding1944(findings1944(out), "top-level-work", 2) {
		t.Fatalf("validate: %v", out)
	}
	if _, text, _ := c.callRaw("manage_script", map[string]any{"command": "get", "name": name}); !strings.Contains(text, "not found") {
		t.Errorf("validate saved the script: %s", text)
	}
}

// TestIssue1944_AScriptSavedBeforeRunsAsItDid: a script that existed before
// this release, with its work at the top level, still runs through run_script
// and from its schedule, and a new version of it is held to main() like any
// other script's (#1965).
func TestIssue1944_AScriptSavedBeforeRunsAsItDid(t *testing.T) {
	c := connect(t)
	db := issue1904DB(t)
	name := name1944("legacy")
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	c.saveScript(map[string]any{
		"command": "create", "name": name, "source": inMain1944,
		"description": "Acceptance #1944: a script saved before main().",
	}, nil)
	issue1904Exec(t, db, `UPDATE scripts SET source_code = $2 WHERE name = $1`, name, topLevel1944)
	issue1904Exec(t, db, `UPDATE script_versions SET source_code = $2
		WHERE script_id = (SELECT id FROM scripts WHERE name = $1)`, name, topLevel1944)

	ran := c.call("run_script", map[string]any{"name": name, "wait_seconds": 120})
	if ran["status"] != "succeeded" || !strings.Contains(fmt.Sprint(ran["result"]), "7") {
		t.Fatalf("run_script on the older script: %v", ran)
	}

	c.call("manage_script", map[string]any{"command": "schedule_set", "name": name, "cron": "0 3 * * *", "timezone": "UTC"})
	issue1904Exec(t, db, `UPDATE script_schedules SET next_run_at = NOW() - interval '1 second'
		WHERE script_id = (SELECT id FROM scripts WHERE name = $1)`, name)
	issue1904Await(t, "the scheduled run of the older script", func() bool {
		return issue1904Count(t, db, `SELECT count(*) FROM script_runs r JOIN scripts s ON s.id = r.script_id
			WHERE s.name = $1 AND r.trigger_kind = 'schedule' AND r.status = 'succeeded'`, name) > 0
	})

	edited := c.call("manage_script", map[string]any{"command": "update", "name": name, "source": "# still at the top level\n" + topLevel1944})
	if edited["status"] != "invalid" || !strings.Contains(fmt.Sprint(edited["findings"]), "top-level-work") {
		t.Errorf("a new version of the older script must be held to main(): %v", edited)
	}
}
