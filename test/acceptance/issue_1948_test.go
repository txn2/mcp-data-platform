//go:build integration

package acceptance

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Issue #1948: a script that registers a table reads it back through
// platform.query by binding the record register= returned, so the save gates'
// refusal of SQL built by concatenation (#1938) leaves it a way that works.
// These criteria save and run scripts through manage_script and run_script on
// the running platform, against the scratch connection #1820's criteria
// register on.
//
// Wire forms: manage_script's command, name, description and source and
// run_script's name are typed strings, and run_script's wait_seconds an
// integer, so each admits one JSON form and is sent as a literal tools/call
// parameter of it.

// readBack1948 registers three rows as a table, then counts them through the
// record: the first verb is the key, the second the connection, the third the
// table name.
const readBack1948 = `COUNT_SQL = "SELECT count(*) AS n FROM :orders"

def main():
    """Registers three orders as a table and reports how many it holds."""
    out = platform.export(
        name = "Acceptance 1948 orders",
        rows = [{"id": 1}, {"id": 2}, {"id": 3}],
        format = "jsonl",
        destination = "resources",
        key = "acceptance/issue-1948/%s",
        register = {"connection": %q, "table_name": %q},
    )
    counted = platform.query(COUNT_SQL, connection = %q, params = {"orders": out["table"]})
    platform.result({"orders": counted["rows"][0]["n"], "registration_id": out["table"]["registration_id"]})
`

// TestIssue1948_ARegisteredTableIsReadBackThroughItsRecord: the saved script
// runs and hands back the count of the rows it registered.
func TestIssue1948_ARegisteredTableIsReadBackThroughItsRecord(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	name := "acc-1948-" + stamp
	source := fmt.Sprintf(readBack1948, "orders-"+stamp+".jsonl", scratchResourceConnection,
		"acc_1948_"+stamp, scratchResourceConnection)
	created := c.saveScript(map[string]any{
		"command": "create", "name": name, "source": source,
		"description": "Acceptance #1948: a registered table read back through its record.",
	}, nil)
	if created["status"] != "created" {
		t.Fatalf("the script was not saved: %v", created)
	}
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })

	ran := c.call("run_script", map[string]any{"name": name, "wait_seconds": 120})
	result, _ := ran["result"].(map[string]any)
	if id, _ := result["registration_id"].(string); id != "" {
		t.Cleanup(func() {
			_, _, _ = c.callRaw("manage_table", map[string]any{"action": "unregister", "registration_id": id})
		})
	}
	if ran["status"] != "succeeded" || fmt.Sprint(result["orders"]) != "3" {
		t.Fatalf("run_script: want succeeded with 3 orders counted: %v", ran)
	}
}

// TestIssue1948_ConcatenationIsRefusedNamingTheRecord: the same read built with
// + is refused on save, and the hint names the record form.
func TestIssue1948_ConcatenationIsRefusedNamingTheRecord(t *testing.T) {
	c := connect(t)
	name := fmt.Sprintf("acc-1948-concat-%d", time.Now().UnixNano())
	source := `def main():
    """Reads a registered table back by concatenation."""
    out = platform.export(name = "x", rows = [{"id": 1}], format = "jsonl", destination = "resources", key = "x.jsonl",
                          register = {"connection": "` + scratchResourceConnection + `", "table_name": "x"})
    platform.query("SELECT count(*) AS n FROM " + out["table"]["query_table"], connection = "` + scratchResourceConnection + `")
`
	refused := c.call("manage_script", map[string]any{
		"command": "create", "name": name, "source": source, "description": "Acceptance #1948.",
	})
	if refused["status"] != "invalid" {
		t.Fatalf("the concatenated read saved: %v", refused)
	}
	for _, f := range findings1944(refused) {
		if f["rule"] == "sql-built-from-values" {
			if hint, _ := f["hint"].(string); !strings.Contains(hint, `params={"t": out["table"]}`) {
				t.Errorf("the hint does not name the record form: %q", hint)
			}
			return
		}
	}
	t.Errorf("no sql-built-from-values finding: %v", refused["findings"])
}

// TestIssue1948_ADraftsPreviewRecordIsRefused: a draft without allow_writes
// registers nothing, so binding the record it reports fails naming why.
func TestIssue1948_ADraftsPreviewRecordIsRefused(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	source := fmt.Sprintf(readBack1948, "draft-"+stamp+".jsonl", scratchResourceConnection,
		"acc_1948_draft_"+stamp, scratchResourceConnection)
	draft := c.call("manage_script", map[string]any{
		"command": "run_draft", "name": "acc-1948-draft-" + stamp, "source": source,
	})
	if draft["status"] != "failed" {
		t.Fatalf("the draft did not fail: %v", draft)
	}
	if e := fmt.Sprint(draft["error"]); !strings.Contains(e, "a draft without allow_writes registers nothing") {
		t.Errorf("the failure does not say why: %q", e)
	}
}
