package appdbconsole

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

type fakeTx struct {
	sent       []postgres.Statement
	failAt     int
	failWith   error
	commitErr  error
	committed  bool
	rolledBack bool
}

func (x *fakeTx) Query(_ context.Context, s postgres.Statement) (*postgres.Result, error) {
	x.sent = append(x.sent, s)
	if x.failWith != nil && len(x.sent) == x.failAt {
		return nil, x.failWith
	}
	return &postgres.Result{Command: "SELECT", RowsAffected: 1, Columns: []postgres.Column{{Name: "n"}}, Rows: [][]any{{int64(1)}}}, nil
}

func (x *fakeTx) Commit(context.Context) error { x.committed = true; return x.commitErr }

func (x *fakeTx) Rollback() { x.rolledBack = true }

type fakeTransactor struct {
	tx       *fakeTx
	readOnly bool
	err      error
}

func (f *fakeTransactor) ConsoleTx(_ context.Context, _ string, readOnly bool) (Tx, error) {
	f.readOnly = readOnly
	if f.err != nil {
		return nil, f.err
	}
	return f.tx, nil
}

func TestParse(t *testing.T) {
	stmts, err := ConsoleRequest{SQL: "SELECT 1; SELECT 2;"}.Parse()
	require.NoError(t, err)
	assert.Equal(t, []ConsoleStatement{{SQL: "SELECT 1"}, {SQL: "SELECT 2"}}, stmts)

	stmts, err = ConsoleRequest{SQL: "SELECT $1", Params: []any{1}}.Parse()
	require.NoError(t, err)
	assert.Equal(t, []any{1}, stmts[0].Params)

	for name, req := range map[string]ConsoleRequest{
		"params with several": {SQL: "SELECT 1; SELECT 2", Params: []any{1}},
		"nothing to run":      {SQL: "  ; "},
		"too many statements": {SQL: strings.Repeat("SELECT 1;", MaxConsoleStatements+1)},
		"too much SQL":        {Statements: []ConsoleStatement{{SQL: strings.Repeat("x", MaxConsoleSQLBytes+1)}}},
	} {
		_, err := req.Parse()
		require.Error(t, err, name)
	}
	off := false
	assert.True(t, ConsoleRequest{}.IsReadOnly())
	assert.False(t, ConsoleRequest{ReadOnly: &off}.IsReadOnly())
}

func TestRunConsole(t *testing.T) {
	ctx := context.Background()
	tx := &fakeTx{}
	k := &fakeTransactor{tx: tx}
	res, err := RunConsole(ctx, k, "shop", ConsoleRequest{SQL: "SELECT 1; SELECT 2"})
	require.NoError(t, err)
	assert.True(t, k.readOnly, "read only unless asked")
	assert.Len(t, res.Results, 2)
	assert.False(t, tx.committed, "a read-only transaction is not committed")
	assert.True(t, tx.rolledBack)

	off := false
	tx = &fakeTx{}
	res, err = RunConsole(ctx, &fakeTransactor{tx: tx}, "shop", ConsoleRequest{SQL: "UPDATE t SET x = 1", ReadOnly: &off})
	require.NoError(t, err)
	assert.True(t, res.Committed)

	tx = &fakeTx{failAt: 2, failWith: &postgres.FailedError{
		Code: postgres.CodeStatementFailed, Summary: "SQLSTATE 23505",
		Statement: &postgres.StatementError{},
	}}
	res, err = RunConsole(ctx, &fakeTransactor{tx: tx}, "shop", ConsoleRequest{SQL: "SELECT 1; INSERT INTO t VALUES (1); SELECT 3", ReadOnly: &off})
	require.NoError(t, err)
	require.NotNil(t, res.Error)
	assert.Equal(t, 1, res.Error.Index)
	assert.Equal(t, "SQLSTATE 23505", res.Error.Summary)
	assert.False(t, res.Committed)
	assert.Len(t, tx.sent, 2, "nothing after the failed statement runs")

	tx = &fakeTx{failAt: 1, failWith: &postgres.RefusedError{Code: postgres.CodeInvalidStatement}}
	res, _ = RunConsole(ctx, &fakeTransactor{tx: tx}, "shop", ConsoleRequest{SQL: "COPY t FROM STDIN"})
	assert.Equal(t, postgres.CodeInvalidStatement, res.Error.Code)

	tx = &fakeTx{commitErr: errors.New("serialization failure")}
	res, _ = RunConsole(ctx, &fakeTransactor{tx: tx}, "shop", ConsoleRequest{SQL: "UPDATE t SET x = 1", ReadOnly: &off})
	assert.False(t, res.Committed)
	assert.Equal(t, 1, res.Error.Index)

	_, err = RunConsole(ctx, &fakeTransactor{err: errors.New("pool")}, "shop", ConsoleRequest{SQL: "SELECT 1"})
	require.Error(t, err)
	_, err = RunConsole(ctx, &fakeTransactor{tx: &fakeTx{}}, "shop", ConsoleRequest{})
	var refused *RefusedError
	require.ErrorAs(t, err, &refused)
}

// Shapes is what the audit row keeps of a statement's parameters: each
// one's name or position and type, never its value.
func TestShapes(t *testing.T) {
	assert.Equal(t, []ParamShape{
		{Position: 1, Type: "number"},
		{Position: 2, Type: "string"},
		{Position: 3, Type: "null"},
		{Position: 4, Type: "boolean"},
		{Position: 5, Type: "array"},
		{Position: 6, Type: "object"},
	}, Shapes([]any{json.Number("1"), "secret-value", nil, true, []any{}, map[string]any{}}))
	got := Shapes(map[string]any{"sku": "secret-value", "qty": json.Number("2")})
	assert.Equal(t, []ParamShape{{Name: "qty", Type: "number"}, {Name: "sku", Type: "string"}}, got)
	raw, _ := json.Marshal(got)
	assert.NotContains(t, string(raw), "secret-value")
	assert.Equal(t, []ParamShape{}, Shapes(nil))
}

type fakeSignaller struct {
	roles   []string
	answers []*postgres.Result
	errs    []error
	sent    []postgres.Statement
}

func (f *fakeSignaller) ConnectionRoles(string) []string { return f.roles }

func (f *fakeSignaller) Query(_ context.Context, s postgres.Statement) (*postgres.Result, error) {
	i := len(f.sent)
	f.sent = append(f.sent, s)
	var err error
	if i < len(f.errs) {
		err = f.errs[i]
	}
	if i < len(f.answers) {
		return f.answers[i], err
	}
	return &postgres.Result{}, err
}

func TestSignal(t *testing.T) {
	ctx := context.Background()
	f := &fakeSignaller{roles: []string{"orders_app", "orders_reader"}, answers: []*postgres.Result{{Rows: [][]any{{true}}}}}
	res, err := Signal(ctx, f, "shop", SignalTerminate, 42)
	require.NoError(t, err)
	assert.True(t, res.Sent)
	assert.Contains(t, f.sent[0].SQL, "pg_terminate_backend")
	assert.Equal(t, []any{int64(42), []any{"orders_app", "orders_reader"}}, f.sent[0].Params,
		"the role check and the signal are one statement")

	f = &fakeSignaller{roles: []string{"orders_app"}, answers: []*postgres.Result{{}, {Rows: [][]any{{"postgres", "orders"}}}}}
	_, err = Signal(ctx, f, "shop", SignalCancel, 7)
	require.ErrorContains(t, err, `logged in as "postgres"`)
	assert.Contains(t, f.sent[0].SQL, "pg_cancel_backend")

	f = &fakeSignaller{roles: []string{"orders_app"}, answers: []*postgres.Result{{}, {}}}
	_, err = Signal(ctx, f, "shop", SignalCancel, 7)
	require.ErrorContains(t, err, "there is no backend 7")

	_, err = Signal(ctx, &fakeSignaller{roles: []string{"a"}}, "shop", "kill", 7)
	require.ErrorContains(t, err, "not cancel or terminate")
	_, err = Signal(ctx, &fakeSignaller{}, "nope", SignalCancel, 7)
	require.ErrorContains(t, err, "no application database")
	_, err = Signal(ctx, &fakeSignaller{roles: []string{"a"}, errs: []error{errors.New("down")}}, "shop", SignalCancel, 7)
	require.Error(t, err)
}

func TestToolkitConsoleTx_AnUnknownConnection(t *testing.T) {
	multi, err := postgres.ParseMultiConfig("shop", map[string]map[string]any{})
	require.NoError(t, err)
	_, err = Toolkit{postgres.NewMulti(multi)}.ConsoleTx(context.Background(), "nope", true)
	require.Error(t, err)
}
