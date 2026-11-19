//go:build integration

package acceptance

import (
	"strings"
	"testing"
	"time"
)

// Acceptance for #2699: an application's diagram and findings read a
// script's platform.batch and platform.transaction statements through the
// shapes real scripts pass them in: a parameter, a field of a dict constant,
// a helper's return value, list concatenation, .append, and a comprehension
// over any of these. Every statement is a module constant, so each table is
// drawn written and reported unwritten by no finding. A step still unresolved
// is one "table named when it runs" card per script and connection, and the
// scripts that write such tables are named once, in a sentence that agrees
// with their number.
//
// Wire forms: manage_application, manage_schema and manage_script take their
// arguments as JSON strings; the diagram route takes the application in its
// path and the version in its query string. No parameter admits a second
// form.

// TestIssue2699_BatchStatementsReachedThroughHelpersAreWrites is the ticket's
// shape: a dict of specs whose "write" lists a page writer runs through a
// comprehension over a parameter's field, and a record written through a
// commit helper given a list concatenated with another helper's return.
func TestIssue2699_BatchStatementsReachedThroughHelpersAreWrites(t *testing.T) {
	admin := connectFor(t, 5*time.Minute)
	app, schema := shop2615(t, admin, "helpers")
	writer := `CONNECTION = "orders"
UPSERT_DOCUMENTS = "INSERT INTO ` + schema + `.doc (b) VALUES (:b)"
RECORD_INVOICE = "INSERT INTO ` + schema + `.inv (d) VALUES (:d)"
SPECS = {
    "documents": {"query": "SELECT 1", "write": [UPSERT_DOCUMENTS]},
}

def write_page(spec, rows):
    """Writes one page of a spec's rows."""
    platform.batch(CONNECTION, [{"sql": sql, "params": {"b": len(rows)}} for sql in spec["write"]])

def commit(statements):
    """Commits statements in one round trip."""
    return platform.batch(CONNECTION, statements)

def invoice_statements(made):
    """The statements of an order's invoices."""
    out = []
    for d in made["invoices"]:
        out.append({"sql": RECORD_INVOICE, "params": {"d": d}})
    return out

def record(made):
    """Records an order's invoices."""
    return commit([{"sql": RECORD_INVOICE, "params": {"d": made["id"]}}] + invoice_statements(made))

def main():
    """Writes every spec's rows, then an order."""
    for _, spec in SPECS.items():
        write_page(spec, [{"b": 1}])
    record({"id": 1, "invoices": [2]})
`
	name := part2539(t, admin, app, "2699-helpers", writer, nil, nil)
	writes2623(t, admin, app, schema, name)
	if msg := runtimeWrites2615(t, admin, app); strings.Contains(msg, name) {
		t.Errorf("the findings name %s as writing a table named only when it runs: %s", name, msg)
	}
}

// TestIssue2699_ATransactionRunsAParametersStatements: platform.transaction
// whose function sends a list its enclosing function was passed through tx.
func TestIssue2699_ATransactionRunsAParametersStatements(t *testing.T) {
	admin := connectFor(t, 5*time.Minute)
	app, schema := shop2615(t, admin, "transaction")
	writer := `ADD_DOCUMENT = "INSERT INTO ` + schema + `.doc (b) VALUES (:b)"
ADD_INVOICE = "INSERT INTO ` + schema + `.inv (d) VALUES (:d)"

def save_all(statements):
    """Saves the statements in one transaction."""

    def write(tx):
        """Sends them in one round trip."""
        tx.batch(statements)

    platform.transaction("orders", write)

def main():
    """Files a document and its invoice."""
    save_all([{"sql": ADD_DOCUMENT, "params": {"b": 1}}, {"sql": ADD_INVOICE, "params": {"d": 1}}])
`
	name := part2539(t, admin, app, "2699-tx", writer, nil, nil)
	writes2623(t, admin, app, schema, name)
}

// TestIssue2699_RunTimeWritesAreOneCardAndNamedOnce: two scripts whose
// statements are built when they run, one with three such steps, are drawn
// with one card each on the connection, and the findings name both once.
func TestIssue2699_RunTimeWritesAreOneCardAndNamedOnce(t *testing.T) {
	admin := connectFor(t, 5*time.Minute)
	app, schema := shop2615(t, admin, "runtime")
	params := []any{map[string]any{"name": "sql", "type": "string", "required": true, "description": "The statement to run."}}
	three := "def main():\n    \"\"\"Runs what it is given three ways.\"\"\"\n" +
		"    platform.execute(run.params[\"sql\"], connection = \"orders\")\n" +
		"    platform.batch(\"orders\", [run.params[\"sql\"]])\n" +
		"    platform.batch(\"orders\", [{\"sql\": run.params[\"sql\"]}])\n"
	insert := map[string]any{"sql": "INSERT INTO " + schema + ".doc (b) VALUES (1)"}
	first := part2539(t, admin, app, "2699-three", three, insert, params)
	one := "def main():\n    \"\"\"Runs what it is given.\"\"\"\n    platform.execute(run.params[\"sql\"], connection = \"orders\")\n"
	second := part2539(t, admin, app, "2699-one", one, insert, params)

	d := diagram2540(t, admin, app, "?version=latest")
	firstID := scriptID2540(admin, first)
	cards := 0
	for _, n := range asList(d["nodes"]) {
		if id, _ := n["id"].(string); strings.HasPrefix(id, "computed:script:"+firstID+":") {
			cards++
		}
	}
	if cards != 1 {
		t.Errorf("%s's three run-time steps on one connection are drawn as %d cards, want 1", first, cards)
	}

	found := findings2615(t, admin, app)
	if len(found) == 0 {
		t.Fatalf("no table of %s is reported unwritten while only run-time statements may write them", schema)
	}
	for node, msg := range found {
		if strings.Contains(msg, first) || strings.Contains(msg, second) {
			t.Errorf("the finding for %s repeats the script list: %s", node, msg)
		}
	}
	msg := runtimeWrites2615(t, admin, app)
	if strings.Count(msg, first) != 1 || strings.Count(msg, second) != 1 || !strings.Contains(msg, " write tables named only when they run") {
		t.Errorf("the scripts that write tables named only when they run are not named once, in the plural: %q", msg)
	}
}

// TestIssue2699_StatementsALibraryDefinesAreWrites: a script whose statement
// constants and statement-building helper come from a library it loads, as a
// real application's do, is drawn writing those tables at the version it
// loads, and named by no finding.
func TestIssue2699_StatementsALibraryDefinesAreWrites(t *testing.T) {
	admin := connectFor(t, 5*time.Minute)
	app, schema := shop2615(t, admin, "library")
	lib := "acc2699-lib-" + unique1579()
	issue1941CreateLibrary(t, admin, lib, `ADD_DOCUMENT = "INSERT INTO `+schema+`.doc (b) VALUES (:b)"
ADD_INVOICE = "INSERT INTO `+schema+`.inv (d) VALUES (:d)"

def invoice_statements(ids):
    """One invoice row per id."""
    return [{"sql": ADD_INVOICE, "params": {"d": i}} for i in ids]

def test_invoice_statements():
    """One statement per id."""
    assert.eq(len(invoice_statements([1, 2])), 2)
`)
	writer := `load("lib:` + lib + `@1", "ADD_DOCUMENT", "invoice_statements")

def main():
    """Files a document and its invoices."""
    platform.execute(ADD_DOCUMENT, connection = "orders", params = {"b": 1})
    platform.batch("orders", invoice_statements([1]))
`
	name := part2539(t, admin, app, "2699-lib", writer, nil, nil)
	writes2623(t, admin, app, schema, name)
	if msg := runtimeWrites2615(t, admin, app); strings.Contains(msg, name) {
		t.Errorf("the findings name %s as writing a table named only when it runs: %s", name, msg)
	}
}
