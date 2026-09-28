//go:build integration

package acceptance

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Issue #1950: platform.execute runs a statement that changes state with its
// values bound as platform.query binds them, and the sql-built-from-values
// save gate reads every SQL a script sends: platform.query, platform.execute,
// and the sql of a platform.call to trino_query or trino_execute. These
// criteria save and run scripts through manage_script and run_script on the
// running platform, writing to a memory-catalog table on the scratch
// connection, as #1833's criteria do.
//
// Wire forms: manage_script's command, name, description and source,
// run_script's name, and trino_execute's and trino_query's connection, sql and
// purpose are typed strings, and run_script's wait_seconds an integer, so each
// admits one JSON form and is sent as a literal tools/call parameter of it.

const issue1950Purpose = "Acceptance #1950: platform.execute."

// issue1950Table creates an empty memory table on the scratch connection and
// drops it when the test ends.
func issue1950Table(t *testing.T, c *client, stamp string) string {
	t.Helper()
	table := "memory.default.acc_1950_" + stamp
	c.call("trino_execute", map[string]any{
		"connection": scratchResourceConnection, "purpose": issue1950Purpose,
		"sql": "CREATE TABLE " + table + " (id bigint, note varchar)",
	})
	t.Cleanup(func() {
		_, _, _ = c.callRaw("trino_execute", map[string]any{
			"connection": scratchResourceConnection, "purpose": issue1950Purpose, "sql": "DROP TABLE IF EXISTS " + table,
		})
	})
	return table
}

// issue1950Rows reads the table's rows, ordered by id.
func issue1950Rows(t *testing.T, c *client, table string) []map[string]any {
	t.Helper()
	out := c.call("trino_query", map[string]any{
		"connection": scratchResourceConnection, "purpose": issue1950Purpose,
		"sql": "SELECT id, note FROM " + table + " ORDER BY id",
	})
	list, _ := out["rows"].([]any)
	rows := make([]map[string]any, 0, len(list))
	for _, r := range list {
		if m, ok := r.(map[string]any); ok {
			rows = append(rows, m)
		}
	}
	return rows
}

// issue1950Count is how many rows carry note.
func issue1950Count(rows []map[string]any, note string) int {
	n := 0
	for _, r := range rows {
		if r["note"] == note {
			n++
		}
	}
	return n
}

func issue1950Stamp() string { return fmt.Sprintf("%d", time.Now().UnixNano()) }

// issue1950Loader registers three rows as a table and loads them into TARGET
// with platform.execute, binding the registered table's record.
const issue1950Loader = `TARGET = %q
CONN = %q
LOAD_SQL = "INSERT INTO " + TARGET + " SELECT id, note FROM :src"

def main():
    """Registers three notes as a table and inserts them into the target."""
    out = platform.export(
        name = "Acceptance 1950 notes",
        rows = [{"id": 1, "note": "one"}, {"id": 2, "note": "two"}, {"id": 3, "note": "three"}],
        format = "jsonl",
        destination = "resources",
        key = "acceptance/issue-1950/%s.jsonl",
        register = {"connection": CONN, "table_name": %q},
    )
    written = platform.execute(LOAD_SQL, connection = CONN, params = {"src": out["table"]})
    platform.result({"written": written["rows"], "registration_id": out["table"]["registration_id"]})
`

// TestIssue1950_ExecuteWritesTheRowsItRegistered is criterion 1: the saved
// script's platform.execute inserts the rows it registered, and run_script
// hands back what the statement answered.
func TestIssue1950_ExecuteWritesTheRowsItRegistered(t *testing.T) {
	c := connect(t)
	stamp := issue1950Stamp()
	table := issue1950Table(t, c, stamp)
	name := "acc-1950-load-" + stamp
	source := fmt.Sprintf(issue1950Loader, table, scratchResourceConnection, stamp, "acc_1950_src_"+stamp)
	created := c.saveScript(map[string]any{
		"command": "create", "name": name, "source": source,
		"description": "Acceptance #1950: rows registered as a table, inserted with platform.execute.",
	}, nil)
	if created["status"] != "created" {
		t.Fatalf("the script was not saved: %v", created)
	}
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })

	// Saving drafts the script with its writes allowed, which inserts the rows
	// once; the run inserts them once more.
	before := issue1950Rows(t, c, table)
	ran := c.call("run_script", map[string]any{"name": name, "wait_seconds": 120})
	result, _ := ran["result"].(map[string]any)
	if id, _ := result["registration_id"].(string); id != "" {
		t.Cleanup(func() {
			_, _, _ = c.callRaw("manage_table", map[string]any{"action": "unregister", "registration_id": id})
		})
	}
	if ran["status"] != "succeeded" {
		t.Fatalf("run_script: %v", ran)
	}
	if !strings.Contains(fmt.Sprint(result["written"]), "3") {
		t.Errorf("run_script did not report the three rows the statement wrote: %v", result)
	}
	after := issue1950Rows(t, c, table)
	if len(after) != len(before)+3 {
		t.Fatalf("the table holds %d rows after the run, %d before; want 3 more", len(after), len(before))
	}
	for _, want := range []string{"one", "two", "three"} {
		if issue1950Count(after, want) != issue1950Count(before, want)+1 {
			t.Errorf("the run did not write one %q row: before %v, after %v", want, before, after)
		}
	}
}

// TestIssue1950_SQLBuiltFromValuesIsRefusedOnEveryPath is criterion 2: SQL
// built with + into a platform.call to trino_query or trino_execute is refused
// on save naming sql-built-from-values, the hint naming platform.query and
// platform.execute.
func TestIssue1950_SQLBuiltFromValuesIsRefusedOnEveryPath(t *testing.T) {
	c := connect(t)
	for _, tool := range []string{"trino_query", "trino_execute"} {
		source := `def main():
    """Builds SQL from a value."""
    platform.call("` + tool + `", {"connection": "` + scratchResourceConnection + `", "sql": "SELECT id FROM t WHERE id = " + run.params["id"]})
`
		refused := c.call("manage_script", map[string]any{
			"command": "create", "name": "acc-1950-concat-" + issue1950Stamp(), "source": source,
			"description": "Acceptance #1950.",
		})
		if refused["status"] != "invalid" {
			t.Fatalf("%s: the concatenated SQL saved: %v", tool, refused)
		}
		found := false
		for _, f := range findings1944(refused) {
			if f["rule"] != "sql-built-from-values" {
				continue
			}
			found = true
			hint, _ := f["hint"].(string)
			if !strings.Contains(hint, "platform.query") || !strings.Contains(hint, "platform.execute") {
				t.Errorf("%s: the hint does not name platform.query and platform.execute: %q", tool, hint)
			}
			if msg, _ := f["message"].(string); !strings.Contains(msg, `platform.call("`+tool+`")`) {
				t.Errorf("%s: the message does not name the call: %q", tool, msg)
			}
		}
		if !found {
			t.Errorf("%s: no sql-built-from-values finding: %v", tool, refused["findings"])
		}
	}
}

// issue1950Quoted inserts one bound value into TARGET.
const issue1950Quoted = `TARGET = %q
CONN = %q
INSERT_SQL = "INSERT INTO " + TARGET + " VALUES (:id, :note)"

def main():
    """Inserts the note it is given."""
    platform.execute(INSERT_SQL, connection = CONN, params = {"id": 7, "note": run.params["note"]})
    platform.result({"inserted": run.params["note"]})
`

// TestIssue1950_AQuotedValueReachesTheTableUnchanged is criterion 3: a value
// holding quotes and statement text is bound, not spliced, and reads back as
// it was sent.
func TestIssue1950_AQuotedValueReachesTheTableUnchanged(t *testing.T) {
	c := connect(t)
	stamp := issue1950Stamp()
	table := issue1950Table(t, c, stamp)
	name := "acc-1950-quote-" + stamp
	note := `it's "quoted"'; DROP TABLE x; --`
	params := []map[string]any{{"name": "note", "type": "string", "required": true}}
	created := c.saveScript(map[string]any{
		"command": "create", "name": name, "params": params,
		"source":      fmt.Sprintf(issue1950Quoted, table, scratchResourceConnection),
		"description": "Acceptance #1950: a bound value holding quotes.",
	}, map[string]any{"note": note})
	if created["status"] != "created" {
		t.Fatalf("the script was not saved: %v", created)
	}
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })

	ran := c.call("run_script", map[string]any{"name": name, "wait_seconds": 120, "args": map[string]any{"note": note}})
	if ran["status"] != "succeeded" {
		t.Fatalf("run_script: %v", ran)
	}
	rows := issue1950Rows(t, c, table)
	if len(rows) == 0 {
		t.Fatal("no row reached the table")
	}
	for _, r := range rows {
		if r["note"] != note {
			t.Errorf("the note read back as %q; want %q", r["note"], note)
		}
	}
}

// TestIssue1950_ValidateReportsExecuteAndItsConnection is criterion 4.
func TestIssue1950_ValidateReportsExecuteAndItsConnection(t *testing.T) {
	c := connect(t)
	report := c.call("manage_script", map[string]any{
		"command": "validate",
		"source": `def main():
    """Deletes one row."""
    platform.execute("DELETE FROM memory.default.t WHERE id = :id", connection = "` + scratchResourceConnection + `", params = {"id": 1})
`,
	})
	// A source with no tests is reported as a save the gate would refuse;
	// what it reaches is reported either way, and that is the criterion.
	if !strings.Contains(fmt.Sprint(report["capabilities"]), "platform.execute") {
		t.Errorf("capabilities do not name platform.execute: %v", report["capabilities"])
	}
	if !strings.Contains(fmt.Sprint(report["connections"]), scratchResourceConnection) {
		t.Errorf("connections do not name %s: %v", scratchResourceConnection, report["connections"])
	}
}
