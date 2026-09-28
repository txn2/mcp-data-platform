//go:build integration

package acceptance

import (
	"fmt"
	"strings"
	"testing"
)

// #1939: every draft run and run records its host calls; a script carries
// test_* functions that replay a recording through testing.replay, and a save
// runs them. Wire forms: run_id is a JSON string (manage_script recording),
// source a JSON string; neither parameter admits another form.

// source1939 queries the dev Trino for two rows carrying marker and exports
// them. The marker makes the query findable in Trino's own query history.
func source1939(marker string) string {
	return fmt.Sprintf(`def main():
    """Exports the two marked rows."""
    rows = platform.query(connection = "acme", sql = "SELECT mark, n FROM (VALUES ('%[1]s', 3), ('%[1]s', 4)) AS t (mark, n) ORDER BY n")["rows"]
    platform.export(name = "marked", rows = rows, format = "csv")
`, marker)
}

// test1939 is a test replaying recording that asserts what the export held.
func test1939(recording, assertion string) string {
	return fmt.Sprintf(`
def test_marked():
    """The recorded draft exports both marked rows."""
    testing.replay(%q)
    main()
    %s
`, recording, assertion)
}

const assert1939 = `assert.eq([r["n"] for r in testing.outputs().exports[0].rows], [3, 4])`

// trinoSaw1939 is how many queries Trino has run whose text carries marker,
// read from Trino's own history rather than from the platform.
func trinoSaw1939(t *testing.T, c *client, marker string) int {
	t.Helper()
	out := c.call("trino_query", map[string]any{
		"connection": "acme",
		"sql": "SELECT count(*) AS n FROM system.runtime.queries WHERE query LIKE '%" + marker +
			"%' AND query NOT LIKE '%system.runtime.queries%'",
		"purpose": "Acceptance #1939: whether a script's test reached Trino.",
	})
	rows, _ := out["rows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("Trino's query history answered %v", out)
	}
	row, _ := rows[0].(map[string]any)
	n, _ := row["n"].(float64)
	return int(n)
}

// draft1939 drafts source under name as c and returns its recording.
func draft1939(t *testing.T, c *client, name, source string) string {
	t.Helper()
	ran := c.call("manage_script", map[string]any{"command": "run_draft", "name": name, "source": source})
	if ran["status"] != "succeeded" {
		t.Fatalf("the draft failed: %v", ran)
	}
	id, _ := ran["recording"].(string)
	if id == "" {
		t.Fatalf("the draft named no recording: %v", ran)
	}
	return id
}

// firstTest1939 is the first test a manage_script test answer reports.
func firstTest1939(t *testing.T, out map[string]any) map[string]any {
	t.Helper()
	tests, _ := out["tests"].([]any)
	if len(tests) == 0 {
		t.Fatalf("the answer reports no test: %v", out)
	}
	first, _ := tests[0].(map[string]any)
	return first
}

func TestIssue1939_ATestReplaysADraftAndTrinoReceivesNoQuery(t *testing.T) {
	c := connect(t)
	marker := "acceptance1939m" + unique1579()
	name := "acceptance-1939-replay-" + unique1579()
	source := source1939(marker)
	recording := draft1939(t, c, name, source)
	before := trinoSaw1939(t, c, marker)
	if before < 1 {
		t.Fatalf("Trino never saw the draft's query")
	}

	out := c.call("manage_script", map[string]any{
		"command": "test", "name": name, "source": source + test1939(recording, assert1939),
	})
	if out["ok"] != true {
		t.Fatalf("the test did not pass: %v", out)
	}
	if after := trinoSaw1939(t, c, marker); after != before {
		t.Fatalf("Trino ran %d queries carrying the marker during the test; want none", after-before)
	}
	t.Logf("recording %s replayed; Trino saw the marker %d time(s), all before the test", recording, before)
}

func TestIssue1939_AQueryTheRecordingLacksFailsNamingIt(t *testing.T) {
	c := connect(t)
	name := "acceptance-1939-missing-" + unique1579()
	source := source1939("acceptance1939x" + unique1579())
	recording := draft1939(t, c, name, source)
	added := strings.Replace(source, `    platform.export(`, `    platform.query(connection = "acme", sql = "SELECT 1939 AS extra")
    platform.export(`, 1)
	out := c.call("manage_script", map[string]any{
		"command": "test", "name": name, "source": added + test1939(recording, assert1939),
	})
	failure, _ := firstTest1939(t, out)["failure"].(string)
	if out["ok"] != false || !strings.Contains(failure, `the recording holds no answer for platform.query("SELECT 1939 AS extra")`) {
		t.Fatalf("a query the recording lacks must fail the test naming it: %v", out)
	}
}

func TestIssue1939_AWrongRowCountFailsNamingTheAssertionAndItsLine(t *testing.T) {
	c := connect(t)
	name := "acceptance-1939-wrong-" + unique1579()
	source := source1939("acceptance1939w" + unique1579())
	recording := draft1939(t, c, name, source)
	out := c.call("manage_script", map[string]any{
		"command": "test", "name": name,
		"source": source + test1939(recording, `assert.eq(testing.outputs().exports[0].row_count, 3)`),
	})
	first := firstTest1939(t, out)
	failure, _ := first["failure"].(string)
	// main is lines 1-4, the blank line 5, the test's def 6, its docstring 7,
	// replay 8, main() 9: the assertion is line 10.
	if failure != "assert.eq: got 2, want 3" || first["line"] != float64(10) {
		t.Fatalf("want the assertion's own words at line 10, got %q at %v", failure, first["line"])
	}
}

func TestIssue1939_ASaveNeedsPassingTests(t *testing.T) {
	c := connect(t)
	name := "acceptance-1939-save-" + unique1579()
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	source := source1939("acceptance1939s" + unique1579())
	recording := draft1939(t, c, name, source)

	untested := c.call("manage_script", map[string]any{"command": "create", "name": name, "source": source})
	if msg, _ := untested["message"].(string); untested["status"] != "invalid" || !strings.Contains(msg, "has no tests") {
		t.Fatalf("a script with no test must be refused: %v", untested)
	}
	failing := c.call("manage_script", map[string]any{
		"command": "create", "name": name,
		"source": source + test1939(recording, `assert.eq(testing.outputs().exports[0].row_count, 3)`),
	})
	if msg, _ := failing["message"].(string); failing["status"] != "invalid" || !strings.Contains(msg, "a test failed") {
		t.Fatalf("a script with a failing test must be refused: %v", failing)
	}
	passing := c.call("manage_script", map[string]any{
		"command": "create", "name": name, "source": source + test1939(recording, assert1939),
	})
	if passing["status"] != "created" {
		t.Fatalf("a script with a passing test must save: %v", passing)
	}
}

func TestIssue1939_ARecordingIsReadByItsOwnerAndAnAdministratorOnly(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	peer := connectAs(t, devPeerAPIKey)
	admin := connect(t)
	name := "acceptance-1939-read-" + unique1579()
	source := source1939("acceptance1939r" + unique1579())
	recording := draft1939(t, owner, name, source)
	created := owner.call("manage_script", map[string]any{
		"command": "create", "name": name, "source": source + test1939(recording, assert1939),
	})
	if created["status"] != "created" {
		t.Fatalf("the script did not save: %v", created)
	}
	t.Cleanup(func() { _, _, _ = owner.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })

	for who, c := range map[string]*client{"owner": owner, "administrator": admin} {
		read := c.call("manage_script", map[string]any{"command": "recording", "run_id": recording})
		calls, _ := read["calls"].([]any)
		if len(calls) != 1 {
			t.Fatalf("the %s should read the recording's one query: %v", who, read)
		}
	}
	res, text, err := peer.callRaw("manage_script", map[string]any{"command": "recording", "run_id": recording})
	if err != nil || !res.IsError || !strings.Contains(text, "no recording") {
		t.Fatalf("another person must be refused the recording: %v %s", err, text)
	}

	// The recording the saved version's test names is kept past the
	// retention sweep; the sweep itself is the RealDB test's
	// (internal/platform/scriptrec/recstore).
	db := issue1904DB(t)
	var kept bool
	var scriptID string
	if err := db.QueryRow(`SELECT kept, COALESCE(script_id::text, '') FROM script_recordings WHERE run_id = $1`, recording).
		Scan(&kept, &scriptID); err != nil {
		t.Fatalf("reading the recording's row: %v", err)
	}
	if !kept || scriptID == "" {
		t.Fatalf("the recording the saved test names is kept and attached to the script: kept=%v script=%q", kept, scriptID)
	}
}

// The reference script an author starts from is a built-in example and a
// built-in knowledge page showing it, and the page shows the example exactly.
func TestIssue1939_TheReferenceScriptIsAPageAndAnExample(t *testing.T) {
	c := connect(t)
	example := c.call("manage_script", map[string]any{"command": "get", "name": "example-weekly-revenue"})
	source, _ := example["source"].(string)
	if !strings.Contains(source, "def test_a_recorded_week():") {
		t.Fatalf("the reference example carries no tests: %v", example)
	}
	page := c.call("fetch", map[string]any{
		"reference": "mcp:knowledge_page:platform-reference-script",
		"purpose":   "Acceptance #1939: the reference script page.",
	})
	if !strings.Contains(fmt.Sprint(page), strings.TrimRight(source, "\n")) {
		t.Fatalf("the page does not show the example's source: %v", page)
	}
}
