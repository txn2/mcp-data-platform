//go:build integration

package acceptance

import (
	"strings"
	"testing"
)

// Acceptance for #2452: savepoints inside platform.transaction. Each criterion
// builds its table with manage_schema in a schema of its own on the dev
// stack's `orders` (postgres-target).
//
// Wire forms: manage_script takes command, name and source as strings,
// allow_writes as a boolean and run_id as a string; manage_schema takes
// command, connection, schema, name and up as strings and version as an
// integer. tx.savepoint takes a function and on_error as a string in the
// script source.

const conn2452 = "orders"

// customers2452 builds a schema with a customers table whose email is unique.
func customers2452(t *testing.T, admin *client, prefix string) string {
	t.Helper()
	schema := "acc2452_" + prefix + "_" + unique1579()
	admin.call("manage_schema", map[string]any{
		"command": "add", "connection": conn2452, "schema": schema, "version": 1, "name": "customers",
		"up": "CREATE TABLE customers (id int PRIMARY KEY, email text NOT NULL UNIQUE)",
	})
	admin.call("manage_schema", map[string]any{"command": "apply", "connection": conn2452, "schema": schema})
	t.Cleanup(func() { targetPSQL(t, "DROP SCHEMA IF EXISTS "+schema+" CASCADE") })
	return schema
}

func TestIssue2452_ADuplicateInASavepointIsUndoneAndTheRowsAroundItCommit(t *testing.T) {
	admin := connect(t)
	schema := customers2452(t, admin, "dup")
	ran := draft2428(admin, "acceptance-2452-dup", `
def main():
    """Inserts A, a duplicate of A's key in a savepoint, then B."""
    def fill(tx):
        """The three inserts."""
        tx.execute("INSERT INTO `+schema+`.customers VALUES (1, 'a@example.com')")
        dup = tx.savepoint(lambda t: t.execute("INSERT INTO `+schema+`.customers VALUES (2, 'a@example.com')"), on_error = "return")
        tx.execute("INSERT INTO `+schema+`.customers VALUES (3, 'b@example.com')")
        return dup["error"]["postgres"]["sqlstate"]
    print(platform.transaction("`+conn2452+`", fill))
`, true)
	if ran["status"] != "succeeded" {
		t.Fatalf("the run failed: %v", ran)
	}
	if log, _ := ran["log"].(string); !strings.HasSuffix(strings.TrimSpace(log), "23505") {
		t.Errorf("the savepoint did not hand back the duplicate's 23505: %q", log)
	}
	if got := strings.TrimSpace(targetPSQL(t, "SELECT string_agg(id::text, ',' ORDER BY id) FROM "+schema+".customers")); got != "1,3" {
		t.Fatalf("committed ids = %q, want A and B (1,3) and not the duplicate", got)
	}

	// Each savepoint step is one of the run's recorded calls, so a test
	// replays it.
	recording, _ := ran["recording"].(string)
	rec := admin.call("manage_script", map[string]any{"command": "recording", "run_id": recording})
	text := asJSON(rec)
	for _, step := range []string{"appdb.savepoint", "appdb.rollback_to", "appdb.commit"} {
		if !strings.Contains(text, step) {
			t.Errorf("the recording holds no %s call", step)
		}
	}
}

func TestIssue2452_ANestedSavepointWhoseInnerFunctionFailsUndoesOnlyTheInnerWork(t *testing.T) {
	admin := connect(t)
	schema := customers2452(t, admin, "nested")
	ran := draft2428(admin, "acceptance-2452-nested", `
def main():
    """An outer savepoint keeps its row; the inner one fails and rolls back."""
    def inner(tx):
        """Inserts a row, then a duplicate."""
        tx.execute("INSERT INTO `+schema+`.customers VALUES (3, 'c@example.com')")
        tx.execute("INSERT INTO `+schema+`.customers VALUES (4, 'b@example.com')")

    def outer(tx):
        """Inserts a row, then runs inner under its own savepoint."""
        tx.execute("INSERT INTO `+schema+`.customers VALUES (2, 'b@example.com')")
        tx.savepoint(inner, on_error = "return")

    def fill(tx):
        """The first row, then outer."""
        tx.execute("INSERT INTO `+schema+`.customers VALUES (1, 'a@example.com')")
        tx.savepoint(outer)
    platform.transaction("`+conn2452+`", fill)
`, true)
	if ran["status"] != "succeeded" {
		t.Fatalf("the run failed: %v", ran)
	}
	if got := strings.TrimSpace(targetPSQL(t, "SELECT string_agg(id::text, ',' ORDER BY id) FROM "+schema+".customers")); got != "1,2" {
		t.Fatalf("committed ids = %q, want 1 and the outer savepoint's 2; the inner savepoint's 3 is undone", got)
	}
}

func TestIssue2452_SavepointSQLIsRefusedNamingTheStatement(t *testing.T) {
	admin := connect(t)
	for sql, statement := range map[string]string{
		"RELEASE SAVEPOINT x": "a RELEASE statement is not run",
		"ROLLBACK TO x":       "a ROLLBACK TO statement is not run",
		"SAVEPOINT x":         "a SAVEPOINT statement is not run",
	} {
		ran := draft2428(admin, "acceptance-2452-refused", `
def main():
    """Writes a savepoint statement as SQL."""
    platform.transaction("`+conn2452+`", lambda tx: tx.execute("`+sql+`"))
`, true)
		if ran["status"] == "succeeded" {
			t.Errorf("%s ran", sql)
			continue
		}
		if text := asJSON(ran); !strings.Contains(text, statement) || !strings.Contains(text, "tx.savepoint") {
			t.Errorf("%s: the refusal does not name the statement and tx.savepoint: %s", sql, text)
		}
	}
}

func TestIssue2452_ARunEndingInsideASavepointLeavesNoTransactionOpen(t *testing.T) {
	admin := connect(t)
	schema := customers2452(t, admin, "ended")
	ran := draft2428(admin, "acceptance-2452-ended", `
def main():
    """Fails inside a savepoint."""
    def inside(tx):
        """Writes a row and stops the run."""
        tx.execute("INSERT INTO `+schema+`.customers VALUES (1, 'a@example.com')")
        fail("stopping inside the savepoint")
    platform.transaction("`+conn2452+`", lambda tx: tx.savepoint(inside))
`, true)
	if ran["status"] == "succeeded" || !strings.Contains(asJSON(ran), "stopping inside the savepoint") {
		t.Fatalf("the run did not fail inside the savepoint: %v", ran)
	}
	waitFor(t, "no transaction left open on orders", func() (bool, string) {
		n := strings.TrimSpace(targetPSQL(t, "SELECT count(*) FROM pg_stat_activity WHERE datname = 'orders' "+
			"AND application_name = 'mcp-data-platform' AND state LIKE 'idle in transaction%'"))
		return n == "0", n + " sessions idle in transaction"
	})
	if got := strings.TrimSpace(targetPSQL(t, "SELECT count(*) FROM "+schema+".customers")); got != "0" {
		t.Fatalf("customers holds %s rows after the failed run, want 0", got)
	}
}
