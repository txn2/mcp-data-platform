package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func firstText(res *mcp.CallToolResult) string {
	tc, _ := res.Content[0].(*mcp.TextContent)
	return tc.Text
}

func allTextOf(res *mcp.CallToolResult) string {
	parts := make([]string, 0, len(res.Content))
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func queryOut(t *testing.T, out any) *QueryOutput {
	t.Helper()
	q, ok := out.(*QueryOutput)
	require.True(t, ok, "output is %T", out)
	return q
}

const selectByID = "SELECT id, total, tags FROM orders WHERE id = $1"

func selectTx() *fakeTx {
	return &fakeTx{
		types: fakeTypes(),
		statements: map[string]fakeStatement{
			selectByID: {
				params: []uint32{oidInt4},
				fields: []pgconn.FieldDescription{field("id", oidInt4), field("total", oidNumeric), field("tags", oidText1D)},
				rows: [][]any{
					{int32(1), mustNumeric("12.50"), []any{"a", "b"}},
					{int32(2), mustNumeric("3.00"), []any{}},
					{int32(3), mustNumeric("0.10"), nil},
				},
				tag: "SELECT 3",
			},
		},
	}
}

func mustNumeric(s string) pgtype.Numeric {
	var n pgtype.Numeric
	if err := n.Scan(s); err != nil {
		panic(err)
	}
	return n
}

func TestHandleQuery_MapsRowsAndBindsParamsAsText(t *testing.T) {
	tx := selectTx()
	tk := fakeToolkit(baseConfig(), tx)

	res, out, err := tk.handleQuery(context.Background(), nil, QueryInput{
		Connection: "db", SQL: selectByID, Params: json.RawMessage(`[1]`), TimeoutSeconds: 5,
	})
	require.NoError(t, err)
	require.False(t, res.IsError, allTextOf(res))
	q := queryOut(t, out)
	assert.Equal(t, []Column{{"id", "integer"}, {"total", "numeric"}, {"tags", "text[]"}}, q.Columns)
	got, _ := json.Marshal(q.Rows)
	assert.JSONEq(t, `[[1,"12.50",["a","b"]],[2,"3.00",[]],[3,"0.10",null]]`, string(got))
	assert.False(t, q.Truncated)

	// The value went as text, for the server to parse as the parameter's type.
	last := tx.queries[len(tx.queries)-1]
	assert.Equal(t, []any{"1"}, last.args)
	assert.Equal(t, []string{setTimeoutSQL}, tx.execs)
	assert.True(t, tx.rolledBack)
	assert.False(t, tx.committed, "a query's READ ONLY transaction is never committed")
}

func TestHandleQuery_StopsAtTheLimit(t *testing.T) {
	tx := selectTx()
	tk := fakeToolkit(baseConfig(), tx)
	_, out, _ := tk.handleQuery(context.Background(), nil, QueryInput{
		Connection: "db", SQL: selectByID, Params: json.RawMessage(`[1]`), Limit: 2,
	})
	q := queryOut(t, out)
	assert.Len(t, q.Rows, 2)
	assert.True(t, q.Truncated)
	assert.Equal(t, 2, q.Limit)
}

func TestHandleQuery_ObjectParamsBindByName(t *testing.T) {
	tx := selectTx()
	tk := fakeToolkit(baseConfig(), tx)
	res, _, _ := tk.handleQuery(context.Background(), nil, QueryInput{
		Connection: "db", SQL: "SELECT id, total, tags FROM orders WHERE id = @id", Params: json.RawMessage(`{"id": 1}`),
	})
	require.False(t, res.IsError, allTextOf(res))

	res, _, _ = tk.handleQuery(context.Background(), nil, QueryInput{
		Connection: "db", SQL: "SELECT id, total, tags FROM orders WHERE id = @id", Params: json.RawMessage(`{"idd": 1}`),
	})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "invalid_statement")
}

func TestHandleQuery_Refusals(t *testing.T) {
	tk := fakeToolkit(baseConfig(), selectTx())
	for _, tc := range []struct {
		name string
		in   QueryInput
		want string
	}{
		{"no connection", QueryInput{SQL: "SELECT 1"}, "missing_required_parameter"},
		{"no sql", QueryInput{Connection: "db"}, "missing_required_parameter"},
		{"unknown connection", QueryInput{Connection: "nope", SQL: "SELECT 1"}, `no connection named "nope"`},
		{"params a string", QueryInput{Connection: "db", SQL: selectByID, Params: json.RawMessage(`"x"`)}, "params must be a list"},
		{"params count", QueryInput{Connection: "db", SQL: selectByID, Params: json.RawMessage(`[1, 2]`)}, "takes 1 parameters and params holds 2"},
		{"commit", QueryInput{Connection: "db", SQL: "COMMIT"}, "COMMIT statement is not run"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, _, _ := tk.handleQuery(context.Background(), nil, tc.in)
			require.True(t, res.IsError)
			assert.Contains(t, allTextOf(res), tc.want)
		})
	}
}

const insertCustomer = "INSERT INTO customers (email, name) VALUES ($1, $2)"

func insertTx(failAt string) *fakeTx {
	dup := &pgconn.PgError{
		Code: "23505", Message: `duplicate key value violates unique constraint "customers_email_key"`,
		Detail: "Key (email)=(secret@example.com) already exists.", SchemaName: "public",
		TableName: "customers", ConstraintName: "customers_email_key",
	}
	statements := map[string]fakeStatement{
		insertCustomer:                      {params: []uint32{oidText, oidText}, tag: "INSERT 0 1"},
		"UPDATE customers SET name = $1":    {params: []uint32{oidText}, tag: "UPDATE 2"},
		"DELETE FROM customers RETURNING 1": {fields: []pgconn.FieldDescription{field("x", oidInt4)}, rows: [][]any{{int32(1)}, {int32(1)}}, tag: "DELETE 2"},
	}
	if failAt != "" {
		st := statements[failAt]
		st.execErr = dup
		statements[failAt] = st
	}
	return &fakeTx{types: fakeTypes(), statements: statements}
}

func TestHandleExecute_CommitsAStatementsList(t *testing.T) {
	tx := insertTx("")
	cfg := baseConfig()
	cfg.MaxRows = 1
	tk := fakeToolkit(cfg, tx)
	res, out, err := tk.handleExecute(context.Background(), nil, ExecuteInput{
		Connection: "db",
		Statements: []StatementInput{
			{SQL: insertCustomer, Params: json.RawMessage(`["a@example.com", "A"]`)},
			{SQL: "UPDATE customers SET name = $1", Params: json.RawMessage(`["B"]`)},
			{SQL: "DELETE FROM customers RETURNING 1"},
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, allTextOf(res))
	e, ok := out.(*ExecuteOutput)
	require.True(t, ok)
	assert.True(t, e.Committed)
	assert.True(t, tx.committed)
	require.Len(t, e.Statements, 3)
	assert.Equal(t, "INSERT", e.Statements[0].Command)
	assert.Equal(t, int64(2), e.Statements[1].RowsAffected)
	assert.Len(t, e.Statements[2].Rows, 1, "RETURNING rows past max_rows are dropped")
	assert.True(t, e.Statements[2].Truncated)
}

func TestHandleExecute_AFailureNamesTheStatementAndKeepsTheDetailFromTheAuditText(t *testing.T) {
	tx := insertTx("UPDATE customers SET name = $1")
	tk := fakeToolkit(baseConfig(), tx)
	res, _, _ := tk.handleExecute(context.Background(), nil, ExecuteInput{
		Connection: "db",
		Statements: []StatementInput{
			{SQL: insertCustomer, Params: json.RawMessage(`["secret@example.com", "A"]`)},
			{SQL: "UPDATE customers SET name = $1", Params: json.RawMessage(`["B"]`)},
		},
	})
	require.True(t, res.IsError)
	assert.False(t, tx.committed)
	assert.True(t, tx.rolledBack)

	first := firstText(res)
	assert.Contains(t, first, "SQLSTATE 23505 (unique_violation) in statement 1")
	assert.Contains(t, first, "constraint customers_email_key")
	assert.Contains(t, first, "table public.customers")
	assert.NotContains(t, first, "secret@example.com")
	assert.NotContains(t, res.GetError().Error(), "secret@example.com")
	assert.Contains(t, allTextOf(res), "DETAIL: Key (email)=(secret@example.com) already exists.")

	sc, ok := res.StructuredContent.(map[string]any)
	require.True(t, ok)
	se, ok := sc["postgres"].(*StatementError)
	require.True(t, ok)
	assert.Equal(t, "customers_email_key", se.Constraint)
	require.NotNil(t, se.Statement)
	assert.Equal(t, 1, *se.Statement)
	envelope, ok := sc["error"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, codeStatementFailed, envelope["code"])
	assert.Equal(t, categoryClientInput, envelope["category"])
}

func TestHandleExecute_Refusals(t *testing.T) {
	tk := fakeToolkit(baseConfig(), insertTx(""))
	readOnly := baseConfig()
	readOnly.ReadOnly = true
	ro := fakeToolkit(readOnly, insertTx(""))
	for _, tc := range []struct {
		name string
		tk   *Toolkit
		in   ExecuteInput
		want string
	}{
		{"read only", ro, ExecuteInput{Connection: "db", SQL: insertCustomer}, "read_only_connection"},
		{"both", tk, ExecuteInput{Connection: "db", SQL: "x", Statements: []StatementInput{{SQL: "y"}}}, "not both"},
		{"neither", tk, ExecuteInput{Connection: "db"}, "sql or statements is required"},
		{"params beside a list", tk, ExecuteInput{Connection: "db", Params: json.RawMessage(`[1]`), Statements: []StatementInput{{SQL: "y"}}}, "inside each statement"},
		{"empty item", tk, ExecuteInput{Connection: "db", Statements: []StatementInput{{SQL: "y"}, {}}}, "statement 1 has no sql"},
		{"bad params", tk, ExecuteInput{Connection: "db", Statements: []StatementInput{{SQL: insertCustomer, Params: json.RawMessage(`[`)}}}, "statement 0: params"},
		{"savepoint", tk, ExecuteInput{Connection: "db", Statements: []StatementInput{{SQL: insertCustomer, Params: json.RawMessage(`["a","b"]`)}, {SQL: "/* x */ SAVEPOINT s"}}}, "statement 1: a SAVEPOINT statement is not run"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, _, _ := tc.tk.handleExecute(context.Background(), nil, tc.in)
			require.True(t, res.IsError)
			assert.Contains(t, allTextOf(res), tc.want)
		})
	}
	many := make([]StatementInput, maxStatements+1)
	for i := range many {
		many[i] = StatementInput{SQL: "SELECT 1"}
	}
	res, _, _ := tk.handleExecute(context.Background(), nil, ExecuteInput{Connection: "db", Statements: many})
	assert.Contains(t, allTextOf(res), "at most 100 statements")
}

func TestInTx_FailuresBeforeAndAfterTheStatement(t *testing.T) {
	cfg := baseConfig()
	cfg.ConnectionName = "db"
	unreachable := &conn{cfg: cfg, types: newTypeCache(), begin: func(context.Context, pgx.TxOptions) (pgx.Tx, error) {
		return nil, errors.New("dial tcp 10.0.0.1:5432: connect: connection refused")
	}}
	tk := &Toolkit{connections: map[string]*conn{"db": unreachable}}
	res, _, _ := tk.handleQuery(context.Background(), nil, QueryInput{Connection: "db", SQL: "SELECT 1"})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), `connection "db" could not reach its database`)

	timedOut := &conn{cfg: cfg, types: newTypeCache(), begin: func(context.Context, pgx.TxOptions) (pgx.Tx, error) {
		return nil, context.DeadlineExceeded
	}}
	tk = &Toolkit{connections: map[string]*conn{"db": timedOut}}
	res, _, _ = tk.handleQuery(context.Background(), nil, QueryInput{Connection: "db", SQL: "SELECT 1"})
	assert.Contains(t, firstText(res), "ran out of time")

	tx := insertTx("")
	tx.execErr = &pgconn.PgError{Code: "42501", Message: "permission denied"}
	tk = fakeToolkit(baseConfig(), tx)
	res, _, _ = tk.handleExecute(context.Background(), nil, ExecuteInput{Connection: "db", SQL: insertCustomer, Params: json.RawMessage(`["a","b"]`)})
	assert.Contains(t, firstText(res), "42501")

	tx = insertTx("")
	tx.commitErr = &pgconn.PgError{Code: "40001", Message: "could not serialize access"}
	tk = fakeToolkit(baseConfig(), tx)
	res, _, _ = tk.handleExecute(context.Background(), nil, ExecuteInput{Connection: "db", SQL: insertCustomer, Params: json.RawMessage(`["a","b"]`)})
	assert.Contains(t, firstText(res), "40001 (serialization_failure)")
}

func TestHandleExecute_PrepareRefusalIsAStatementError(t *testing.T) {
	tx := &fakeTx{types: fakeTypes(), statements: map[string]fakeStatement{
		"SELEC 1": {prepErr: &pgconn.PgError{Code: "42601", Message: `syntax error at or near "SELEC"`, Position: 1}},
	}}
	tk := fakeToolkit(baseConfig(), tx)
	res, _, _ := tk.handleExecute(context.Background(), nil, ExecuteInput{Connection: "db", SQL: "SELEC 1"})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "42601 (syntax_error) in statement 0")
	assert.Contains(t, allTextOf(res), `syntax error at or near "SELEC"`)
}
