//go:build integration

package acceptance

import (
	"strings"
	"testing"
	"time"
)

// Acceptance for #2623: two statement shapes #2615 left unread are read as the
// writes they are. A tx.batch list built by a comprehension whose element is a
// dict with a module constant under "sql" writes each table that constant
// inserts into; a statement picked out of a literal by a lookup (spec[3],
// where spec is one value of a dict of lists) is the union of its candidates,
// and when every candidate is a constant each write counts and the script is
// not named as writing a table chosen when it runs. A lookup with one
// candidate built at run time keeps that message.
//
// Wire forms: manage_application, manage_schema and manage_script take their
// arguments as JSON strings (params as a list of objects of strings and a
// boolean); the diagram route takes the application in its path and the
// version in its query string. No parameter admits a second form.

// writes2623 fails unless the application reports neither table unwritten and
// the diagram draws a writes link from the script name to each.
func writes2623(t *testing.T, admin *client, app, schema, name string) {
	t.Helper()
	for node, msg := range findings2615(t, admin, app) {
		if strings.HasSuffix(node, schema+".inv") || strings.HasSuffix(node, schema+".doc") {
			t.Errorf("manage_application get reports %s unwritten: %s", node, msg)
		}
	}
	got := links2540(diagram2540(t, admin, app, "?version=latest"))
	for _, table := range []string{"inv", "doc"} {
		want := "script:" + scriptID2540(admin, name) + " writes table:" + conn2539 + "/" + schema + "." + table
		if !contains2540(got, want) {
			t.Errorf("the diagram lacks %q: %v", want, got)
		}
	}
}

// The comprehension shape: one parameterised statement repeated per row.
func TestIssue2623_AComprehensionOfAConstantStatementIsAWrite(t *testing.T) {
	admin := connectFor(t, 5*time.Minute)
	app, schema := shop2615(t, admin, "listcomp")
	writer := `INSERT_DOCUMENT = """
    WITH added AS (INSERT INTO ` + schema + `.doc (b) VALUES (:b) RETURNING id)
    INSERT INTO ` + schema + `.inv (d) SELECT id FROM added
"""

def save(documents):
    """Files each document and its invoice in one transaction."""
    statements = [{"sql": INSERT_DOCUMENT, "params": d} for d in documents]

    def write(tx):
        """Sends the statements in one round trip."""
        if statements:
            tx.batch(statements)

    return platform.transaction("orders", write)

def main():
    """Files two documents."""
    save([{"b": 1}, {"b": 2}])
`
	name := part2539(t, admin, app, "2623-listcomp", writer, nil, nil)
	writes2623(t, admin, app, schema, name)
}

// The lookup shape: each table's statement picked out of a dict of lists by
// the run's parameter, every candidate a module constant.
func TestIssue2623_ALookupWhoseCandidatesAreConstantsIsAWrite(t *testing.T) {
	admin := connectFor(t, 5*time.Minute)
	app, schema := shop2615(t, admin, "lookup")
	writer := `UPSERT_DOCUMENTS = "INSERT INTO ` + schema + `.doc (b) SELECT x FROM unnest(ARRAY[1, 2]) AS x"
UPSERT_INVOICES = "INSERT INTO ` + schema + `.inv (d) SELECT x FROM unnest(ARRAY[1, 2]) AS x"

def refresh(kind):
    """Fills one cache table."""
    spec = {
        "documents": ["documents", "doc", UPSERT_DOCUMENTS],
        "invoices": ["invoices", "inv", UPSERT_INVOICES],
    }[kind]
    platform.execute(spec[2], connection = "orders")

def main():
    """Fills the table the run names."""
    refresh(run.params["kind"])
`
	params := []any{map[string]any{"name": "kind", "type": "string", "required": true, "description": "Which table to fill."}}
	name := part2539(t, admin, app, "2623-lookup", writer, map[string]any{"kind": "documents"}, params)
	found := findings2615(t, admin, app)
	for node, msg := range found {
		if strings.Contains(msg, name+" writes a table named only when it runs") {
			t.Errorf("the finding for %s names %s as writing a table chosen when it runs: %s", node, name, msg)
		}
	}
	writes2623(t, admin, app, schema, name)
}

// A lookup with one candidate built at run time keeps the message.
func TestIssue2623_ALookupWithACandidateBuiltAtRunTimeMayBeTheWriter(t *testing.T) {
	admin := connectFor(t, 5*time.Minute)
	app, schema := shop2615(t, admin, "partial")
	writer := `UPSERT_DOCUMENTS = "INSERT INTO ` + schema + `.doc (b) VALUES (1)"

def main():
    """Runs the statement its kind names."""
    spec = {
        "documents": [UPSERT_DOCUMENTS],
        "built": ["INSERT INTO " + run.params["table"] + " VALUES (1)"],
    }[run.params["kind"]]
    platform.execute(spec[0], connection = "orders")
`
	params := []any{
		map[string]any{"name": "kind", "type": "string", "required": true, "description": "Which statement to run."},
		map[string]any{"name": "table", "type": "string", "required": false, "description": "The table a built statement fills."},
	}
	name := part2539(t, admin, app, "2623-partial", writer, map[string]any{"kind": "documents"}, params)
	msg, ok := findings2615(t, admin, app)["table:"+conn2539+"/"+schema+".inv"]
	if !ok || !strings.Contains(msg, name+" writes a table named only when it runs") {
		t.Fatalf("the finding for %s.inv does not name %s as writing a table chosen when it runs: %q", schema, name, msg)
	}
}
