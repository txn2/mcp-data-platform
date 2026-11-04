package appdbdrift

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

func table(name string) postgres.Table {
	return postgres.Table{
		Name: name, Kind: "table",
		Columns:     []postgres.TableColumn{{Name: "id", Type: "bigint"}, {Name: "note", Type: "text", Nullable: true}},
		Constraints: []postgres.Constraint{{Name: name + "_pkey", Kind: "primary_key", Definition: "PRIMARY KEY (id)"}},
		Indexes:     []postgres.TableIndex{{Name: name + "_pkey", Definition: "CREATE UNIQUE INDEX ..."}},
		Policies:    []postgres.Policy{}, Triggers: []postgres.Trigger{}, Partitions: []postgres.Partition{},
	}
}

func desc(tables ...postgres.Table) *postgres.Description {
	return &postgres.Description{Tables: tables, Functions: []postgres.Function{}, Enums: []postgres.Enum{}, Domains: []postgres.Domain{}}
}

func TestCompare_SameSchemaHasNoDifference(t *testing.T) {
	assert.Empty(t, Compare(desc(table("orders")), desc(table("orders"))))
}

func TestCompare_EveryKindOfObject(t *testing.T) {
	live, exp := table("orders"), table("orders")
	live.Columns = append(live.Columns, postgres.TableColumn{Name: "coupon", Type: "text", Nullable: true})
	live.Columns[1].Default = "'x'"
	live.Columns[0].Nullable = true
	live.Indexes = append(live.Indexes, postgres.TableIndex{Name: "orders_note_idx", Definition: "CREATE INDEX ..."})
	exp.Constraints = append(exp.Constraints, postgres.Constraint{Name: "total_cap", Kind: "check", Definition: "CHECK (id > 0)"})
	live.Triggers = []postgres.Trigger{{Name: "touch", Definition: "CREATE TRIGGER touch ...", Enabled: "O"}}
	exp.Triggers = []postgres.Trigger{{Name: "touch", Definition: "CREATE TRIGGER touch ...", Enabled: "D"}}
	live.Policies = []postgres.Policy{{Name: "own", Command: "SELECT", Roles: []string{"public"}, Permissive: false, Using: "true"}}
	exp.Policies = []postgres.Policy{{Name: "own", Command: "SELECT", Roles: []string{"public"}, Permissive: true, Using: "true"}}
	live.RowSecurity, live.ForceRowSecurity = true, true
	exp.RowSecurity = true
	live.Partitions = []postgres.Partition{{Name: "p1", Bound: "FOR VALUES FROM (1) TO (2)"}}
	live.PartitionKey = "RANGE (id)"
	view, viewExp := table("open_orders"), table("open_orders")
	view.Kind, viewExp.Kind = "view", "view"
	view.View = &postgres.View{Definition: "SELECT 1", SecurityInvoker: true, SecurityBarrier: true, CheckOption: "local"}
	viewExp.View = &postgres.View{Definition: "SELECT 1"}

	l := desc(live, view, table("extra"))
	e := desc(exp, viewExp, table("gone"))
	l.Functions = []postgres.Function{{Name: "f", Arguments: "x int", Kind: "function", Body: "a", SecurityDefiner: true}}
	e.Functions = []postgres.Function{{Name: "f", Arguments: "x int", Kind: "function", Body: "b"}}
	l.Enums = []postgres.Enum{{Name: "status", Values: []string{"new", "paid"}}}
	e.Enums = []postgres.Enum{{Name: "status", Values: []string{"new"}}}
	l.Domains = []postgres.Domain{{Name: "money", BaseType: "numeric", Default: "0", Checks: []string{"CHECK (VALUE >= 0)"}}}
	e.Domains = []postgres.Domain{{Name: "money", BaseType: "numeric", Nullable: true, Checks: []string{}}}

	got := map[string]string{}
	for _, d := range Compare(l, e) {
		got[d.Kind+" "+d.Object] = d.Change
	}
	assert.Equal(t, map[string]string{
		"table extra":                  "added",
		"table gone":                   "missing",
		"column orders.coupon":         "added",
		"column orders.note":           "changed",
		"column orders.id":             "changed",
		"index orders.orders_note_idx": "added",
		"constraint orders.total_cap":  "missing",
		"trigger orders.touch":         "changed",
		"policy orders.own":            "changed",
		"row_security orders":          "changed",
		"partition orders (key)":       "changed",
		"partition orders.p1":          "added",
		"view open_orders":             "changed",
		"function f(x int)":            "changed",
		"enum status":                  "changed",
		"domain money":                 "changed",
	}, got)
}

// fakeKit answers a drift check and an adoption.
type fakeKit struct {
	status      *postgres.LedgerStatus
	statusErr   error
	built, live *postgres.Description
	builtErr    error
	liveErr     error
	builtFrom   []postgres.Migration
	tx          *fakeTx
	txErr       error
	applied     []postgres.ApplyOptions
	applyErr    error
}

func (k *fakeKit) Describe(context.Context, string, string) (*postgres.Description, error) {
	return k.live, k.liveErr
}

func (k *fakeKit) DescribeBuilt(_ context.Context, _, _ string, ms []postgres.Migration) (*postgres.Description, error) {
	k.builtFrom = ms
	return k.built, k.builtErr
}

func (k *fakeKit) Status(context.Context, string, string, []postgres.Migration) (*postgres.LedgerStatus, error) {
	return k.status, k.statusErr
}

func (k *fakeKit) ReadTx(context.Context, string) (Tx, error) {
	if k.txErr != nil {
		return nil, k.txErr
	}
	return k.tx, nil
}

func (k *fakeKit) Apply(_ context.Context, o postgres.ApplyOptions, ms []postgres.Migration) ([]postgres.Migration, error) {
	k.applied = append(k.applied, o)
	return ms, k.applyErr
}

// fakeTx answers the catalog reads: unsupported objects, then one DDL row
// per section.
type fakeTx struct {
	unsupported [][]any
	failOn      string
	truncate    bool
	rolledBack  bool
	sent        []string
}

func (x *fakeTx) Query(_ context.Context, s postgres.Statement) (*postgres.Result, error) {
	x.sent = append(x.sent, s.SQL)
	if x.failOn != "" && strings.Contains(s.SQL, x.failOn) {
		return nil, errors.New("appdb: SQLSTATE 42501")
	}
	if strings.Contains(s.SQL, "set_config('search_path'") {
		return &postgres.Result{}, nil
	}
	if strings.Contains(s.SQL, "SELECT kind, name FROM") {
		return &postgres.Result{Rows: x.unsupported}, nil
	}
	return &postgres.Result{Rows: [][]any{{"-- ddl"}}, Truncated: x.truncate}, nil
}

func (x *fakeTx) Rollback() { x.rolledBack = true }

// fakeStore keeps migrations and their history in memory.
type fakeStore struct {
	migrations []postgres.Migration
	events     []postgres.MigrationEvent
	listErr    error
	putErr     error
}

func (s *fakeStore) List(context.Context, string, string) ([]postgres.Migration, error) {
	return s.migrations, s.listErr
}

func (s *fakeStore) Put(_ context.Context, m postgres.Migration) (bool, error) {
	if s.putErr != nil {
		return false, s.putErr
	}
	s.migrations = append(s.migrations, m)
	return false, nil
}

func (s *fakeStore) Record(_ context.Context, e postgres.MigrationEvent) error {
	s.events = append(s.events, e)
	return nil
}

func TestCheck(t *testing.T) {
	m1 := postgres.Migration{Version: 1, Schema: "public"}
	m2 := postgres.Migration{Version: 2, Schema: "public"}
	ctx := context.Background()

	res := Check(ctx, &fakeKit{}, &fakeStore{listErr: errors.New("down")}, "shop", "public")
	assert.False(t, res.Checked)
	assert.Contains(t, res.Reason, "reading the migrations")

	res = Check(ctx, &fakeKit{}, &fakeStore{}, "shop", "public")
	assert.Contains(t, res.Reason, "adopt")

	store := &fakeStore{migrations: []postgres.Migration{m1, m2}}
	res = Check(ctx, &fakeKit{statusErr: errors.New("unreachable")}, store, "shop", "public")
	assert.Equal(t, "unreachable", res.Reason)

	res = Check(ctx, &fakeKit{status: &postgres.LedgerStatus{Drift: []postgres.Drift{{Version: 1, Reason: "checksum"}}}}, store, "shop", "public")
	assert.Contains(t, res.Reason, "ledger disagrees")

	res = Check(ctx, &fakeKit{status: &postgres.LedgerStatus{}}, store, "shop", "public")
	assert.Contains(t, res.Reason, "no migration is applied")

	applied := &postgres.LedgerStatus{Applied: []postgres.Applied{{Version: 1}}}
	res = Check(ctx, &fakeKit{status: applied, builtErr: errors.New("createdb_required")}, store, "shop", "public")
	assert.Contains(t, res.Reason, "createdb_required")

	res = Check(ctx, &fakeKit{status: applied, built: desc(), liveErr: errors.New("gone")}, store, "shop", "public")
	assert.Contains(t, res.Reason, "reading the live schema")

	kit := &fakeKit{status: applied, built: desc(table("orders")), live: desc(table("orders"), table("extra"))}
	res = Check(ctx, kit, store, "shop", "public")
	require.True(t, res.Checked)
	assert.Equal(t, 1, res.AppliedVersion)
	assert.Equal(t, []postgres.Migration{m1}, kit.builtFrom, "a pending migration is not built")
	assert.Equal(t, []Difference{{Kind: "table", Object: "extra", Change: ChangeAdded}}, res.Differences)
}

func TestRender(t *testing.T) {
	ctx := context.Background()
	tx := &fakeTx{}
	up, unsupported, err := Render(ctx, &fakeKit{tx: tx}, "shop", "legacy")
	require.NoError(t, err)
	assert.Empty(t, unsupported)
	assert.True(t, strings.HasPrefix(up, baselineHeader))
	assert.Equal(t, len(renderSections), strings.Count(up, "-- ddl"))
	assert.True(t, tx.rolledBack)
	assert.Contains(t, tx.sent[0], "pg_catalog", "every name is qualified")

	_, unsupported, err = Render(ctx, &fakeKit{tx: &fakeTx{unsupported: [][]any{{"rule", "r1"}}}}, "shop", "legacy")
	require.NoError(t, err)
	assert.Equal(t, []Unsupported{{Kind: "rule", Name: "r1"}}, unsupported)

	_, _, err = Render(ctx, &fakeKit{tx: &fakeTx{truncate: true}}, "shop", "legacy")
	require.ErrorIs(t, err, ErrTooLarge)

	for _, failOn := range []string{"search_path", "SELECT kind, name", "CREATE TYPE"} {
		_, _, err = Render(ctx, &fakeKit{tx: &fakeTx{failOn: failOn}}, "shop", "legacy")
		require.Error(t, err, failOn)
	}
	_, _, err = Render(ctx, &fakeKit{txErr: errors.New("no pool")}, "shop", "legacy")
	require.Error(t, err)
}

func TestAdopt(t *testing.T) {
	ctx := context.Background()
	empty := &postgres.LedgerStatus{}

	_, err := Adopt(ctx, &fakeKit{status: empty}, &fakeStore{migrations: []postgres.Migration{{Version: 1}}}, Target{Connection: "shop", Schema: "public", Actor: "a"})
	require.ErrorIs(t, err, ErrManaged)
	_, err = Adopt(ctx, &fakeKit{status: &postgres.LedgerStatus{Applied: []postgres.Applied{{Version: 1}}}}, &fakeStore{}, Target{Connection: "shop", Schema: "public", Actor: "a"})
	require.ErrorIs(t, err, ErrManaged)
	_, err = Adopt(ctx, &fakeKit{}, &fakeStore{listErr: errors.New("down")}, Target{Connection: "shop", Schema: "public", Actor: "a"})
	require.Error(t, err)
	_, err = Adopt(ctx, &fakeKit{statusErr: errors.New("down")}, &fakeStore{}, Target{Connection: "shop", Schema: "public", Actor: "a"})
	require.Error(t, err)

	res, err := Adopt(ctx, &fakeKit{status: empty, tx: &fakeTx{unsupported: [][]any{{"rule", "r"}}}}, &fakeStore{}, Target{Connection: "shop", Schema: "public", Actor: "a"})
	require.NoError(t, err)
	assert.False(t, res.Adopted)
	assert.Len(t, res.Unsupported, 1)

	_, err = Adopt(ctx, &fakeKit{status: empty, tx: &fakeTx{}, builtErr: errors.New("from zero")}, &fakeStore{}, Target{Connection: "shop", Schema: "public", Actor: "a"})
	require.ErrorContains(t, err, "applying the baseline from zero")
	_, err = Adopt(ctx, &fakeKit{status: empty, tx: &fakeTx{}, built: desc(), liveErr: errors.New("gone")}, &fakeStore{}, Target{Connection: "shop", Schema: "public", Actor: "a"})
	require.Error(t, err)

	store := &fakeStore{}
	res, err = Adopt(ctx, &fakeKit{status: empty, tx: &fakeTx{}, built: desc(), live: desc(table("t"))}, store, Target{Connection: "shop", Schema: "public", Actor: "a"})
	require.NoError(t, err)
	assert.False(t, res.Adopted)
	assert.Len(t, res.Differences, 1)
	assert.Empty(t, store.migrations, "a baseline that differs is not saved")

	kit := &fakeKit{status: empty, tx: &fakeTx{}, built: desc(table("t")), live: desc(table("t"))}
	res, err = Adopt(ctx, kit, store, Target{Connection: "shop", Schema: "public", Actor: "admin@example.com"})
	require.NoError(t, err)
	assert.True(t, res.Adopted)
	require.Len(t, store.migrations, 1)
	assert.Equal(t, BaselineVersion, store.migrations[0].Version)
	assert.Equal(t, postgres.ChecksumOf(res.Up, "", true), store.migrations[0].Checksum)
	require.Len(t, kit.applied, 1)
	assert.True(t, kit.applied[0].MarkOnly, "the baseline is marked applied, never run")
	assert.Equal(t, []string{postgres.EventAdded, postgres.EventApplied}, []string{store.events[0].Event, store.events[1].Event})

	_, err = Adopt(ctx, &fakeKit{status: empty, tx: &fakeTx{}, built: desc(), live: desc()}, &fakeStore{putErr: errors.New("x")}, Target{Connection: "shop", Schema: "public", Actor: "a"})
	require.ErrorContains(t, err, "saving the baseline")
	_, err = Adopt(ctx, &fakeKit{status: empty, tx: &fakeTx{}, built: desc(), live: desc(), applyErr: errors.New("x")}, &fakeStore{}, Target{Connection: "shop", Schema: "public", Actor: "a"})
	require.ErrorContains(t, err, "marking the baseline applied")
}

func TestToolkitReadTx_AnUnknownConnection(t *testing.T) {
	multi, err := postgres.ParseMultiConfig("shop", map[string]map[string]any{})
	require.NoError(t, err)
	_, err = Toolkit{postgres.NewMulti(multi)}.ReadTx(context.Background(), "nope")
	require.Error(t, err)
}

func TestRender_AShortRowIsSkipped(t *testing.T) {
	_, unsupported, err := Render(context.Background(), &fakeKit{tx: &fakeTx{unsupported: [][]any{{"rule"}}}}, "shop", "s")
	require.NoError(t, err)
	assert.Empty(t, unsupported)
}
