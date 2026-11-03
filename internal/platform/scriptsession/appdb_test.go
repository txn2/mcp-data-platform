package scriptsession

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// fakeAppDB is a runner serving one connection, "shop".
type fakeAppDB struct {
	// ids are the identities each call carried (#2403).
	ids       []postgres.Identity
	got       []postgres.Statement
	execs     int
	result    *postgres.Result
	err       error
	batches   [][]postgres.Statement
	batchConn string
	begun     []postgres.TxOptions
	beginErr  error
	txs       []*fakeTx
}

func (*fakeAppDB) HasConnection(name string) bool { return name == "shop" }

func (f *fakeAppDB) Query(ctx context.Context, s postgres.Statement) (*postgres.Result, error) {
	f.ids = append(f.ids, postgres.IdentityOf(ctx))
	f.got = append(f.got, s)
	return f.result, f.err
}

func (f *fakeAppDB) Execute(ctx context.Context, s postgres.Statement) (*postgres.Result, error) {
	f.ids = append(f.ids, postgres.IdentityOf(ctx))
	f.execs++
	f.got = append(f.got, s)
	return f.result, f.err
}

func (f *fakeAppDB) Batch(ctx context.Context, connection string, stmts []postgres.Statement) ([]*postgres.Result, error) {
	f.ids = append(f.ids, postgres.IdentityOf(ctx))
	f.batches = append(f.batches, stmts)
	f.batchConn = connection
	if f.err != nil {
		return nil, f.err
	}
	out := make([]*postgres.Result, len(stmts))
	for i := range stmts {
		out[i] = &postgres.Result{Connection: connection, Command: "INSERT", RowsAffected: 1, Rows: [][]any{}}
	}
	return out, nil
}

func (f *fakeAppDB) Begin(ctx context.Context, o postgres.TxOptions) (AppDBTx, error) {
	f.ids = append(f.ids, postgres.IdentityOf(ctx))
	f.begun = append(f.begun, o)
	if f.beginErr != nil {
		return nil, f.beginErr
	}
	tx := &fakeTx{conn: o.Connection, db: f}
	f.txs = append(f.txs, tx)
	return tx, nil
}

// fakeTx is a transaction of fakeAppDB that records what it was asked.
type fakeTx struct {
	conn       string
	db         *fakeAppDB
	stmts      []postgres.Statement
	committed  bool
	rolledBack bool
	commitErr  error
}

func (x *fakeTx) Connection() string { return x.conn }

func (x *fakeTx) Query(_ context.Context, s postgres.Statement) (*postgres.Result, error) {
	x.stmts = append(x.stmts, s)
	return x.db.result, x.db.err
}

func (x *fakeTx) Execute(_ context.Context, s postgres.Statement) (*postgres.Result, error) {
	x.stmts = append(x.stmts, s)
	return x.db.result, x.db.err
}

func (x *fakeTx) Batch(ctx context.Context, stmts []postgres.Statement) ([]*postgres.Result, error) {
	x.stmts = append(x.stmts, stmts...)
	return x.db.Batch(ctx, x.conn, stmts)
}

func (x *fakeTx) Commit(context.Context) error {
	x.committed = true
	return x.commitErr
}

func (x *fakeTx) Rollback() { x.rolledBack = true }

// install puts db in place for one test and removes it after.
func install(t *testing.T, db AppDBRunner, authorize func(context.Context, string) error) {
	t.Helper()
	var auth func(context.Context, string) (string, error)
	if authorize != nil {
		auth = func(ctx context.Context, c string) (string, error) { return "", authorize(ctx, c) }
	}
	InstallAppDB(AppDB{Runner: func() AppDBRunner { return db }, Authorize: auth})
	t.Cleanup(func() { installed.Store(nil) })
}

func TestIsAppDB(t *testing.T) {
	installed.Store(nil)
	assert.False(t, IsAppDB("shop"), "nothing installed")
	install(t, &fakeAppDB{}, nil)
	assert.True(t, IsAppDB("shop"))
	assert.False(t, IsAppDB("warehouse"))
	assert.False(t, IsAppDB(""))
	InstallAppDB(AppDB{Runner: func() AppDBRunner { return nil }})
	assert.False(t, IsAppDB("shop"), "a deployment with no runner")
}

func TestAppDBCaller_QueryReturnsTheQueryToolShape(t *testing.T) {
	db := &fakeAppDB{result: &postgres.Result{
		Connection: "shop", Command: "SELECT", Limit: 10, Truncated: true,
		Columns: []postgres.Column{{Name: "id", Type: "integer"}, {Name: "total", Type: "numeric"}},
		Rows:    [][]any{{int64(1), "12.50"}},
	}}
	install(t, db, nil)
	c := newAppDBCaller(context.Background())
	out, err := c.call(context.Background(), AppDBQuery, map[string]any{
		"connection": "shop", "sql": "SELECT id, total FROM orders WHERE id = $1", "params": []any{int64(1)},
		"limit": 10, "timeout_seconds": int64(3),
	})
	require.NoError(t, err)
	assert.Equal(t, []any{map[string]any{"id": int64(1), "total": "12.50"}}, out["rows"])
	assert.Equal(t, []any{map[string]any{"name": "id", "type": "integer"}, map[string]any{"name": "total", "type": "numeric"}}, out["columns"])
	assert.Equal(t, map[string]any{"truncated": true, "limit": 10}, out["stats"])
	assert.Equal(t, 1, out["row_count"])
	require.Len(t, db.got, 1)
	assert.Equal(t, postgres.Statement{
		Connection: "shop", SQL: "SELECT id, total FROM orders WHERE id = $1", Params: []any{int64(1)}, Limit: 10, TimeoutSeconds: 3,
	}, db.got[0])
	assert.Zero(t, db.execs)

	_, err = c.call(context.Background(), AppDBExecute, map[string]any{"connection": "shop", "sql": "DELETE FROM carts", "limit": 2.0})
	require.NoError(t, err)
	assert.Equal(t, 1, db.execs)
	assert.Equal(t, 2, db.got[1].Limit)
}

func TestAppDBCaller_AuthorizesEachConnectionOnce(t *testing.T) {
	db := &fakeAppDB{result: &postgres.Result{}}
	calls := 0
	var seen context.Context
	type key struct{}
	install(t, db, func(ctx context.Context, conn string) error {
		calls++
		seen = ctx
		if conn != "shop" {
			return errors.New("not allowed")
		}
		return nil
	})
	sessionCtx := context.WithValue(context.Background(), key{}, "run identity")
	c := newAppDBCaller(sessionCtx)
	for range 3 {
		_, err := c.call(context.Background(), AppDBQuery, map[string]any{"connection": "shop", "sql": "SELECT 1"})
		require.NoError(t, err)
	}
	assert.Equal(t, 1, calls, "checked on first use only")
	assert.Equal(t, "run identity", seen.Value(key{}), "the session's identity is what is authorized, not the call's context")

	_, err := c.call(context.Background(), AppDBQuery, map[string]any{"connection": "other", "sql": "SELECT 1"})
	var refusal *RefusalError
	require.ErrorAs(t, err, &refusal)
	assert.Contains(t, refusal.Error(), "not allowed")
	assert.Equal(t, "connection_not_allowed", refusal.Envelope["code"])
}

func TestAppDBCaller_FailureKeepsDetailInTheEnvelopeOnly(t *testing.T) {
	const secret = "secret@example.com"
	db := &fakeAppDB{err: &postgres.FailedError{
		Code: postgres.CodeStatementFailed, Summary: "appdb: statement failed with SQLSTATE 23505 (unique_violation)",
		Statement: &postgres.StatementError{SQLState: "23505", Detail: "Key (email)=(" + secret + ") already exists.", Constraint: "c_email"},
	}}
	install(t, db, nil)
	c := newAppDBCaller(context.Background())
	_, err := c.call(context.Background(), AppDBExecute, map[string]any{"connection": "shop", "sql": "INSERT ..."})
	var refusal *RefusalError
	require.ErrorAs(t, err, &refusal)
	assert.NotContains(t, refusal.Error(), secret)
	assert.Contains(t, refusal.Error(), "23505")
	pg, ok := refusal.Envelope["postgres"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, pg["detail"], secret)
	assert.Equal(t, "c_email", pg["constraint"])
	assert.Equal(t, postgres.CodeStatementFailed, refusal.Envelope["code"])
}

func TestAppDBCaller_Refusals(t *testing.T) {
	db := &fakeAppDB{err: &postgres.RefusedError{Code: postgres.CodeReadOnly}}
	install(t, db, nil)
	c := newAppDBCaller(context.Background())

	_, err := c.call(context.Background(), AppDBExecute, map[string]any{"connection": "shop", "sql": "x"})
	var refusal *RefusalError
	require.ErrorAs(t, err, &refusal)
	assert.Equal(t, postgres.CodeReadOnly, refusal.Envelope["code"])

	_, err = c.call(context.Background(), AppDBQuery, map[string]any{"sql": "SELECT 1"})
	require.ErrorAs(t, err, &refusal)
	assert.Contains(t, refusal.Error(), "names its connection")

	db.err = errors.New("boom")
	_, err = c.call(context.Background(), AppDBQuery, map[string]any{"connection": "shop", "sql": "SELECT 1"})
	require.ErrorAs(t, err, &refusal)
	assert.Equal(t, postgres.CodeUnavailable, refusal.Envelope["code"])

	var none *appdbCaller
	_, err = none.call(context.Background(), AppDBQuery, map[string]any{"connection": "shop"})
	require.ErrorAs(t, err, &refusal)
	assert.Contains(t, refusal.Error(), "no application database")
}

func TestNewAppDBCaller_NothingInstalled(t *testing.T) {
	installed.Store(nil)
	assert.Nil(t, newAppDBCaller(context.Background()))
}

func TestIntArg(t *testing.T) {
	assert.Equal(t, 3, intArg(3))
	assert.Equal(t, 3, intArg(int64(3)))
	assert.Equal(t, 3, intArg(3.0))
	assert.Equal(t, 0, intArg("3"))
}

func TestAppDBCaller_TransactionCommits(t *testing.T) {
	db := &fakeAppDB{result: &postgres.Result{Connection: "shop", Command: "INSERT", RowsAffected: 1}}
	install(t, db, nil)
	c := newAppDBCaller(context.Background())

	out, err := c.call(context.Background(), AppDBBegin, map[string]any{
		"connection": "shop", "isolation": "serializable", "read_only": false, "attempt": 2,
	})
	require.NoError(t, err)
	assert.Equal(t, "tx1", out["transaction"], "ids are numbered per session, so a replay matches")
	require.Len(t, db.begun, 1)
	assert.Equal(t, postgres.TxOptions{Connection: "shop", Isolation: "serializable", Attempt: 2}, db.begun[0])

	_, err = c.call(context.Background(), AppDBTxExecute, map[string]any{
		"transaction": "tx1", "sql": "INSERT INTO orders VALUES ($1)", "params": []any{int64(1)},
	})
	require.NoError(t, err)
	_, err = c.call(context.Background(), AppDBTxQuery, map[string]any{"transaction": "tx1", "sql": "SELECT 1"})
	require.NoError(t, err)
	res, err := c.call(context.Background(), AppDBTxBatch, map[string]any{"transaction": "tx1", "statements": []any{
		map[string]any{"sql": "INSERT INTO items VALUES ($1)", "params": []any{"a"}},
		map[string]any{"sql": "INSERT INTO items VALUES ('b')"},
	}})
	require.NoError(t, err)
	assert.Len(t, res["results"], 2)

	tx := db.txs[0]
	require.Len(t, tx.stmts, 4)
	assert.Equal(t, "shop", tx.stmts[0].Connection, "a statement in a transaction runs on its connection")
	assert.Equal(t, []any{int64(1)}, tx.stmts[0].Params)

	out, err = c.call(context.Background(), AppDBCommit, map[string]any{"transaction": "tx1"})
	require.NoError(t, err)
	assert.Equal(t, true, out["committed"])
	assert.True(t, tx.committed)

	_, err = c.call(context.Background(), AppDBTxQuery, map[string]any{"transaction": "tx1", "sql": "SELECT 1"})
	var refusal *RefusalError
	require.ErrorAs(t, err, &refusal)
	assert.Equal(t, postgres.CodeTransactionEnded, refusal.Code)
}

func TestAppDBCaller_StatementOutsideAnOpenTransactionIsRefused(t *testing.T) {
	db := &fakeAppDB{result: &postgres.Result{Connection: "shop"}}
	install(t, db, nil)
	c := newAppDBCaller(context.Background())
	_, err := c.call(context.Background(), AppDBBegin, map[string]any{"connection": "shop"})
	require.NoError(t, err)
	for _, name := range []string{AppDBQuery, AppDBExecute} {
		_, err := c.call(context.Background(), name, map[string]any{"connection": "shop", "sql": "SELECT 1"})
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), "tx.query", name)
	}
	_, err = c.call(context.Background(), AppDBBatch, map[string]any{"connection": "shop", "statements": []any{map[string]any{"sql": "SELECT 1"}}})
	require.Error(t, err)
	_, err = c.call(context.Background(), AppDBRollback, map[string]any{"transaction": "tx1"})
	require.NoError(t, err)
	assert.True(t, db.txs[0].rolledBack)
	_, err = c.call(context.Background(), AppDBQuery, map[string]any{"connection": "shop", "sql": "SELECT 1"})
	require.NoError(t, err, "the statement runs once the transaction ended")
}

func TestAppDBCaller_CloseRollsBackOpenTransactions(t *testing.T) {
	db := &fakeAppDB{}
	install(t, db, nil)
	c := newAppDBCaller(context.Background())
	for _, conn := range []string{"shop", "ledger"} {
		_, err := c.call(context.Background(), AppDBBegin, map[string]any{"connection": conn})
		require.NoError(t, err)
	}
	_, err := c.call(context.Background(), AppDBBegin, map[string]any{"connection": "shop"})
	require.ErrorContains(t, err, "a transaction is open", "a second transaction on one connection")
	c.close()
	for _, tx := range db.txs {
		assert.True(t, tx.rolledBack)
	}
	var nilCaller *appdbCaller
	nilCaller.close()
}

func TestAppDBCaller_BeginIsAuthorizedAndRefusals(t *testing.T) {
	db := &fakeAppDB{}
	install(t, db, func(_ context.Context, connection string) error {
		if connection == "shop" {
			return errors.New("not for you")
		}
		return nil
	})
	c := newAppDBCaller(context.Background())
	_, err := c.call(context.Background(), AppDBBegin, map[string]any{"connection": "shop"})
	require.ErrorContains(t, err, "not for you")
	assert.Empty(t, db.begun)

	_, err = c.call(context.Background(), AppDBBegin, map[string]any{})
	require.ErrorContains(t, err, "names its connection")
	_, err = c.call(context.Background(), AppDBCommit, map[string]any{"transaction": "tx9"})
	require.ErrorContains(t, err, `no transaction "tx9"`)

	db.beginErr = &postgres.RefusedError{Code: postgres.CodeReadOnly}
	install(t, db, nil)
	c = newAppDBCaller(context.Background())
	_, err = c.call(context.Background(), AppDBBegin, map[string]any{"connection": "shop"})
	var refusal *RefusalError
	require.ErrorAs(t, err, &refusal)
	assert.Equal(t, postgres.CodeReadOnly, refusal.Code)
}

func TestAppDBCaller_Batch(t *testing.T) {
	db := &fakeAppDB{}
	install(t, db, nil)
	c := newAppDBCaller(context.Background())
	out, err := c.call(context.Background(), AppDBBatch, map[string]any{"connection": "shop", "statements": []any{
		map[string]any{"sql": "INSERT INTO t VALUES ($1)", "params": []any{"x"}},
	}})
	require.NoError(t, err)
	assert.Equal(t, "shop", db.batchConn)
	results, _ := out["results"].([]any)
	require.Len(t, results, 1)
	first, _ := results[0].(map[string]any)
	assert.Equal(t, "INSERT", first["command"])

	for name, args := range map[string]map[string]any{
		"no connection": {"statements": []any{map[string]any{"sql": "SELECT 1"}}},
		"no statements": {"connection": "shop"},
		"not an object": {"connection": "shop", "statements": []any{"SELECT 1"}},
	} {
		_, err := c.call(context.Background(), AppDBBatch, args)
		require.Error(t, err, name)
	}
	db.err = &postgres.FailedError{Code: postgres.CodeStatementFailed, Summary: "statement 2 of the batch: failed"}
	_, err = c.call(context.Background(), AppDBBatch, map[string]any{"connection": "shop", "statements": []any{map[string]any{"sql": "SELECT 1"}}})
	require.ErrorContains(t, err, "statement 2 of the batch")
}

func TestAppDBCaller_CommitFailureIsReported(t *testing.T) {
	db := &fakeAppDB{}
	install(t, db, nil)
	c := newAppDBCaller(context.Background())
	_, err := c.call(context.Background(), AppDBBegin, map[string]any{"connection": "shop"})
	require.NoError(t, err)
	db.txs[0].commitErr = &postgres.FailedError{
		Code: postgres.CodeStatementFailed, Summary: "serialization failure",
		Statement: &postgres.StatementError{SQLState: "40001"},
	}
	_, err = c.call(context.Background(), AppDBCommit, map[string]any{"transaction": "tx1"})
	var refusal *RefusalError
	require.ErrorAs(t, err, &refusal)
	pg, _ := refusal.Envelope["postgres"].(map[string]any)
	assert.Equal(t, "40001", pg["sqlstate"])

	_, err = c.call(context.Background(), AppDBTxExecute, map[string]any{"transaction": "tx1", "sql": "x"})
	require.Error(t, err, "a committed or failed transaction is gone from the session")
}

func TestIsAppDBCall(t *testing.T) {
	for _, name := range []string{AppDBQuery, AppDBExecute, AppDBBatch, AppDBBegin, AppDBTxQuery, AppDBTxExecute, AppDBTxBatch, AppDBCommit, AppDBRollback} {
		assert.True(t, IsAppDBCall(name), name)
	}
	assert.False(t, IsAppDBCall("trino_query"))
}

// Every call carries who it runs for (#2403): the persona the connection was
// allowed as, and the endpoint caller's claims, or the author's outside an
// endpoint, never the script principal.
func TestAppDBCaller_Identity(t *testing.T) {
	db := &fakeAppDB{result: &postgres.Result{Connection: "shop", Rows: [][]any{}}}
	InstallAppDB(AppDB{Runner: func() AppDBRunner { return db }, Authorize: func(context.Context, string) (string, error) {
		return "clerk", nil
	}})
	t.Cleanup(func() { installed.Store(nil) })
	run := middleware.WithPreAuthenticatedUser(context.Background(), &middleware.UserInfo{
		UserID: "script:orders", Email: "owner@example.com", OnBehalfOf: "author@example.com", OnBehalfOfSub: "sub-author",
	})
	stmt := map[string]any{"connection": "shop", "sql": "SELECT 1"}
	batch := map[string]any{"connection": "shop", "statements": []any{map[string]any{"sql": "SELECT 1"}}}

	c := newAppDBCaller(run)
	_, err := c.call(context.Background(), AppDBQuery, stmt)
	require.NoError(t, err)
	_, err = c.call(context.Background(), AppDBExecute, stmt)
	require.NoError(t, err)
	_, err = c.call(context.Background(), AppDBBatch, batch)
	require.NoError(t, err)
	_, err = c.call(context.Background(), AppDBBegin, map[string]any{"connection": "shop"})
	require.NoError(t, err)
	author := postgres.Identity{Persona: "clerk", UserID: "sub-author", Email: "author@example.com", Source: postgres.SourceScript}
	assert.Equal(t, []postgres.Identity{author, author, author, author}, db.ids)

	db.ids = nil
	signedIn := newAppDBCaller(WithEndpoint(run, EndpointCaller{UserID: "sub-caller", Email: "caller@example.com"}))
	_, err = signedIn.call(context.Background(), AppDBQuery, stmt)
	require.NoError(t, err)
	public := newAppDBCaller(WithEndpoint(run, EndpointCaller{}))
	_, err = public.call(context.Background(), AppDBQuery, stmt)
	require.NoError(t, err)
	assert.Equal(t, []postgres.Identity{
		{Persona: "clerk", UserID: "sub-caller", Email: "caller@example.com", Source: postgres.SourceEndpoint},
		{Persona: "clerk", Source: postgres.SourceEndpoint},
	}, db.ids, "an endpoint's statements claim its caller, and a public endpoint's claim nobody")

	db.ids = nil
	draft := middleware.WithPreAuthenticatedUser(context.Background(), &middleware.UserInfo{UserID: "sub-drafter", Email: "drafter@example.com"})
	_, err = newAppDBCaller(draft).call(context.Background(), AppDBQuery, stmt)
	require.NoError(t, err)
	assert.Equal(t, []postgres.Identity{{Persona: "clerk", UserID: "sub-drafter", Email: "drafter@example.com", Source: postgres.SourceScript}},
		db.ids, "a draft acts as the person running it")

	db.ids = nil
	_, err = newAppDBCaller(context.Background()).call(context.Background(), AppDBQuery, stmt)
	require.NoError(t, err)
	assert.Equal(t, []postgres.Identity{{Persona: "clerk"}}, db.ids, "a session with no identity claims nothing")
}
