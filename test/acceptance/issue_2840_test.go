//go:build integration

package acceptance

import (
	"strings"
	"testing"
	"time"
)

// Acceptance for #2840: an application's findings no longer read a name a
// statement's WITH clause defines as a table, and a data-modifying WITH
// statement writes its CTE's target and its own. The statement is the
// ticket's shape: an upsert in a CTE, a batch id held in a MATERIALIZED CTE
// referenced as CROSS JOIN b, and the audit row inserted by the statement
// itself, run with tx.query inside platform.transaction, opening with a
// comment.
//
// Wire forms: manage_application, manage_schema and manage_script take their
// arguments as JSON strings (version as a JSON number); the diagram route
// takes the application in its path and the version in its query string. No
// parameter admits a second form.

// TestIssue2840_AWithStatementsNamesAreNotTablesAndItsWritesAreWrites: with
// the writer saved as a part, no finding names a table b, old or up, neither
// rate nor rate_change is reported written by no part, and the diagram draws
// a writes link to each.
func TestIssue2840_AWithStatementsNamesAreNotTablesAndItsWritesAreWrites(t *testing.T) {
	admin := connectFor(t, 5*time.Minute)
	app := app2539(t, admin, admin, "2840")
	schema := schema2539(t, "w2840")
	addSchema2539(admin, app, conn2539, schema)
	migration2539(admin, conn2539, schema, 1,
		"CREATE TABLE rate (id int PRIMARY KEY, n int);\nCREATE TABLE rate_change (batch_id uuid, id int, old_values jsonb);\n"+
			writeRole(schema, "SELECT, INSERT, UPDATE", "rate, rate_change"),
		"DROP TABLE rate_change; DROP TABLE rate")
	admin.call("manage_schema", map[string]any{"command": "apply", "connection": conn2539, "schema": schema})

	reader := "def main():\n    \"\"\"Counts the recorded rate changes.\"\"\"\n" +
		"    rows = platform.query(\"SELECT count(*) AS n FROM " + schema + ".rate_change\", connection = \"orders\")[\"rows\"]\n" +
		"    print(\"changes %d\" % rows[0][\"n\"])\n"
	part2539(t, admin, app, "2840-reader", reader, nil, nil)

	writer := `RATE_SQL = """-- one batch id for the whole change
WITH b AS MATERIALIZED (SELECT gen_random_uuid() AS id),
old AS (SELECT r.id, jsonb_build_object('n', r.n) AS v FROM ` + schema + `.rate r),
up AS (
    INSERT INTO ` + schema + `.rate (id, n) VALUES (1, 2)
    ON CONFLICT (id) DO UPDATE SET n = EXCLUDED.n
    RETURNING id, n
)
INSERT INTO ` + schema + `.rate_change (batch_id, id, old_values)
SELECT b.id, up.id, old.v
  FROM up
 CROSS JOIN b
  LEFT JOIN old ON old.id = up.id
RETURNING id, batch_id::text AS batch_id"""

def apply(tx):
    """Changes the rate and records the change in one statement."""
    return tx.query(RATE_SQL)

def main():
    """Changes one rate."""
    platform.transaction("orders", apply)
`
	name := part2539(t, admin, app, "2840-writer", writer, nil, nil)
	id := scriptID2540(admin, name)

	for node, msg := range findings2615(t, admin, app) {
		for _, bad := range []string{"." + "b", ".old", ".up", ".rate", ".rate_change"} {
			if strings.HasSuffix(node, schema+bad) {
				t.Errorf("manage_application get reports %s unwritten: %s", node, msg)
			}
		}
	}
	got := links2540(diagram2540(t, admin, app, "?version=latest"))
	for _, table := range []string{"rate", "rate_change"} {
		want := "script:" + id + " writes table:" + conn2539 + "/" + schema + "." + table
		if !contains2540(got, want) {
			t.Errorf("the diagram lacks %q: %v", want, got)
		}
	}
	for _, l := range got {
		for _, cte := range []string{"." + "b", ".old", ".up"} {
			if strings.HasSuffix(l, schema+cte) {
				t.Errorf("the diagram draws a WITH name as a table: %s", l)
			}
		}
	}
}
