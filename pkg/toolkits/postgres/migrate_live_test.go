package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/mcpcontext"
)

// fakeSession is a held connection for the migration code: it answers the
// ledger's reads from ledger, records what it runs, and opens fakeMigTx
// transactions.
type fakeSession struct {
	ledger    fakeLedger
	execs     []string
	scripts   []string
	txs       []*fakeMigTx
	lockErr   error
	execErr   error
	scriptErr error
	beginErr  error
	released  bool
	// txScriptErr fails the script of the transaction numbered by its key.
	txScriptErr map[int]error
	txCommitErr error
}

func (s *fakeSession) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	s.execs = append(s.execs, sql)
	if strings.Contains(sql, "pg_advisory_lock") && s.lockErr != nil {
		return pgconn.CommandTag{}, s.lockErr
	}
	return pgconn.NewCommandTag("SELECT 1"), s.execErr
}

func (s *fakeSession) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return s.ledger.QueryRow(ctx, sql, args...)
}

func (s *fakeSession) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return s.ledger.Query(ctx, sql, args...)
}

func (s *fakeSession) Begin(context.Context) (pgx.Tx, error) {
	if s.beginErr != nil {
		return nil, s.beginErr
	}
	tx := &fakeMigTx{fakeTx: &fakeTx{statements: map[string]fakeStatement{
		planChangesSQL: {rows: [][]any{{"public.orders"}, {"public.orders.note"}}},
		planLocksSQL:   {rows: [][]any{{"public.orders", "AccessExclusiveLock"}}},
	}, commitErr: s.txCommitErr}, scriptErr: s.txScriptErr[len(s.txs)]}
	s.txs = append(s.txs, tx)
	return tx, nil
}

func (s *fakeSession) Script(_ context.Context, sql string) error {
	s.scripts = append(s.scripts, sql)
	return s.scriptErr
}

func (s *fakeSession) Release() { s.released = true }

// fakeMigTx is a migration's transaction: a fakeTx that runs scripts.
type fakeMigTx struct {
	*fakeTx
	scripts   []string
	scriptErr error
}

func (x *fakeMigTx) Script(_ context.Context, sql string) error {
	x.scripts = append(x.scripts, sql)
	return x.scriptErr
}

func liveToolkit(sess *fakeSession, cfg Config) *Toolkit {
	cfg.ConnectionName = "shop"
	cfg.LedgerSchema = DefaultLedgerSchema
	c := &conn{
		cfg: cfg, types: newTypeCache(), cache: newStmtCache("shop", nil), ledgerQ: &sess.ledger,
		openSession: func(context.Context) (session, error) { return sess, nil },
	}
	return &Toolkit{connections: map[string]*conn{"shop": c}}
}

func migration(v int, up, down string, transactional bool) Migration {
	return Migration{
		Connection: "shop", Schema: DefaultSchema, Version: v, Name: "m", Up: up, Down: down,
		Transactional: transactional, Checksum: ChecksumOf(up, down, transactional),
	}
}

func applyOpts() ApplyOptions {
	return ApplyOptions{Connection: "shop", Schema: DefaultSchema, Actor: "admin@example.com"}
}

func TestApplyRunsEachMigrationInItsOwnTransaction(t *testing.T) {
	sess := &fakeSession{ledger: fakeLedger{exists: true}}
	tk := liveToolkit(sess, baseConfig())
	before := tk.connections["shop"].cache.gen.Load()
	ms := []Migration{
		migration(1, "CREATE TABLE a (id int)", "DROP TABLE a", true),
		migration(2, "CREATE INDEX CONCURRENTLY i ON a (id)", "", false),
		migration(3, "ALTER TABLE a ADD COLUMN n int", "", true),
	}
	var told []int
	o := applyOpts()
	o.OnDone = func(m Migration, _ time.Duration, err error) {
		require.NoError(t, err)
		told = append(told, m.Version)
	}
	done, err := tk.Apply(context.Background(), o, ms)
	require.NoError(t, err)
	assert.Equal(t, []int{1, 2, 3}, versionsOf(done))
	assert.Equal(t, []int{1, 2, 3}, told)
	assert.True(t, sess.released, "the session goes back to the pool, and its lock with it")
	require.Len(t, sess.txs, 2, "the non-transactional migration runs outside a transaction")
	assert.Equal(t, []string{"CREATE TABLE a (id int)"}, sess.txs[0].scripts)
	assert.True(t, sess.txs[0].committed)
	assert.Contains(t, strings.Join(sess.txs[0].execs, "\n"), "INSERT INTO mcp_schema.migrations", "the ledger row is written in the migration's transaction")
	assert.Contains(t, strings.Join(sess.scripts, "\n"), "CREATE TABLE IF NOT EXISTS mcp_schema.migrations", "the ledger is created on first use")
	assert.Contains(t, strings.Join(sess.scripts, "\n"), "CREATE INDEX CONCURRENTLY")
	assert.Contains(t, strings.Join(sess.execs, "\n"), "pg_advisory_lock")
	assert.Equal(t, before+1, tk.connections["shop"].cache.gen.Load(), "an apply empties the statement cache")

	two := 2
	o.UpTo = &two
	sess2 := &fakeSession{ledger: fakeLedger{exists: true}}
	done, err = liveToolkit(sess2, baseConfig()).Apply(context.Background(), o, ms)
	require.NoError(t, err)
	assert.Equal(t, []int{1, 2}, versionsOf(done))
}

func TestApplyStops(t *testing.T) {
	ms := []Migration{migration(1, "CREATE TABLE a (id int)", "", true), migration(2, "CREATE TABLE b (id int)", "", true)}
	ctx := context.Background()

	drift := &fakeSession{ledger: fakeLedger{exists: true, applied: []Applied{{Version: 1, Checksum: "edited"}}}}
	_, err := liveToolkit(drift, baseConfig()).Apply(ctx, applyOpts(), ms)
	require.ErrorContains(t, err, "version 1")
	assert.Empty(t, drift.txs, "no migration runs over drift")

	failing := &fakeSession{ledger: fakeLedger{exists: true}, txScriptErr: map[int]error{0: &pgconn.PgError{Code: "42P07", Message: "exists"}}}
	done, err := liveToolkit(failing, baseConfig()).Apply(ctx, applyOpts(), ms)
	require.ErrorContains(t, err, "migration 1")
	assert.Empty(t, done)
	assert.True(t, failing.txs[0].rolledBack)
	assert.False(t, failing.txs[0].committed)
	assert.Len(t, failing.txs, 1, "the apply stops at the failure")

	locked := &fakeSession{ledger: fakeLedger{exists: true}, lockErr: &pgconn.PgError{Code: queryCanceled}}
	_, err = liveToolkit(locked, baseConfig()).Apply(ctx, applyOpts(), ms)
	var failed *FailedError
	require.ErrorAs(t, err, &failed)
	assert.Equal(t, CodeApplyLocked, failed.Code)
	assert.True(t, locked.released)

	sneaky := []Migration{migration(1, "CREATE TABLE a (id int); COMMIT; DROP TABLE b", "", true)}
	stored := &fakeSession{ledger: fakeLedger{exists: true}}
	_, err = liveToolkit(stored, baseConfig()).Apply(ctx, applyOpts(), sneaky)
	require.ErrorContains(t, err, "COMMIT", "a migration stored before the check is refused before it runs")
	assert.Empty(t, stored.txs)

	for name, sess := range map[string]*fakeSession{
		"lock refused":  {ledger: fakeLedger{exists: true}, lockErr: errors.New("connection reset")},
		"no ledger":     {ledger: fakeLedger{exists: true}, scriptErr: errors.New("permission denied")},
		"no timeout":    {ledger: fakeLedger{exists: true}, execErr: errors.New("connection reset")},
		"ledger unread": {ledger: fakeLedger{err: errors.New("connection reset")}},
		"no tx":         {ledger: fakeLedger{exists: true}, beginErr: errors.New("connection reset")},
		"commit":        {ledger: fakeLedger{exists: true}, txCommitErr: &pgconn.PgError{Code: "40001"}},
	} {
		_, err := liveToolkit(sess, baseConfig()).Apply(ctx, applyOpts(), ms)
		require.Error(t, err, name)
	}

	ro := baseConfig()
	ro.ReadOnly = true
	_, err = liveToolkit(&fakeSession{}, ro).Apply(ctx, applyOpts(), ms)
	require.ErrorContains(t, err, "read_only")
	o := applyOpts()
	o.Connection = "nope"
	_, err = liveToolkit(&fakeSession{}, baseConfig()).Apply(ctx, o, ms)
	require.Error(t, err)

	tk := liveToolkit(&fakeSession{}, baseConfig())
	tk.connections["shop"].openSession = func(context.Context) (session, error) {
		return nil, &poolWaitError{connection: "shop", maxConns: 5, wait: time.Second}
	}
	_, err = tk.Apply(ctx, applyOpts(), ms)
	require.ErrorAs(t, err, &failed)
	assert.Equal(t, CodePoolWait, failed.Code)
}

func TestApplyAloneFailures(t *testing.T) {
	ms := []Migration{migration(1, "CREATE INDEX CONCURRENTLY i ON a (id)", "", false)}
	for name, sess := range map[string]*fakeSession{
		"script": {ledger: fakeLedger{exists: true}},
	} {
		tk := liveToolkit(sess, baseConfig())
		_, err := tk.Apply(context.Background(), applyOpts(), ms)
		require.NoError(t, err, name)
	}
	// The ledger script succeeds and the migration's fails.
	sess := &fakeSession{ledger: fakeLedger{exists: true}}
	tk := liveToolkit(sess, baseConfig())
	calls := 0
	tk.connections["shop"].openSession = func(context.Context) (session, error) {
		return &scriptFailsSecond{fakeSession: sess, calls: &calls}, nil
	}
	_, err := tk.Apply(context.Background(), applyOpts(), ms)
	require.ErrorContains(t, err, "migration 1")
}

// scriptFailsSecond fails every script after the first (the ledger's).
type scriptFailsSecond struct {
	*fakeSession
	calls *int
}

func (s *scriptFailsSecond) Script(ctx context.Context, sql string) error {
	*s.calls++
	if *s.calls > 1 {
		return &pgconn.PgError{Code: "55000", Message: "cannot run"}
	}
	return s.fakeSession.Script(ctx, sql)
}

func TestRevert(t *testing.T) {
	ms := []Migration{migration(1, "CREATE TABLE a (id int)", "DROP TABLE a", true), migration(2, "CREATE TABLE b (id int)", "DROP TABLE b", true)}
	applied := []Applied{{Version: 1, Checksum: ms[0].Checksum}, {Version: 2, Checksum: ms[1].Checksum}}
	ctx := context.Background()

	sess := &fakeSession{ledger: fakeLedger{exists: true, applied: applied}}
	done, err := liveToolkit(sess, baseConfig()).Revert(ctx, applyOpts(), ms, 0)
	require.NoError(t, err)
	assert.Equal(t, []int{2, 1}, versionsOf(done), "newest first")
	assert.Equal(t, []string{"DROP TABLE b"}, sess.txs[0].scripts)
	assert.Contains(t, strings.Join(sess.txs[0].execs, "\n"), "DELETE FROM mcp_schema.migrations")

	noDown := []Migration{ms[0], migration(2, "CREATE TABLE b (id int)", "", true)}
	sess = &fakeSession{ledger: fakeLedger{exists: true, applied: []Applied{applied[0], {Version: 2, Checksum: noDown[1].Checksum}}}}
	_, err = liveToolkit(sess, baseConfig()).Revert(ctx, applyOpts(), noDown, -1)
	require.ErrorContains(t, err, "no down SQL")

	sess = &fakeSession{ledger: fakeLedger{exists: true, applied: []Applied{{Version: 9}}}}
	_, err = liveToolkit(sess, baseConfig()).Revert(ctx, applyOpts(), ms, -1)
	require.ErrorContains(t, err, "nothing was reverted")

	sess = &fakeSession{ledger: fakeLedger{exists: true, applied: applied}, txScriptErr: map[int]error{0: &pgconn.PgError{Code: "2BP01"}}}
	_, err = liveToolkit(sess, baseConfig()).Revert(ctx, applyOpts(), ms, -1)
	require.ErrorContains(t, err, "migration 2")

	sneaky := []Migration{ms[0], migration(2, "CREATE TABLE b (id int)", "ROLLBACK", true)}
	sess = &fakeSession{ledger: fakeLedger{exists: true, applied: []Applied{applied[0], {Version: 2, Checksum: sneaky[1].Checksum}}}}
	_, err = liveToolkit(sess, baseConfig()).Revert(ctx, applyOpts(), sneaky, -1)
	require.ErrorContains(t, err, "ROLLBACK")

	_, err = liveToolkit(&fakeSession{lockErr: errors.New("reset")}, baseConfig()).Revert(ctx, applyOpts(), ms, -1)
	require.Error(t, err)
	for name, sess := range map[string]*fakeSession{
		"no tx":  {ledger: fakeLedger{exists: true, applied: applied}, beginErr: errors.New("reset")},
		"commit": {ledger: fakeLedger{exists: true, applied: applied}, txCommitErr: errors.New("reset")},
	} {
		_, err := liveToolkit(sess, baseConfig()).Revert(ctx, applyOpts(), ms, -1)
		require.Error(t, err, name)
	}
}

func TestPlanMigrations(t *testing.T) {
	ctx := context.Background()
	sess := &fakeSession{ledger: fakeLedger{exists: true}}
	tk := liveToolkit(sess, baseConfig())
	plan, err := tk.PlanMigrations(ctx, "shop", DefaultSchema, []Migration{
		migration(1, "ALTER TABLE orders ADD COLUMN note text", "", true),
		migration(2, "CREATE INDEX CONCURRENTLY i ON orders (note)", "", false),
	})
	require.NoError(t, err)
	assert.Equal(t, []int{1}, plan.Migrations)
	assert.Equal(t, []int{2}, plan.Unplanned)
	assert.Equal(t, []string{"public.orders", "public.orders.note"}, plan.Changed)
	assert.Equal(t, []Lock{{Relation: "public.orders", Mode: "AccessExclusiveLock"}}, plan.Locks)
	assert.True(t, sess.txs[0].rolledBack)
	assert.False(t, sess.txs[0].committed, "a plan never commits")
	assert.True(t, sess.released)

	_, err = tk.PlanMigrations(ctx, "shop", DefaultSchema, []Migration{migration(1, "SELECT 1; COMMIT", "", true)})
	require.ErrorContains(t, err, "COMMIT")
	sess = &fakeSession{txScriptErr: map[int]error{0: &pgconn.PgError{Code: "42601"}}}
	_, err = liveToolkit(sess, baseConfig()).PlanMigrations(ctx, "shop", DefaultSchema, []Migration{migration(1, "SELEC 1", "", true)})
	require.ErrorContains(t, err, "migration 1")
	_, err = liveToolkit(&fakeSession{beginErr: errors.New("reset")}, baseConfig()).PlanMigrations(ctx, "shop", DefaultSchema, nil)
	require.Error(t, err)
	_, err = liveToolkit(&fakeSession{}, baseConfig()).PlanMigrations(ctx, "nope", DefaultSchema, nil)
	require.Error(t, err)

	tk = liveToolkit(&fakeSession{}, baseConfig())
	tk.connections["shop"].openSession = func(context.Context) (session, error) { return nil, errors.New("reset") }
	_, err = tk.PlanMigrations(ctx, "shop", DefaultSchema, nil)
	require.Error(t, err)
}

// The tool's apply, down and plan through the same fakes.
func TestSchemaToolAppliesRevertsAndPlans(t *testing.T) {
	sess := &fakeSession{ledger: fakeLedger{exists: true}}
	tk := liveToolkit(sess, baseConfig())
	store := &memStore{migrations: []Migration{migration(1, "CREATE TABLE a (id int)", "DROP TABLE a", true)}}
	tk.SetMigrationStore(store)
	admin := mcpcontext.WithCaller(context.Background(), mcpcontext.Caller{UserID: "a", Email: "admin@example.com", IsAdmin: true})

	out, text := run(admin, t, tk, schemaInput{Command: schemaApply, TimeoutSeconds: 60})
	require.Empty(t, text)
	assert.Len(t, out["done"], 1)
	assert.Equal(t, 1, store.eventsOf(EventApplied))

	// The fake ledger records no insert, so version 1 still reads as pending.
	plan, err := tk.PendingPlan(admin, "shop", DefaultSchema)
	require.NoError(t, err)
	assert.Equal(t, []int{1}, plan.Migrations)
	out, text = run(admin, t, tk, schemaInput{Command: schemaPlan})
	require.Empty(t, text)
	assert.Equal(t, true, out["rolled_back"])

	sess.ledger.applied = []Applied{{Version: 1, Checksum: store.migrations[0].Checksum}}
	zero := 0
	out, text = run(admin, t, tk, schemaInput{Command: schemaDown, ToVersion: &zero})
	require.Empty(t, text)
	assert.Len(t, out["done"], 1)
	assert.Equal(t, 1, store.eventsOf(EventReverted))

	next := len(sess.txs)
	sess.txScriptErr = map[int]error{next: &pgconn.PgError{Code: "42P01"}}
	_, text = run(admin, t, tk, schemaInput{Command: schemaDown})
	assert.Contains(t, text, "done before it")
	assert.Equal(t, 1, store.eventsOf(EventFailed))
}
