//go:build integration

package acceptance

import (
	"fmt"
	"strings"
	"testing"
)

// Issue #1952: a save refuses tests that leave an output they produce unread.
// These criteria draft a script against the dev Trino and save it through
// manage_script on the running platform with tests reading more or less of
// what it produced.
//
// Wire forms: manage_script's command, name and source are typed strings, so
// each admits one JSON form and is sent as a literal tools/call parameter of
// it.

// weekly1952 exports two regions with their counts and totals, and stages the
// number of regions and how many runs there have been as its state.
func weekly1952(marker string) string {
	return fmt.Sprintf(`def main():
    """Exports the marked regions and remembers how many there were."""
    rows = platform.query(connection = "acme", sql = "SELECT region, n, total FROM (VALUES ('%[1]s-east', 1, 10), ('%[1]s-west', 2, 20)) AS t (region, n, total) ORDER BY n")["rows"]
    platform.export(name = "weekly", rows = rows, format = "csv")
    platform.save_state({"regions": len(rows), "runs": run.state.get("runs", 0) + 1})
`, marker)
}

// test1952 is a test replaying recording that makes the given assertions.
func test1952(recording string, asserts ...string) string {
	return fmt.Sprintf(`
def test_weekly():
    """The recorded draft exports both regions."""
    testing.replay(%q)
    main()
    out = testing.outputs()
    %s
`, recording, strings.Join(asserts, "\n    "))
}

func draft1952(t *testing.T, c *client, name, source string) string {
	t.Helper()
	ran := c.call("manage_script", map[string]any{"command": "run_draft", "name": name, "source": source})
	recording, _ := ran["recording"].(string)
	if ran["status"] != "succeeded" || recording == "" {
		t.Fatalf("the draft failed or kept no recording: %v", ran)
	}
	return recording
}

func TestIssue1952_ATestAssertingOnlyTheRowCountIsRefusedNamingTheColumns(t *testing.T) {
	c := connect(t)
	name := "acceptance-1952-count-" + unique1579()
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	source := weekly1952("acceptance1952c" + unique1579())
	recording := draft1952(t, c, name, source)

	countOnly := source + test1952(recording,
		`assert.eq(out.exports[0].row_count, 2)`,
		`assert.eq(out.state, {"regions": 2, "runs": 1})`)
	refused := c.call("manage_script", map[string]any{"command": "create", "name": name, "source": countOnly})
	msg, _ := refused["message"].(string)
	for _, column := range []string{"region", "n", "total"} {
		if !strings.Contains(msg, fmt.Sprintf(`output "weekly" column %q is never asserted on`, column)) {
			t.Fatalf("the refusal must name column %q: %v", column, refused)
		}
	}
	if refused["status"] != "invalid" {
		t.Fatalf("a test reading only the row count must be refused: %v", refused)
	}
	tested := c.call("manage_script", map[string]any{"command": "test", "name": name, "source": countOnly})
	if unread, _ := tested["unread"].([]any); len(unread) != 3 {
		t.Fatalf("command=test must list the three unread columns: %v", tested["unread"])
	}

	// An assertion on those columns lets it save.
	readsColumns := source + test1952(recording,
		`assert.eq(out.exports[0].row_count, 2)`,
		`assert.eq([(r["n"], r["total"]) for r in out.exports[0].rows], [(1, 10), (2, 20)])`,
		`assert.eq([r["region"].split("-")[-1] for r in out.exports[0].rows], ["east", "west"])`,
		`assert.eq(out.state, {"regions": 2, "runs": 1})`)
	saved := c.call("manage_script", map[string]any{"command": "create", "name": name, "source": readsColumns})
	if saved["status"] != "created" {
		t.Fatalf("a test reading every column must save: %v", saved)
	}
}

func TestIssue1952_ATestReadingTheStateAndEveryExportSaves(t *testing.T) {
	c := connect(t)
	name := "acceptance-1952-all-" + unique1579()
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	marker := "acceptance1952a" + unique1579()
	// Two exports, the second a summary of the first, and the state.
	source := strings.Replace(weekly1952(marker), `    platform.save_state(`, `    platform.export(name = "summary", rows = [{"regions": len(rows)}], format = "json")
    platform.save_state(`, 1)
	recording := draft1952(t, c, name, source)

	stateOnly := source + test1952(recording, `assert.eq(out.state, {"regions": 2, "runs": 1})`)
	refused := c.call("manage_script", map[string]any{"command": "create", "name": name, "source": stateOnly})
	if msg, _ := refused["message"].(string); refused["status"] != "invalid" || !strings.Contains(msg, `output "summary" column "regions" is never asserted on`) {
		t.Fatalf("a test reading only the state must be refused naming the exports' columns: %v", refused)
	}

	everything := source + test1952(recording,
		`assert.eq(out.state, {"regions": 2, "runs": 1})`,
		`assert.eq(out.exports[1].rows, [{"regions": 2}])`,
		`assert.eq([(r["n"], r["total"]) for r in out.exports[0].rows], [(1, 10), (2, 20)])`,
		`assert.eq([r["region"] for r in out.exports[0].rows], ["`+marker+`-east", "`+marker+`-west"])`)
	saved := c.call("manage_script", map[string]any{"command": "create", "name": name, "source": everything})
	if saved["status"] != "created" {
		t.Fatalf("a test reading the state and every export must save: %v", saved)
	}
}
