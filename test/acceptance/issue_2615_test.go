//go:build integration

package acceptance

import (
	"strings"
	"testing"
	"time"
)

// Acceptance for #2615: the application analysis no longer reports a table
// written by no part when a part does write it. A data-modifying CTE in
// tx.batch inside platform.transaction is a write of each table it inserts
// into, and the diagram draws the link; a statement chosen when the script
// runs (platform.execute of a dict lookup) may be the writer, so the finding
// no longer says no part writes the table; and a table nothing writes in any
// form is still reported.
//
// Wire forms: manage_application, manage_schema and manage_script take their
// arguments as JSON strings (version as a JSON number, params as a list of
// objects of strings and a boolean); the diagram route takes the application
// in its path and the version in its query string. No parameter admits a
// second form.

// findings2615 is the unwritten_table findings manage_application get reports,
// keyed by the table node each names.
func findings2615(t *testing.T, c *client, app string) map[string]string {
	t.Helper()
	got := get2539(c, app)
	out := map[string]string{}
	for _, f := range asList(got["findings"]) {
		if f["kind"] == "unwritten_table" {
			node, _ := f["node"].(string)
			msg, _ := f["message"].(string)
			out[node] = msg
		}
	}
	return out
}

// shop2615 is an application holding a schema whose migration creates doc and
// inv and fills neither, with a part that reads both.
func shop2615(t *testing.T, admin *client, label string) (app, schema string) {
	t.Helper()
	app = app2539(t, admin, admin, "2615-"+label)
	schema = schema2539(t, "w"+label)
	addSchema2539(admin, app, conn2539, schema)
	migration2539(admin, conn2539, schema, 1,
		"CREATE TABLE doc (id serial PRIMARY KEY, b int);\nCREATE TABLE inv (d int);\n"+writeRole(schema, "SELECT, INSERT", "doc, inv"),
		"DROP TABLE inv; DROP TABLE doc; DROP OWNED BY "+schema+"_write; DROP ROLE "+schema+"_write")
	admin.call("manage_schema", map[string]any{"command": "apply", "connection": conn2539, "schema": schema})
	reader := "def main():\n    \"\"\"Counts invoices and documents.\"\"\"\n" +
		"    a = platform.query(\"SELECT count(*) AS n FROM " + schema + ".inv\", connection = \"orders\")[\"rows\"]\n" +
		"    b = platform.query(\"SELECT count(*) AS n FROM " + schema + ".doc\", connection = \"orders\")[\"rows\"]\n" +
		"    print(\"inv %d doc %d\" % (a[0][\"n\"], b[0][\"n\"]))\n"
	part2539(t, admin, app, "2615-reader", reader, nil, nil)
	return app, schema
}

// TestIssue2615_ACTEInTxBatchIsAWrite: a script writing inv and doc only
// through a data-modifying CTE in tx.batch inside platform.transaction leaves
// neither with an unwritten_table finding, and the diagram draws a writes link
// from it to each.
func TestIssue2615_ACTEInTxBatchIsAWrite(t *testing.T) {
	admin := connectFor(t, 5*time.Minute)
	app, schema := shop2615(t, admin, "cte")
	writer := "def record(tx):\n    \"\"\"Records a document and its invoice.\"\"\"\n" +
		"    tx.batch([\"WITH a AS (INSERT INTO " + schema + ".doc (b) VALUES (1) RETURNING id) INSERT INTO " + schema + ".inv (d) SELECT id FROM a\"])\n\n" +
		"def main():\n    \"\"\"Files one invoice.\"\"\"\n    platform.transaction(\"orders\", record)\n"
	name := part2539(t, admin, app, "2615-writer", writer, nil, nil)
	id := scriptID2540(admin, name)

	for node, msg := range findings2615(t, admin, app) {
		if strings.HasSuffix(node, schema+".inv") || strings.HasSuffix(node, schema+".doc") {
			t.Errorf("manage_application get reports %s unwritten: %s", node, msg)
		}
	}
	got := links2540(diagram2540(t, admin, app, "?version=latest"))
	for _, table := range []string{"inv", "doc"} {
		want := "script:" + id + " writes table:" + conn2539 + "/" + schema + "." + table
		if !contains2540(got, want) {
			t.Errorf("the diagram lacks %q: %v", want, got)
		}
	}
}

// TestIssue2615_AStatementChosenWhenItRunsMayBeTheWriter: with the only writer
// platform.execute of a dict lookup, no finding says the tables are written by
// no part; each names the script that writes a table chosen when it runs.
func TestIssue2615_AStatementChosenWhenItRunsMayBeTheWriter(t *testing.T) {
	admin := connectFor(t, 5*time.Minute)
	app, schema := shop2615(t, admin, "computed")
	writer := "STATEMENTS = {\"invoice\": [\"INSERT INTO " + schema + ".inv (d) VALUES (1)\"]}\n\n" +
		"def main():\n    \"\"\"Runs the statement its kind names.\"\"\"\n" +
		"    platform.execute(STATEMENTS[run.params[\"kind\"]][0], connection = \"orders\")\n"
	params := []any{map[string]any{"name": "kind", "type": "string", "required": true, "description": "Which statement to run."}}
	name := part2539(t, admin, app, "2615-chosen", writer, nil, params)

	found := findings2615(t, admin, app)
	if len(found) == 0 {
		t.Fatalf("manage_application get reports no unwritten table; want each read table reported as possibly written by %s", name)
	}
	for node, msg := range found {
		if strings.Contains(msg, "written by no part") {
			t.Errorf("%s is reported written by no part while %s may write it: %s", node, name, msg)
		}
		if !strings.Contains(msg, name+" writes a table named only when it runs") {
			t.Errorf("the finding for %s does not name %s: %s", node, name, msg)
		}
	}
}

// TestIssue2615_ATableNothingWritesIsStillReported: with no part writing in
// any form, known or computed, each table read is reported written by no part.
func TestIssue2615_ATableNothingWritesIsStillReported(t *testing.T) {
	admin := connectFor(t, 5*time.Minute)
	app, schema := shop2615(t, admin, "none")
	found := findings2615(t, admin, app)
	for _, table := range []string{"inv", "doc"} {
		node := "table:" + conn2539 + "/" + schema + "." + table
		if msg, ok := found[node]; !ok || !strings.Contains(msg, "written by no part") {
			t.Errorf("%s is not reported written by no part: %q (findings %v)", node, msg, found)
		}
	}
}
