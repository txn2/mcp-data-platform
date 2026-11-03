package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/mcpcontext"
)

// fakeLedger answers the ledger's reads from applied.
type fakeLedger struct {
	exists  bool
	applied []Applied
	err     error
}

func (f *fakeLedger) QueryRow(context.Context, string, ...any) pgx.Row {
	return ledgerRow{exists: f.exists, err: f.err}
}

func (f *fakeLedger) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return &ledgerRows{applied: f.applied}, nil
}

type ledgerRow struct {
	exists bool
	err    error
}

func (r ledgerRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	p, ok := dest[0].(*bool)
	if !ok {
		return errors.New("fake ledger: the existence check scans into a bool")
	}
	*p = r.exists
	return nil
}

type ledgerRows struct {
	fakeRows
	applied []Applied
	i       int
}

func (r *ledgerRows) Next() bool { r.i++; return r.i <= len(r.applied) }

func (r *ledgerRows) Scan(dest ...any) error {
	a := r.applied[r.i-1]
	version, vok := dest[0].(*int)
	name, nok := dest[1].(*string)
	sum, sok := dest[2].(*string)
	at, aok := dest[3].(*time.Time)
	by, bok := dest[4].(*string)
	took, tok := dest[5].(*int64)
	if !vok || !nok || !sok || !aok || !bok || !tok {
		return errors.New("fake ledger: a ledger row scans into int, string, string, time, string, int64")
	}
	*version, *name, *sum, *at, *by, *took = a.Version, a.Name, a.Checksum, a.AppliedAt, a.AppliedBy, a.DurationMS
	return nil
}

func schemaToolkit(ledger *fakeLedger, cfg Config) (*Toolkit, *memStore) {
	cfg.ConnectionName = "shop"
	if cfg.LedgerSchema == "" {
		cfg.LedgerSchema = DefaultLedgerSchema
	}
	store := &memStore{}
	tk := &Toolkit{name: "shop", connections: map[string]*conn{"shop": {cfg: cfg, types: newTypeCache(), ledgerQ: ledger}}}
	tk.SetMigrationStore(store)
	return tk, store
}

func run(ctx context.Context, t *testing.T, tk *Toolkit, in schemaInput) (out map[string]any, refusal string) {
	t.Helper()
	if in.Connection == "" {
		in.Connection = "shop"
	}
	res, _, err := tk.handleSchema(ctx, &mcp.CallToolRequest{}, in)
	require.NoError(t, err)
	content, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	if res.IsError {
		return nil, content.Text
	}
	require.NoError(t, json.Unmarshal([]byte(content.Text), &out))
	return out, ""
}

func applied(versions ...int) []Applied {
	out := make([]Applied, 0, len(versions))
	for _, v := range versions {
		out = append(out, Applied{Version: v, Name: "m", Checksum: ChecksumOf("up", "", true), AppliedAt: time.Unix(1700000000, 0), AppliedBy: "a@example.com"})
	}
	return out
}

func TestSchemaToolIsRegisteredWithAStore(t *testing.T) {
	tk := &Toolkit{}
	assert.Empty(t, tk.Tools())
	tk.RegisterTools(mcp.NewServer(&mcp.Implementation{Name: "t"}, nil))
	tk.SetMigrationStore(&memStore{})
	assert.Equal(t, []string{SchemaToolName}, tk.Tools())
	s := mcp.NewServer(&mcp.Implementation{Name: "t"}, nil)
	tk.RegisterTools(s)
}

func TestSchemaAddAndStatus(t *testing.T) {
	ledger := &fakeLedger{exists: true, applied: applied(1)}
	tk, store := schemaToolkit(ledger, baseConfig())
	ctx := mcpcontext.WithCaller(context.Background(), mcpcontext.Caller{UserID: "u1", Email: "dev@example.com"})
	store.migrations = []Migration{{Connection: "shop", Schema: DefaultSchema, Version: 1, Name: "m", Up: "up", Transactional: true, Checksum: ChecksumOf("up", "", true)}}

	out, text := run(ctx, t, tk, schemaInput{Command: schemaAdd, Version: 2, Name: "items", Up: "CREATE TABLE items ()", Down: "DROP TABLE items"})
	require.Empty(t, text)
	assert.Equal(t, false, out["replaced"])
	out, _ = run(ctx, t, tk, schemaInput{Command: schemaAdd, Version: 2, Name: "items", Up: "CREATE TABLE items (id int)"})
	assert.Equal(t, true, out["replaced"], "a pending migration is replaced")
	out, _ = run(ctx, t, tk, schemaInput{Command: schemaAdd, Version: 2, Name: "items", Up: "CREATE TABLE items (id int)"})
	assert.Equal(t, true, out["unchanged"])
	assert.Equal(t, 1, store.eventsOf(EventAdded))
	assert.Equal(t, 1, store.eventsOf(EventEdited))
	assert.Equal(t, "dev@example.com", store.migrations[1].Author)

	no := false
	_, text = run(ctx, t, tk, schemaInput{Command: schemaAdd, Version: 3, Name: "idx", Up: "CREATE INDEX CONCURRENTLY i ON items (id)", Transactional: &no})
	require.Empty(t, text)
	assert.False(t, store.migrations[2].Transactional)

	status, _ := run(ctx, t, tk, schemaInput{Command: schemaStatus})
	assert.Equal(t, float64(1), status["current_version"])
	assert.Len(t, status["pending"], 2)
	assert.Equal(t, "mcp_schema.migrations", status["ledger"])
	assert.Empty(t, status["drift"])

	history, _ := run(ctx, t, tk, schemaInput{Command: schemaHistory, Limit: 2})
	assert.Len(t, history["events"], 2)
}

func TestSchemaAddRefusals(t *testing.T) {
	ledger := &fakeLedger{exists: true, applied: applied(1, 2)}
	tk, _ := schemaToolkit(ledger, baseConfig())
	ctx := context.Background()
	for name, in := range map[string]schemaInput{
		"no version": {Command: schemaAdd, Name: "x", Up: "SELECT 1"},
		"no name":    {Command: schemaAdd, Version: 3, Up: "SELECT 1"},
		"no up":      {Command: schemaAdd, Version: 3, Name: "x"},
		"applied":    {Command: schemaAdd, Version: 2, Name: "x", Up: "SELECT 1"},
		"below":      {Command: schemaAdd, Version: 1, Name: "x", Up: "SELECT 1"},
	} {
		_, text := run(ctx, t, tk, in)
		assert.NotEmpty(t, text, name)
	}
	_, text := run(ctx, t, tk, schemaInput{Command: schemaAdd, Version: 2, Name: "x", Up: "SELECT 1"})
	assert.Contains(t, text, "immutable")

	ro := baseConfig()
	ro.ReadOnly = true
	tk, _ = schemaToolkit(ledger, ro)
	_, text = run(ctx, t, tk, schemaInput{Command: schemaAdd, Version: 3, Name: "x", Up: "SELECT 1"})
	assert.Contains(t, text, "read_only")
	_, text = run(ctx, t, tk, schemaInput{Command: schemaPlan})
	assert.Contains(t, text, "read_only")
}

func TestSchemaCommandRefusals(t *testing.T) {
	tk, store := schemaToolkit(&fakeLedger{}, baseConfig())
	ctx := context.Background()
	_, text := run(ctx, t, tk, schemaInput{Command: "drop"})
	assert.Contains(t, text, "unknown command")
	_, text = run(ctx, t, tk, schemaInput{Command: schemaStatus, Connection: "nope"})
	assert.Contains(t, text, "no application database")
	_, text = run(ctx, t, tk, schemaInput{Command: schemaStatus, Schema: "Bad-Name"})
	assert.Contains(t, text, "lowercase identifier")
	_, text = run(ctx, t, tk, schemaInput{Command: schemaStatus, Schema: DefaultLedgerSchema})
	assert.Contains(t, text, "holds the migration ledger")
	for _, system := range []string{"pg_catalog", "pg_toast", "information_schema"} {
		_, text = run(ctx, t, tk, schemaInput{Command: schemaStatus, Schema: system})
		assert.Contains(t, text, "PostgreSQL's own", system)
	}

	_, text = run(ctx, t, tk, schemaInput{Command: schemaApply})
	assert.Contains(t, text, "schema_apply_personas", "apply without the grant")
	_, text = run(ctx, t, tk, schemaInput{Command: schemaDown})
	assert.Contains(t, text, "schema_apply_personas")
	admin := mcpcontext.WithCaller(ctx, mcpcontext.Caller{UserID: "a", IsAdmin: true})
	_, text = run(admin, t, tk, schemaInput{Command: schemaApply, TimeoutSeconds: 3600})
	assert.Contains(t, text, "timeout_seconds")

	store.err = errors.New("store down")
	for _, cmd := range []string{schemaStatus, schemaHistory, schemaPlan} {
		_, text = run(ctx, t, tk, schemaInput{Command: cmd})
		assert.Contains(t, text, "store down", cmd)
	}
	_, text = run(admin, t, tk, schemaInput{Command: schemaApply})
	assert.Contains(t, text, "store down")
	_, text = run(ctx, t, tk, schemaInput{Command: schemaAdd, Version: 1, Name: "x", Up: "SELECT 1"})
	assert.Contains(t, text, "store down")
}

func TestSchemaStatusReadsAMissingLedgerAsNothingApplied(t *testing.T) {
	tk, store := schemaToolkit(&fakeLedger{exists: false}, baseConfig())
	store.migrations = []Migration{{Connection: "shop", Schema: DefaultSchema, Version: 1, Name: "m", Up: "up", Checksum: "c"}}
	status, text := run(context.Background(), t, tk, schemaInput{Command: schemaStatus})
	require.Empty(t, text)
	assert.Len(t, status["pending"], 1)
	assert.Equal(t, float64(0), status["current_version"])

	tk, _ = schemaToolkit(&fakeLedger{err: errors.New("connection refused")}, baseConfig())
	_, text = run(context.Background(), t, tk, schemaInput{Command: schemaStatus})
	assert.NotEmpty(t, text)
}

func TestReport(t *testing.T) {
	ledger := &fakeLedger{exists: true, applied: []Applied{
		{Version: 1, Checksum: "c1", AppliedAt: time.Unix(1700000000, 0), AppliedBy: "a@example.com"},
		{Version: 2, Checksum: "tampered"},
	}}
	tk, store := schemaToolkit(ledger, baseConfig())
	store.migrations = []Migration{
		{Connection: "shop", Schema: DefaultSchema, Version: 1, Name: "a", Checksum: "c1"},
		{Connection: "shop", Schema: DefaultSchema, Version: 2, Name: "b", Checksum: "c2"},
		{Connection: "shop", Schema: DefaultSchema, Version: 3, Name: "c", Checksum: "c3"},
	}
	_ = store.Record(context.Background(), MigrationEvent{Connection: "shop", Schema: DefaultSchema, Version: 1, Event: EventApplied})
	report, err := tk.Report(context.Background(), "shop", DefaultSchema)
	require.NoError(t, err)
	assert.Equal(t, 2, report.CurrentVersion)
	states := make([]string, 0, len(report.Migrations))
	for _, m := range report.Migrations {
		states = append(states, m.State)
	}
	assert.Equal(t, []string{StateApplied, StateDrift, StatePending}, states)
	assert.Equal(t, "a@example.com", report.Migrations[0].AppliedBy)
	assert.Len(t, report.Drift, 1)
	assert.Len(t, report.Events, 1)

	_, err = (&Toolkit{}).Report(context.Background(), "shop", DefaultSchema)
	require.ErrorIs(t, err, ErrNoMigrationStore)
	_, err = (&Toolkit{}).PendingPlan(context.Background(), "shop", DefaultSchema)
	require.ErrorIs(t, err, ErrNoMigrationStore)
	_, err = tk.Report(context.Background(), "nope", DefaultSchema)
	require.Error(t, err)
	_, err = tk.PendingPlan(context.Background(), "nope", DefaultSchema)
	require.Error(t, err)
	_, err = tk.PendingPlan(context.Background(), "shop", DefaultSchema)
	require.ErrorContains(t, err, "checksum", "a plan over drift is refused")
}

func TestCompare(t *testing.T) {
	m := func(v int, sum string) Migration { return Migration{Version: v, Checksum: sum} }
	a := func(v int, sum string) Applied { return Applied{Version: v, Checksum: sum} }
	pending, drift := Compare([]Migration{m(3, "c"), m(1, "a"), m(2, "b")}, []Applied{a(1, "a")})
	assert.Equal(t, []int{2, 3}, versionsOf(pending), "pending in version order")
	assert.Empty(t, drift)

	_, drift = Compare([]Migration{m(1, "a")}, []Applied{a(1, "x"), a(2, "b")})
	require.Len(t, drift, 2)
	assert.Contains(t, drift[0].Reason, "checksum")
	assert.Contains(t, drift[1].Reason, "holds no such migration")

	pending, drift = Compare([]Migration{m(1, "a"), m(2, "b"), m(3, "c")}, []Applied{a(1, "a"), a(3, "c")})
	assert.Empty(t, pending)
	require.Len(t, drift, 1)
	assert.Contains(t, drift[0].Reason, "below version 3")
}

func TestMigrationHelpers(t *testing.T) {
	assert.NotEqual(t, ChecksumOf("a", "", true), ChecksumOf("a", "", false))
	assert.NotEqual(t, ChecksumOf("a", "b", true), ChecksumOf("a", "", true))
	assert.Len(t, ChecksumOf("a", "", true), 64)

	two := 2
	ms := []Migration{{Version: 1}, {Version: 2}, {Version: 3}}
	assert.Equal(t, []int{1, 2}, versionsOf(upTo(ms, &two)))
	assert.Len(t, upTo(ms, nil), 3)

	appl := []Applied{{Version: 1}, {Version: 2}, {Version: 3}}
	assert.Equal(t, []int{3}, revertTargets(appl, -1))
	assert.Equal(t, []int{3, 2}, revertTargets(appl, 1))
	assert.Empty(t, revertTargets(appl, 3))

	f := migrationFailure(4, &FailedError{Summary: "boom"})
	assert.Equal(t, "migration 4: boom", f.Error())
	e := migrationFailure(5, errors.New("plain"))
	assert.Equal(t, "migration 5: plain", e.Error())
	assert.Equal(t, "plain", errors.Unwrap(e).Error())
	assert.Equal(t, "250", millis(250*time.Millisecond))
	assert.Empty(t, nonNilDrift(nil))
	assert.Equal(t, 0, currentOf(nil))

	ctx := mcpcontext.WithPersona(context.Background(), "builder")
	c := &conn{cfg: Config{ConnectionName: "shop", SchemaApplyPersonas: []string{"builder"}}}
	require.NoError(t, mayApply(ctx, c))
	require.Error(t, mayApply(context.Background(), c))
	assert.Empty(t, actorOf(context.Background()))
	assert.Equal(t, "u9", actorOf(mcpcontext.WithCaller(context.Background(), mcpcontext.Caller{UserID: "u9"})))
	assert.Equal(t, "shop", strings.TrimSpace((&conn{cfg: Config{LedgerSchema: "shop"}}).cfg.LedgerSchema))
}

func TestRecordFailureIsNotTheCommandsFailure(t *testing.T) {
	tk, store := schemaToolkit(&fakeLedger{exists: true}, baseConfig())
	store.err = errors.New("history down")
	tk.record(context.Background(), Migration{Connection: "shop", Version: 1}, EventApplied, time.Second, errors.New("statement failed"))
	assert.Len(t, store.events, 1)
	assert.Equal(t, EventFailed, store.events[0].Event)
	assert.Contains(t, store.events[0].Detail, "applied: statement failed")
}

func TestLedgerSchemaIsValidated(t *testing.T) {
	cfg := baseConfig()
	cfg.PoolWait, cfg.TransactionIdle, cfg.LedgerSchema = DefaultPoolWait, DefaultTransactionIdle, "Bad Schema"
	require.ErrorContains(t, cfg.Validate(), "schema_ledger")
	cfg.LedgerSchema = DefaultLedgerSchema
	cfg.PoolWait = 0
	require.ErrorContains(t, cfg.Validate(), "pool_wait_timeout")
	cfg.PoolWait = DefaultPoolWait
	cfg.TransactionIdle = time.Hour
	require.ErrorContains(t, cfg.Validate(), "transaction_idle_timeout")

	parsed, err := ParseConfig(map[string]any{
		"host": "h", "database": "d", "user": "u",
		"statement_cache": false, "schema_apply_personas": []any{"builder", " ", 3}, "pool_wait_timeout": "2s",
	})
	require.NoError(t, err)
	assert.False(t, parsed.StatementCache)
	assert.Equal(t, []string{"builder"}, parsed.SchemaApplyPersonas)
	assert.Equal(t, 2*time.Second, parsed.PoolWait)
	assert.Equal(t, DefaultLedgerSchema, parsed.LedgerSchema)
}

func TestDescribeReadsTheCatalog(t *testing.T) {
	tx := &fakeTx{types: fakeTypes(), statements: map[string]fakeStatement{
		describeTablesSQL:      {rows: [][]any{{"items", "r", "order lines"}, {"totals", "v", ""}}},
		describeColumnsSQL:     {rows: [][]any{{"items", "sku", "text", false, "", ""}, {"items", "qty", "integer", false, "1", "how many"}, {"gone", "x", "int", true, "", ""}}},
		describeConstraintsSQL: {rows: [][]any{{"items", "items_qty_check", "c", "CHECK ((qty > 0))"}, {"gone", "x", "p", "PRIMARY KEY (x)"}}},
		describeIndexesSQL:     {rows: [][]any{{"items", "items_sku", "CREATE INDEX items_sku ON public.items USING btree (sku)"}, {"gone", "y", "z"}}},
	}}
	tk := fakeToolkit(baseConfig(), tx)
	tk.connections["shop"] = tk.connections["db"]
	tk.connections["shop"].cfg.LedgerSchema = DefaultLedgerSchema
	tables, err := tk.Describe(context.Background(), "shop", DefaultSchema)
	require.NoError(t, err)
	require.Len(t, tables, 2)
	items := tables[0]
	assert.Equal(t, "table", items.Kind)
	assert.Equal(t, "order lines", items.Comment)
	assert.Equal(t, []TableColumn{{Name: "sku", Type: "text"}, {Name: "qty", Type: "integer", Default: "1", Comment: "how many"}}, items.Columns)
	assert.Equal(t, []Constraint{{Name: "items_qty_check", Kind: "check", Definition: "CHECK ((qty > 0))"}}, items.Constraints)
	assert.Len(t, items.Indexes, 1)
	assert.Equal(t, "view", tables[1].Kind)
	assert.NotNil(t, tables[1].Columns, "an empty list is [], never null")
	assert.False(t, tx.committed, "describe reads in a READ ONLY transaction it never commits")

	_, err = tk.Describe(context.Background(), "nope", DefaultSchema)
	require.Error(t, err)
	tx.statements[describeColumnsSQL] = fakeStatement{execErr: errors.New("connection reset")}
	_, err = tk.Describe(context.Background(), "shop", DefaultSchema)
	require.Error(t, err)
}

func TestReleasingTxReleasesOnce(t *testing.T) {
	released := 0
	tx := &releasingTx{Tx: &fakeTx{}, release: func() { released++ }}
	require.NoError(t, tx.Commit(context.Background()))
	require.NoError(t, tx.Rollback(context.Background()))
	assert.Equal(t, 1, released)
	assert.Equal(t, "boom", (&refusedInputError{err: errors.New("boom")}).Error())
}
