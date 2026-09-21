//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// Issue #1818: a CSV the platform stored prefixed every value starting with
// '-', '=', '+' or '@' with an apostrophe, a spreadsheet formula guard applied
// to a data file. An identifier "-AbCdEfGhIj" staged by a script came back from
// the table registered over it as "'-AbCdEfGhIj", and nothing said so.
//
// What these hold, against the running platform: a managed script's
// platform.export to the managed-resource library, and a trino_export to the
// same library, both store each value as it was, so the table registered over
// either file returns exactly the values that went in.
//
// Wire forms: manage_script's `command`, `name`, `description` and `source`,
// run_script's `name`, manage_table's `action`, `reference`, `connection`,
// `table_name` and `registration_id`, and trino_query's `connection`, `sql` and
// `purpose` are all typed string, and run_script's `wait_seconds` an integer,
// so each is sent in its one form. trino_export's `resource` admits only the
// object form; its string form is refused, which #1663's criteria hold.

const issue1818Purpose = "Acceptance for #1818: a stored CSV keeps a value that starts with a sign."

// issue1818Values are the values the formula guard rewrote, keyed by the column
// each is exported under.
var issue1818Values = map[string]string{
	"id":     "-AbCdEfGhIj",
	"neg":    "-5",
	"eq":     "=1+1",
	"plus":   "+44 20 7946 0000",
	"at":     "@handle",
	"plain":  "untouched",
	"ending": "trailing-",
}

// issue1818ScriptSource exports one row of those values to the managed-resource
// library and records the reference, so the run's own record says where the
// file landed. The first verb is the row as a JSON string literal, the second
// the key.
const issue1818ScriptSource = `
out = platform.export(
    name="Acceptance 1818 script output",
    rows=[json.decode(%q)],
    format="csv",
    destination="resources",
    key="acceptance/issue-1818/%s",
)
platform.save_state({"reference": out["reference"]})
`

// TestIssue1818_AScriptExportKeepsALeadingSign is the ticket's reproduction:
// a script stages rows as a CSV resource, a table is registered over it, and
// the table is read back.
func TestIssue1818_AScriptExportKeepsALeadingSign(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	row, err := json.Marshal(issue1818Values)
	if err != nil {
		t.Fatal(err)
	}
	reference := runExportScript(t, c, "acc-1818-"+stamp,
		fmt.Sprintf(issue1818ScriptSource, string(row), "acc-1818-script-"+stamp+".csv"))

	got := registeredRow(t, c, reference, "acc_1818_script_"+stamp, issue1818Purpose)
	assertValues(t, issue1818Values, got)
}

// TestIssue1818_ATrinoExportKeepsALeadingSign holds the same for the query
// export, which writes through the same formatter: every stored CSV is a data
// file that a table can be registered over.
func TestIssue1818_ATrinoExportKeepsALeadingSign(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	sql := "SELECT"
	sep := " "
	for col, v := range issue1818Values {
		sql += fmt.Sprintf("%s'%s' AS %s", sep, v, col)
		sep = ", "
	}
	landed := issue1663Landing(t, c.call("trino_export", map[string]any{
		"sql":     sql,
		"format":  "csv",
		"name":    "Acceptance 1818 query " + stamp,
		"purpose": issue1818Purpose,
		"resource": map[string]any{
			"path": "acceptance/issue-1818", "filename": "acc-1818-query-" + stamp + ".csv",
		},
	}))
	reference, _ := landed["reference"].(string)
	if reference == "" {
		t.Fatalf("trino_export returned no reference: %v", landed)
	}

	got := registeredRow(t, c, reference, "acc_1818_query_"+stamp, issue1818Purpose)
	assertValues(t, issue1818Values, got)
}

// runExportScript saves a script, runs it once, and returns the reference its
// state recorded. The script is removed when the test ends.
func runExportScript(t *testing.T, c *client, name, source string) string {
	t.Helper()
	_, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name})
	c.call("manage_script", map[string]any{
		"command":     "create",
		"name":        name,
		"description": "Acceptance: a script export read back through a registered table.",
		"source":      source,
	})
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name})
	})
	state := issue1663RunScript(t, c, name)
	reference, _ := state["reference"].(string)
	if reference == "" {
		t.Fatalf("the run recorded no reference: %v", state)
	}
	return reference
}

// registeredRow registers a table over a one-row file and returns that row as
// the table serves it. The registration is removed when the test ends.
func registeredRow(t *testing.T, c *client, reference, table, purpose string) map[string]any {
	t.Helper()
	reg := c.call("manage_table", map[string]any{
		"action": "register", "reference": reference,
		"connection": scratchResourceConnection, "table_name": table,
	})
	query, _ := reg["query_table"].(string)
	if query == "" {
		t.Fatalf("manage_table did not register the file: %v", reg)
	}
	t.Cleanup(func() {
		if id, _ := reg["registration_id"].(string); id != "" {
			_, _, _ = c.callRaw("manage_table", map[string]any{"action": "unregister", "registration_id": id})
		}
	})
	result := c.call("trino_query", map[string]any{
		"connection": scratchResourceConnection,
		"sql":        "SELECT * FROM " + query,
		"purpose":    purpose,
	})
	rows, _ := result["rows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("the table serves %d rows, not the file's one: %v", len(rows), result)
	}
	row, _ := rows[0].(map[string]any)
	return row
}

// assertValues fails for every column whose value the table does not serve
// exactly as it was written.
func assertValues(t *testing.T, want map[string]string, got map[string]any) {
	t.Helper()
	for col, v := range want {
		if s, _ := got[col].(string); s != v {
			t.Errorf("column %s = %q; want %q exactly", col, got[col], v)
		}
	}
}
