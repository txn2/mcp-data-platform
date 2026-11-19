package scriptflow

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptlib"
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

// TestDataRefs_CopyInIsAWrite pins #2665 (a): rows written through COPY are a
// write to the table copy_in names, in a transaction (tx.copy_in) and outside
// one (platform.copy_in); a table the script computes makes the step computed.
func TestDataRefs_CopyInIsAWrite(t *testing.T) {
	refs := DataRefs(`
COLUMNS = ["id", "url"]

def work(tx):
    """Loads the photos."""
    tx.copy_in("s.photos", COLUMNS, [])

def main():
    """Syncs."""
    platform.transaction("appdb", work)
    platform.copy_in("appdb", "s.albums", COLUMNS, [])
    platform.copy_in("appdb", table = "s.tags", columns = COLUMNS, rows = [])
    platform.copy_in("appdb", run.params["t"], COLUMNS, [])
`)
	require.Len(t, refs, 4)
	assert.Equal(t, DataRef{Connection: "appdb", Reads: []string{}, Writes: []string{"s.photos"}, Write: true}, refs[0],
		"tx.copy_in is the transaction's write")
	assert.Equal(t, DataRef{Connection: "appdb", Reads: []string{}, Writes: []string{"s.albums"}, Write: true}, refs[1])
	assert.Equal(t, DataRef{Connection: "appdb", Reads: []string{}, Writes: []string{"s.tags"}, Write: true}, refs[2], "by keyword")
	assert.True(t, refs[3].Computed, "a computed table")
	assert.Empty(t, refs[3].Writes)

	g := Derive(`
def work(tx):
    """Loads the photos."""
    tx.copy_in("s.photos", ["id"], [])

def main():
    """Syncs."""
    platform.transaction("appdb", work)
    platform.copy_in("appdb", "s.albums", ["id"], [])
`)
	var details []string
	subtitles := make([]string, 0, len(g.Nodes))
	for _, n := range g.Nodes {
		details = append(details, n.Detail...)
		subtitles = append(subtitles, n.Subtitle)
	}
	assert.Contains(t, details, "COPY s.photos", "the transaction card lists the copy")
	assert.Contains(t, subtitles, "COPY s.albums")
}

// TestDataRefs_HandlersNamedAsValues pins #2665 (b): a function reached only
// by being named, as the argument of a wrapper that returns a lambda calling
// it or as a route table's value, is followed, so its statements count.
func TestDataRefs_HandlersNamedAsValues(t *testing.T) {
	const handler = `ADD_SQL = "INSERT INTO s.guesses (g) VALUES (:g)"

def refuse(code, msg):
    """Refuses."""
    return {"status": code, "body": msg}

def signed_in(fn):
    """Gates a route on a signed-in caller."""
    return lambda req: fn(req) if req["caller"] else refuse(401, "sign in")

def guess(req):
    """Records a guess."""
    platform.execute(ADD_SQL, {"g": 1}, connection = "appdb")
    return {"status": 200}

def handle(req):
    """Dispatches."""
    fn = routes().get(req["method"] + " " + req["path"])
    return fn(req)
`
	for name, routes := range map[string]string{
		"through a wrapper": "\ndef routes():\n    \"\"\"Routes.\"\"\"\n    return {\"POST guess\": signed_in(guess)}\n",
		"named directly":    "\ndef routes():\n    \"\"\"Routes.\"\"\"\n    return {\"POST guess\": guess}\n",
	} {
		t.Run(name, func(t *testing.T) {
			refs := DataRefs(handler + routes)
			require.Len(t, refs, 1)
			assert.Equal(t, DataRef{Connection: "appdb", Reads: []string{}, Writes: []string{"s.guesses"}, Write: true}, refs[0])
		})
	}

	shadowed := DataRefs(`
def guess(req):
    """Records a guess."""
    platform.execute("INSERT INTO s.guesses (g) VALUES (1)", connection = "appdb")

def main():
    """A local variable named like a function is the variable."""
    guess = 1
    print(guess)
`)
	assert.Empty(t, shadowed, "a variable shadowing a function name is not a call of it")
}

// writesOf is the tables every step of a script writes, and whether any step
// is computed.
func writesOf(src string) (writes []string, computed bool) {
	for _, r := range DataRefs(src) {
		writes = union(writes, r.Writes)
		computed = computed || r.Computed
	}
	return writes, computed
}

// TestDataRefs_BatchStatementsFollowed is #2699's shape: platform.batch and
// platform.transaction statements that are module constants reached through a
// parameter, a field of a dict constant, a helper's return value, list
// concatenation, .append, and a comprehension over any of these. Every table
// is drawn written and no step is computed.
func TestDataRefs_BatchStatementsFollowed(t *testing.T) {
	writes, computed := writesOf(`
CONNECTION = "shop"
UPSERT_CUSTOMERS = "INSERT INTO shop.customer (code, name) SELECT code, name FROM x ON CONFLICT (code) DO UPDATE SET name = EXCLUDED.name"
UPSERT_ITEMS = "INSERT INTO shop.item (sku) VALUES ($1)"
RECORD_ORDER = "INSERT INTO shop.orders (id) VALUES ($1)"
INSERT_LINE = "INSERT INTO shop.order_line (order_id) VALUES ($1)"
TOUCH = "UPDATE shop.sync SET at = now()"
SPECS = {
    "customers": {"query": "q", "write": [UPSERT_CUSTOMERS]},
    "items": {"query": "q", "write": [UPSERT_ITEMS]},
}

def write_page(spec, rows):
    """Writes one page of a spec's rows."""
    platform.batch(CONNECTION, [{"sql": sql, "params": {"rows": json.encode(rows)}} for sql in spec["write"]])

def commit(statements):
    """Commits statements in one round trip."""
    return platform.batch(CONNECTION, statements, on_error = "return")

def line_statements(made):
    """The statements of an order's lines."""
    out = []
    for line in made["lines"]:
        out.append({"sql": INSERT_LINE, "params": [line]})
    return out

def record(made):
    """Records an order and its lines."""
    return commit([{"sql": RECORD_ORDER, "params": [made["id"]]}] + line_statements(made))

def touch(tx):
    """Touches the sync row."""
    tx.batch([TOUCH])

def main():
    """Syncs every spec, then an order."""
    for name, spec in SPECS.items():
        write_page(spec, [])
    record({"id": 1, "lines": []})
    platform.transaction(CONNECTION, touch)
`)
	assert.False(t, computed, "every statement is a module constant")
	assert.ElementsMatch(t, []string{"shop.customer", "shop.item", "shop.orders", "shop.order_line", "shop.sync"}, writes)
}

// TestDataRefs_BatchFollowedOneSpec: a parameter passed one dict of a dict
// constant is that dict's statements alone, and a statement built when the
// script runs still leaves its step computed.
func TestDataRefs_BatchFollowedOneSpec(t *testing.T) {
	writes, computed := writesOf(`
A = "INSERT INTO s.a VALUES (1)"
B = "INSERT INTO s.b VALUES (1)"
SPECS = {"a": {"write": [A]}, "b": {"write": [B]}}

def write(spec):
    """Writes."""
    platform.batch("c", [{"sql": s} for s in spec["write"]])

def main():
    """One spec."""
    write(SPECS["a"])
`)
	assert.False(t, computed)
	assert.Equal(t, []string{"s.a"}, writes)

	_, computed = writesOf(`
def write(statements):
    """Writes."""
    platform.batch("c", statements)

def main():
    """Built when it runs."""
    write(["INSERT INTO " + run.params["t"] + " VALUES (1)"])
`)
	assert.True(t, computed, "a statement built at run time")

	_, computed = writesOf(`
def write(statements):
    """Writes."""
    platform.batch("c", statements)

def main():
    """From a call the source does not define."""
    write(json.decode(run.params["statements"]))
`)
	assert.True(t, computed, "a list from a call the source does not define")
}

// TestDataRefs_BatchFollowedThroughASpecBuilder: the spec a page writer is
// handed is a field of a dict whose value a function returns from a dict it
// builds (spec_of(phase) returning specs[phase]); every table any spec's
// "write" list names is written (#2699).
func TestDataRefs_BatchFollowedThroughASpecBuilder(t *testing.T) {
	writes, computed := writesOf(`
CONNECTION = "apps"
UPSERT_A = "INSERT INTO s.a (code) SELECT 1"
UPSERT_B = "INSERT INTO s.b (code) SELECT 1"
RENAME_B = "UPDATE s.b_type SET name = 'x'"
PHASES = ["a", "b"]

def spec_of(phase, flag):
    """How one phase writes."""
    specs = {
        "a": {"query": "qa", "write": [UPSERT_A], "page": 10},
        "b": {"query": "qb" if flag else "qc", "write": [UPSERT_B, RENAME_B], "page": 10},
    }
    return specs[phase]

def write_page(spec, rows):
    """Writes one page."""
    encoded = json.encode(rows)
    platform.batch(CONNECTION, [{"sql": sql, "params": {"rows": encoded}} for sql in spec["write"]])

def refresh_phase(phase, job):
    """Pages one list."""
    reading = {"spec": spec_of(phase, job["flag"]), "ctx": {}}
    for _ in range(3):
        spec = reading["spec"]
        write_page(spec, [1])

def main():
    """Every phase."""
    job = {"flag": True}
    for phase in PHASES:
        refresh_phase(phase, job)
`)
	assert.False(t, computed)
	assert.ElementsMatch(t, []string{"s.a", "s.b", "s.b_type"}, writes)
}

// TestDataRefs_BatchFollowedThroughHelperStatements: a list one helper builds
// and grows, holding statements another helper returns, committed through a
// third (#2699).
func TestDataRefs_BatchFollowedThroughHelperStatements(t *testing.T) {
	writes, computed := writesOf(`
CONNECTION = "apps"
SEND = "UPDATE s.invoice SET status = 'sent'"
HOLD = "INSERT INTO s.invoice_hold (id) VALUES (:id)"
EVENT = "INSERT INTO s.invoice_event (kind) VALUES (:kind)"

def event(invoice_id, kind):
    """The statement recording an event."""
    return {"sql": EVENT, "params": {"id": invoice_id, "kind": kind}}

def commit(statements):
    """Runs statements in one transaction."""
    return platform.batch(CONNECTION, statements, on_error = "return")

def finish(invoice_id, statements):
    """Commits an action's statements."""
    return commit(statements)

def send(inv):
    """Sends an invoice."""
    return finish(inv["id"], [{"sql": SEND, "params": {"id": inv["id"]}}, event(inv["id"], "sent")])

def hold(inv):
    """Holds an invoice."""
    statements = [event(inv["id"], "checked")]
    if inv["bad"]:
        statements.append({"sql": HOLD, "params": {"id": inv["id"]}})
    return finish(inv["id"], statements)

def main():
    """Both."""
    send({"id": 1})
    hold({"id": 1, "bad": True})
`)
	assert.False(t, computed)
	assert.ElementsMatch(t, []string{"s.invoice", "s.invoice_event", "s.invoice_hold"}, writes)
}

// TestDataRefs_NamesLoadedFromALibrary: a statement constant and a helper a
// script loads from a library are read from the library's source at the
// version the load names (#2699); without it they are run-time values.
func TestDataRefs_NamesLoadedFromALibrary(t *testing.T) {
	lib := `PREFIX = "x3ap"
ADD_EVENT = "INSERT INTO " + PREFIX + ".invoice_event (kind) VALUES (:kind)"
WITHDRAW = "UPDATE x3ap.approval_request SET status = 'withdrawn'"
REPLACED = "UPDATE x3ap.approval_request SET replaced_by = :new"

def result_statements(made):
    """One history row per request."""
    return [{"sql": ADD_EVENT, "params": {"kind": m}} for m in made] + [{"sql": REPLACED, "params": {}}]
`
	src := `load("lib:ap-approval@2", "ADD_EVENT", "WITHDRAW", "result_statements")

CONNECTION = "apps"

def main():
    """Withdraws, records and explains."""
    platform.execute(WITHDRAW, connection = CONNECTION)
    platform.execute(ADD_EVENT, connection = CONNECTION, params = {"kind": "x"})
    platform.batch(CONNECTION, result_statements(["a"]), on_error = "return")
`
	libs := Libraries{scriptlib.Ref{Name: "ap-approval", Version: 2}: lib}
	var writes []string
	computed := false
	for _, r := range DataRefsWith(src, libs) {
		writes = union(writes, r.Writes)
		computed = computed || r.Computed
	}
	assert.False(t, computed)
	assert.ElementsMatch(t, []string{"x3ap.approval_request", "x3ap.invoice_event"}, writes)

	without := false
	for _, r := range DataRefsWith(src, Libraries{scriptlib.Ref{Name: "ap-approval", Version: 1}: lib}) {
		without = without || r.Computed
	}
	assert.True(t, without, "a version the load does not name is not read")
}

// TestDataRefs_ALookupIntoAGrownList: a statement picked out of a list the
// function built in steps is each statement the list holds (#2699).
func TestDataRefs_ALookupIntoAGrownList(t *testing.T) {
	writes, computed := writesOf(`
A = "INSERT INTO s.a VALUES (1)"
B = "INSERT INTO s.b VALUES (1)"

def main():
    """Runs one of two statements."""
    xs = [A]
    xs.append(B)
    platform.execute(xs[run.params["i"]], connection = "c")
`)
	assert.False(t, computed)
	assert.ElementsMatch(t, []string{"s.a", "s.b"}, writes)
}

// TestDataRefs_AParameterGrownInTheCallee: a parameter the function only
// appends to holds what the caller passed, then what it added (#2699).
func TestDataRefs_AParameterGrownInTheCallee(t *testing.T) {
	writes, computed := writesOf(`
A = "INSERT INTO s.a VALUES (1)"
B = "INSERT INTO s.b VALUES (1)"

def commit(statements):
    """Adds the audit row and commits."""
    statements.append(B)
    platform.batch("c", statements)

def main():
    """Commits."""
    commit([A])
`)
	assert.False(t, computed)
	assert.ElementsMatch(t, []string{"s.a", "s.b"}, writes)

	_, computed = writesOf(`
B = "INSERT INTO s.b VALUES (1)"

def main():
    """A list grown from what a loop unpacks."""
    for _, statements in run.params["pairs"]:
        statements.append(B)
        platform.batch("c", statements)
`)
	assert.True(t, computed, "what the list held first is not known")
}
