package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// errText is an error's text, or "" for nil.
func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
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

func TestQuery_MapsRowsAndBindsParamsAsText(t *testing.T) {
	tx := selectTx()
	tk := fakeToolkit(baseConfig(), tx)

	res, err := tk.Query(context.Background(), Statement{
		Connection: "db", SQL: selectByID, Params: []any{json.Number("1")}, TimeoutSeconds: 5,
	})
	require.NoError(t, err)
	assert.Equal(t, []Column{{"id", "integer"}, {"total", "numeric"}, {"tags", "text[]"}}, res.Columns)
	got, _ := json.Marshal(res.Rows)
	assert.JSONEq(t, `[[1,"12.50",["a","b"]],[2,"3.00",[]],[3,"0.10",null]]`, string(got))
	assert.False(t, res.Truncated)
	assert.Equal(t, "SELECT", res.Command)

	// The value went as text, for the server to parse as the parameter's type.
	last := tx.queries[len(tx.queries)-1]
	assert.Equal(t, []any{"1"}, last.args)
	assert.Empty(t, tx.execs, "the bound is set in the BEGIN's round trip")
	assert.Equal(t, "BEGIN READ ONLY; SET LOCAL statement_timeout = 5000", tx.opts.BeginQuery)
	assert.True(t, tx.rolledBack)
	assert.False(t, tx.committed, "a query's READ ONLY transaction is never committed")
}

func TestQuery_AGoIntegerBindsAsText(t *testing.T) {
	tx := selectTx()
	tk := fakeToolkit(baseConfig(), tx)
	_, err := tk.Query(context.Background(), Statement{Connection: "db", SQL: selectByID, Params: []any{int64(1)}})
	require.NoError(t, err)
	assert.Equal(t, []any{"1"}, tx.queries[len(tx.queries)-1].args)
}

func TestQuery_StopsAtTheLimit(t *testing.T) {
	tk := fakeToolkit(baseConfig(), selectTx())
	res, err := tk.Query(context.Background(), Statement{Connection: "db", SQL: selectByID, Params: []any{"1"}, Limit: 2})
	require.NoError(t, err)
	assert.Len(t, res.Rows, 2)
	assert.True(t, res.Truncated)
	assert.Equal(t, 2, res.Limit)
}

func TestQuery_MapParamsBindByName(t *testing.T) {
	tk := fakeToolkit(baseConfig(), selectTx())
	const byName = "SELECT id, total, tags FROM orders WHERE id = @id"
	_, err := tk.Query(context.Background(), Statement{Connection: "db", SQL: byName, Params: map[string]any{"id": "1"}})
	require.NoError(t, err)

	_, err = tk.Query(context.Background(), Statement{Connection: "db", SQL: byName, Params: map[string]any{"idd": "1"}})
	var refused *RefusedError
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, CodeInvalidStatement, refused.Code)
}

func TestQuery_Refusals(t *testing.T) {
	tk := fakeToolkit(baseConfig(), selectTx())
	for _, tc := range []struct {
		name string
		in   Statement
		code string
		want string
	}{
		{"no sql", Statement{Connection: "db"}, CodeInvalidStatement, "the statement is empty"},
		{"unknown connection", Statement{Connection: "nope", SQL: "SELECT 1"}, CodeUnknownConnection, `no application database named "nope"`},
		{"params a string", Statement{Connection: "db", SQL: selectByID, Params: "x"}, CodeInvalidStatement, "params must be a list"},
		{"params count", Statement{Connection: "db", SQL: selectByID, Params: []any{"1", "2"}}, CodeInvalidStatement, "takes 1 parameters and params holds 2"},
		{"commit", Statement{Connection: "db", SQL: "COMMIT"}, CodeInvalidStatement, "COMMIT statement is not run"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tk.Query(context.Background(), tc.in)
			var refused *RefusedError
			require.ErrorAs(t, err, &refused)
			assert.Equal(t, tc.code, refused.Code)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
	assert.True(t, tk.HasConnection("db"))
	assert.False(t, tk.HasConnection("nope"))
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
		"DELETE FROM customers RETURNING 1": {fields: []pgconn.FieldDescription{field("x", oidInt4)}, rows: [][]any{{int32(1)}, {int32(1)}}, tag: "DELETE 2"},
	}
	if failAt != "" {
		st := statements[failAt]
		st.execErr = dup
		statements[failAt] = st
	}
	return &fakeTx{types: fakeTypes(), statements: statements}
}

func TestExecute_CommitsAndCapsReturningRows(t *testing.T) {
	tx := insertTx("")
	cfg := baseConfig()
	cfg.MaxRows = 1
	tk := fakeToolkit(cfg, tx)
	res, err := tk.Execute(context.Background(), Statement{Connection: "db", SQL: insertCustomer, Params: []any{"a@example.com", "A"}})
	require.NoError(t, err)
	assert.True(t, tx.committed)
	assert.Equal(t, "INSERT", res.Command)
	assert.Equal(t, int64(1), res.RowsAffected)

	tx = insertTx("")
	tk = fakeToolkit(cfg, tx)
	res, err = tk.Execute(context.Background(), Statement{Connection: "db", SQL: "DELETE FROM customers RETURNING 1"})
	require.NoError(t, err)
	assert.Len(t, res.Rows, 1, "RETURNING rows past max_rows are dropped")
	assert.True(t, res.Truncated)
	assert.Equal(t, int64(2), res.RowsAffected)
}

func TestExecute_AFailureKeepsTheDetailOutOfTheErrorText(t *testing.T) {
	tx := insertTx(insertCustomer)
	tk := fakeToolkit(baseConfig(), tx)
	_, err := tk.Execute(context.Background(), Statement{Connection: "db", SQL: insertCustomer, Params: []any{"secret@example.com", "A"}})
	var failed *FailedError
	require.ErrorAs(t, err, &failed)
	assert.False(t, tx.committed)
	assert.True(t, tx.rolledBack)

	text := err.Error()
	assert.Contains(t, text, "SQLSTATE 23505 (unique_violation)")
	assert.Contains(t, text, "constraint customers_email_key")
	assert.Contains(t, text, "table public.customers")
	assert.NotContains(t, text, "secret@example.com")
	assert.Equal(t, CodeStatementFailed, failed.Code)
	assert.Contains(t, failed.CallerText(), "DETAIL: Key (email)=(secret@example.com) already exists.")
	require.NotNil(t, failed.Statement)
	assert.Equal(t, "customers_email_key", failed.Statement.Constraint)
}

func TestExecute_RefusedOnAReadOnlyConnection(t *testing.T) {
	cfg := baseConfig()
	cfg.ReadOnly = true
	tx := insertTx("")
	tk := fakeToolkit(cfg, tx)
	_, err := tk.Execute(context.Background(), Statement{Connection: "db", SQL: insertCustomer, Params: []any{"a", "b"}})
	var refused *RefusedError
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, CodeReadOnly, refused.Code)
	assert.Empty(t, tx.queries, "nothing ran")

	_, err = tk.Execute(context.Background(), Statement{Connection: "db", SQL: "/* x */ SAVEPOINT s"})
	require.ErrorAs(t, err, &refused)
}

func TestRun_FailuresBeforeAndAfterTheStatement(t *testing.T) {
	cfg := baseConfig()
	cfg.ConnectionName = "db"
	unreachable := &conn{cfg: cfg, types: newTypeCache(), begin: func(context.Context, pgx.TxOptions) (pgx.Tx, error) {
		return nil, errors.New("dial tcp 10.0.0.1:5432: connect: connection refused")
	}}
	tk := &Toolkit{connections: map[string]*conn{"db": unreachable}}
	_, err := tk.Query(context.Background(), Statement{Connection: "db", SQL: "SELECT 1"})
	var failed *FailedError
	require.ErrorAs(t, err, &failed)
	assert.Equal(t, CodeUnavailable, failed.Code)
	assert.Contains(t, errText(err), `connection "db" could not reach its database`)
	assert.Equal(t, errText(err), failed.CallerText(), "no server account to add")

	timedOut := &conn{cfg: cfg, types: newTypeCache(), begin: func(context.Context, pgx.TxOptions) (pgx.Tx, error) {
		return nil, context.DeadlineExceeded
	}}
	tk = &Toolkit{connections: map[string]*conn{"db": timedOut}}
	_, err = tk.Query(context.Background(), Statement{Connection: "db", SQL: "SELECT 1"})
	assert.Contains(t, errText(err), "ran out of time")

	tx := insertTx("")
	st := tx.statements[insertCustomer]
	st.execErr = &pgconn.PgError{Code: "42501", Message: "permission denied"}
	tx.statements[insertCustomer] = st
	tk = fakeToolkit(baseConfig(), tx)
	_, err = tk.Execute(context.Background(), Statement{Connection: "db", SQL: insertCustomer, Params: []any{"a", "b"}})
	assert.Contains(t, errText(err), "42501")

	tx = insertTx("")
	tx.commitErr = &pgconn.PgError{Code: "40001", Message: "could not serialize access"}
	tk = fakeToolkit(baseConfig(), tx)
	_, err = tk.Execute(context.Background(), Statement{Connection: "db", SQL: insertCustomer, Params: []any{"a", "b"}})
	assert.Contains(t, errText(err), "40001 (serialization_failure)")
}

func TestExecute_PrepareRefusalIsAStatementError(t *testing.T) {
	tx := &fakeTx{types: fakeTypes(), statements: map[string]fakeStatement{
		"SELEC 1": {prepErr: &pgconn.PgError{Code: "42601", Message: `syntax error at or near "SELEC"`, Position: 1}},
	}}
	tk := fakeToolkit(baseConfig(), tx)
	_, err := tk.Execute(context.Background(), Statement{Connection: "db", SQL: "SELEC 1"})
	var failed *FailedError
	require.ErrorAs(t, err, &failed)
	assert.Contains(t, errText(err), "42601 (syntax_error)")
	assert.Contains(t, failed.CallerText(), `syntax error at or near "SELEC"`)
}
