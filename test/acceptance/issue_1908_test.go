//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"testing"
)

// Issue #1908: two versions compared on the Flow tab, served by
// GET /api/v1/portal/scripts/{id}/versions/{version}/graph?compare={older}:
// the newer version's diagram with each node marked added, changed (naming
// what it was) or removed.
//
// The ticket's destination criterion names a bucket destination; the local
// stack declares none, so the change is from the portal to the managed-resource
// library, the other destination a script writes to without configuration. The
// comparison reads a destination the same way for both.
//
// Wire forms: the path parameters and `compare` are each sent once as the URL
// string their route admits; manage_script's arguments once as strings.

func compare1908(t *testing.T, c *client, id string, newer, older int) []map[string]any {
	t.Helper()
	path := fmt.Sprintf("/api/v1/portal/scripts/%s/versions/%d/graph?compare=%d", id, newer, older)
	status, out := c.rest(http.MethodGet, path, nil)
	if status != http.StatusOK {
		t.Fatalf("GET %s: status %d: %v", path, status, out)
	}
	if v, _ := out["compared_with"].(float64); int(v) != older {
		t.Fatalf("compared_with is %v, want %d", out["compared_with"], older)
	}
	var changed []map[string]any
	nodes, _ := out["nodes"].([]any)
	for _, n := range nodes {
		m, _ := n.(map[string]any)
		if m["change"] != nil {
			changed = append(changed, m)
		}
	}
	return changed
}

func update1908(t *testing.T, c *client, name, source string) {
	t.Helper()
	if out := c.call("manage_script", map[string]any{"command": "update", "name": name, "source": source}); out["error"] != nil || out["status"] == "invalid" {
		t.Fatalf("update refused: %v", out)
	}
}

// base1908 is two stages main() runs in turn, in the shape the #1913 gates
// require of a saved script. main() is last, so a test adds a step to it by
// appending one indented line.
const base1908 = `
def stage():
    """Exports the staged rows."""
    rows = platform.query("SELECT 1 AS n", connection = "acme")
    platform.export("acc-1908", rows["rows"], format = "csv")

def report():
    """Exports the summary rows."""
    rows = platform.query("SELECT 2 AS n", connection = "acme")
    platform.export("acc-1908-summary", rows["rows"], format = "csv")

def main():
    """Stages the rows, then reports on them."""
    stage()
    report()
`

func TestIssue1908_AnAddedExportIsOneAddedCard(t *testing.T) {
	c := connectAs(t, devOwnerAPIKey)
	id, name := script1906(t, c, "added", base1908)
	update1908(t, c, name, base1908+"    platform.export(\"acc-1908-extra\", [], format = \"csv\", destination = \"resources\", key = \"acc-1908/x.csv\")\n")
	changed := compare1908(t, c, id, 2, 1)
	if len(changed) != 1 || changed[0]["change"] != "added" || changed[0]["title"] != "Export CSV to resources" {
		t.Fatalf("changes are %v, want one added export to resources", changed)
	}
}

func TestIssue1908_AMovedDestinationIsOneChangedCardNamingBoth(t *testing.T) {
	c := connectAs(t, devOwnerAPIKey)
	const head = "def main():\n    \"\"\"Exports the rows.\"\"\"\n"
	v1 := head + "    platform.export(\"acc-1908-move\", [], format = \"csv\")\n"
	id, name := script1906(t, c, "moved", v1)
	update1908(t, c, name, head+"    platform.export(\"acc-1908-move\", [], format = \"csv\", destination = \"resources\", key = \"acc-1908/m.csv\")\n")
	changed := compare1908(t, c, id, 2, 1)
	if len(changed) != 1 || changed[0]["change"] != "changed" {
		t.Fatalf("changes are %v, want one changed export", changed)
	}
	was, _ := changed[0]["was"].(map[string]any)
	if changed[0]["title"] != "Export CSV to resources" || was["title"] != "Export CSV to portal" {
		t.Errorf("the change reads %v, was %v", changed[0]["title"], was["title"])
	}
}

func TestIssue1908_ReorderingFunctionsChangesNothing(t *testing.T) {
	c := connectAs(t, devOwnerAPIKey)
	id, name := script1906(t, c, "reorder", base1908)
	update1908(t, c, name, `
def report():
    """Exports the summary rows."""
    rows = platform.query("SELECT 2 AS n", connection = "acme")
    platform.export("acc-1908-summary", rows["rows"], format = "csv")

def stage():
    """Exports the staged rows."""
    rows = platform.query("SELECT 1 AS n", connection = "acme")
    platform.export("acc-1908", rows["rows"], format = "csv")

def main():
    """Reports on the rows, then stages them."""
    report()
    stage()
`)
	if changed := compare1908(t, c, id, 2, 1); len(changed) != 0 {
		t.Errorf("reordering reads as %d changes: %v", len(changed), changed)
	}
}
