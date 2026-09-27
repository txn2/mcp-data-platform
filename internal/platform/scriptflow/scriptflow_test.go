package scriptflow

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/syntax"
)

// nodeByTitle finds the one node whose title starts with prefix.
func nodeByTitle(t *testing.T, g Graph, prefix string) Node {
	t.Helper()
	var found []Node
	for _, n := range g.Nodes {
		if strings.HasPrefix(n.Title, prefix) {
			found = append(found, n)
		}
	}
	require.Len(t, found, 1, "nodes titled %q in %+v", prefix, g.Nodes)
	return found[0]
}

func hasEdge(g Graph, from, to string) bool {
	for _, e := range g.Edges {
		if e.From == from && e.To == to {
			return true
		}
	}
	return false
}

const threeSteps = `
day = run.params["day"]
orders = platform.query("SELECT id, total FROM hive.sales.orders WHERE day = '" + day + "'", connection="trino")
ids = [r["id"] for r in orders["rows"]]
crm = platform.call("api_invoke_endpoint", {
    "connection": "crm",
    "method": "GET",
    "path": "/v1/accounts",
    "purpose": "Look up the account behind each order.",
    "query": {"ids": ids},
})
platform.export("orders", crm["rows"], format="parquet", destination="acme-drop", key="daily/orders.parquet",
                register={"connection": "trino", "table_name": "daily_orders"})
`

func TestDerive_StepsAndTheValuesBetweenThem(t *testing.T) {
	g := deriveGraph(threeSteps)
	require.True(t, g.OK, "%+v", g.Findings)

	q := nodeByTitle(t, g, "Query trino")
	assert.Equal(t, RoleReads, q.Role)
	assert.Equal(t, []string{"hive.sales.orders"}, q.Detail)
	assert.False(t, q.Computed)

	api := nodeByTitle(t, g, "API crm")
	assert.Equal(t, "GET /v1/accounts", api.Subtitle)
	assert.Equal(t, "Look up the account behind each order.", api.Purpose)

	exp := nodeByTitle(t, g, "Export PARQUET to acme-drop")
	assert.Equal(t, RoleOutput, exp.Role)
	assert.Equal(t, []string{"daily/orders.parquet"}, exp.Detail)

	tbl := nodeByTitle(t, g, "Table on trino")
	assert.Equal(t, "daily_orders", tbl.Subtitle)
	assert.Equal(t, KindTable, tbl.Kind)

	assert.True(t, hasEdge(g, q.ID, api.ID), "the query's ids feed the API call")
	assert.True(t, hasEdge(g, api.ID, exp.ID), "the API rows are exported")
	assert.True(t, hasEdge(g, exp.ID, tbl.ID), "the table is registered over the file")
	assert.False(t, hasEdge(g, q.ID, exp.ID), "implied by query -> api -> export")
	assert.Len(t, g.Edges, 3)

	require.Len(t, g.Params, 1)
	assert.Equal(t, "day", g.Params[0].Name)
	assert.Equal(t, []string{q.ID}, g.Params[0].Reaches)
	assert.False(t, g.Params[0].Decides)
}

const helperTwice = `
# Pull both feeds and write them out.
def pull():
    a = fetch("/v1/a", "Read the a feed.")
    b = fetch("/v1/b", "Read the b feed.")
    platform.export("both", a + b)

def fetch(path, why):
    return platform.call("api_invoke_endpoint", {"connection": "crm", "method": "GET", "path": path, "purpose": why})

pull()
`

func TestDerive_AHelperIsAStepPerCallSiteFoldedIntoItsCaller(t *testing.T) {
	g := deriveGraph(helperTwice)
	require.True(t, g.OK, "%+v", g.Findings)

	var apis []Node
	for _, n := range g.Nodes {
		if n.Kind == KindAPI {
			apis = append(apis, n)
		}
	}
	require.Len(t, apis, 2)
	assert.Equal(t, "GET /v1/a", apis[0].Subtitle)
	assert.Equal(t, "Read the a feed.", apis[0].Purpose)
	assert.Equal(t, "GET /v1/b", apis[1].Subtitle)
	assert.Equal(t, "Read the b feed.", apis[1].Purpose)
	for _, n := range apis {
		assert.Equal(t, "fetch", n.Wrapper, "a one-call wrapper is a chip on its step")
		assert.NotZero(t, n.Site)
	}
	assert.Equal(t, 4, apis[0].Site)
	assert.Equal(t, 5, apis[1].Site)

	// load() is the whole script, so there is no box around everything, and
	// fetch is folded, so it is no box either.
	assert.Empty(t, g.Groups)
}

const boxed = `
# Stage the day's orders.
def stage(day):
    rows = platform.query("SELECT * FROM hive.sales.orders WHERE day = '%s'" % day)
    platform.export("orders-" + day, rows["rows"], format="jsonl", destination="resources", key="orders/" + day + ".jsonl")

def report():
    rows = platform.query("SELECT count(*) AS n FROM hive.sales.orders")
    platform.export("summary", rows["rows"], format="csv")

stage("2026-01-01")
stage("2026-01-02")
report()
`

func TestDerive_AFunctionWithStepsIsOneBox(t *testing.T) {
	g := deriveGraph(boxed)
	require.True(t, g.OK, "%+v", g.Findings)
	require.Len(t, g.Groups, 2)
	assert.Equal(t, "/report", g.Groups[0].ID)
	st := g.Groups[1]
	assert.Equal(t, "/stage", st.ID)
	assert.Equal(t, "stage(day)", st.Label)
	assert.Equal(t, "Stage the day's orders.", st.Caption)
	assert.Equal(t, []int{11, 12}, st.CalledFrom)
	assert.Equal(t, 3, st.DefLine)

	// Two expansions of stage, one box, each rendered with its own argument.
	var keys []string
	for _, n := range g.Nodes {
		if n.Kind == KindExport && n.Group == "/stage" {
			keys = append(keys, n.Detail[0])
		}
	}
	assert.Equal(t, []string{"orders/2026-01-01.jsonl", "orders/2026-01-02.jsonl"}, keys)
}

func TestDerive_ModuleConstantsAreShownByValue(t *testing.T) {
	g := deriveGraph(`
WAREHOUSE = "warehouse"
SCHEMA = "hive.sales"
rows = platform.query("SELECT * FROM " + SCHEMA + ".orders", connection=WAREHOUSE)
`)
	require.True(t, g.OK)
	q := nodeByTitle(t, g, "Query warehouse")
	assert.Equal(t, []string{"hive.sales.orders"}, q.Detail)
	assert.False(t, q.Computed)
}

func TestDerive_AComputedDestinationIsNeverGuessed(t *testing.T) {
	g := deriveGraph(`
dest = run.params["target"]
platform.export("out", [], format="csv", destination=dest, key="x.csv")
`)
	require.True(t, g.OK)
	exp := nodeByTitle(t, g, "Export CSV to")
	assert.True(t, exp.Computed)
	assert.Equal(t, `Export CSV to {run.params["target"]}`, exp.Title)
}

func TestDerive_ComputedPartsOfATableNameAreKept(t *testing.T) {
	g := deriveGraph(`
for t in ["a", "b"]:
    platform.query("SELECT * FROM warehouse.public." + t)
`)
	require.True(t, g.OK)
	q := nodeByTitle(t, g, "Query default connection")
	assert.Equal(t, []string{"warehouse.public.{t}"}, q.Detail)
	assert.True(t, q.Computed)
	assert.Equal(t, []string{`for t in ["a", "b"]`}, q.Loops)
}

func TestDerive_ParametersReachTheStepsTheirValueReaches(t *testing.T) {
	g := deriveGraph(`
mode = run.params["mode"]
limit = run.params.get("limit", 10)
rows = platform.query("SELECT * FROM t LIMIT %d" % limit)
if mode == "full":
    platform.export("all", rows["rows"])
else:
    platform.export("some", rows["rows"][:5])
`)
	require.True(t, g.OK)
	require.Len(t, g.Params, 2)
	byName := map[string]Param{}
	for _, p := range g.Params {
		byName[p.Name] = p
	}
	assert.Equal(t, []string{"op:1"}, byName["limit"].Reaches)
	assert.False(t, byName["limit"].Decides)
	assert.Empty(t, byName["mode"].Reaches)
	assert.True(t, byName["mode"].Decides)
}

func TestDerive_StateReadAndSaved(t *testing.T) {
	g := deriveGraph(`
since = run.state.get("since", "2026-01-01")
rows = platform.query("SELECT * FROM t WHERE ts > '" + since + "'")
platform.save_state({"since": rows["rows"][-1]["ts"]})
`)
	require.True(t, g.OK)
	require.Equal(t, StateNodeID, g.Nodes[0].ID)
	assert.Equal(t, RoleInput, g.Nodes[0].Role)
	assert.Equal(t, 2, g.Nodes[0].Line)
	save := nodeByTitle(t, g, "Save state")
	assert.True(t, hasEdge(g, StateNodeID, "op:1"))
	var back *Edge
	for i := range g.Edges {
		if g.Edges[i].Kind == EdgeState {
			back = &g.Edges[i]
		}
	}
	require.NotNil(t, back)
	assert.Equal(t, save.ID, back.From)
	assert.Equal(t, StateNodeID, back.To)
}

func TestDerive_AddingAnExportAddsOneCard(t *testing.T) {
	v1 := `rows = platform.query("SELECT * FROM hive.sales.orders")
platform.export("a", rows["rows"])
`
	v2 := v1 + `platform.export("b", rows["rows"], format="csv", destination="acme-drop", key="b.csv")
`
	g1, g2 := deriveGraph(v1), deriveGraph(v2)
	require.Len(t, g2.Nodes, len(g1.Nodes)+1)
	for i, n := range g1.Nodes {
		assert.Equal(t, n.Title, g2.Nodes[i].Title)
	}
	assert.Equal(t, "Export CSV to acme-drop", g2.Nodes[len(g2.Nodes)-1].Title)
}

func TestDerive_ASourceThatDoesNotParseReturnsItsFindings(t *testing.T) {
	g := deriveGraph("def f(:\n  pass\n")
	assert.False(t, g.OK)
	require.NotEmpty(t, g.Findings)
	assert.Equal(t, 1, g.Findings[0].Line)
	assert.Empty(t, g.Nodes)

	g = deriveGraph("platform.query(undefined_name)")
	assert.False(t, g.OK)
	assert.Contains(t, g.Findings[0].Message, "undefined")
}

func TestDerive_AnEmptyGraphIsEmptyListsNotNull(t *testing.T) {
	for _, src := range []string{"x = 1 + 2\nprint(x)\n", "def f(:\n"} {
		b, err := json.Marshal(deriveGraph(src))
		require.NoError(t, err)
		for _, key := range []string{`"nodes":[]`, `"edges":[]`, `"groups":[]`, `"params":[]`, `"findings":`} {
			assert.Contains(t, string(b), key)
		}
		assert.NotContains(t, string(b), "null")
	}
}

func TestDerive_ProgressIsNotAStep(t *testing.T) {
	g := deriveGraph(`platform.progress("half way", done=1, total=2)
platform.result({"ok": True})
`)
	require.True(t, g.OK)
	require.Len(t, g.Nodes, 1)
	assert.Equal(t, KindResult, g.Nodes[0].Kind)
}

func TestDerive_MemberCards(t *testing.T) {
	g := deriveGraph(`
rows = platform.query("SELECT 1", connection="trino")
platform.publish_data("sales-dashboard", {"rows": rows["rows"]})
platform.notify("ops", "Loaded", body="done")
platform.publish("ops", "sales-dashboard")
platform.call("trino_execute", {"connection": "rw", "sql": "INSERT INTO hive.s.t SELECT 1"})
platform.call("trino_execute", {"connection": "rw", "sql": run.params["sql"]})
platform.call("trino_query", {"sql": "SELECT * FROM hive.s.u"})
platform.call("manage_asset", {"action": "update", "asset_id": "a1"})
platform.call("s3_list", {"connection": "lake"})
platform.call(run.params["tool"], {})
platform.call("api_invoke_endpoint", {"connection": "crm", "method": "delete", "path": "/x"})
platform.call("api_invoke_endpoint", {"connection": "crm", "operation_id": "listThings"})
platform.call("api_export", {"connection": "crm", "method": "GET", "path": "/big", "destination": "resources", "key": "big.json"})
platform.call("s3_object", run.params["args"])
`)
	require.True(t, g.OK, "%+v", g.Findings)
	pd := nodeByTitle(t, g, "Refresh data on portal")
	assert.Equal(t, "sales-dashboard", pd.Subtitle)
	nt := nodeByTitle(t, g, "Notify ops")
	assert.Equal(t, "Loaded", nt.Subtitle)
	pb := nodeByTitle(t, g, "Post to ops")
	assert.Equal(t, "sales-dashboard", pb.Subtitle)

	var writes []Node
	for _, n := range g.Nodes {
		if n.Kind == KindWrite {
			writes = append(writes, n)
		}
	}
	require.Len(t, writes, 2)
	assert.Equal(t, "INSERT hive.s.t", writes[0].Subtitle)
	assert.False(t, writes[0].Computed)
	assert.Equal(t, "SQL assembled at run time", writes[1].Subtitle)
	assert.True(t, writes[1].Computed)

	tq := nodeByTitle(t, g, "Query default connection")
	assert.Equal(t, []string{"hive.s.u"}, tq.Detail)
	ma := nodeByTitle(t, g, "manage asset")
	assert.Equal(t, RoleWrites, ma.Role)
	assert.Equal(t, []string{"asset_id a1"}, ma.Detail)
	assert.Equal(t, RoleReads, nodeByTitle(t, g, "s3 list").Role)
	assert.True(t, nodeByTitle(t, g, "Call {").Computed)
	assert.True(t, nodeByTitle(t, g, "s3 object").Computed, "arguments the source computes hide the connection")

	var apis []Node
	for _, n := range g.Nodes {
		if n.Kind == KindAPI {
			apis = append(apis, n)
		}
	}
	require.Len(t, apis, 3)
	assert.Equal(t, RoleWrites, apis[0].Role)
	assert.Equal(t, "listThings", apis[1].Subtitle)
	assert.Equal(t, "API crm to file", apis[2].Title)
	assert.Equal(t, []string{"resources", "big.json"}, apis[2].Detail)
}

func TestDerive_ALocalNeverReadsAsTheConstantItShadows(t *testing.T) {
	g := deriveGraph(`
CONN = "warehouse"
def pull(CONN):
    platform.query("SELECT 1", connection=CONN)
pull(run.params["c"])
`)
	require.True(t, g.OK)
	q := g.Nodes[0]
	assert.True(t, q.Computed)
	assert.Equal(t, `Query {run.params["c"]}`, q.Title)
}

func TestDerive_BranchesAgreeOrTheValueIsComputed(t *testing.T) {
	g := deriveGraph(`
def main():
    if run.params.get("prod"):
        conn = "prod"
    else:
        conn = "dev"
    platform.query("SELECT 1", connection=conn)
    same = "x"
    if run.params.get("y"):
        same = "x"
    platform.query("SELECT 2", connection=same)
main()
`)
	require.True(t, g.OK)
	assert.True(t, g.Nodes[0].Computed)
	assert.Equal(t, "Query x", g.Nodes[1].Title)
	assert.False(t, g.Nodes[1].Computed)
}

// A module-level name bound more than once is not a constant, whatever values
// it is bound to: which binding a later line sees is a run-time fact.
func TestDerive_AReboundModuleNameIsComputed(t *testing.T) {
	g := deriveGraph(`
conn = "a"
if run.params.get("b"):
    conn = "b"
platform.query("SELECT 1", connection=conn)
`)
	require.True(t, g.OK)
	assert.True(t, g.Nodes[0].Computed)
}

func TestDerive_ValuesPassingThroughPureFunctionsNameThem(t *testing.T) {
	g := deriveGraph(`
def clean(rows):
    return [r for r in rows if r]
rows = platform.query("SELECT * FROM hive.s.t")
platform.export("t", clean(rows["rows"]))
`)
	require.True(t, g.OK)
	require.Len(t, g.Edges, 1)
	assert.Equal(t, []string{"clean()"}, g.Edges[0].Via)
}

func TestDerive_ANestedDefSeesItsEnclosingFunction(t *testing.T) {
	g := deriveGraph(`
def outer():
    table = "hive.s.t"
    def inner():
        return platform.query("SELECT * FROM " + table)
    platform.export("t", inner()["rows"])
outer()
`)
	require.True(t, g.OK, "%+v", g.Findings)
	q := nodeByTitle(t, g, "Query")
	assert.Equal(t, []string{"hive.s.t"}, q.Detail)
}

func TestDerive_ADiagramStopsAtItsStepBound(t *testing.T) {
	src := "def one(i):\n    platform.query(\"SELECT 1\")\n" + strings.Repeat("one(1)\n", maxSteps+5)
	g := deriveGraph(src)
	require.True(t, g.OK)
	assert.True(t, g.Truncated)
	assert.Len(t, g.Nodes, maxSteps)
}

func TestDerive_IsCachedBySource(t *testing.T) {
	src := `platform.query("SELECT 42")`
	a, b := Derive(src), Derive(src)
	assert.Equal(t, a, b)
}

func TestGraphCache_EvictsTheLeastRecentlyUsed(t *testing.T) {
	c := newGraphCache(2)
	k := func(b byte) [32]byte { return [32]byte{b} }
	c.put(k(1), Graph{Lines: 1})
	c.put(k(2), Graph{Lines: 2})
	_, _ = c.get(k(1))
	c.put(k(3), Graph{Lines: 3})
	c.put(k(3), Graph{Lines: 3})
	_, ok := c.get(k(2))
	assert.False(t, ok, "2 was least recently used")
	g, ok := c.get(k(1))
	assert.True(t, ok)
	assert.Equal(t, 1, g.Lines)
}

// The value paths a real script takes between steps: mutation through a
// method, an element store, tuple unpacking, an augmented assignment, a
// default argument, keyword arguments, a slice, a lambda, and a dict held in a
// variable.
const plumbing = `
REG = {"connection": "trino", "follow": False}

def collect(sql, conn="warehouse", *, tag="x"):
    # ------------------------------------------------------------
    # Collect one query's rows under a tag.
    # ------------------------------------------------------------
    out = []
    rows = platform.query(sql, connection=conn)
    out.append(rows)
    return out

def stage(rows, name):
    # Write the staged rows out. Everything after the first sentence is dropped.
    reg = {"connection": "trino"}
    platform.export(name, rows, format="jsonl", destination="resources", key=name + ".jsonl", register=reg, append=True)

def main():
    a = collect("SELECT * FROM hive.s.a JOIN hive.s.b ON 1=1 JOIN hive.s.c ON 1=1 JOIN hive.s.d ON 1=1 JOIN hive.s.e ON 1=1")
    b = collect(sql="SELECT * FROM hive.s.f", conn="lake", tag="y")
    both = {}
    both["a"] = a
    x, y = b, -len(a)
    total = 0
    total += len(b[:2])
    pick = lambda r: r
    stage(pick(both), "both")
    platform.export("reg", x, format="csv", destination="resources", key="r.csv", register=REG)
    platform.export("dyn", x, format="csv", destination="resources", key="d.csv", register=run.params["reg"])
    platform.call("trino_query", {"sql": run.params["q"]})

main()
`

func TestDerive_ValuesFollowEveryWayAScriptMovesThem(t *testing.T) {
	g := deriveGraph(plumbing)
	require.True(t, g.OK, "%+v", g.Findings)

	q := nodeByTitle(t, g, "Query warehouse")
	assert.Equal(t, []string{"hive.s.a", "hive.s.b", "hive.s.c", "hive.s.d", "+ 1 more"}, q.Detail)
	lake := nodeByTitle(t, g, "Query lake")

	both := nodeByTitle(t, g, "Export JSONL to resources")
	assert.Equal(t, []string{"both.jsonl", "appended across batches"}, both.Detail)
	assert.True(t, hasEdge(g, q.ID, both.ID), "rows reach the export through append, an element store and a lambda")

	var tables []Node
	for _, n := range g.Nodes {
		if n.Kind == KindTable {
			tables = append(tables, n)
		}
	}
	require.Len(t, tables, 3)
	assert.Equal(t, "named after the file", tables[0].Subtitle)
	assert.Equal(t, []string{"follows each new version of the file"}, tables[0].Detail)
	assert.Empty(t, tables[1].Detail, "follow=False")
	assert.True(t, tables[2].Computed)
	assert.Equal(t, `Table on {run.params["reg"]}`, tables[2].Title)

	assert.True(t, hasEdge(g, lake.ID, "op:4"), "b reaches the reg export through tuple unpacking")
	assert.Equal(t, "stage", tables[0].Wrapper, "the table is drawn where its export is")
	assert.Equal(t, both.Site, tables[0].Site)

	tq := nodeByTitle(t, g, "Query default connection")
	assert.True(t, tq.Computed)
	assert.Equal(t, []string{"SQL assembled at run time"}, tq.Detail)
	assert.Empty(t, g.Groups, "collect and stage are one-call wrappers")
}

func TestDerive_ABoxIsCaptionedByItsAuthor(t *testing.T) {
	g := deriveGraph(`
def collect(sql, conn="warehouse", *, tag="x"):
    # ------------------------------------------------------------
    # Collect one query's rows under a tag.
    # ------------------------------------------------------------
    platform.query(sql, connection=conn)
    platform.query(sql, connection=conn)

# Write the staged rows out. Everything after the first sentence is dropped.
def stage(name):
    platform.export(name, [])
    platform.export(name + "-copy", [])

collect("SELECT 1")
stage("a")
`)
	require.True(t, g.OK)
	require.Len(t, g.Groups, 2)
	assert.Equal(t, `collect(sql, conn="warehouse", *, tag="x")`, g.Groups[0].Label)
	assert.Equal(t, "Collect one query's rows under a tag.", g.Groups[0].Caption)
	assert.Equal(t, "Write the staged rows out.", g.Groups[1].Caption)
	assert.Equal(t, "Query warehouse", g.Nodes[0].Title, "a default argument is rendered")
}

func TestDerive_ACallThatOnlyReachesStepsThroughAnotherFunctionIsABox(t *testing.T) {
	g := deriveGraph(`
def read():
    return platform.query("SELECT 1")
def twice():
    read()
    read()
def outer():
    twice()
    platform.export("x", [])
outer()
outer()
`)
	require.True(t, g.OK)
	ids := make([]string, 0, len(g.Groups))
	for _, gr := range g.Groups {
		ids = append(ids, gr.ID)
	}
	assert.Equal(t, []string{"/outer", "/outer/twice"}, ids)
	assert.Equal(t, "/outer", g.Groups[1].Parent)
}

func TestFirstSentence_ClipsALongComment(t *testing.T) {
	g := deriveGraph("# " + strings.Repeat("word ", 40) + "\ndef f():\n    platform.query(\"SELECT 1\")\n    platform.query(\"SELECT 2\")\nf()\nf()\n")
	require.True(t, g.OK)
	require.Len(t, g.Groups, 1)
	assert.LessOrEqual(t, len([]rune(g.Groups[0].Caption)), maxCaption)
	assert.True(t, strings.HasSuffix(g.Groups[0].Caption, "…"))
}

// The assignment shapes Starlark allows, each carrying a step's value on.
func TestDerive_EveryAssignmentShapeCarriesTheValue(t *testing.T) {
	g := deriveGraph(`
acc = {}
def gather():
    rows = platform.query("SELECT * FROM hive.s.t")
    (a, [b, c]) = rows, [rows, rows]
    for k, (v, w) in [(1, (a, b))]:
        acc[k] = v
    acc.update(c)
    holder = struct_like()
    holder.x = w
    make().append(a)
    extra = []
    extra.extend(fresh)
    return acc
def struct_like():
    return {}
def make():
    return []
fresh = []
out = gather()
tag = run.id
x = out["rows"][1:2]
first = run.params
platform.export("t", [out, x, tag, first], key=x[0])
platform.call("api_invoke_endpoint", {"method": "GET", "path": "/v1/x"})
(lambda r: r)(1)
`)
	require.True(t, g.OK, "%+v", g.Findings)
	exp := nodeByTitle(t, g, "Export CSV to portal")
	assert.True(t, hasEdge(g, "op:1", exp.ID), "the rows reach the export through tuple, list, loop and element stores")
	assert.Equal(t, []string{`{x[0]}`}, exp.Detail)
	api := nodeByTitle(t, g, "API")
	assert.Equal(t, "API", api.Title, "a call naming no connection names none")
}

func TestDerive_AModuleTupleBindingIsNotAConstant(t *testing.T) {
	g := deriveGraph(`
A, B = "a", "b"
[C] = ["c"]
(D) = "d"
platform.query("SELECT 1", connection=A)
platform.query("SELECT 1", connection=D)
`)
	require.True(t, g.OK)
	assert.True(t, g.Nodes[0].Computed, "a tuple binding is not a constant")
	assert.True(t, g.Nodes[1].Computed, "only a plain name = value binding is a constant")
}

// A loop body that assigns a name, a nested loop and a nested def included,
// leaves the name's text unknown after the loop; a branch that binds a name
// the other does not adds its origins.
func TestDerive_ALoopForgetsWhatItsBodyReassigns(t *testing.T) {
	g := deriveGraph(`
def main():
    conn = "a"
    rows = []
    for d in run.params["days"]:
        for h in [1, 2]:
            conn = "b-" + str(h)
        def inner():
            conn2 = "c"
            return conn2
        if d:
            rows = platform.query("SELECT 1")
            only_here = rows
            rows.meta[0] = only_here
        (p) = rows
        (hdr, body) = p, p
    platform.query("SELECT 2", connection=conn)
    platform.export("x", body)
    (rows)[0] = 1
    (rows).append(1)
main()
`)
	require.True(t, g.OK, "%+v", g.Findings)
	assert.True(t, g.Nodes[1].Computed)
	assert.True(t, hasEdge(g, "op:1", "op:3"))
}

// A recursive call is refused by the interpreter when it is made, so it is not
// expanded: the function's steps are drawn once, and the walk ends.
func TestDerive_ARecursiveCallIsNotExpanded(t *testing.T) {
	g := deriveGraph(`
def walk(n):
    platform.query("SELECT 1")
    if n > 0:
        walk(n - 1)
        walk(n - 2)
walk(3)
`)
	require.True(t, g.OK, "%+v", g.Findings)
	assert.False(t, g.Truncated)
	assert.Len(t, g.Nodes, 1)
}

// A parameter read by a conditional expression decides which value flows on,
// and the values of both branches reach the step.
func TestDerive_AConditionalExpressionDecidesAndCarriesBothBranches(t *testing.T) {
	g := deriveGraph(`
a = platform.query("SELECT 1")
b = platform.query("SELECT 2")
platform.export("x", a["rows"] if run.params.get("first") else b["rows"])
`)
	require.True(t, g.OK)
	assert.True(t, hasEdge(g, "op:1", "op:3"))
	assert.True(t, hasEdge(g, "op:2", "op:3"))
	require.Len(t, g.Params, 1)
	assert.True(t, g.Params[0].Decides)
	assert.Empty(t, g.Params[0].Reaches)
}

func TestElements_IsTheItemsOfATupleOrListOnly(t *testing.T) {
	x := &syntax.Ident{Name: "x"}
	assert.Equal(t, []syntax.Expr{x}, elements(&syntax.TupleExpr{List: []syntax.Expr{x}}))
	assert.Equal(t, []syntax.Expr{x}, elements(&syntax.ListExpr{List: []syntax.Expr{x}}))
	assert.Nil(t, elements(x))
}
