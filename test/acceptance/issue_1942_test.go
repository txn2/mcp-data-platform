//go:build integration

package acceptance

import (
	"fmt"
	"strings"
	"testing"
)

// #1942: a new version of a script that has run replays its recent recorded
// runs and compares what it reaches; a difference needs change_summary and
// user_agreed. Wire forms: change_summary is a JSON string and user_agreed a
// JSON boolean (the schema admits no other form); source a JSON string.

// source1942 exports region and n rows through a helper named helper.
func source1942(marker, helper string) string {
	return fmt.Sprintf(`def %[2]s():
    """The marked rows."""
    return platform.query(connection = "acme", sql = "SELECT region, n FROM (VALUES ('%[1]s-east', 3), ('%[1]s-west', 4)) AS t (region, n) ORDER BY n")["rows"]

def main():
    """Exports the marked rows."""
    platform.export(name = "regions", rows = %[2]s(), format = "csv")
`, marker, helper)
}

// ran1942 saves source as name through saveScript, runs it once as the
// worker runs it, which is what records a run the next save replays, and
// returns the saved source.
func ran1942(t *testing.T, c *client, name, source string) string {
	t.Helper()
	c.saveScript(map[string]any{"command": "create", "name": name, "source": source}, nil)
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	run := c.call("run_script", map[string]any{"name": name, "wait_seconds": 120})
	if run["status"] != "succeeded" {
		t.Fatalf("the run did not succeed: %v", run)
	}
	got := c.call("manage_script", map[string]any{"command": "get", "name": name})
	saved, _ := got["source"].(string)
	return saved
}

// withTests is source with the test_* functions of saved, which is how an
// edit keeps the tests of the version it replaces.
func withTests(source, saved string) string {
	i := strings.Index(saved, "\ndef test_")
	if i < 0 {
		return source
	}
	return strings.TrimRight(source, "\n") + "\n" + saved[i:]
}

func TestIssue1942_ARefactorSavesWithoutASummary(t *testing.T) {
	c := connect(t)
	marker := "acceptance1942r" + unique1579()
	name := "acceptance-1942-refactor-" + unique1579()
	saved := ran1942(t, c, name, source1942(marker, "marked"))

	renamed := withTests(source1942(marker, "marked_rows"), saved)
	out := c.call("manage_script", map[string]any{"command": "update", "name": name, "source": renamed})
	if out["status"] != "updated" {
		t.Fatalf("a refactor that changes no output must save without a summary: %v", out)
	}
	if diffs, _ := out["differences"].([]any); len(diffs) != 0 {
		t.Fatalf("the refactor reported differences: %v", diffs)
	}
	if replayed, _ := out["replayed"].([]any); len(replayed) == 0 {
		t.Fatalf("the save replayed no recorded run: %v", out)
	}
}

func TestIssue1942_AChangedColumnNeedsAnAgreedSummary(t *testing.T) {
	c := connect(t)
	marker := "acceptance1942c" + unique1579()
	name := "acceptance-1942-column-" + unique1579()
	saved := ran1942(t, c, name, source1942(marker, "marked"))

	// The export drops the n column; its test is rewritten to match.
	changed := strings.Replace(source1942(marker, "marked"), `rows = marked(),`, `rows = [{"region": r["region"]} for r in marked()],`, 1)
	edit := c.tested(map[string]any{"command": "update", "name": name, "source": changed}, nil)
	_ = saved

	validated := c.call("manage_script", map[string]any{"command": "validate", "name": name, "source": edit["source"]})
	refusal, _ := validated["save_refusal"].(string)
	if validated["change_needed"] != true || !strings.Contains(refusal, `no longer has column "n"`) {
		t.Fatalf("validate must report the change without saving: %v", validated)
	}

	refused := c.call("manage_script", edit)
	msg, _ := refused["message"].(string)
	if refused["status"] != "invalid" || !strings.Contains(msg, `output "regions" no longer has column "n"`) {
		t.Fatalf("a changed column must be refused naming it: %v", refused)
	}

	edit["change_summary"] = "The regions file lists the regions without their counts."
	edit["user_agreed"] = true
	agreed := c.call("manage_script", edit)
	if agreed["status"] != "updated" {
		t.Fatalf("with the summary and the agreement it must save: %v", agreed)
	}
	versions := c.call("manage_script", map[string]any{"command": "versions", "name": name})
	list, _ := versions["versions"].([]any)
	latest, _ := list[0].(map[string]any)
	if latest["change_summary"] != "The regions file lists the regions without their counts." || latest["change_agreed_at"] == nil {
		t.Fatalf("the version history must carry the summary and when it was agreed: %v", latest)
	}
}

func TestIssue1942_ANewToolNeedsAnAgreedSummary(t *testing.T) {
	c := connect(t)
	marker := "acceptance1942t" + unique1579()
	name := "acceptance-1942-tool-" + unique1579()
	ran1942(t, c, name, source1942(marker, "marked"))

	reaching := strings.Replace(source1942(marker, "marked"), `    platform.export(`, `    platform.call("list_connections", {})
    platform.export(`, 1)
	edit := c.tested(map[string]any{"command": "update", "name": name, "source": reaching}, nil)
	refused := c.call("manage_script", edit)
	msg, _ := refused["message"].(string)
	if refused["status"] != "invalid" || !strings.Contains(msg, `reaches tool "list_connections", which the saved version does not`) {
		t.Fatalf("a new tool must be refused naming it: %v", refused)
	}
}
