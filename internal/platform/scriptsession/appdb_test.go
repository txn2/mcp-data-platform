package scriptsession

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// fakeAppDB is a runner serving one connection, "shop".
type fakeAppDB struct {
	got    []postgres.Statement
	execs  int
	result *postgres.Result
	err    error
}

func (*fakeAppDB) HasConnection(name string) bool { return name == "shop" }

func (f *fakeAppDB) Query(_ context.Context, s postgres.Statement) (*postgres.Result, error) {
	f.got = append(f.got, s)
	return f.result, f.err
}

func (f *fakeAppDB) Execute(_ context.Context, s postgres.Statement) (*postgres.Result, error) {
	f.execs++
	f.got = append(f.got, s)
	return f.result, f.err
}

// install puts db in place for one test and removes it after.
func install(t *testing.T, db AppDBRunner, authorize func(context.Context, string) error) {
	t.Helper()
	InstallAppDB(AppDB{Runner: func() AppDBRunner { return db }, Authorize: authorize})
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
