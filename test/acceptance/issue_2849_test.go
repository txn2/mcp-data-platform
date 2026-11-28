//go:build integration

package acceptance

import (
	"strings"
	"testing"
	"time"
)

// Acceptance for #2849: text inside a SQL string literal or dollar-quoted
// body is not read for table names. A writer builds a proposed rule's title
// from text holding "from a correction" and "join the queue"; no finding
// names a table a, the, or any word of that text, and the rule table is
// written and read as the statement does.
//
// Found with it: a table named with double-quoted parts was read by nothing
// that reads a statement's tables, and a covered caller of a transform that
// protects a column used the column freely by quoting the table's name.
//
// Wire forms: manage_application, manage_schema and manage_script take their
// arguments as JSON strings (version as a JSON number); manage_script
// handler_set takes kind "transform", connection a string, personas and
// protected JSON arrays of strings; trino_query takes connection and sql
// strings; the diagram route takes the application in its path and the
// version in its query string. No parameter admits a second form.

// TestIssue2849_WordsInALiteralAreNotTables: with the writer saved as a part,
// no finding and no diagram link names a table the literal's words make up,
// and the diagram draws the part writing rule and reading supplier.
func TestIssue2849_WordsInALiteralAreNotTables(t *testing.T) {
	admin := connectFor(t, 5*time.Minute)
	app := app2539(t, admin, admin, "2849")
	schema := schema2539(t, "w2849")
	addSchema2539(admin, app, conn2539, schema)
	migration2539(admin, conn2539, schema, 1,
		"CREATE TABLE supplier (id int PRIMARY KEY, name text);\nCREATE TABLE rule (id serial PRIMARY KEY, kind text, title text);\n"+
			writeRole(schema, "SELECT, INSERT", "supplier, rule"),
		"DROP TABLE rule; DROP TABLE supplier")
	admin.call("manage_schema", map[string]any{"command": "apply", "connection": conn2539, "schema": schema})

	reader := "def main():\n    \"\"\"Lists the proposed rules.\"\"\"\n" +
		"    print(platform.query(\"SELECT title FROM " + schema + ".rule\", connection = \"orders\")[\"rows\"])\n"
	part2539(t, admin, app, "2849-reader", reader, nil, nil)

	writer := `PROPOSE_SQL = """INSERT INTO ` + schema + `.rule (kind, title)
SELECT 'coding',
       coalesce(s.name, 'a supplier') || ' (from a correction on invoice ' || $1 || ', join the queue)' || $$ from the note $$
  FROM ` + schema + `.supplier s
 WHERE NOT EXISTS (SELECT 1 FROM ` + schema + `.rule r WHERE r.title LIKE '%from a correction%')"""

def propose(tx):
    """Proposes a rule from a correction."""
    return tx.query(PROPOSE_SQL, params = ["INV-1"])

def main():
    """Proposes one rule."""
    platform.transaction("orders", propose)
`
	name := part2539(t, admin, app, "2849-writer", writer, nil, nil)
	id := scriptID2540(admin, name)

	words := []string{".a", ".the", ".correction", ".queue", ".invoice"}
	for node, msg := range findings2615(t, admin, app) {
		for _, w := range words {
			if strings.HasSuffix(node, w) {
				t.Errorf("manage_application get reports a table from a literal's words, %s: %s", node, msg)
			}
		}
		if strings.HasSuffix(node, schema+".rule") {
			t.Errorf("manage_application get reports the written rule table: %s", msg)
		}
	}
	got := links2540(diagram2540(t, admin, app, "?version=latest"))
	for _, want := range []string{
		"script:" + id + " writes table:" + conn2539 + "/" + schema + ".rule",
		"script:" + id + " reads table:" + conn2539 + "/" + schema + ".supplier",
	} {
		if !contains2540(got, want) {
			t.Errorf("the diagram lacks %q: %v", want, got)
		}
	}
	for _, l := range got {
		for _, w := range words {
			if strings.HasSuffix(l, w) {
				t.Errorf("the diagram draws a literal's word as a table: %s", l)
			}
		}
	}
}

// TestIssue2849_AQuotedTableNameIsStillProtected: a covered persona's
// statement that uses the protected column other than by selecting it is
// refused naming the column and the handler however the table's name is
// quoted, and selecting the column as it is through a quoted name still
// answers, masked.
func TestIssue2849_AQuotedTableNameIsStillProtected(t *testing.T) {
	admin := connect(t)
	persona, covered, _ := tempPersona(t, admin, "acc-2849-quoted", nil)
	table := protectedTable(t, admin)
	name := handlerNonce("acc-2849-quoted")
	saveHandlerScript(t, admin, name, maskTransform, "transform")
	bindHandler(t, admin, name, "", "transform", map[string]any{"connection": transformReader, "personas": []string{persona}, "protected": []string{table + ".ssn"}})
	parts := strings.Split(table, ".")
	quoted := `"` + strings.Join(parts, `"."`) + `"`
	lastQuoted := parts[0] + "." + parts[1] + `."` + parts[2] + `"`
	for label, sql := range map[string]string{
		"every part quoted":     "SELECT substr(ssn, 1, 3) FROM " + quoted,
		"the table part quoted": "SELECT name FROM " + lastQuoted + " WHERE ssn = '" + rawSSN + "'",
		"a quoted join":         "SELECT a.name FROM " + table + " a JOIN " + quoted + " b ON a.ssn = b.ssn",
	} {
		res, text := trinoRead(t, covered, sql)
		if !res.IsError || !strings.Contains(text, table+".ssn") || !strings.Contains(text, name) {
			t.Errorf("%s: the protected column was not refused naming the column and %s: %s", label, name, text)
		}
		if strings.Contains(text, rawSSN[:3]+"\"") {
			t.Errorf("%s leaked part of the value: %s", label, text)
		}
	}
	res, text := trinoRead(t, covered, "SELECT name, ssn FROM "+quoted)
	if res.IsError || strings.Contains(text, rawSSN) || !strings.Contains(text, maskedSSN) {
		t.Errorf("selecting the column as it is through a quoted name: %s", text)
	}
}
