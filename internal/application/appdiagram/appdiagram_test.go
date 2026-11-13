package appdiagram

import (
	"context"
	"errors"
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
	schedules map[string]*script.Schedule
	bindings  map[string]*Binding
	assets    map[string][2]string
	hidden    map[string]bool
	content   map[string]string
	filled    []string
	appdb     map[string]bool
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

func (f *fake) Schedule(_ context.Context, id string) (*script.Schedule, error) {
	return f.schedules[id], nil
}

func (f *fake) Binding(_ context.Context, id string) (*Binding, error) { return f.bindings[id], nil }

func (f *fake) Asset(_ context.Context, id string) (name, identity string, ok bool) {
	a, found := f.assets[id]
	return a[0], a[1], found && !f.hidden[id]
}

func (f *fake) AssetContent(_ context.Context, id string, _ int) string { return f.content[id] }

func (f *fake) SchemaWrites(context.Context, string, string) []string { return f.filled }

func (f *fake) IsAppDB(c string) bool { return f.appdb[c] }

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
			intake:  {ID: intake, Name: "intake", EventSource: "form-signup"},
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
		schedules: map[string]*script.Schedule{refresh: {CronSpec: "0 6 * * *", Timezone: "UTC", Enabled: true}},
		bindings:  map[string]*Binding{api: {Name: "shop-api", Path: "/"}, form: {Name: "signup", Path: "/"}},
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
	assert.Contains(t, node(d, "script:"+api).Detail, "endpoint /endpoints/shop-api/")
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
		assert.Contains(t, fd.Message, "intake writes a table named only when it runs")
	}

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
	f.sources[intake][2] = `UPSERT_COMPANIES = "INSERT INTO shop.company (code) SELECT code FROM x ON CONFLICT (code) DO UPDATE SET code = excluded.code"
UPSERT_SITES = "INSERT INTO shop.site (code) SELECT code FROM x ON CONFLICT (code) DO UPDATE SET code = excluded.code"
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
	for _, fd := range found {
		assert.Contains(t, fd.Message, "intake writes a table named only when it runs", "a candidate built when it runs")
	}
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
