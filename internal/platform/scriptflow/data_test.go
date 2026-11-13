package scriptflow

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDataRefs is what each statement step names of its data (#2539, #2540):
// its connection and the tables it reads and writes, marked computed when
// either is only known when the script runs.
func TestDataRefs(t *testing.T) {
	refs := DataRefs(`
def place(tx):
    """Places the order."""
    tx.execute("INSERT INTO shop.orders (id) SELECT id FROM shop.carts", params = {})
    tx.query("SELECT count(*) FROM shop.items")

platform.query("SELECT * FROM shop.orders o JOIN billing.invoices i ON i.o = o.id", connection = "appdb")
platform.query("SELECT 1 FROM hive.s.t")
platform.execute("UPDATE shop.stock SET n = n - 1", connection = "appdb")
platform.query("SELECT * FROM shop.orders", connection = run.params["c"])
platform.query("SELECT * FROM " + run.params["t"], connection = "appdb")
platform.transaction("appdb", place)
platform.batch("appdb", ["DELETE FROM shop.holds", {"sql": "INSERT INTO audit.log VALUES (1)"}])
`)
	require.Len(t, refs, 7)
	assert.Equal(t, DataRef{Connection: "appdb", Reads: []string{"shop.orders", "billing.invoices"}, Writes: []string{}}, refs[0])
	assert.Equal(t, DataRef{Reads: []string{"hive.s.t"}, Writes: []string{}}, refs[1], "no connection named: the default one")
	assert.Equal(t, DataRef{Connection: "appdb", Reads: []string{}, Writes: []string{"shop.stock"}, Write: true}, refs[2])
	assert.True(t, refs[3].Computed, "a computed connection")
	assert.Empty(t, refs[3].Connection)
	assert.True(t, refs[4].Computed, "a computed table")
	assert.Equal(t, DataRef{Connection: "appdb", Reads: []string{"shop.carts", "shop.items"}, Writes: []string{"shop.orders"}, Write: true}, refs[5],
		"a transaction's statements through tx")
	assert.Equal(t, DataRef{Connection: "appdb", Reads: []string{}, Writes: []string{"shop.holds", "audit.log"}, Write: true}, refs[6])

	assert.Equal(t, "public", SchemaOf("orders"))
	assert.Equal(t, "shop", SchemaOf("shop.orders"))
	assert.Equal(t, "shop", SchemaOf(`"shop"."orders"`))
	assert.Equal(t, "s", SchemaOf("hive.s.t"))
	assert.Empty(t, DataRefs("not starlark ("))
}

// TestDataRefs_WritesTheAnalyzerMissed is each statement shape #2615 found
// written by a part and reported as written by none: a data-modifying common
// table expression, statements a transaction's function passes to tx.batch,
// written in the call or bound to a name, a top-level platform.batch of a
// bound list, and a statement chosen when the script runs, which is a write
// whose table is unknown.
func TestDataRefs_WritesTheAnalyzerMissed(t *testing.T) {
	refs := DataRefs(`
STATEMENTS = {"a": ["UPDATE s.counter SET n = n + 1"]}

def record(tx):
    """Records a document and its invoice."""
    tx.batch([{"sql": "WITH added AS (INSERT INTO s.document (body) VALUES ($1) RETURNING id) INSERT INTO s.invoice (document_id) SELECT id FROM added", "params": ["x"]}])

def tidy(tx):
    """Tidies, from a list bound in the function."""
    steps = ["DELETE FROM s.hold WHERE expired", {"sql": "UPDATE s.stock SET n = 0"}]
    tx.batch(statements = steps)

def main():
    """Runs."""
    platform.transaction("appdb", record)
    platform.transaction("appdb", tidy)
    pending = ["INSERT INTO s.audit VALUES (1)"]
    platform.batch("appdb", pending)
    platform.execute(run.params["sql"], connection = "appdb")
    platform.batch("appdb", run.params["stmts"])
    platform.query("WITH recent AS (SELECT * FROM s.invoice) SELECT * FROM recent", connection = "appdb")
`)
	require.Len(t, refs, 6)
	assert.Equal(t, DataRef{Connection: "appdb", Reads: []string{}, Writes: []string{"s.document", "s.invoice"}, Write: true}, refs[0],
		"a common table expression's target and the statement's own are writes, and the CTE is not a read")
	assert.Equal(t, DataRef{Connection: "appdb", Reads: []string{}, Writes: []string{"s.hold", "s.stock"}, Write: true}, refs[1],
		"tx.batch of a list bound in the function")
	assert.Equal(t, DataRef{Connection: "appdb", Reads: []string{}, Writes: []string{"s.audit"}, Write: true}, refs[2],
		"platform.batch of a bound list")
	assert.True(t, refs[3].Computed, "a statement chosen when the script runs")
	assert.True(t, refs[3].Write, "platform.execute writes, whatever table it names")
	assert.Equal(t, "appdb", refs[3].Connection)
	assert.True(t, refs[4].Computed, "a batch of statements assembled at run time")
	assert.True(t, refs[4].Write)
	assert.Equal(t, DataRef{Connection: "appdb", Reads: []string{"s.invoice"}, Writes: []string{}}, refs[5],
		"a read-only common table expression writes nothing")
}

// TestDataRefs_ComprehensionsAndLookups is #2623: the two shapes an
// application's scripts wrote its tables with and the analyzer read as no
// write. A tx.batch list built by a comprehension over a module constant,
// bound in the function the transaction's function is nested in, and a
// statement picked by a lookup into a dict of constant lists are each read as
// the statements they can run. A lookup with one candidate assembled at run
// time is still a write to a table named only when it runs.
func TestDataRefs_ComprehensionsAndLookups(t *testing.T) {
	refs := DataRefs(`
INSERT_DOCUMENT = """
    WITH added AS (
        INSERT INTO app.document (body) SELECT $1 ON CONFLICT (content_hash) DO NOTHING RETURNING id
    )
    INSERT INTO app.invoice (document_id) SELECT id FROM added
"""
INSERT_EMAIL = "INSERT INTO app.email (subject) VALUES ($1)"
UPSERT_COMPANIES = "INSERT INTO app.company (code) SELECT code FROM x ON CONFLICT (code) DO UPDATE SET code = excluded.code"
UPSERT_SITES = "INSERT INTO app.site (code) SELECT code FROM x ON CONFLICT (code) DO UPDATE SET code = excluded.code"
UPSERT_SUPPLIERS = "INSERT INTO app.supplier (code) SELECT code FROM x ON CONFLICT (code) DO UPDATE SET code = excluded.code"
CONNECTION = "appdb"

def save(documents, email):
    """Saves the documents with the email they came in."""
    statements = [{"sql": INSERT_DOCUMENT, "params": d} for d in documents]

    def write(tx):
        tx.execute(INSERT_EMAIL, params = email)
        if statements:
            tx.batch(statements)

    return platform.transaction(CONNECTION, write, on_error = "return")

def refresh(kind, rows):
    """Refreshes one cache table."""
    spec = {
        "companies": ["q", "pkg", "company", UPSERT_COMPANIES],
        "sites": ["q", "pkg", "site", UPSERT_SITES],
        "suppliers": ["q", "pkg", "supplier", UPSERT_SUPPLIERS],
    }[kind]
    platform.execute(spec[3], connection = CONNECTION, params = {"rows": rows})

def custom(kind):
    """One candidate is built when it runs."""
    spec = {"a": [UPSERT_SITES], "b": ["INSERT INTO app.t_" + kind + " VALUES (1)"]}[kind]
    platform.execute(spec[0], connection = CONNECTION)

def pending(rows):
    """A top-level batch of a comprehension."""
    platform.batch(CONNECTION, [UPSERT_SITES for r in rows])

def main():
    """Runs."""
    save(run.params["docs"], run.params["email"])
    refresh(run.params["kind"], [])
    custom(run.params["kind"])
    pending([])
`)
	require.Len(t, refs, 4)
	assert.Equal(t, DataRef{Connection: "appdb", Reads: []string{}, Writes: []string{"app.email", "app.document", "app.invoice"}, Write: true}, refs[0],
		"a tx.batch of a comprehension bound in the enclosing function")
	assert.Equal(t, DataRef{Connection: "appdb", Reads: []string{"x"}, Writes: []string{"app.company", "app.site", "app.supplier"}, Write: true}, refs[1],
		"a statement picked by a lookup is every statement it can pick")
	assert.True(t, refs[2].Computed, "one candidate is assembled at run time")
	assert.True(t, refs[2].Write)
	assert.Equal(t, []string{"app.site"}, refs[3].Writes, "a top-level batch of a comprehension")
}

// TestCandidates is what a lookup can evaluate to: a constant key picks one
// value, a computed key every value, through names, nested lookups, tuples,
// negative indexes and a local whose value is known; and what it cannot say.
func TestCandidates(t *testing.T) {
	g := Derive(`
A = "INSERT INTO s.a VALUES (1)"
B = "INSERT INTO s.b VALUES (1)"
PAIRS = {"x": (A, B), "y": (B, A)}

def main():
    """Runs."""
    k = "y"
    platform.execute(PAIRS["x"][-1], connection = "c")
    platform.execute(PAIRS[run.params["p"]][0], connection = "c")
    platform.execute(PAIRS[k][0], connection = "c")
    platform.execute(PAIRS["z"][0], connection = "c")
    platform.execute([A, B][5], connection = "c")
    platform.execute(run.params["m"][0], connection = "c")
    platform.execute(("UPDATE s.u SET n = 1"), connection = "c")
`)
	var subtitles []string
	for _, n := range g.Nodes {
		if n.Kind == KindWrite {
			subtitles = append(subtitles, n.Subtitle)
		}
	}
	assert.Equal(t, []string{
		"INSERT s.b",
		"INSERT s.a, s.b",
		"INSERT s.b",
		"SQL assembled at run time",
		"SQL assembled at run time",
		"SQL assembled at run time",
		"UPDATE s.u",
	}, subtitles)
}

// TestWritesOf is the tables a statement writes, by its verb, and the
// positions a verb is not a write in.
func TestWritesOf(t *testing.T) {
	tests := []struct {
		name string
		sql  string
		want []write
	}{
		{"a plain insert", "INSERT INTO s.t (a) VALUES (1)", []write{{"INSERT", "s.t"}}},
		{
			"a CTE insert and the statement's own", "with a as (insert into s.x values (1) returning id) update s.y set n = 1 from a",
			[]write{{"INSERT", "s.x"}, {"UPDATE", "s.y"}},
		},
		{
			"two CTEs, one deleting", "WITH a AS (DELETE FROM s.old RETURNING *), b AS (SELECT 1) INSERT INTO s.archive SELECT * FROM a",
			[]write{{"DELETE", "s.old"}, {"INSERT", "s.archive"}},
		},
		{
			"ON CONFLICT DO UPDATE is not a second write", "INSERT INTO s.t (id) VALUES (1) ON CONFLICT (id) DO UPDATE SET n = 1",
			[]write{{"INSERT", "s.t"}},
		},
		{
			"a CTE upsert", "WITH a AS (INSERT INTO s.t (id) VALUES (1) ON CONFLICT (id) DO UPDATE SET n = 1 RETURNING id) SELECT * FROM a",
			[]write{{"INSERT", "s.t"}},
		},
		{"SELECT FOR UPDATE writes nothing", "WITH a AS (SELECT * FROM s.t FOR UPDATE) SELECT * FROM a", nil},
		{"a read", "SELECT * FROM s.t", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, writesOf(tt.sql))
		})
	}
}

// TestOutputRefs is what each output step names (#2540).
func TestOutputRefs(t *testing.T) {
	refs := OutputRefs(`
def main():
    """Writes."""
    platform.export("daily", [{"a": 1}], "csv")
    platform.export("x-" + run.params["d"], [{"a": 1}], "csv")
    platform.export("drop", [{"a": 1}], "csv", destination = "acme-drop")
    platform.publish_data("dash", {"n": 1})
    platform.notify("ops", "Done", "body")
    platform.publish("team", "report")
`)
	require.Len(t, refs, 6)
	assert.Equal(t, OutputRef{Kind: KindExport, Name: "daily", Portal: true}, refs[0])
	assert.True(t, refs[1].Computed)
	assert.False(t, refs[2].Portal, "a bucket destination is not an asset")
	assert.Equal(t, OutputRef{Kind: KindPublishData, Name: "dash", Portal: true}, refs[3])
	assert.Equal(t, OutputRef{Kind: KindNotify, Name: "ops"}, refs[4])
	assert.Equal(t, KindPublish, refs[5].Kind)
	assert.Equal(t, "team", refs[5].Name)
}

// TestDataRefs_BatchForms is the other ways a batch's statements are written
// (#2615): a parenthesized list, a module-level list, the statements keyword
// after another keyword, and a batch naming none.
func TestDataRefs_BatchForms(t *testing.T) {
	refs := DataRefs(`
PURGE = ["DELETE FROM s.stale"]

def sweep(tx):
    """Sweeps, with a keyword before the statements."""
    tx.batch(isolation = "serializable", statements = ["UPDATE s.counter SET n = 0"])

def idle(tx):
    """Names no statements."""
    tx.batch()

def main():
    """Runs."""
    platform.batch("appdb", (["INSERT INTO s.paren VALUES (1)"]))
    platform.batch("appdb", PURGE)
    platform.transaction("appdb", sweep)
    platform.transaction("appdb", idle)
`)
	require.Len(t, refs, 4)
	assert.Equal(t, []string{"s.paren"}, refs[0].Writes, "a parenthesized list")
	assert.Equal(t, []string{"s.stale"}, refs[1].Writes, "a module-level list")
	assert.Equal(t, []string{"s.counter"}, refs[2].Writes, "the statements keyword after another")
	assert.Empty(t, refs[3].Writes, "a batch naming no statements writes no table it names")
}
