package schemaref

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUnqualified(t *testing.T) {
	got, n := Unqualified(`SELECT * FROM orders.invoices i JOIN "orders".lines l ON true WHERE note = 'open orders.'`, []string{"orders"})
	assert.Equal(t, 2, n)
	assert.Equal(t, `SELECT * FROM invoices i JOIN lines l ON true WHERE note = 'open orders.'`, got)
	got, n = Unqualified("FROM warehouse.orders.invoices, reorders.x", []string{"orders"})
	assert.Zero(t, n, "a three-part name and a name only ending in the schema's stay")
	assert.Equal(t, "FROM warehouse.orders.invoices, reorders.x", got)
}

// TestAdded is what an edit adds, which the save gate refuses (#2709): a
// qualified name the new source's strings hold more often than the old
// one's, on the line it first appears.
func TestAdded(t *testing.T) {
	before := "A = \"SELECT * FROM orders.invoices\"\n\ndef main():\n    \"\"\"Runs.\"\"\"\n    orders = []\n    return orders\n"
	_, ok := Added(before, before+"\n# a comment\n", []string{"orders"})
	assert.False(t, ok, "what the script already said is left alone")
	_, ok = Added(before, before+"B = \"SELECT 1 FROM invoices\"\n", []string{"orders"})
	assert.False(t, ok, "an unqualified name is what an application writes")

	after := before + "B = \"INSERT INTO orders.refunds VALUES (1)\"\n"
	got, ok := Added(before, after, []string{"orders", "orders_dev"})
	assert.True(t, ok)
	assert.Equal(t, Name{Line: 7, Schema: "orders", Name: "refunds"}, got)

	twice := before + "C = \"SELECT * FROM orders.invoices\"\n"
	got, ok = Added(before, twice, []string{"orders"})
	assert.True(t, ok, "a second copy of a name it already had is added")
	assert.Equal(t, Name{Line: 7, Schema: "orders", Name: "invoices"}, got)
	_, ok = Added("", "def (:\n", []string{"orders"})
	assert.False(t, ok, "a source that does not parse adds nothing")
}

// TestScript rewrites only strings, counts each name, reads loads, and moves
// a load to a library's new version (#2709).
func TestScript(t *testing.T) {
	src := "load(\"lib:sql@2\", \"Q\")\n\nA = \"SELECT * FROM orders.invoices\"\n\ndef main():\n    \"\"\"Runs.\"\"\"\n    orders = [A, A]\n    orders.append(Q)\n    return orders\n"
	out, n, loads := Script(src, []string{"orders"})
	assert.Equal(t, 1, n)
	assert.Contains(t, out, `A = "SELECT * FROM invoices"`)
	assert.Contains(t, out, "orders.append(Q)", "a Starlark name is code")
	assert.Equal(t, "sql", loads[0].Name)
	assert.Contains(t, WithLoads(out, map[string]int{"sql": 3}, loads), `load("lib:sql@3"`)
	assert.Equal(t, out, WithLoads(out, map[string]int{"sql": 2}, loads), "a load at the version already is left")
	same, none, _ := Script("def (:", []string{"orders"})
	assert.Equal(t, "def (:", same)
	assert.Zero(t, none)
}

// TestUnqualified_OnlyWhereSQLNamesAnObject leaves a quoted SQL value, a file
// and a sentence as written, and rewrites a name at the start of a text, after
// punctuation and after a keyword (#2709).
func TestUnqualified_OnlyWhereSQLNamesAnObject(t *testing.T) {
	schemas := []string{"x3ap"}
	for _, tc := range []struct{ in, want string }{
		{`INSERT INTO x3ap.outbox (source_table) VALUES ('x3ap.rule')`, `INSERT INTO outbox (source_table) VALUES ('x3ap.rule')`},
		{`SELECT 1 FROM x3ap.outbox WHERE source_table = 'x3ap.chat_message' AND note = 'it''s x3ap.rule'`, `SELECT 1 FROM outbox WHERE source_table = 'x3ap.chat_message' AND note = 'it''s x3ap.rule'`},
		{`x3ap.rule`, `rule`},
		{`SELECT x3ap.is_admin() AS a, coalesce(x3ap.f(1), 0), a.id, x3ap.token_hash(t)`, `SELECT is_admin() AS a, coalesce(f(1), 0), a.id, token_hash(t)`},
		{`UPDATE x3ap.links SET n = 1 WHERE x3ap.links.target = greatest(x3ap.links.expires_at, now())`, `UPDATE links SET n = 1 WHERE links.target = greatest(links.expires_at, now())`},
		{"SELECT a\\n  FROM x3ap.invoice", "SELECT a\\n  FROM invoice"},
	} {
		got, _ := Unqualified(tc.in, schemas)
		assert.Equal(t, tc.want, got, tc.in)
	}
	for _, same := range []string{`<link href="/_static/hello.css">`, `the page loads hello.css and hello.js`, `Follow the hello.outbox webhook`} {
		got, n := Unqualified(same, []string{"hello"})
		assert.Zero(t, n, same)
		assert.Equal(t, same, got)
	}
}

// TestScript_RewritesTheLiteralBody rewrites a string whose whole value is a
// qualified name and a single-quoted Starlark string, and lists the names a
// quoted SQL value holds without rewriting them (#2709).
func TestScript_RewritesTheLiteralBody(t *testing.T) {
	src := "A = 'SELECT * FROM x3ap.invoice'\n\ndef main():\n    \"\"\"Runs.\"\"\"\n    tx = []\n    tx.append(\"x3ap.lines\")\n    tx.append(\"\"\"INSERT INTO x3ap.outbox (source_table)\n    VALUES ('x3ap.invoice')\"\"\")\n    return [A, tx]\n"
	out, n, _ := Script(src, []string{"x3ap"})
	assert.Equal(t, 3, n)
	assert.Contains(t, out, `A = 'SELECT * FROM invoice'`)
	assert.Contains(t, out, `tx.append("lines")`)
	assert.Contains(t, out, `INSERT INTO outbox (source_table)`)
	assert.Contains(t, out, `VALUES ('x3ap.invoice')`)
	assert.Equal(t, []Value{{Line: 8, Text: "x3ap.invoice"}}, Values(src, []string{"x3ap"}))
	assert.Empty(t, Values("def (:", []string{"x3ap"}))
}

// TestAdded_IgnoresValuesAndFiles refuses no edit for a quoted SQL value or a
// file that only looks like a qualified name (#2709).
func TestAdded_IgnoresValuesAndFiles(t *testing.T) {
	before := "def main():\n    \"\"\"Runs.\"\"\"\n    return 1\n"
	_, ok := Added(before, before+"P = \"<link href='/_static/hello.css'>\"\n", []string{"hello"})
	assert.False(t, ok)
	_, ok = Added(before, before+"Q = \"SELECT 1 FROM outbox WHERE source_table = 'hello.greetings'\"\n", []string{"hello"})
	assert.False(t, ok)
}
