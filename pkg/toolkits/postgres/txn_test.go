package postgres

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// txToolkit is a toolkit whose connection "db" opens fresh fake transactions,
// each recorded in opened.
func txToolkit(cfg Config, statements map[string]fakeStatement) (*Toolkit, *[]*fakeTx) {
	cfg.ConnectionName = "db"
	opened := &[]*fakeTx{}
	tk := &Toolkit{name: "db", defaultName: "db", connections: map[string]*conn{"db": {
		cfg: cfg, types: newTypeCache(),
		begin: func(_ context.Context, o pgx.TxOptions) (pgx.Tx, error) {
			tx := &fakeTx{statements: statements, types: fakeTypes(), opts: o}
			*opened = append(*opened, tx)
			return tx, nil
		},
	}}}
	return tk, opened
}

func insertStatement() map[string]fakeStatement {
	return map[string]fakeStatement{
		"INSERT INTO t VALUES ($1)": {params: []uint32{oidInt4}, tag: "INSERT 0 1"},
		"SELECT n FROM t":           {fields: []pgconn.FieldDescription{field("n", oidInt4)}, rows: [][]any{{int32(1)}, {int32(2)}, {int32(3)}}, tag: "SELECT 3"},
		"SELECT bad":                {prepErr: &pgconn.PgError{Code: "42601", Message: "syntax error"}},
	}
}

func TestBeginRefusals(t *testing.T) {
	cfg := baseConfig()
	cfg.ReadOnly = true
	tk, _ := txToolkit(cfg, nil)
	_, err := tk.Begin(context.Background(), TxOptions{Connection: "nope"})
	var refused *RefusedError
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, CodeUnknownConnection, refused.Code)
	_, err = tk.Begin(context.Background(), TxOptions{Connection: "db", Isolation: "chaos"})
	require.ErrorContains(t, err, "isolation")
	_, err = tk.Begin(context.Background(), TxOptions{Connection: "db"})
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, CodeReadOnly, refused.Code)
	x, err := tk.Begin(context.Background(), TxOptions{Connection: "db", ReadOnly: true})
	require.NoError(t, err)
	_, err = x.Execute(context.Background(), Statement{SQL: "INSERT INTO t VALUES ($1)", Params: []any{"1"}})
	require.ErrorAs(t, err, &refused, "a read-only transaction executes nothing")
	x.Rollback()
}

func TestTransactionRunsStatementsAndCommits(t *testing.T) {
	tk, opened := txToolkit(baseConfig(), insertStatement())
	ctx := context.Background()
	x, err := tk.Begin(ctx, TxOptions{Connection: "db", Isolation: IsolationSerializable, Attempt: 2})
	require.NoError(t, err)
	assert.Equal(t, "db", x.Connection())
	tx := (*opened)[0]
	assert.Empty(t, tx.execs, "the bounds are set in the BEGIN's round trip")
	assert.Equal(t, "BEGIN ISOLATION LEVEL SERIALIZABLE READ WRITE; SET LOCAL statement_timeout = 30000; "+
		"SET LOCAL idle_in_transaction_session_timeout = 15000", tx.opts.BeginQuery)

	res, err := x.Execute(ctx, Statement{SQL: "INSERT INTO t VALUES ($1)", Params: []any{"1"}})
	require.NoError(t, err)
	assert.Equal(t, "INSERT", res.Command)
	got, err := x.Query(ctx, Statement{SQL: "SELECT n FROM t", Limit: 2})
	require.NoError(t, err)
	assert.Len(t, got.Rows, 2)
	assert.True(t, got.Truncated)

	_, err = x.Query(ctx, Statement{})
	require.ErrorContains(t, err, "empty")
	_, err = x.Query(ctx, Statement{SQL: "SELECT n FROM t", Params: map[string]any{"x": 1}})
	require.Error(t, err)

	require.NoError(t, x.Commit(ctx))
	assert.True(t, tx.committed)
	_, err = x.Query(ctx, Statement{SQL: "SELECT n FROM t"})
	var failed *FailedError
	require.ErrorAs(t, err, &failed)
	assert.Equal(t, CodeTransactionEnded, failed.Code)
	assert.Contains(t, err.Error(), "committed")
	x.Rollback()
	assert.False(t, tx.rolledBack, "rolling back an ended transaction does nothing")
	assert.Empty(t, tk.open, "a committed transaction is no longer held")
}

func TestAFailedStatementEndsTheTransaction(t *testing.T) {
	tk, opened := txToolkit(baseConfig(), insertStatement())
	ctx := context.Background()
	x, err := tk.Begin(ctx, TxOptions{Connection: "db"})
	require.NoError(t, err)
	// Refused before it was sent: the transaction is still usable.
	_, err = x.Execute(ctx, Statement{SQL: "BEGIN"})
	require.ErrorContains(t, err, "is not run")
	_, err = x.Query(ctx, Statement{SQL: "SELECT n FROM t"})
	require.NoError(t, err)
	// Refused by the server: the transaction is over.
	_, err = x.Query(ctx, Statement{SQL: "SELECT bad"})
	require.ErrorContains(t, err, "42601")
	assert.True(t, (*opened)[0].rolledBack)
	_, err = x.Query(ctx, Statement{SQL: "SELECT n FROM t"})
	require.ErrorContains(t, err, "a statement in it failed")
	require.Error(t, x.Commit(ctx))
}

func TestCommitFailureIsTheCommitsError(t *testing.T) {
	tk, opened := txToolkit(baseConfig(), insertStatement())
	x, err := tk.Begin(context.Background(), TxOptions{Connection: "db"})
	require.NoError(t, err)
	(*opened)[0].commitErr = &pgconn.PgError{Code: "40001", Message: "could not serialize access"}
	err = x.Commit(context.Background())
	var failed *FailedError
	require.ErrorAs(t, err, &failed)
	assert.Equal(t, "40001", failed.Statement.SQLState)
	_, err = x.Query(context.Background(), Statement{SQL: "SELECT n FROM t"})
	require.ErrorContains(t, err, "its commit failed")
}

func TestTransactionEndsWithItsRun(t *testing.T) {
	tk, opened := txToolkit(baseConfig(), insertStatement())
	ctx, cancel := context.WithCancel(context.Background())
	x, err := tk.Begin(ctx, TxOptions{Connection: "db"})
	require.NoError(t, err)
	cancel()
	require.Eventually(t, func() bool {
		x.mu.Lock()
		defer x.mu.Unlock()
		return x.ended != ""
	}, 5*time.Second, 5*time.Millisecond)
	assert.True(t, (*opened)[0].rolledBack)
	_, err = x.Query(context.Background(), Statement{SQL: "SELECT n FROM t"})
	require.ErrorContains(t, err, "run that opened it ended")
}

func TestIdleTransactionEnds(t *testing.T) {
	cfg := baseConfig()
	cfg.TransactionIdle = 20 * time.Millisecond
	tk, _ := txToolkit(cfg, insertStatement())
	x, err := tk.Begin(context.Background(), TxOptions{Connection: "db"})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		x.mu.Lock()
		defer x.mu.Unlock()
		return x.ended != ""
	}, 5*time.Second, 5*time.Millisecond)
	_, err = x.Query(context.Background(), Statement{SQL: "SELECT n FROM t"})
	require.ErrorContains(t, err, "transaction_idle_timeout")
}

func TestAbandonOpenRollsBackEveryTransaction(t *testing.T) {
	tk, opened := txToolkit(baseConfig(), insertStatement())
	for range 2 {
		_, err := tk.Begin(context.Background(), TxOptions{Connection: "db"})
		require.NoError(t, err)
	}
	tk.abandonOpen()
	for _, tx := range *opened {
		assert.True(t, tx.rolledBack)
	}
	assert.Empty(t, tk.open)
}

func TestBeginFailures(t *testing.T) {
	cfg := baseConfig()
	cfg.ConnectionName = "db"
	tk := &Toolkit{connections: map[string]*conn{"db": {
		cfg: cfg, types: newTypeCache(),
		begin: func(context.Context, pgx.TxOptions) (pgx.Tx, error) { return nil, errors.New("connection reset") },
	}}}
	_, err := tk.Begin(context.Background(), TxOptions{Connection: "db"})
	require.ErrorContains(t, err, "could not reach")

	tk.connections["db"].begin = func(context.Context, pgx.TxOptions) (pgx.Tx, error) {
		return nil, &poolWaitError{connection: "db", maxConns: 5, wait: time.Second}
	}
	_, err = tk.Begin(context.Background(), TxOptions{Connection: "db"})
	var failed *FailedError
	require.ErrorAs(t, err, &failed)
	assert.Equal(t, CodePoolWait, failed.Code)
	assert.Contains(t, err.Error(), "max_conns")
}

func TestBindAllRefusals(t *testing.T) {
	ctx := context.Background()
	_, err := bindAll(ctx, nil)
	require.ErrorContains(t, err, "no statement")
	_, err = bindAll(ctx, make([]Statement, MaxBatch+1))
	require.ErrorContains(t, err, "more than")
	_, err = bindAll(ctx, []Statement{{SQL: "SELECT 1"}, {}})
	require.ErrorContains(t, err, "statement 2 of the batch is empty")
	_, err = bindAll(ctx, []Statement{{SQL: "SELECT $1", Params: 7}})
	require.ErrorContains(t, err, "statement 1 of the batch")
	got, err := bindAll(ctx, []Statement{{SQL: "SELECT $1", Params: []any{"a"}}})
	require.NoError(t, err)
	assert.Len(t, got, 1)

	tk, _ := txToolkit(baseConfig(), nil)
	_, err = tk.Batch(ctx, "nope", []Statement{{SQL: "SELECT 1"}})
	require.ErrorContains(t, err, "no application database")
	_, err = tk.Batch(ctx, "db", nil)
	require.ErrorContains(t, err, "no statement")
	x, err := tk.Begin(ctx, TxOptions{Connection: "db"})
	require.NoError(t, err)
	_, err = x.Batch(ctx, nil)
	require.ErrorContains(t, err, "no statement")
	x.Rollback()
	_, err = x.Batch(ctx, []Statement{{SQL: "SELECT 1"}})
	require.ErrorContains(t, err, "is over")
}

func TestPreparedHelpers(t *testing.T) {
	values, err := textValues([]any{"a", nil})
	require.NoError(t, err)
	assert.Equal(t, [][]byte{[]byte("a"), nil}, values)
	_, err = textValues([]any{3})
	require.ErrorContains(t, err, "not text")

	word, n := commandOf(pgconn.NewCommandTag("INSERT 0 3"), nil)
	assert.Equal(t, "INSERT", word)
	assert.Equal(t, int64(3), n)
	word, _ = commandOf(pgconn.NewCommandTag("BEGIN"), nil)
	assert.Equal(t, "BEGIN", word)

	be := &batchError{n: 2, err: errors.New("boom")}
	assert.Equal(t, "statement 2 of the batch: boom", be.Error())
	assert.Equal(t, "boom", errors.Unwrap(be).Error())

	c := &conn{cfg: Config{ConnectionName: "db"}}
	ctx := context.Background()
	assert.Equal(t, ctx, c.traceStart(ctx, nil, "SELECT 1"), "no tracer, nothing traced")
	c.traceEnd(ctx, nil, nil)
	tr := &statementTracer{connection: "db", metrics: &atomic.Pointer[observability.Metrics]{}}
	c.tracer = tr
	traced := c.traceStart(ctx, nil, "SELECT 1")
	assert.NotEqual(t, ctx, traced)
	c.traceEnd(traced, nil, nil)

	failure := c.failure(ctx, &batchError{n: 3, err: &pgconn.PgError{Code: "23505", Message: "dup"}})
	assert.True(t, strings.HasPrefix(failure.Error(), "statement 3 of the batch: "), failure.Error())
	assert.Nil(t, c.rec())
}

func TestStmtCacheBookkeeping(t *testing.T) {
	s := newStmtCache("db", nil)
	a, b := new(pgx.Conn), new(pgx.Conn)
	set, flush, stale := s.setOf(a)
	assert.False(t, flush)
	assert.Empty(t, stale)
	assert.Empty(t, s.use(set, "q1"))
	assert.Empty(t, s.use(set, "q2"))
	assert.Empty(t, s.use(set, "q1"), "a second use moves it, not duplicates it")
	assert.Equal(t, []string{"q2", "q1"}, set.order)

	for i := range stmtCacheSize {
		s.use(set, strings.Repeat("x", i+1))
	}
	assert.Len(t, set.order, stmtCacheSize)

	// A stale statement is dropped at the connection's next use.
	s.failed(&connTx{pc: a}, "q1", &pgconn.PgError{Code: staleResultType})
	s.failed(&connTx{pc: a}, "q9", errors.New("other"))
	_, _, stale = s.setOf(a)
	assert.Equal(t, []string{"q1"}, stale)

	// A schema change drops everything at each connection's next use.
	_, _, _ = s.setOf(b)
	s.invalidate()
	set, flush, _ = s.setOf(a)
	assert.True(t, flush)
	assert.Empty(t, set.order)
	s.forget(b)
	_, held := s.conns[b]
	assert.False(t, held)
	_, held = s.conns[a]
	assert.True(t, held)

	tk := &Toolkit{connections: map[string]*conn{"db": {cache: s}, "plain": {}}}
	before := s.gen.Load()
	tk.InvalidateStatements("db")
	tk.InvalidateStatements("plain")
	tk.InvalidateStatements("nope")
	assert.Equal(t, before+1, s.gen.Load())
}

// connTx is a transaction that reports which physical connection it is on.
type connTx struct {
	pgx.Tx
	pc *pgx.Conn
}

func (c *connTx) Conn() *pgx.Conn { return c.pc }

func TestSessionControlIsRefused(t *testing.T) {
	for _, sql := range []string{"DEALLOCATE ALL", "discard plans", "  BEGIN", "PREPARE TRANSACTION 'x'"} {
		require.Error(t, refuseTransactionControl(sql), sql)
	}
	require.NoError(t, refuseTransactionControl("SELECT 1"))
}

func TestResetKeepsPreparedOnlyWithTheCache(t *testing.T) {
	assert.NotNil(t, resetSession(true))
	assert.NotNil(t, resetSession(false))
	assert.NotContains(t, discardAllButPrepared, "DEALLOCATE")
	for _, step := range []string{"CLOSE ALL", "SET SESSION AUTHORIZATION DEFAULT", "RESET ALL", "UNLISTEN *", "pg_advisory_unlock_all", "DISCARD PLANS", "DISCARD TEMP", "DISCARD SEQUENCES"} {
		assert.Contains(t, discardAllButPrepared, step)
	}
}

func TestPoolWaitError(t *testing.T) {
	e := &poolWaitError{connection: "orders", maxConns: 3, wait: 2 * time.Second}
	assert.Contains(t, e.Error(), `connection "orders"`)
	assert.Contains(t, e.Error(), "all 3")
}
