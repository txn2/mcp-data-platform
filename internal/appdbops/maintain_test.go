package appdbops

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeMaint struct {
	stmts        []string
	lock, budget time.Duration
	err          error
}

func (f *fakeMaint) RunMaintenance(_ context.Context, _, stmt string, lockTimeout, timeout time.Duration) error {
	f.stmts, f.lock, f.budget = append(f.stmts, stmt), lockTimeout, timeout
	return f.err
}

func TestStatement(t *testing.T) {
	for _, tc := range []struct{ command, table, want string }{
		{CommandAnalyze, "", "ANALYZE"},
		{CommandAnalyze, "orders", `ANALYZE "orders"`},
		{CommandVacuum, "", "VACUUM (ANALYZE)"},
		{CommandVacuum, "app.orders", `VACUUM (ANALYZE) "app"."orders"`},
		{CommandVacuumFull, "orders", `VACUUM (FULL, ANALYZE) "orders"`},
		{CommandReindex, "orders", `REINDEX TABLE CONCURRENTLY "orders"`},
	} {
		got, err := Statement(tc.command, tc.table)
		require.NoError(t, err, tc.command)
		assert.Equal(t, tc.want, got)
	}
	for _, bad := range []string{`orders"; DROP TABLE x; --`, "a.b.c", "1abc", "orders "} {
		_, err := Statement(CommandAnalyze, bad)
		require.Error(t, err, bad)
	}
	_, err := Statement(CommandVacuumFull, "")
	require.ErrorIs(t, err, ErrTableRequired)
	_, err = Statement(CommandReindex, "")
	require.ErrorIs(t, err, ErrTableRequired)
	for _, schemaChange := range []string{"create_index", "drop_index", "cluster", ""} {
		_, err = Statement(schemaChange, "orders")
		require.ErrorIs(t, err, ErrSchemaChange, schemaChange)
	}
	assert.Contains(t, ErrSchemaChange.Error(), "manage_schema")
}

func TestMaintain(t *testing.T) {
	f := &fakeMaint{}
	out, err := Maintain(context.Background(), f, Request{Connection: "shop", Command: CommandAnalyze, Table: "orders", Timeout: time.Minute})
	require.NoError(t, err)
	assert.Equal(t, `ANALYZE "orders"`, out.Statement)
	assert.Equal(t, DefaultLockTimeout, f.lock)
	assert.Equal(t, time.Minute, f.budget)

	_, err = Maintain(context.Background(), f, Request{Connection: "shop", Command: "create_index", Table: "orders"})
	require.ErrorIs(t, err, ErrSchemaChange)
	assert.Len(t, f.stmts, 1, "a refused command runs nothing")

	f.err = errors.New("55P03")
	_, err = Maintain(context.Background(), f, Request{Connection: "shop", Command: CommandVacuum, LockTimeout: time.Second})
	require.Error(t, err)
	assert.Equal(t, time.Second, f.lock)
	assert.Equal(t, []string{CommandAnalyze, CommandVacuum, CommandVacuumFull, CommandReindex}, Commands)
}
