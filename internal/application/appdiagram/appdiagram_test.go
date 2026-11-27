package appdiagram

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// fake is a Reader over maps.
type fake struct {
	scripts   map[string]*script.Script
	sources   map[string]map[int]string
	schedules map[string][]script.Schedule
	bindings  map[string][]Binding
	events    map[string][]string
	assets    map[string][2]string
	hidden    map[string]bool
	content   map[string]string
	filled    []string
	created   []string
	appdb     map[string]bool
	libraries map[string]string
}

func (f *fake) Script(_ context.Context, id string) (*script.Script, error) {
	if sc, ok := f.scripts[id]; ok {
		return sc, nil
	}
	return nil, errors.New("no script")
}

func (f *fake) ScriptVersion(_ context.Context, id string, v int) (*script.Version, error) {
	src, ok := f.sources[id][v]
	if !ok {
		return nil, errors.New("no version")
	}
	return &script.Version{ScriptID: id, Version: v, Source: src}, nil
}

func (f *fake) Schedules(_ context.Context, id string) ([]script.Schedule, error) {
	return f.schedules[id], nil
}

func (f *fake) Bindings(_ context.Context, id string) ([]Binding, error) { return f.bindings[id], nil }

func (f *fake) EventSources(_ context.Context, id string) []string { return f.events[id] }

func (f *fake) Asset(_ context.Context, id string) (name, identity string, ok bool) {
	a, found := f.assets[id]
	return a[0], a[1], found && !f.hidden[id]
}

func (f *fake) AssetContent(_ context.Context, id string, _ int) string { return f.content[id] }

func (f *fake) SchemaTables(context.Context, string, string) SchemaTables {
	return SchemaTables{Created: f.created, Filled: f.filled}
}

func (f *fake) IsAppDB(_ context.Context, c string) bool { return f.appdb[c] }

func (f *fake) LibrarySource(_ context.Context, name string, version int) (string, bool) {
	text, ok := f.libraries[fmt.Sprintf("%s@%d", name, version)]
	return text, ok
}

const (
	intake  = "11111111-0000-0000-0000-000000000001"
	refresh = "11111111-0000-0000-0000-000000000002"
	api     = "11111111-0000-0000-0000-000000000003"
	form    = "11111111-0000-0000-0000-000000000004"
	lib     = "11111111-0000-0000-0000-000000000005"
)

// shop is the application of #2540's criteria: a form embedded in an asset,
// the form's source, a script on that source writing a schema table, a
// scheduled script reading that table and refreshing an asset's data region,
// and an asset calling an endpoint script.
func shop() (*fake, Input) {
	f := &fake{
		scripts: map[string]*script.Script{
			intake:  {ID: intake, Name: "intake"},
			refresh: {ID: refresh, Name: "refresh"},
			api:     {ID: api, Name: "api"},
			form:    {ID: form, Name: "signup"},
			lib:     {ID: lib, Name: "helpers", Library: true},
		},
		sources: map[string]map[int]string{
			intake:  {2: "def main():\n    \"\"\"Files.\"\"\"\n    platform.execute(\"INSERT INTO shop.signups (email) VALUES (:e)\", connection = \"orders\", params = {\"e\": run.event})\n"},
			refresh: {1: "load(\"lib:helpers@3\", \"fmt\")\n\ndef main():\n    \"\"\"Refreshes.\"\"\"\n    rows = platform.query(\"SELECT count(*) FROM shop.signups\", connection = \"orders\")\n    platform.publish_data(\"dash\", rows)\n    platform.export(\"daily-\" + run.params[\"d\"], rows, \"csv\")\n    platform.notify(\"ops\", \"Refreshed\", \"done\")\n"},
			api:     {1: "def handle(req):\n    \"\"\"Answers.\"\"\"\n    return {\"json\": platform.query(\"SELECT * FROM shop.products\", connection = \"orders\")}\n"},
			form:    {1: "def form_spec(req):\n    \"\"\"Spec.\"\"\"\n    return {}\n\ndef form_submit(req, values):\n    \"\"\"Accepts.\"\"\"\n    return {}\n"},
			lib:     {3: "def fmt(x):\n    \"\"\"Formats.\"\"\"\n    return x\n"},
		},
		schedules: map[string][]script.Schedule{refresh: {
			{CronSpec: "0 6 * * *", Timezone: "UTC", Enabled: true},
			{CronSpec: "0 18 * * *", Timezone: "UTC", Enabled: true},
		}},
		bindings: map[string][]Binding{
			api:  {{Name: "shop-api", Path: "/"}, {Name: "shop-api-v2", Path: "/{id}"}},
			form: {{Name: "signup", Path: "/"}},
		},
		events: map[string][]string{intake: {"form-signup", "shop-orders"}},
		assets: map[string][2]string{
			"dash-asset": {"Dashboard", script.OutputIdentityKey(refresh, "dash")},
			"store":      {"Storefront", ""},
		},
		content: map[string]string{
			"store":      `<div data-form="mcp:form:signup"></div><script>fetch("/endpoints/shop-api/"); fetch("mcp:endpoint:shop-api")</script><a href="/endpoints/elsewhere/x">`,
			"dash-asset": `<script id="data"></script>`,
		},
		appdb: map[string]bool{"orders": true},
	}
	app := &application.Application{Name: "shop", Connection: "orders", Schema: "shop"}
	parts := []Part{
		part(application.KindScript, intake, 2),
		part(application.KindScript, refresh, 1),
		part(application.KindScript, api, 1),
		part(application.KindScript, form, 1),
		part(application.KindScript, lib, 3),
		part(application.KindAsset, "dash-asset", 4),
		part(application.KindAsset, "store", 1),
		part(application.KindSource, "form-signup", 0),
		part(application.KindSchema, "orders/shop", 0),
	}
	return f, Input{App: app, Parts: parts, Version: "current"}
}

// part is one part drawn at a version.
func part(kind, id string, version int) Part {
	p := Part{Version: version}
	p.Kind, p.ID = kind, id
	return p
}

func hasLink(d *Diagram, from, to, kind string) bool {
	return slices.ContainsFunc(d.Links, func(l Link) bool { return l.From == from && l.To == to && l.Kind == kind })
}

func node(d *Diagram, id string) *Node {
	for i := range d.Nodes {
		if d.Nodes[i].ID == id {
			return &d.Nodes[i]
		}
	}
	return nil
}

// TestBuild_EveryLinkOfTheShop is #2540's application: every link is drawn
// with its kind.
func TestBuild_EveryLinkOfTheShop(t *testing.T) {
	f, in := shop()
	d := Build(context.Background(), f, in)
	signups := "table:orders/shop.signups"
	for _, l := range []Link{
		{From: "asset:store", To: "script:" + form, Kind: LinkEmbeds},
		{From: "script:" + form, To: "source:form-signup", Kind: LinkSubmits},
		{From: "source:form-signup", To: "script:" + intake, Kind: LinkEvent},
		{From: "source:shop-orders", To: "script:" + intake, Kind: LinkEvent},
		{From: "script:" + intake, To: signups, Kind: LinkWrites},
		{From: "script:" + refresh, To: signups, Kind: LinkReads},
		{From: "script:" + refresh, To: "asset:dash-asset", Kind: LinkRefresh},
		{From: "asset:store", To: "script:" + api, Kind: LinkCalls},
		{From: "script:" + refresh, To: "script:" + lib, Kind: LinkLoads},
		{From: "script:" + refresh, To: "channel:ops", Kind: LinkNotifies},
		{From: "asset:store", To: "endpoint:elsewhere", Kind: LinkCalls},
	} {
		assert.True(t, hasLink(d, l.From, l.To, l.Kind), "%s -%s-> %s: %+v", l.From, l.Kind, l.To, d.Links)
	}
	require.NotNil(t, node(d, signups))
	assert.Equal(t, GroupSchema, node(d, signups).Group, "a table of the application's schema is drawn in it")
	assert.Contains(t, node(d, "script:"+refresh).Detail, "schedule 0 6 * * * UTC", "a schedule is on its script's card")
	assert.Contains(t, node(d, "script:"+refresh).Detail, "schedule 0 18 * * * UTC", "and every other schedule it holds (#2434)")
	assert.Contains(t, node(d, "script:"+api).Detail, "endpoint /endpoints/shop-api/")
	assert.Contains(t, node(d, "script:"+api).Detail, "endpoint /endpoints/shop-api-v2/{id}", "and every other endpoint (#2441)")
	require.NotNil(t, node(d, "source:shop-orders"), "a second source a script runs for is drawn")
	assert.Equal(t, GroupOutside, node(d, "source:shop-orders").Group, "outside the application, which holds no such part")
	assert.Equal(t, NodeLibrary, node(d, "script:"+lib).Kind)
	assert.Equal(t, GroupOutside, node(d, "channel:ops").Group)

	computed := 0
	for _, n := range d.Nodes {
		if n.Computed {
			computed++
			assert.Equal(t, NodeOutput, n.Kind, "the computed export name is drawn computed")
		}
	}
	assert.Equal(t, 1, computed)
	for _, l := range d.Links {
		if l.Kind == LinkExports {
			assert.NotEqual(t, "asset:dash-asset", l.To, "a computed export name is never matched to an asset")
		}
	}
	// products is read by the api and written by nothing: a finding.
	require.Len(t, d.Findings, 1, "%+v", d.Findings)
	assert.Equal(t, FindingUnwrittenTable, d.Findings[0].Kind)
	assert.Contains(t, d.Findings[0].Message, "shop.products")
	assert.False(t, d.Findings[0].Blocks)
	f.filled = []string{"products"}
	assert.Empty(t, Build(context.Background(), f, in).Findings, "a table a migration fills is written by the schema")
}

// TestBuild_EveryTableTheMigrationsCreate pins #2665: a table the schema's
// migrations create is drawn in the schema's group whether or not a script
// names it, once, and a table a script names keeps its node.
func TestBuild_EveryTableTheMigrationsCreate(t *testing.T) {
	f, in := shop()
	f.filled = []string{"products"}
	f.created = []string{"products", "audit_trail"}
	d := Build(context.Background(), f, in)
	unused := node(d, "table:orders/shop.audit_trail")
	require.NotNil(t, unused, "a table nothing uses is drawn")
	assert.Equal(t, GroupSchema, unused.Group)
	assert.Equal(t, "audit_trail", unused.Label)
	count := 0
	for _, n := range d.Nodes {
		if n.ID == "table:orders/shop.products" {
			count++
		}
	}
	assert.Equal(t, 1, count, "a created table a script also names is one node")
	assert.Empty(t, d.Findings)
}

// TestBuild_Findings: a library loaded at a version other than the one drawn
// blocks; a schema the application does not hold is named.
func TestBuild_Findings(t *testing.T) {
	f, in := shop()
	f.filled = []string{"products"}
	in.Parts[4].Version = 4 // the library at 4; refresh loads 3
	f.sources[lib][4] = f.sources[lib][3]
	f.sources[api][1] = "def handle(req):\n    \"\"\"Answers.\"\"\"\n    return platform.query(\"SELECT * FROM billing.invoices\", connection = \"orders\")\n"
	d := Build(context.Background(), f, in)
	kinds := map[string]Finding{}
	for _, fd := range d.Findings {
		kinds[fd.Kind] = fd
	}
	require.Contains(t, kinds, FindingLibraryVersion)
	assert.True(t, kinds[FindingLibraryVersion].Blocks)
	assert.Contains(t, kinds[FindingLibraryVersion].Message, `load("lib:helpers@4"`)
	require.Contains(t, kinds, FindingForeignSchema)
	assert.Contains(t, kinds[FindingForeignSchema].Message, "billing.invoices")
	assert.Equal(t, []string{kinds[FindingForeignSchema].Message, kinds[FindingLibraryVersion].Message},
		Problems(&Diagram{Findings: []Finding{kinds[FindingForeignSchema], kinds[FindingLibraryVersion]}}))
}

// runtimeWrites is the message of every runtime_writes finding of a diagram.
func runtimeWrites(d *Diagram) []string {
	var out []string
	for _, fd := range d.Findings {
		if fd.Kind == FindingRuntimeWrites {
			out = append(out, fd.Message)
		}
	}
	return out
}

// unwrittenFindings is every unwritten_table finding of a diagram.
func unwrittenFindings(d *Diagram) []Finding {
	var out []Finding
	for _, fd := range d.Findings {
		if fd.Kind == FindingUnwrittenTable {
			out = append(out, fd)
		}
	}
	return out
}

// TestBuild_WritesTheAnalyzerMissed is #2615: a table the application's own
// script writes through a data-modifying CTE in tx.batch inside a transaction
// has a writes link and no unwritten_table finding; a table only a statement
// chosen at run time could write is not reported as written by no part; and a
// table nothing writes in any form still is.
func TestBuild_WritesTheAnalyzerMissed(t *testing.T) {
	f, in := shop()
	f.filled = []string{"products"}
	f.sources[api][1] = "def handle(req):\n    \"\"\"Answers.\"\"\"\n    return platform.query(\"SELECT * FROM shop.invoice JOIN shop.document ON true\", connection = \"orders\")\n"
	f.sources[intake][2] = "def record(tx):\n    \"\"\"Records.\"\"\"\n    tx.batch([\"WITH a AS (INSERT INTO shop.document (b) VALUES (1) RETURNING id) INSERT INTO shop.invoice (d) SELECT id FROM a\"])\n\n" +
		"def main():\n    \"\"\"Files.\"\"\"\n    platform.transaction(\"orders\", record)\n    platform.execute(\"INSERT INTO shop.signups (email) VALUES (1)\", connection = \"orders\")\n"
	d := Build(context.Background(), f, in)
	assert.True(t, hasLink(d, "script:"+intake, "table:orders/shop.invoice", LinkWrites), "the CTE's outer target is written")
	assert.True(t, hasLink(d, "script:"+intake, "table:orders/shop.document", LinkWrites), "the CTE's own target is written")
	assert.Empty(t, unwrittenFindings(d), "every table read is written by a part")

	f.filled = nil
	f.sources[intake][2] = "def main():\n    \"\"\"Files.\"\"\"\n    platform.execute(run.params[\"sql\"], connection = \"orders\")\n"
	f.sources[api][1] = "def handle(req):\n    \"\"\"Answers.\"\"\"\n    return platform.query(\"SELECT * FROM shop.products\", connection = \"orders\")\n"
	d = Build(context.Background(), f, in)
	found := unwrittenFindings(d)
	require.NotEmpty(t, found)
	for _, fd := range found {
		assert.NotContains(t, fd.Message, "written by no part", "a run-time write may be the writer")
		assert.Contains(t, fd.Message, "a script that writes a table named only when it runs may")
		assert.NotContains(t, fd.Message, "intake", "the scripts are named once, not on every table")
	}
	assert.Equal(t, []string{"script intake writes a table named only when it runs, which may be a table of shop that no part is known to write"},
		runtimeWrites(d))

	f.sources[intake][2] = "def main():\n    \"\"\"Files.\"\"\"\n    platform.execute(run.params[\"sql\"], connection = \"elsewhere\")\n"
	d = Build(context.Background(), f, in)
	found = unwrittenFindings(d)
	require.NotEmpty(t, found, "a run-time write on another connection cannot be the schema's writer")
	assert.Contains(t, found[0].Message, "written by no part")
}

// TestBuild_LookupsAndComprehensionsAreWrites is #2623: a refresh script that
// picks each cache table's upsert out of a dict of constant lists, and a save
// that hands tx.batch a comprehension over a constant from the function its
// transaction's function is nested in, are the writers of those tables. No
// unwritten_table finding is reported for them, and each gets a writes link.
// A lookup with one candidate built when the script runs still is a write to
// a table named only when it runs.
func TestBuild_LookupsAndComprehensionsAreWrites(t *testing.T) {
	f, in := shop()
	f.sources[intake][2] = `UPSERT_COMPANIES = "INSERT INTO shop.company (code) SELECT code FROM (VALUES (1)) AS v(code) ON CONFLICT (code) DO UPDATE SET code = excluded.code"
UPSERT_SITES = "INSERT INTO shop.site (code) SELECT code FROM (VALUES (1)) AS v(code) ON CONFLICT (code) DO UPDATE SET code = excluded.code"
INSERT_DOCUMENT = "WITH added AS (INSERT INTO shop.document (b) SELECT $1 RETURNING id) INSERT INTO shop.invoice (d) SELECT id FROM added"

def save(documents):
    """Saves."""
    statements = [{"sql": INSERT_DOCUMENT, "params": d} for d in documents]

    def write(tx):
        if statements:
            tx.batch(statements)

    return platform.transaction("orders", write, on_error = "return")

def main():
    """Refreshes."""
    spec = {"companies": ["q", UPSERT_COMPANIES], "sites": ["q", UPSERT_SITES]}[run.params["kind"]]
    platform.execute(spec[1], connection = "orders", params = {})
    save(run.params["docs"])
    platform.execute("INSERT INTO shop.signups (email) VALUES (1)", connection = "orders")
`
	f.sources[api][1] = "def handle(req):\n    \"\"\"Answers.\"\"\"\n    return platform.query(\"SELECT * FROM shop.company JOIN shop.site ON true JOIN shop.invoice ON true JOIN shop.document ON true JOIN shop.products ON true\", connection = \"orders\")\n"
	f.filled = []string{"products"}
	d := Build(context.Background(), f, in)
	for _, table := range []string{"company", "site", "document", "invoice"} {
		assert.True(t, hasLink(d, "script:"+intake, "table:orders/shop."+table, LinkWrites), "shop.%s is written", table)
	}
	assert.Empty(t, unwrittenFindings(d), "every table read is written by a part")

	f.sources[intake][2] = "def main():\n    \"\"\"Refreshes.\"\"\"\n    spec = {\"a\": [\"INSERT INTO shop.company (code) VALUES (1)\"], \"b\": [\"INSERT INTO shop.t_\" + run.params[\"k\"] + \" VALUES (1)\"]}[run.params[\"k\"]]\n    platform.execute(spec[0], connection = \"orders\")\n"
	d = Build(context.Background(), f, in)
	found := unwrittenFindings(d)
	require.NotEmpty(t, found)
	assert.Len(t, runtimeWrites(d), 1, "a candidate built when it runs")
	assert.Contains(t, runtimeWrites(d)[0], "intake writes a table named only when it runs")
}

// TestBuild_AnAssetTheCallerCannotOpen is drawn unnamed and not read.
func TestBuild_AnAssetTheCallerCannotOpen(t *testing.T) {
	f, in := shop()
	f.hidden = map[string]bool{"store": true}
	d := Build(context.Background(), f, in)
	assert.Equal(t, "Asset you can't open", node(d, "asset:store").Label)
	assert.False(t, hasLink(d, "asset:store", "script:"+api, LinkCalls), "its content is not read")
}

// TestCompare marks nodes and links added, removed, changed and unchanged.
func TestCompare(t *testing.T) {
	f, in := shop()
	prevIn := in
	prevIn.Version = "1"
	prevIn.Parts = append([]Part{}, in.Parts...)
	prevIn.Parts[1].Version = 0 // refresh drawn at a version with nothing
	f.sources[refresh][0] = "def main():\n    \"\"\"Old.\"\"\"\n    platform.query(\"SELECT 1 FROM shop.old\", connection = \"orders\")\n"
	prev := Build(context.Background(), f, prevIn)
	next := Compare(Build(context.Background(), f, in), prev)
	assert.Equal(t, "1", next.ComparedWith)
	assert.Equal(t, ChangeChanged, node(next, "script:"+refresh).Change)
	assert.Equal(t, 0, node(next, "script:"+refresh).WasVersion)
	assert.Equal(t, ChangeAdded, node(next, "channel:ops").Change)
	assert.Equal(t, ChangeRemoved, node(next, "table:orders/shop.old").Change)
	assert.Equal(t, ChangeUnchanged, node(next, "script:"+api).Change)
	changes := map[string]int{}
	for _, l := range next.Links {
		changes[l.Change]++
	}
	assert.Positive(t, changes[ChangeAdded])
	assert.Positive(t, changes[ChangeRemoved])
	assert.Positive(t, changes[ChangeUnchanged])

	// A load moved to another version is changed, with what it said.
	f.sources[refresh][5] = strings.Replace(f.sources[refresh][1], "lib:helpers@3", "lib:helpers@4", 1)
	moved := in
	moved.Parts = append([]Part{}, in.Parts...)
	moved.Parts[1].Version = 5
	cmp := Compare(Build(context.Background(), f, moved), Build(context.Background(), f, in))
	for _, l := range cmp.Links {
		if l.Kind == LinkLoads {
			assert.Equal(t, ChangeChanged, l.Change)
			assert.Equal(t, "v3", l.WasDetail)
			assert.Equal(t, "v4", l.Detail)
		}
	}
}

// TestBuild_WithoutASchemaOrReadableParts draws what it can read.
func TestBuild_WithoutASchemaOrReadableParts(t *testing.T) {
	f := &fake{scripts: map[string]*script.Script{}, sources: map[string]map[int]string{}}
	in := Input{App: &application.Application{Name: "bare"}, Version: "latest", Parts: []Part{
		part(application.KindScript, "gone", 1),
	}}
	d := Build(context.Background(), f, in)
	require.Len(t, d.Nodes, 1)
	assert.Equal(t, "gone", d.Nodes[0].Label)
	assert.Empty(t, d.Links)
	assert.NotNil(t, d.Findings)
	assert.True(t, strings.HasPrefix(d.Nodes[0].ID, "script:"))
}

// TestBuild_AnEndpointReferenceIsACall is #2457: an asset that calls an
// endpoint by mcp:endpoint:<name> is drawn calling it, as one that fetches
// /endpoints/<name> is.
func TestBuild_AnEndpointReferenceIsACall(t *testing.T) {
	f, in := shop()
	f.content["store"] = `<script>const EP = "mcp:endpoint:shop-api";</script>`
	d := Build(context.Background(), f, in)
	assert.True(t, hasLink(d, "asset:store", "script:"+api, LinkCalls))
}

// TestBuild_RuntimeWritesAreOneCardAndOneFinding is #2699: a script with
// several steps whose tables are named only when it runs is drawn with one
// such card per connection, and several such scripts are named once, in a
// sentence that agrees with their number.
func TestBuild_RuntimeWritesAreOneCardAndOneFinding(t *testing.T) {
	f, in := shop()
	f.sources[intake][2] = "def main():\n    \"\"\"Files.\"\"\"\n" +
		"    platform.execute(run.params[\"a\"], connection = \"orders\")\n" +
		"    platform.batch(\"orders\", json.decode(run.params[\"b\"]))\n" +
		"    platform.execute(run.params[\"c\"], connection = \"elsewhere\")\n"
	f.sources[api][1] = "def handle(req):\n    \"\"\"Answers.\"\"\"\n    platform.execute(run.params[\"x\"], connection = \"orders\")\n" +
		"    return platform.query(\"SELECT * FROM shop.products\", connection = \"orders\")\n"
	d := Build(context.Background(), f, in)
	var cards []string
	for _, n := range d.Nodes {
		if strings.HasPrefix(n.ID, "computed:script:"+intake+":") {
			cards = append(cards, n.ID)
		}
	}
	assert.ElementsMatch(t, []string{"computed:script:" + intake + ":orders", "computed:script:" + intake + ":elsewhere"}, cards)
	got := runtimeWrites(d)
	require.Len(t, got, 1)
	assert.Contains(t, got[0], "scripts ")
	assert.Contains(t, got[0], " write tables named only when they run, which may be tables of shop that no part is known to write")
	assert.Equal(t, 1, strings.Count(strings.Join(Problems(d), "\n"), "intake"), "the script list is stated once: %q", Problems(d))
}

// TestBuild_StatementsALibraryDefinesAreWrites is #2699 on the shape a real
// application had: a script whose statements and helper come from a library
// it loads is drawn writing those tables, with no unwritten finding for them.
func TestBuild_StatementsALibraryDefinesAreWrites(t *testing.T) {
	f, in := shop()
	f.libraries = map[string]string{"shop-sql@3": "ADD_SIGNUP = \"INSERT INTO shop.signups (email) VALUES (:e)\"\n\n" +
		"def product_rows(names):\n    \"\"\"One product row per name.\"\"\"\n    return [{\"sql\": \"INSERT INTO shop.products (name) VALUES (:n)\", \"params\": {\"n\": n}} for n in names]\n"}
	f.sources[intake][2] = "load(\"lib:shop-sql@3\", \"ADD_SIGNUP\", \"product_rows\")\n\n" +
		"def main():\n    \"\"\"Files.\"\"\"\n    platform.execute(ADD_SIGNUP, connection = \"orders\", params = {\"e\": 1})\n" +
		"    platform.batch(\"orders\", product_rows([\"a\"]))\n"
	f.sources[api][1] = "def handle(req):\n    \"\"\"Answers.\"\"\"\n    return platform.query(\"SELECT * FROM shop.products JOIN shop.signups ON true\", connection = \"orders\")\n"
	d := Build(context.Background(), f, in)
	assert.True(t, hasLink(d, "script:"+intake, "table:orders/shop.signups", LinkWrites))
	assert.True(t, hasLink(d, "script:"+intake, "table:orders/shop.products", LinkWrites))
	for _, fd := range unwrittenFindings(d) {
		assert.NotContains(t, fd.Node, "shop.signups")
		assert.NotContains(t, fd.Node, "shop.products")
	}
	assert.Empty(t, runtimeWrites(d))
}

// A table written without a schema, as each environment's runs read it in
// their own schema, is the application's, and so is one qualified by an
// environment's schema; neither is a foreign schema (#2709).
func TestBuild_AnUnqualifiedTableIsTheApplications(t *testing.T) {
	f, in := shop()
	in.App.Environments = []application.Environment{{Name: "dev", Schema: "shop_dev"}, {Name: "prod", Schema: "shop"}}
	f.sources[api][1] = "def handle(req):\n    \"\"\"Answers.\"\"\"\n    platform.query(\"SELECT * FROM products\", connection = \"orders\")\n" +
		"    return platform.query(\"SELECT * FROM shop_dev.products\", connection = \"orders\")\n"
	d := Build(context.Background(), f, in)
	for _, fd := range d.Findings {
		assert.NotEqual(t, FindingForeignSchema, fd.Kind, fd.Message)
	}
	found := false
	for _, n := range d.Nodes {
		found = found || (n.Label == "products" && n.Group == GroupSchema)
	}
	assert.True(t, found, "products is drawn in the application's schema")
}

// TestBuild_AWithStatementsNamesAreNotTables is #2840: a script that writes an
// audit row in the same statement as the change it records, with a batch id
// held in a MATERIALIZED common table expression, writes both tables and reads
// no table named for an expression. No finding names the expression, and the
// audit table the script reads elsewhere is written by it, even when the
// statement opens with a comment.
func TestBuild_AWithStatementsNamesAreNotTables(t *testing.T) {
	f, in := shop()
	f.filled = []string{"products"}
	f.sources[intake][2] = `RATE_SQL = """
-- one batch id for the whole change
WITH b AS MATERIALIZED (SELECT gen_random_uuid() AS id),
old AS (SELECT r.code, jsonb_build_object('n', r.n) AS v FROM company r JOIN site s ON s.code = r.code),
up AS (
    INSERT INTO company (code, n) SELECT d.code, $1 FROM document d
    ON CONFLICT (code) DO UPDATE SET n = EXCLUDED.n
    RETURNING code, n
)
INSERT INTO invoice (batch_id, code, old_values)
SELECT b.id, up.code, old.v
  FROM up
 CROSS JOIN b
  LEFT JOIN old ON old.code = up.code
RETURNING code, batch_id::text AS batch_id
"""

def apply(tx):
    """Applies the rate change and records it."""
    return tx.query(RATE_SQL, params = [1])

def main():
    """Changes the rates."""
    platform.transaction("orders", apply)
    platform.execute("INSERT INTO shop.document (b) VALUES (1)", connection = "orders")
    platform.execute("INSERT INTO shop.site (code) VALUES (1)", connection = "orders")
    platform.execute("INSERT INTO shop.signups (email) VALUES (1)", connection = "orders")
`
	f.sources[api][1] = "def handle(req):\n    \"\"\"Answers.\"\"\"\n    return platform.query(\"SELECT * FROM invoice JOIN products ON true\", connection = \"orders\")\n"
	d := Build(context.Background(), f, in)
	for _, n := range d.Nodes {
		assert.NotContains(t, []string{"b", "old", "up"}, n.Label, "a WITH name is drawn as a table")
	}
	assert.True(t, hasLink(d, "script:"+intake, "table:orders/shop.invoice", LinkWrites), "the statement's own target is written")
	assert.True(t, hasLink(d, "script:"+intake, "table:orders/shop.company", LinkWrites), "the CTE's target is written")
	assert.Empty(t, unwrittenFindings(d), "every table read is written by a part")
}
