package scriptsession

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

func TestTestDatabases_AnswerFromTheCloneTheTestOpened(t *testing.T) {
	clone := &fakeAppDB{result: &postgres.Result{Connection: "shop", Command: "INSERT", RowsAffected: 1, Rows: [][]any{}}}
	var opened, dropped []string
	var authorized []string
	InstallAppDB(AppDB{
		Runner:    func() AppDBRunner { return &fakeAppDB{err: errors.New("the real database is never reached")} },
		Authorize: func(_ context.Context, c string) (string, error) { authorized = append(authorized, c); return "", nil },
		TestDatabase: func(_ context.Context, connection string, _ []string) (AppDBRunner, func(), error) {
			opened = append(opened, connection)
			return clone, func() { dropped = append(dropped, connection) }, nil
		},
	})
	t.Cleanup(func() { installed.Store(nil) })

	ctx := context.Background()
	dbs := NewTestDatabases(ctx)
	require.NotNil(t, dbs)
	assert.False(t, dbs.Active())
	require.NoError(t, dbs.Open("shop", []string{"app"}))
	require.NoError(t, dbs.Open("shop", []string{"app"}), "opening twice builds one database")
	assert.ErrorContains(t, dbs.Open("shop", nil), "a test builds one per connection")
	assert.True(t, dbs.Active())
	assert.Equal(t, []string{"shop"}, opened)
	assert.Equal(t, []string{"shop"}, authorized)

	out, err := dbs.Call(ctx, AppDBExecute, map[string]any{"connection": "shop", "sql": "INSERT INTO users (email) VALUES ($1)", "params": []any{"a@x"}})
	require.NoError(t, err)
	assert.NotNil(t, out)
	require.Len(t, clone.got, 1)
	assert.Equal(t, "INSERT INTO users (email) VALUES ($1)", clone.got[0].SQL)

	tx, err := dbs.Call(ctx, AppDBBegin, map[string]any{"connection": "shop"})
	require.NoError(t, err)
	_, err = dbs.Call(ctx, AppDBTxExecute, map[string]any{"transaction": tx["transaction"], "sql": "DELETE FROM users"})
	require.NoError(t, err)
	_, err = dbs.Call(ctx, AppDBBatch, map[string]any{"connection": "shop", "statements": []any{map[string]any{"sql": "SELECT 1"}}})
	require.Error(t, err, "a statement outside the open transaction is refused as in a run")

	_, err = dbs.Call(ctx, AppDBQuery, map[string]any{"connection": "warehouse", "sql": "SELECT 1"})
	var refusal *RefusalError
	require.ErrorAs(t, err, &refusal)
	assert.Contains(t, refusal.Text, `testing.database("warehouse")`)
	_, err = dbs.Call(ctx, AppDBBatch, map[string]any{"connection": "warehouse", "statements": []any{map[string]any{"sql": "SELECT 1"}}})
	require.ErrorAs(t, err, &refusal)
	_, err = dbs.Call(ctx, AppDBBegin, map[string]any{"connection": "warehouse"})
	require.ErrorAs(t, err, &refusal)
	_, err = dbs.Call(ctx, AppDBExecute, map[string]any{"connection": "warehouse", "sql": "SELECT 1"})
	require.ErrorAs(t, err, &refusal)

	dbs.Close()
	assert.Equal(t, []string{"shop"}, dropped)
	assert.True(t, clone.txs[0].rolledBack, "an open transaction is rolled back before the clone is dropped")
	assert.False(t, dbs.Active())
}

func TestTestDatabases_Refusals(t *testing.T) {
	installed.Store(nil)
	assert.Nil(t, NewTestDatabases(context.Background()))
	var none *TestDatabases
	assert.False(t, none.Active())
	assert.ErrorContains(t, none.Open("shop", nil), "no application database")
	none.Close()

	InstallAppDB(AppDB{
		Runner:    func() AppDBRunner { return &fakeAppDB{} },
		Authorize: func(context.Context, string) (string, error) { return "", errors.New("not for this persona") },
		TestDatabase: func(context.Context, string, []string) (AppDBRunner, func(), error) {
			return nil, nil, errors.New("user lacks the CREATEDB privilege")
		},
	})
	t.Cleanup(func() { installed.Store(nil) })
	dbs := NewTestDatabases(context.Background())
	assert.ErrorContains(t, dbs.Open("shop", nil), "not for this persona")

	InstallAppDB(AppDB{
		Runner: func() AppDBRunner { return &fakeAppDB{} },
		TestDatabase: func(context.Context, string, []string) (AppDBRunner, func(), error) {
			return nil, nil, errors.New("user lacks the CREATEDB privilege")
		},
	})
	dbs = NewTestDatabases(context.Background())
	err := dbs.Open("shop", nil)
	assert.ErrorContains(t, err, `building a test database for "shop"`)
	assert.ErrorContains(t, err, "CREATEDB")
	assert.False(t, dbs.Active())
}
