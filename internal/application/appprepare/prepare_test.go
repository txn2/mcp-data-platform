package appprepare

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/application/appschema"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptbehavior"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptlib"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrec"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsave"
	"github.com/txn2/mcp-data-platform/internal/schemaref"
	"github.com/txn2/mcp-data-platform/pkg/script"
	postgreskit "github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

const ordersScript = `load("lib:shop-sql@2", "INSERT_EVENT")

ORDERS = "SELECT id FROM orders.orders WHERE status = :s"

def main():
    """Lists the open orders."""
    orders = []
    orders.append(1)
    rows = platform.query("db", "SELECT * FROM \"orders\".lines JOIN orders.orders o ON true", {})
    return rows
`

// TestScriptText rewrites only strings: a Starlark name that is the schema's
// is code and stays, and a library load is read (#2709).
func TestScriptText(t *testing.T) {
	after, names, loads := schemaref.Script(ordersScript, []string{"orders"})
	assert.Equal(t, 3, names)
	assert.Contains(t, after, `"SELECT id FROM orders WHERE status = :s"`)
	assert.Contains(t, after, `"SELECT * FROM lines JOIN orders o ON true"`)
	assert.Contains(t, after, "orders.append(1)", "a Starlark name is not SQL")
	require.Len(t, loads, 1)
	assert.Equal(t, "shop-sql", loads[0].Name)

	assert.Contains(t, schemaref.WithLoads(after, map[string]int{"shop-sql": 5}, loads), `load("lib:shop-sql@5"`)
	unchanged, n, _ := schemaref.Script("def main(:\n", []string{"orders"})
	assert.Equal(t, "def main(:\n", unchanged, "a source that does not parse names nothing")
	assert.Zero(t, n)
	_, n, _ = schemaref.Script(`X = "SELECT * FROM warehouse.orders.orders"`+"\n", []string{"orders"})
	assert.Zero(t, n, "a three-part name is another catalog's")
}

// TestMigrationText leaves out every statement naming a role, removes the
// schema qualifier from the rest, and keeps a migration that would hold
// nothing applyable (#2709).
func TestMigrationText(t *testing.T) {
	up := "CREATE ROLE orders_write NOLOGIN;\nCREATE TABLE orders.invoice (id int);\nGRANT INSERT ON orders.invoice TO orders_write"
	got, n := migrationText(up, "orders")
	assert.Equal(t, 3, n)
	assert.Equal(t, "CREATE TABLE invoice (id int);\n", got)
	got, n = migrationText("CREATE TABLE invoice (id int)", "orders")
	assert.Zero(t, n)
	assert.Equal(t, "CREATE TABLE invoice (id int)", got, "a migration naming nothing is left as written")
	got, _ = migrationText("GRANT USAGE ON SCHEMA orders TO orders_read", "orders")
	assert.Equal(t, keptNothing, got)
	got, n = migrationText("", "orders")
	assert.Empty(t, got)
	assert.Zero(t, n)
}

type fakeScripts struct {
	byID  map[string]*script.Script
	saved []string
	fail  error
}

func (f *fakeScripts) GetByID(_ context.Context, id string) (*script.Script, error) {
	sc, ok := f.byID[id]
	if !ok {
		return nil, errors.New("no script " + id)
	}
	cp := *sc
	return &cp, nil
}

func (f *fakeScripts) UpdateWithVersion(_ context.Context, sc *script.Script, a script.Author) error {
	if f.fail != nil {
		return f.fail
	}
	sc.Version++
	f.byID[sc.ID] = sc
	f.saved = append(f.saved, fmt.Sprintf("%s@%d by %s", sc.Name, sc.Version, a.Email))
	return nil
}

type fakeLibraries struct{ scripts *fakeScripts }

func (fakeLibraries) LibrarySource(_ context.Context, name string, version int) (string, error) {
	if name == "shop-sql" && version == 2 {
		return "INSERT_EVENT = \"INSERT INTO orders.events (k) VALUES (:k)\"\n", nil
	}
	return "", errors.New("no such library")
}

func (f fakeLibraries) LibraryNamed(_ context.Context, name string) (*script.Script, error) {
	return f.scripts.GetByID(context.Background(), "lib-"+name)
}

type fakeMigrations struct {
	ms  []postgreskit.Migration
	put []postgreskit.Migration
}

func (f *fakeMigrations) List(context.Context, string, string) ([]postgreskit.Migration, error) {
	return f.ms, nil
}

func (f *fakeMigrations) Put(_ context.Context, m postgreskit.Migration) (bool, error) {
	f.put = append(f.put, m)
	return true, nil
}

type fakePending struct{ pending []int }

func (f fakePending) StateIn(context.Context, appschema.Target) (*appschema.State, error) {
	return &appschema.State{Pending: f.pending}, nil
}

// fakeGate passes every source, finding a difference when differ names it.
type fakeGate struct {
	differ  string
	checked []string
}

func (g *fakeGate) Check(_ context.Context, req scriptsave.Request) scriptsave.Result {
	g.checked = append(g.checked, req.Name)
	res := scriptsave.Result{Replayed: []string{"run_1"}, Differences: []scriptbehavior.Difference{}}
	res.Lint.Source = req.Source
	if req.Name == g.differ {
		res.Differences = append(res.Differences, scriptbehavior.Difference{})
		res.Refusal = "a behavior change needs a summary and the person's agreement"
	}
	return res
}

// prepFixture is a preparer and the fakes it reads and writes through.
type prepFixture struct {
	p          *Preparer
	scripts    *fakeScripts
	migrations *fakeMigrations
	gate       *fakeGate
}

func preparerFixture() prepFixture {
	scripts := &fakeScripts{byID: map[string]*script.Script{
		"s1":           {ID: "s1", Name: "orders-api", Version: 4, Source: ordersScript},
		"s2":           {ID: "s2", Name: "clean", Version: 1, Source: "def main():\n    \"\"\"Clean.\"\"\"\n    return 1\n"},
		"lib-shop-sql": {ID: "lib-shop-sql", Name: "shop-sql", Version: 3, Library: true},
	}}
	migrations := &fakeMigrations{ms: []postgreskit.Migration{
		{Version: 1, Name: "applied", Up: "CREATE TABLE orders.orders (id int)"},
		{Version: 2, Name: "pending", Up: "CREATE TABLE orders.events (k text);\nGRANT INSERT ON orders.events TO orders_write"},
		{Version: 3, Name: "clean", Up: "CREATE INDEX ON events (k)"},
	}}
	gate := &fakeGate{}
	return prepFixture{p: &Preparer{
		Scripts: scripts, Libraries: fakeLibraries{scripts: scripts}, Migrations: migrations,
		Schema: fakePending{pending: []int{2, 3}}, Gate: gate,
	}, scripts: scripts, migrations: migrations, gate: gate}
}

var shop = &application.Application{Name: "shop", Connection: "db", Schema: "orders", Parts: []application.Part{
	{Kind: application.KindScript, ID: "s1"}, {Kind: application.KindScript, ID: "s2"}, {Kind: application.KindAsset, ID: "a1"},
}}

func TestNamedAndPlan(t *testing.T) {
	ctx := context.Background()
	fx := preparerFixture()
	p := fx.p
	gate := fx.gate
	counts, err := p.Named(ctx, shop)
	require.NoError(t, err)
	assert.Equal(t, Counts{Scripts: 1, Libraries: 1, Migrations: 1}, counts, "an applied migration stays as its ledger records it")
	assert.Equal(t, 3, counts.Total())
	assert.Empty(t, gate.checked, "counting replays nothing")

	plan, err := p.Plan(ctx, shop, scriptsave.Caller{Email: "admin@example.com", Admin: true})
	require.NoError(t, err)
	require.Len(t, plan.Files, 3)
	assert.False(t, plan.Ready)
	assert.Equal(t, []string{"orders-api", "shop-sql"}, gate.checked)
	assert.Equal(t, []string{"run_1"}, plan.Files[0].Replayed)
	assert.Equal(t, KindMigration, plan.Files[2].Kind)
	assert.Equal(t, "CREATE TABLE events (k text);\n", plan.Files[2].After)

	none, err := p.Plan(ctx, &application.Application{Name: "plain"}, scriptsave.Caller{})
	require.NoError(t, err)
	assert.True(t, none.Ready, "an application holding no schema is ready")
}

// Save saves the library first, then the scripts loading its new version,
// then the migrations; a gate refusing any script saves nothing (#2709).
func TestSave(t *testing.T) {
	ctx := context.Background()
	fx := preparerFixture()
	p := fx.p
	scripts := fx.scripts
	migrations := fx.migrations
	gate := fx.gate
	gate.differ = "orders-api"
	plan, err := p.Save(ctx, shop, scriptsave.Caller{Email: "admin@example.com", Admin: true}, script.Author{Email: "admin@example.com"})
	require.ErrorIs(t, err, ErrNotReady)
	assert.False(t, plan.Saved)
	assert.Empty(t, scripts.saved, "a difference blocks every save")

	gate.differ = ""
	plan, err = p.Save(ctx, shop, scriptsave.Caller{Email: "admin@example.com", Admin: true}, script.Author{Email: "admin@example.com"})
	require.NoError(t, err)
	assert.True(t, plan.Saved)
	assert.Equal(t, []string{"shop-sql@4 by admin@example.com", "orders-api@5 by admin@example.com"}, scripts.saved)
	assert.Contains(t, scripts.byID["s1"].Source, `load("lib:shop-sql@4"`, "the script loads the library's prepared version")
	require.Len(t, migrations.put, 1)
	assert.Equal(t, 2, migrations.put[0].Version)
	assert.Equal(t, postgreskit.ChecksumOf(migrations.put[0].Up, migrations.put[0].Down, false), migrations.put[0].Checksum)

	failing := preparerFixture()
	failing.scripts.fail = errors.New("down")
	_, err = failing.p.Save(ctx, shop, scriptsave.Caller{}, script.Author{})
	require.ErrorContains(t, err, "down")
}

// recs is an in-memory scriptrec.Store.
type recs struct{ by map[string]scriptrec.Stored }

func (r *recs) Save(_ context.Context, s scriptrec.Stored) error { r.by[s.RunID] = s; return nil }
func (r *recs) Get(_ context.Context, id string) (*scriptrec.Stored, error) {
	s, ok := r.by[id]
	if !ok {
		return nil, scriptrec.ErrNotFound
	}
	return &s, nil
}

func (r *recs) Recent(_ context.Context, scriptID string, _ int) ([]scriptrec.Stored, error) {
	out := []scriptrec.Stored{}
	for _, s := range r.by {
		if s.ScriptID == scriptID {
			out = append(out, s)
		}
	}
	return out, nil
}
func (*recs) Keep(context.Context, string, string, []string) error { return nil }
func (*recs) Purge(context.Context, time.Duration) (int64, error)  { return 0, nil }

// A script's recorded runs are stored again with its statements rewritten
// once prepare saves it, so the next save replays them (#2709).
func TestSave_RewritesTheRecordingsItReplays(t *testing.T) {
	ctx := context.Background()
	fx := preparerFixture()
	p := fx.p
	store := &recs{by: map[string]scriptrec.Stored{}}
	rec := scriptrec.NewRecorder(scriptrec.Header{RunID: "run_9"})
	rec.OnCall("appdb_query", map[string]any{"sql": "SELECT id FROM orders.orders"}, map[string]any{"rows": []any{}}, nil)
	require.True(t, rec.SaveTo(ctx, store, scriptrec.Meta{RunID: "run_9", ScriptID: "s1", Kind: scriptrec.KindRun, Succeeded: true}))
	p.Recordings = store

	_, err := p.Save(ctx, shop, scriptsave.Caller{Email: "admin@example.com", Admin: true}, script.Author{Email: "admin@example.com"})
	require.NoError(t, err)
	got, err := scriptrec.Decode(store.by["run_9"].Data)
	require.NoError(t, err)
	require.Len(t, got.Calls, 1)
	assert.Equal(t, "SELECT id FROM orders", got.Calls[0].Args["sql"])
	assert.Equal(t, scriptrec.ToolKey("appdb_query", got.Calls[0].Args), got.Calls[0].Key, "the call's key is rebuilt from what it now says")
}

// Libraries are read in name and version order, the schemas an application
// runs in are its own and each environment's, and a failed read says so.
func TestPrepareHelpers(t *testing.T) {
	assert.Negative(t, compareRefs(scriptlib.Ref{Name: "a", Version: 2}, scriptlib.Ref{Name: "b", Version: 1}))
	assert.Positive(t, compareRefs(scriptlib.Ref{Name: "b", Version: 1}, scriptlib.Ref{Name: "a", Version: 2}))
	assert.Negative(t, compareRefs(scriptlib.Ref{Name: "a", Version: 1}, scriptlib.Ref{Name: "a", Version: 2}))
	app := &application.Application{Schema: "orders", Environments: []application.Environment{{Name: "dev", Schema: "orders_dev"}, {Name: "prod"}, {Name: "qa", Schema: "orders_dev"}}}
	assert.Equal(t, []string{"orders", "orders_dev"}, schemas(app))

	fx := preparerFixture()
	_, err := fx.p.Named(context.Background(), &application.Application{Name: "x", Schema: "orders", Parts: []application.Part{{Kind: application.KindScript, ID: "missing"}}})
	require.ErrorContains(t, err, "reading script missing")

	broken := &recs{by: map[string]scriptrec.Stored{"run_x": {RunID: "run_x", ScriptID: "s1", Data: []byte("not a recording")}}}
	fx.p.Recordings = broken
	require.NoError(t, fx.p.rewriteRecordings(context.Background(), &File{ID: "s1", Name: "orders-api", After: "x"}, []string{"orders"}),
		"a recording that does not read is left as it is")
}

// TestPlan_ListsQuotedValuesAndIsReadyWithThem lists a qualified name inside
// a quoted SQL value for a person to change and leaves it as written; such a
// value alone does not keep an application from being ready (#2709).
func TestPlan_ListsQuotedValuesAndIsReadyWithThem(t *testing.T) {
	ctx := context.Background()
	src := "def main():\n    \"\"\"Files an event.\"\"\"\n    return platform.query(\"db\", \"SELECT id FROM outbox WHERE source_table = 'orders.invoices'\", {})\n"
	scripts := &fakeScripts{byID: map[string]*script.Script{"s3": {ID: "s3", Name: "events", Version: 2, Source: src}}}
	p := &Preparer{Scripts: scripts, Libraries: fakeLibraries{scripts: scripts}, Gate: &fakeGate{}}
	app := &application.Application{Name: "shop", Connection: "db", Schema: "orders", Parts: []application.Part{{Kind: application.KindScript, ID: "s3"}}}
	plan, err := p.Plan(ctx, app, scriptsave.Caller{Email: "admin@example.com", Admin: true})
	require.NoError(t, err)
	assert.Empty(t, plan.Files, "a quoted value is not rewritten")
	assert.True(t, plan.Ready)
	assert.Equal(t, []Value{{Kind: KindScript, Name: "events", Line: 3, Text: "orders.invoices"}}, plan.Values)
}
