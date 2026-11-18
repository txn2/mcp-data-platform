package integration

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mockStore(t *testing.T) (*Store, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return &Store{DB: db}, mock
}

var bindingCols = []string{
	"id", "operation", "script_id", "interval_seconds", "current_run_id", "retry_requested",
	"last_started_at", "last_run_id", "last_status", "last_reason", "last_error", "last_finished_at",
	"created_by", "updated_by", "created_at", "updated_at",
}

func bindingRow(op string) *sqlmock.Rows {
	now := time.Now()
	return sqlmock.NewRows(bindingCols).AddRow("b1", op, "s1", 900, "", true, nil, "", "", "", "", nil, "a", "a", now, now)
}

func TestStore_Bindings(t *testing.T) {
	s, mock := mockStore(t)
	ctx := context.Background()

	mock.ExpectQuery("FROM integration_bindings WHERE operation").WillReturnRows(bindingRow(OpSiteRouting))
	b, err := s.Binding(ctx, OpSiteRouting)
	require.NoError(t, err)
	assert.Equal(t, "s1", b.ScriptID)

	mock.ExpectQuery("FROM integration_bindings WHERE operation").WillReturnError(sql.ErrNoRows)
	b, err = s.Binding(ctx, OpSiteRouting)
	require.NoError(t, err)
	assert.Nil(t, b)

	mock.ExpectQuery("FROM integration_bindings WHERE operation").WillReturnError(errors.New("down"))
	_, err = s.Binding(ctx, OpSiteRouting)
	require.ErrorContains(t, err, "down")

	mock.ExpectQuery("INSERT INTO integration_bindings").WithArgs(OpSiteRouting, "s1", 900, "ops@example.com").WillReturnRows(bindingRow(OpSiteRouting))
	mock.ExpectExec("pg_notify").WillReturnResult(sqlmock.NewResult(0, 1))
	_, err = s.Bind(ctx, OpSiteRouting, "s1", 900, "ops@example.com")
	require.NoError(t, err)
	mock.ExpectQuery("INSERT INTO integration_bindings").WillReturnError(errors.New("fk"))
	_, err = s.Bind(ctx, OpSiteRouting, "s1", 900, "x")
	require.ErrorContains(t, err, "choosing")

	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM integration_bindings").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM integration_items").WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectCommit()
	cleared, err := s.Clear(ctx, OpSiteRouting)
	require.NoError(t, err)
	assert.True(t, cleared)
	mock.ExpectBegin().WillReturnError(errors.New("down"))
	_, err = s.Clear(ctx, OpSiteRouting)
	require.Error(t, err)
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM integration_bindings").WillReturnError(errors.New("down"))
	mock.ExpectRollback()
	_, err = s.Clear(ctx, OpSiteRouting)
	require.Error(t, err)
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM integration_bindings").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM integration_items").WillReturnError(errors.New("down"))
	mock.ExpectRollback()
	_, err = s.Clear(ctx, OpSiteRouting)
	require.Error(t, err)

	mock.ExpectExec("UPDATE integration_bindings SET retry_requested").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("pg_notify").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.RequestRetry(ctx, OpSiteRouting))
	mock.ExpectExec("UPDATE integration_bindings SET retry_requested").WillReturnResult(sqlmock.NewResult(0, 0))
	require.ErrorIs(t, s.RequestRetry(ctx, OpSiteRouting), ErrNotBound)
	mock.ExpectExec("UPDATE integration_bindings SET retry_requested").WillReturnError(errors.New("down"))
	require.Error(t, s.RequestRetry(ctx, OpSiteRouting))

	mock.ExpectExec("UPDATE integration_bindings SET retry_requested = FALSE, last_started_at").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.RecordRefusal(ctx, OpSiteRouting, "no version"))
	mock.ExpectExec("UPDATE integration_bindings SET retry_requested = FALSE, last_started_at").WillReturnError(errors.New("down"))
	require.Error(t, s.RecordRefusal(ctx, OpSiteRouting, "no version"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestStore_Items(t *testing.T) {
	s, mock := mockStore(t)
	ctx := context.Background()
	cols := []string{"item_key", "attrs", "status", "message", "removing", "failed_run", "checked_at"}
	mock.ExpectQuery("FROM integration_items WHERE operation").WillReturnRows(sqlmock.NewRows(cols).
		AddRow("a.example.com", []byte(`{"hostname":"a.example.com"}`), "ready", "ok", false, "", time.Now()))
	items, err := s.Items(ctx, OpSiteRouting)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "a.example.com", items[0].Entry["hostname"])

	mock.ExpectQuery("FROM integration_items WHERE operation").WillReturnRows(sqlmock.NewRows(cols).
		AddRow("a", []byte(`{`), "ready", "", false, "", nil))
	_, err = s.Items(ctx, OpSiteRouting)
	require.Error(t, err)
	mock.ExpectQuery("FROM integration_items WHERE operation").WillReturnError(errors.New("down"))
	_, err = s.Items(ctx, OpSiteRouting)
	require.Error(t, err)

	byKey, err := s.ItemsByKey(ctx, OpSiteRouting, nil)
	require.NoError(t, err)
	assert.Empty(t, byKey)
	mock.ExpectQuery("item_key = ANY").WillReturnRows(sqlmock.NewRows([]string{"item_key", "status", "message", "failed_run", "checked_at"}).
		AddRow("a.example.com", "failed", "The site routing script failed", "run1", time.Now()))
	byKey, err = s.ItemsByKey(ctx, OpSiteRouting, []string{"a.example.com"})
	require.NoError(t, err)
	assert.Equal(t, "run1", byKey["a.example.com"].FailedRun)
	mock.ExpectQuery("item_key = ANY").WillReturnError(errors.New("down"))
	_, err = s.ItemsByKey(ctx, OpSiteRouting, []string{"a"})
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestStore_Runs(t *testing.T) {
	s, mock := mockStore(t)
	ctx := context.Background()
	r := NewRun{
		ID: "run1", ScriptID: "s1", VersionID: "v1", Version: 7, Approver: "ops@example.com",
		Call: map[string]any{"integration": OpSiteRouting}, Reason: ReasonChange,
		Added: []Item{host("a.example.com")}, Removed: []string{"c.example.com"},
	}
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO integration_items").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("SET removing = TRUE").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO script_runs").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("SET current_run_id").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectExec("pg_notify").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.StartRun(ctx, OpSiteRouting, r))

	for i, fail := range []string{"INSERT INTO integration_items", "SET removing = TRUE", "INSERT INTO script_runs", "SET current_run_id"} {
		mock.ExpectBegin()
		steps := []string{"INSERT INTO integration_items", "SET removing = TRUE", "INSERT INTO script_runs", "SET current_run_id"}
		for _, step := range steps[:i] {
			mock.ExpectExec(step).WillReturnResult(sqlmock.NewResult(0, 1))
		}
		mock.ExpectExec(fail).WillReturnError(errors.New("down"))
		mock.ExpectRollback()
		require.Error(t, s.StartRun(ctx, OpSiteRouting, r), fail)
	}
	mock.ExpectBegin().WillReturnError(errors.New("down"))
	require.Error(t, s.StartRun(ctx, OpSiteRouting, r))

	runCols := []string{"status", "error", "result", "call", "finished_at"}
	mock.ExpectQuery("FROM script_runs WHERE id").WillReturnRows(sqlmock.NewRows(runCols).
		AddRow("succeeded", "", []byte(`{}`), []byte(`{"integration":"site_routing"}`), time.Now()))
	out, err := s.RunOutcome(ctx, "run1")
	require.NoError(t, err)
	assert.True(t, out.Finished)
	assert.Equal(t, OpSiteRouting, out.Call["integration"])
	mock.ExpectQuery("FROM script_runs WHERE id").WillReturnError(sql.ErrNoRows)
	out, err = s.RunOutcome(ctx, "gone")
	require.NoError(t, err)
	assert.True(t, out.Finished, "a run that is gone ended")
	mock.ExpectQuery("FROM script_runs WHERE id").WillReturnError(errors.New("down"))
	_, err = s.RunOutcome(ctx, "x")
	require.Error(t, err)

	a := Applied{
		RunID: "run1", Results: map[string]ItemResult{"a.example.com": {Status: StatusReady}},
		Failed: []string{"b.example.com"}, FailWith: "The site routing script failed", Removed: []string{"c.example.com"}, Status: "succeeded",
	}
	steps := []string{"FOR UPDATE", "SET status = \\$3", "SET status = 'failed'", "DELETE FROM integration_items", "SET current_run_id = NULL"}
	mock.ExpectBegin()
	for _, step := range steps {
		mock.ExpectExec(step).WillReturnResult(sqlmock.NewResult(0, 1))
	}
	mock.ExpectCommit()
	require.NoError(t, s.Apply(ctx, OpSiteRouting, a))
	for i, fail := range steps {
		mock.ExpectBegin()
		for _, step := range steps[:i] {
			mock.ExpectExec(step).WillReturnResult(sqlmock.NewResult(0, 1))
		}
		mock.ExpectExec(fail).WillReturnError(errors.New("down"))
		mock.ExpectRollback()
		require.Error(t, s.Apply(ctx, OpSiteRouting, a), fail)
	}
	mock.ExpectBegin().WillReturnError(errors.New("down"))
	require.Error(t, s.Apply(ctx, OpSiteRouting, a))
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestRuntime: Notify never blocks, and a runtime with no service starts and
// stops as nothing.
func TestRuntime(t *testing.T) {
	var none *Runtime
	require.NoError(t, none.Start(context.Background()))
	require.NoError(t, none.Stop(context.Background()))

	w := fixture()
	svc, mem := w.svc, w.mem
	mem.locked = true
	rt := &Runtime{Service: svc}
	require.NoError(t, rt.Start(context.Background()))
	rt.Notify()
	rt.Notify()
	require.NoError(t, rt.Stop(context.Background()))
}
