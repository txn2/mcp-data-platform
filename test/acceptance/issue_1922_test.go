//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #1922: lineage written by the platform as it moves data. The edges are
// read back through fetch, whose dataset document carries the lineage within
// three hops each way. The portal dataset page that shows them is #1921, and
// the tool that replaces datahub_get_lineage is #1924.
//
// Wire forms: trino_execute's `connection` and `sql`, run_script's `name`,
// manage_script's `command`, `name`, `description` and `source`, and fetch's
// `reference` are typed string; run_script's `wait_seconds` is an integer.
// Each is sent in that one form.

// upstream1922 returns the sources of the edges within three hops upstream of
// a dataset.
func upstream1922(t *testing.T, c *client, ref string) map[string]bool {
	t.Helper()
	content := issue1917Fetch(t, c, ref)
	hops, _ := content["upstream"].([]any)
	out := map[string]bool{}
	for _, h := range hops {
		hop, _ := h.(map[string]any)
		out[fmt.Sprint(hop["source"])] = true
	}
	return out
}

// TestIssue1922_ACreateTableAsSelectRecordsAnEdgeFromEachSource: a CREATE
// TABLE ... AS SELECT run through the platform produces edges from each source
// table.
func TestIssue1922_ACreateTableAsSelectRecordsAnEdgeFromEachSource(t *testing.T) {
	c := connectFor(t, scanTimeout+time.Minute)
	schema := schema1918(t, c, "1922ctas", "orders", "users", "joined")
	exec1918(c, "CREATE TABLE memory."+schema+".orders (id bigint, user_id bigint)")
	exec1918(c, "CREATE TABLE memory."+schema+".users (id bigint, name varchar)")
	exec1918(c, "CREATE TABLE memory."+schema+".joined AS SELECT o.id, u.name FROM memory."+schema+
		".orders o JOIN memory."+schema+".users u ON o.user_id = u.id")
	scan1918(t, c)

	sources := upstream1922(t, c, ref1918(schema, "joined"))
	for _, table := range []string{"orders", "users"} {
		if !sources[ref1918(schema, table)] {
			t.Errorf("no edge from %s to the table created from it: %v", table, sources)
		}
	}
}

// script1922 reads a table and exports its rows to a file registered as a
// table.
const script1922 = `
def main():
    """Copies a table's rows into a registered file."""
    got = platform.query(connection=%q, sql="SELECT id, name FROM memory.%s.src")
    platform.export(name="Acceptance 1922", rows=got["rows"], format="jsonl",
        destination="resources", key="acceptance/issue-1922/%s",
        register={"connection": %q, "table_name": %q})
`

// TestIssue1922_AScriptRunRecordsAnEdgeFromWhatItReadToWhatItExported: a
// script run that reads a table and exports a table produces an edge between
// them.
func TestIssue1922_AScriptRunRecordsAnEdgeFromWhatItReadToWhatItExported(t *testing.T) {
	c := connectFor(t, scanTimeout+3*time.Minute)
	schema := schema1918(t, c, "1922script", "src")
	exec1918(c, "CREATE TABLE memory."+schema+".src (id bigint, name varchar)")
	exec1918(c, "INSERT INTO memory."+schema+".src VALUES (1, 'north'), (2, 'south')")
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	table := "acc_1922_" + stamp
	name := "acc-1922-" + stamp
	c.saveScript(map[string]any{
		"command": "create", "name": name,
		"description": "Acceptance #1922: reads a table and exports it as a registered table.",
		"source":      fmt.Sprintf(script1922, conn1918, schema, stamp+".jsonl", scratchResourceConnection, table),
	}, nil)
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })

	run := c.call("run_script", map[string]any{"name": name, "wait_seconds": 120})
	if status, _ := run["status"].(string); status != "succeeded" {
		t.Fatalf("the run did not succeed: %v", run)
	}
	queryTable, regID := registration1922(t, c, run, table)
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_table", map[string]any{"action": "unregister", "registration_id": regID})
	})
	exported := fmt.Sprintf("mcp:dataset:(trino,%s,%s)", scratchResourceConnection, queryTable)
	if !upstream1922(t, c, exported)[ref1918(schema, "src")] {
		t.Errorf("walking upstream from the exported table does not reach the table the run read: %v",
			upstream1922(t, c, exported))
	}
}

// registration1922 finds the registration a run made over the file it wrote,
// by the table name the script gave it.
func registration1922(t *testing.T, c *client, run map[string]any, table string) (queryTable, id string) {
	t.Helper()
	outputs, _ := run["outputs"].([]any)
	var resourceID string
	for _, o := range outputs {
		out, _ := o.(map[string]any)
		if rid, _ := out["resource_id"].(string); rid != "" {
			resourceID = rid
		}
	}
	if resourceID == "" {
		t.Fatalf("the run wrote no managed resource: %v", run)
	}
	t.Cleanup(func() { _, _ = connect(t).rest(http.MethodDelete, "/api/v1/resources/"+resourceID, http.NoBody) })
	out := c.call("manage_table", map[string]any{"action": "list", "reference": "mcp:resource:" + resourceID})
	regs, _ := out["table_registrations"].([]any)
	for _, r := range regs {
		reg, _ := r.(map[string]any)
		if qt, _ := reg["query_table"].(string); strings.HasSuffix(qt, table) {
			id, _ = reg["registration_id"].(string)
			return qt, id
		}
	}
	t.Fatalf("the run registered no table named %s: %v", table, out)
	return "", ""
}
