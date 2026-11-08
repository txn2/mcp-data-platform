//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// Acceptance for #2453: platform.copy_in and platform.copy_out on an
// application database.
//
// Wire forms: manage_script takes command, name and source as strings,
// allow_writes as a boolean and run_id as a string; run_script takes name as a
// string and wait_seconds as an integer; the admin connection route takes one
// JSON object whose persona_roles and persona_role_passwords are JSON objects
// of strings. copy_in's rows are lists and dicts in the script source.

const conn2453 = "orders"

// items2453 builds a schema with one table and returns it.
func items2453(t *testing.T, admin *client, prefix string) string {
	t.Helper()
	schema := "acc2453_" + prefix + "_" + unique1579()
	holdSchema(t, admin, conn2453, schema)
	admin.call("manage_schema", map[string]any{
		"command": "add", "connection": conn2453, "schema": schema, "version": 1, "name": "items",
		"up": "CREATE TABLE items (id int PRIMARY KEY, sku text NOT NULL, qty int NOT NULL)",
	})
	admin.call("manage_schema", map[string]any{"command": "apply", "connection": conn2453, "schema": schema})
	t.Cleanup(func() { targetPSQL(t, "DROP SCHEMA IF EXISTS "+schema+" CASCADE") })
	return schema
}

func TestIssue2453_FiftyThousandRowsCopyInAndTheRecordKeepsNoValue(t *testing.T) {
	admin := connect(t)
	schema := items2453(t, admin, "bulk")
	k := newCanary2363(t)
	// The row value is built at run time from two halves, so the script's
	// stored source does not hold it; only a row could carry it.
	head, tail := k.stamp+"-copy", "-Zq7row"
	k.know("a copy_in row value", head+tail)
	// The control is printed: the platform stores the source and answers
	// with the log, so a search that misses a table or a response fails.
	ran := draft2428(admin, "acceptance-2453-bulk", `
def main():
    """Copies 50,000 rows in, one of them carrying the marked sku."""
    marked = "`+head+`" + "`+tail+`"
    rows = [[i, "sku-%d" % i, 1] for i in range(1, 50001)]
    rows[41] = [42, marked, 7]
    n = platform.copy_in("`+conn2453+`", "`+schema+`.items", ["id", "sku", "qty"], rows)
    print("copied", n)
    print("`+k.control+`")
`, true)
	if ran["status"] != "succeeded" {
		t.Fatalf("the copy failed: %v", ran)
	}
	if log, _ := ran["log"].(string); !strings.Contains(log, "copied 50000") {
		t.Errorf("copy_in did not report 50000 rows: %q", log)
	}
	if got := strings.TrimSpace(targetPSQL(t, "SELECT count(*) FROM "+schema+".items")); got != "50000" {
		t.Fatalf("items holds %s rows, want 50000", got)
	}
	marked := head + tail
	if got := strings.TrimSpace(targetPSQL(t, "SELECT sku FROM "+schema+".items WHERE id = 42")); got != marked {
		t.Fatalf("row 42's sku = %q, want the marked value", got)
	}

	// The run's answer and its recorded calls hold the table, the columns
	// and the count, and no row's value in any form.
	recording, _ := ran["recording"].(string)
	rec := admin.call("manage_script", map[string]any{"command": "recording", "run_id": recording})
	recText := asJSON(rec)
	for _, want := range []string{`"appdb.copy_in"`, schema + ".items", `"row_count":50000`, `"columns":["id","sku","qty"]`} {
		if !strings.Contains(recText, want) {
			t.Errorf("the recording does not hold %s", want)
		}
	}
	for where, text := range map[string]string{"the recording": recText, "the run's answer": asJSON(ran)} {
		for _, form := range forms2363(marked) {
			if strings.Contains(text, form) {
				t.Errorf("%s holds the row value as %q", where, form)
			}
		}
		if strings.Contains(text, "sku-49999") {
			t.Errorf("%s holds an ordinary row's value", where)
		}
	}
	k.read("run_draft acceptance-2453-bulk", []byte(asJSON(ran)))
	// Every table of the platform database, both replicas' logs and the
	// collector's spans and log records hold no form of the row value.
	k.awaitWriters(admin)
	k.searchDatabase()
	k.searchFiles()
	k.searchResponses()
	for _, sink := range []string{"table", "response"} {
		if !k.found[sink] {
			t.Errorf("the control value was not found in any %s; the search would miss a leak there", sink)
		}
	}
	// The run's audit row carries the run, not its rows.
	db := platformDB2402(t)
	var held int
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM audit_logs a
		WHERE a.timestamp > now() - interval '10 minutes' AND a::text LIKE $1`,
		"%"+marked+"%").Scan(&held); err != nil {
		t.Fatalf("searching audit rows: %v", err)
	}
	if held != 0 {
		t.Errorf("%d audit rows hold the row value", held)
	}
}

func TestIssue2453_CopyInARoleMayNotInsertIntoFailsWith42501(t *testing.T) {
	admin := connect(t)
	owner := connectAs(t, devOwnerAPIKey)
	schema, granted, refused, gpw, rpw := roles2403(t)
	targetPSQL(t, "GRANT INSERT ON "+schema+".ledger TO "+granted)
	conn := roleConn2403(t, admin, granted, gpw, refused, rpw)
	source := func(id int) string {
		return fmt.Sprintf(`
def main():
    """Copies one row into the ledger."""
    print(json.encode(platform.copy_in("%s", "%s.ledger", ["id", "owner_id", "note"], [[%d, "x", "copied"]], on_error = "return")))
`, conn, schema, id)
	}
	if got := draft2428(admin, "acceptance-2453-granted", source(10), true); !strings.Contains(asJSON(got), `"log":"1\n"`) {
		t.Fatalf("the granted persona's copy, as %s, did not write its row: %v", granted, got)
	}
	got := draft2428(owner, "acceptance-2453-refused", source(11), true)
	if log, _ := got["log"].(string); !strings.Contains(log, "42501") {
		t.Fatalf("the collaborator persona's copy, as %s, must fail with 42501: %v", refused, got)
	}
	if n := strings.TrimSpace(targetPSQL(t, "SELECT count(*) FROM "+schema+".ledger WHERE id = 11")); n != "0" {
		t.Fatalf("the refused copy wrote %s rows", n)
	}
}

func TestIssue2453_CopyInInsideATransactionWhoseFunctionFailsLeavesNoRows(t *testing.T) {
	admin := connect(t)
	schema := items2453(t, admin, "rollback")
	ran := draft2428(admin, "acceptance-2453-rollback", `
def main():
    """Copies rows in a transaction, then fails."""
    def fill(tx):
        """The copy, then the failure."""
        tx.copy_in("`+schema+`.items", ["id", "sku", "qty"], [{"id": 1, "sku": "a", "qty": 1}, {"id": 2, "sku": "b", "qty": 2}])
        fail("after the copy")
    platform.transaction("`+conn2453+`", fill)
`, true)
	if ran["status"] == "succeeded" {
		t.Fatalf("the failing function succeeded: %v", ran)
	}
	if got := strings.TrimSpace(targetPSQL(t, "SELECT count(*) FROM "+schema+".items")); got != "0" {
		t.Fatalf("items holds %s rows after the rolled-back copy, want 0", got)
	}
}

func TestIssue2453_CopyOutPastMaxRowsWritesEveryRow(t *testing.T) {
	admin := connect(t)
	schema := items2453(t, admin, "out")
	targetPSQL(t, "INSERT INTO "+schema+".items SELECT g, 'sku-' || g, g % 7 FROM generate_series(1, 12000) g")
	name := "acc-2453-copy-out-" + unique1579()
	created := admin.saveScript(map[string]any{
		"command": "create", "name": name, "description": "Acceptance #2453: copy_out past max_rows.",
		"source": `def main():
    """Writes every item to one CSV output."""
    out = platform.copy_out("` + conn2453 + `", "items-` + name + `", "SELECT id, sku, qty FROM ` + schema + `.items WHERE qty >= :min ORDER BY id", params = {"min": 0})
    print(out["row_count"])
`,
	}, nil)
	if created["status"] != "created" {
		t.Fatalf("manage_script create: %v", created)
	}
	t.Cleanup(func() { _, _, _ = admin.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	run := admin.call("run_script", map[string]any{"name": name, "wait_seconds": 120})
	if run["status"] != "succeeded" {
		t.Fatalf("the run failed: %v", run)
	}
	got := admin.call("manage_script", map[string]any{"command": "get_run", "run_id": run["run_id"]})
	outputs, _ := got["outputs"].([]any)
	if len(outputs) != 1 {
		t.Fatalf("the run recorded %d outputs, want 1: %v", len(outputs), got)
	}
	out, _ := outputs[0].(map[string]any)
	if number(t, out, "row_count") != 12000 {
		t.Errorf("the output carries %v rows, want 12000 (max_rows is 1000)", out["row_count"])
	}
	assetID, _ := out["asset_id"].(string)
	status, body := admin.restText("/api/v1/portal/assets/" + assetID + "/content")
	if status != http.StatusOK {
		t.Fatalf("reading the output: %d %s", status, body)
	}
	lines := strings.Split(strings.TrimSuffix(body, "\n"), "\n")
	if len(lines) != 12001 || lines[0] != "id,sku,qty" || lines[12000] != "12000,sku-12000,2" {
		t.Fatalf("the output holds %d lines from %q to %q; want a header and 12000 rows", len(lines), lines[0], lines[len(lines)-1])
	}
}

func TestIssue2453_COPYWrittenAsSQLIsRefused(t *testing.T) {
	admin := connect(t)
	for _, sql := range []string{"COPY pg_class TO STDOUT", "COPY pg_class FROM PROGRAM 'id'"} {
		ran := draft2428(admin, "acceptance-2453-copy-sql", `
def main():
    """Writes COPY as SQL."""
    platform.execute("`+sql+`", connection = "`+conn2453+`")
`, true)
		if ran["status"] == "succeeded" || !strings.Contains(asJSON(ran), "a COPY statement is not run") {
			t.Errorf("%s was not refused: %v", sql, ran)
		}
	}
}
