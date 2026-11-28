//go:build integration

package acceptance

import (
	"strings"
	"testing"
	"time"
)

// Acceptance for #2848: a function a script defines inside another is the
// one its name refers to where it is written. Two writers each define a
// nested work and pass it to platform.transaction; each transaction's writes
// are the ones its own work runs, so neither writer's tables are reported
// written by no part.
//
// Wire forms: manage_application, manage_schema and manage_script take their
// arguments as JSON strings (version as a JSON number); the diagram route
// takes the application in its path and the version in its query string. No
// parameter admits a second form.

// TestIssue2848_TwoNestedFunctionsOfOneNameKeepTheirOwnWrites: with the two
// writers saved as one part and a reader of every table, no finding reports
// rate, rate_change, volume or volume_change written by no part, and the
// diagram draws a writes link from the part to each.
func TestIssue2848_TwoNestedFunctionsOfOneNameKeepTheirOwnWrites(t *testing.T) {
	admin := connectFor(t, 5*time.Minute)
	app := app2539(t, admin, admin, "2848")
	schema := schema2539(t, "w2848")
	addSchema2539(admin, app, conn2539, schema)
	tables := []string{"rate", "rate_change", "volume", "volume_change"}
	migration2539(admin, conn2539, schema, 1,
		"CREATE TABLE rate (id int PRIMARY KEY, n int);\nCREATE TABLE rate_change (id int, n int);\n"+
			"CREATE TABLE volume (id int PRIMARY KEY, n int);\nCREATE TABLE volume_change (id int, n int);\n"+
			writeRole(schema, "SELECT, INSERT, UPDATE", strings.Join(tables, ", ")),
		"DROP TABLE volume_change; DROP TABLE volume; DROP TABLE rate_change; DROP TABLE rate")
	admin.call("manage_schema", map[string]any{"command": "apply", "connection": conn2539, "schema": schema})

	reader := "def main():\n    \"\"\"Counts what the writers recorded.\"\"\"\n"
	for _, table := range tables {
		reader += "    print(platform.query(\"SELECT count(*) AS n FROM " + schema + "." + table + "\", connection = \"orders\")[\"rows\"])\n"
	}
	part2539(t, admin, app, "2848-reader", reader, nil, nil)

	writer := `RATE_SQL = "WITH up AS (INSERT INTO ` + schema + `.rate (id, n) VALUES (1, 2) ON CONFLICT (id) DO UPDATE SET n = EXCLUDED.n RETURNING id, n) INSERT INTO ` + schema + `.rate_change (id, n) SELECT id, n FROM up RETURNING id"
VOLUME_SQL = "WITH up AS (INSERT INTO ` + schema + `.volume (id, n) VALUES (1, 3) ON CONFLICT (id) DO UPDATE SET n = EXCLUDED.n RETURNING id, n) INSERT INTO ` + schema + `.volume_change (id, n) SELECT id, n FROM up RETURNING id"

def write_rates():
    """Changes the rate and records the change."""
    def work(tx):
        """Runs the rate statement."""
        return tx.query(RATE_SQL)["rows"]
    return platform.transaction("orders", work, on_error = "return")

def write_volume():
    """Changes the volume and records the change."""
    def work(tx):
        """Runs the volume statement."""
        return tx.query(VOLUME_SQL)["rows"]
    return platform.transaction("orders", work, on_error = "return")

def main():
    """Runs both writers."""
    write_rates()
    write_volume()
`
	name := part2539(t, admin, app, "2848-writer", writer, nil, nil)
	id := scriptID2540(admin, name)

	for node, msg := range findings2615(t, admin, app) {
		for _, table := range tables {
			if strings.HasSuffix(node, schema+"."+table) {
				t.Errorf("manage_application get reports %s: %s", node, msg)
			}
		}
	}
	got := links2540(diagram2540(t, admin, app, "?version=latest"))
	for _, table := range tables {
		want := "script:" + id + " writes table:" + conn2539 + "/" + schema + "." + table
		if !contains2540(got, want) {
			t.Errorf("the diagram lacks %q: %v", want, got)
		}
	}
}
