package scriptsession

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/calltransform"
	"github.com/txn2/mcp-data-platform/internal/platform/callhandler"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// asked records what the handlers were asked, refuses SQL naming "secret",
// and covers SQL naming "masked" with a transform hiding column n.
type asked struct {
	got []string
}

func (a *asked) Statement(_ context.Context, st callhandler.Statement) (calltransform.Transformer, error) {
	a.got = append(a.got, strings.Join([]string{st.User, st.Persona, st.Connection, st.Tool, st.SQL}, "|"))
	switch {
	case strings.Contains(st.SQL, "secret"):
		return nil, errors.New("POLICY_DENIED: refused by the guard")
	case strings.Contains(st.SQL, "masked"):
		return masking{}, nil
	}
	return nil, nil //nolint:nilnil // the contract: nothing covers the statement
}

type masking struct{ err error }

func (m masking) Rows(_ context.Context, _ []string, rows []map[string]any) ([]map[string]any, error) {
	for _, r := range rows {
		if _, ok := r["n"]; ok {
			r["n"] = "***"
		}
	}
	return rows, m.err
}

func (masking) Body(_ context.Context, body any) (any, error) { return body, nil }

func installHandlers(t *testing.T, h AppDBHandlers) {
	t.Helper()
	InstallAppDBHandlers(h)
	t.Cleanup(func() { InstallAppDBHandlers(nil) })
}

// TestAppDBHandlers_EveryStatementPathIsAsked: a statement reaches the
// handlers on every path a script has to an application database (a query, a
// statement in a transaction, each statement of a batch, a cursor, COPY in
// and out, maintenance), under the operation the same statement has outside
// a transaction, as the run's user and persona.
func TestAppDBHandlers_EveryStatementPathIsAsked(t *testing.T) {
	db := &fakeAppDB{result: &postgres.Result{Connection: "shop", Columns: []postgres.Column{{Name: "n"}}, Rows: [][]any{{int64(1)}}}}
	installAs(t, db, "admin")
	h := &asked{}
	installHandlers(t, h)
	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{UserEmail: "jo@example.com"})
	c := newAppDBCaller(ctx)

	calls := []struct {
		name string
		args map[string]any
	}{
		{AppDBQuery, map[string]any{"connection": "shop", "sql": "SELECT 1"}},
		{AppDBExecute, map[string]any{"connection": "shop", "sql": "UPDATE t SET a = 1"}},
		{AppDBBatch, map[string]any{"connection": "shop", "statements": []any{map[string]any{"sql": "INSERT a"}, map[string]any{"sql": "INSERT b"}}}},
		{AppDBCursorOpen, map[string]any{"connection": "shop", "sql": "SELECT c"}},
		{AppDBCopyIn, map[string]any{"connection": "shop", "table": "items", "columns": []any{"sku"}, "rows": []any{[]any{"a"}}}},
		{AppDBCopyOut, map[string]any{"connection": "shop", "sql": "SELECT o"}},
		{AppDBMaintain, map[string]any{"connection": "shop", "command": "analyze", "table": "items"}},
		{AppDBBegin, map[string]any{"connection": "shop"}},
		{AppDBTxQuery, map[string]any{"transaction": "tx1", "sql": "SELECT tq"}},
		{AppDBTxExecute, map[string]any{"transaction": "tx1", "sql": "DELETE tx"}},
		{AppDBTxBatch, map[string]any{"transaction": "tx1", "statements": []any{map[string]any{"sql": "INSERT tb"}}}},
		{AppDBCursorOpen, map[string]any{"transaction": "tx1", "sql": "SELECT tc"}},
		{AppDBCopyIn, map[string]any{"transaction": "tx1", "table": "items", "rows": []any{[]any{"b"}}}},
	}
	for _, k := range calls {
		_, err := c.call(ctx, k.name, k.args)
		require.NoError(t, err, k.name)
	}
	pre := "jo@example.com|admin|shop|"
	assert.Equal(t, []string{
		pre + "appdb.query|SELECT 1",
		pre + "appdb.execute|UPDATE t SET a = 1",
		pre + "appdb.batch|INSERT a",
		pre + "appdb.batch|INSERT b",
		pre + "appdb.query|SELECT c",
		pre + "appdb.copy_in|COPY items (sku) FROM STDIN",
		pre + "appdb.copy_out|SELECT o",
		pre + "appdb.maintain|ANALYZE items",
		pre + "appdb.query|SELECT tq",
		pre + "appdb.execute|DELETE tx",
		pre + "appdb.batch|INSERT tb",
		pre + "appdb.query|SELECT tc",
		pre + "appdb.copy_in|COPY items FROM STDIN",
	}, h.got)
}

// TestAppDBHandlers_ARefusalRunsNothing: a statement the policies refuse is
// refused on every path, and nothing reaches the database.
func TestAppDBHandlers_ARefusalRunsNothing(t *testing.T) {
	db := &fakeAppDB{result: &postgres.Result{Connection: "shop"}}
	installAs(t, db, "admin")
	installHandlers(t, &asked{})
	ctx := context.Background()
	c := newAppDBCaller(ctx)
	_, err := c.call(ctx, AppDBBegin, map[string]any{"connection": "shop"})
	require.NoError(t, err)
	tx := c.txs["tx1"]
	_, err = c.call(ctx, AppDBTxQuery, map[string]any{"transaction": "tx1", "sql": "SELECT secret"})
	require.ErrorContains(t, err, "POLICY_DENIED")
	_, err = c.call(ctx, AppDBTxBatch, map[string]any{"transaction": "tx1", "statements": []any{map[string]any{"sql": "SELECT 1"}, map[string]any{"sql": "SELECT secret"}}})
	require.ErrorContains(t, err, "POLICY_DENIED", "one refused statement refuses the batch before any runs")
	_, err = c.call(ctx, AppDBCursorOpen, map[string]any{"transaction": "tx1", "sql": "SELECT secret"})
	require.Error(t, err)
	_, err = c.call(ctx, AppDBCopyIn, map[string]any{"transaction": "tx1", "table": "secret", "rows": []any{[]any{"a"}}})
	require.Error(t, err)
	ftx, ok := tx.(*fakeTx)
	require.True(t, ok)
	assert.Empty(t, ftx.stmts, "nothing ran in the transaction")
	_, err = c.call(ctx, AppDBRollback, map[string]any{"transaction": "tx1"})
	require.NoError(t, err)

	for name, args := range map[string]map[string]any{
		AppDBBatch:      {"connection": "shop", "statements": []any{map[string]any{"sql": "SELECT secret"}}},
		AppDBCursorOpen: {"connection": "shop", "sql": "SELECT secret"},
		AppDBCopyIn:     {"connection": "shop", "table": "secret", "rows": []any{[]any{"a"}}},
		AppDBCopyOut:    {"connection": "shop", "sql": "SELECT secret"},
		AppDBMaintain:   {"connection": "shop", "command": "vacuum", "table": "secret"},
	} {
		_, err := c.call(ctx, name, args)
		require.ErrorContains(t, err, "POLICY_DENIED", name)
	}
	assert.Empty(t, db.batches)
	assert.Empty(t, db.cursors)
	assert.Empty(t, db.copied)
	assert.Empty(t, db.maintained)
}

// TestAppDBHandlers_ATransformChangesEveryRowSet: a covered run's rows pass
// through the transform on a query, in a transaction, in each batch result
// and on every page a cursor reads; COPY OUT, whose bytes no transform reads,
// is refused.
func TestAppDBHandlers_ATransformChangesEveryRowSet(t *testing.T) {
	db := &fakeAppDB{result: &postgres.Result{Connection: "shop", Columns: []postgres.Column{{Name: "n"}}, Rows: [][]any{{int64(7)}}}}
	installAs(t, db, "admin")
	installHandlers(t, &asked{})
	ctx := context.Background()
	c := newAppDBCaller(ctx)
	masked := []any{map[string]any{"n": "***"}}

	out, err := c.call(ctx, AppDBQuery, map[string]any{"connection": "shop", "sql": "SELECT masked"})
	require.NoError(t, err)
	assert.Equal(t, masked, out["rows"])

	out, err = c.call(ctx, AppDBCursorOpen, map[string]any{"connection": "shop", "sql": "SELECT n FROM masked"})
	require.NoError(t, err)
	for range 2 {
		page, err := c.call(ctx, AppDBCursorFetch, map[string]any{"cursor": out["cursor"]})
		require.NoError(t, err)
		assert.Equal(t, masked, page["rows"], "every page")
	}
	_, err = c.call(ctx, AppDBCursorClose, map[string]any{"cursor": out["cursor"]})
	require.NoError(t, err)

	_, err = c.call(ctx, AppDBCopyOut, map[string]any{"connection": "shop", "sql": "SELECT n FROM masked"})
	require.ErrorContains(t, err, "platform.query or query_iter")

	_, err = c.call(ctx, AppDBBegin, map[string]any{"connection": "shop"})
	require.NoError(t, err)
	out, err = c.call(ctx, AppDBTxQuery, map[string]any{"transaction": "tx1", "sql": "SELECT masked"})
	require.NoError(t, err)
	assert.Equal(t, masked, out["rows"])

	results := map[string]any{"results": []any{
		map[string]any{"rows": []any{map[string]any{"n": 1}}, "columns": []any{map[string]any{"name": "n"}}},
		map[string]any{"rows": []any{map[string]any{"n": 2}}},
		"not a result",
	}}
	got, err := transformResults(ctx, []calltransform.Transformer{masking{}, nil}, results)
	require.NoError(t, err)
	list, ok := got["results"].([]any)
	require.True(t, ok)
	first, ok := list[0].(map[string]any)
	require.True(t, ok)
	second, ok := list[1].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, masked, first["rows"])
	assert.Equal(t, []any{map[string]any{"n": 2}}, second["rows"], "a statement no transform covers is left alone")
	_, err = transformResults(ctx, []calltransform.Transformer{masking{err: errors.New("timed out")}}, map[string]any{"results": []any{map[string]any{}}})
	require.ErrorContains(t, err, "timed out")
}

// TestAppDBHandlers_AHandlersOwnRunIsNotAsked and a run with none installed.
func TestAppDBHandlers_AHandlersOwnRunIsNotAsked(t *testing.T) {
	db := &fakeAppDB{result: &postgres.Result{Connection: "shop"}}
	installAs(t, db, "admin")
	h := &asked{}
	installHandlers(t, h)
	ctx := callhandler.WithinHandler(context.Background())
	_, err := newAppDBCaller(ctx).call(ctx, AppDBQuery, map[string]any{"connection": "shop", "sql": "SELECT secret"})
	require.NoError(t, err)
	assert.Empty(t, h.got)
	InstallAppDBHandlers(nil)
	_, err = newAppDBCaller(context.Background()).call(context.Background(), AppDBQuery, map[string]any{"connection": "shop", "sql": "SELECT secret"})
	require.NoError(t, err)
}

// TestRunUser is who a run's statements act for.
func TestRunUser(t *testing.T) {
	assert.Empty(t, runUser(context.Background()))
	pc := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{UserEmail: "pc@example.com"})
	assert.Equal(t, "pc@example.com", runUser(pc))
	pre := middleware.WithPreAuthenticatedUser(pc, &middleware.UserInfo{Email: "author@example.com"})
	assert.Equal(t, "author@example.com", runUser(pre))
	behalf := middleware.WithPreAuthenticatedUser(pc, &middleware.UserInfo{Email: "script:x", OnBehalfOf: "owner@example.com"})
	assert.Equal(t, "owner@example.com", runUser(behalf))
}
