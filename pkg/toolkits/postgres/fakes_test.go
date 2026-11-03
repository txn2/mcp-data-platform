package postgres

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// fakeStatement is how the fake transaction answers one SQL text.
type fakeStatement struct {
	params  []uint32
	fields  []pgconn.FieldDescription
	rows    [][]any
	tag     string
	prepErr error
	execErr error
}

type queryCall struct {
	sql  string
	args []any
}

// fakeTx stands in for a pgx transaction: it describes and runs the
// statements it was given, answers the type query from types, and records
// what it was asked.
type fakeTx struct {
	pgx.Tx
	statements map[string]fakeStatement
	types      map[uint32]typeInfo
	execs      []string
	queries    []queryCall
	committed  bool
	rolledBack bool
	commitErr  error
	execErr    error
	// opts is what the transaction was begun with.
	opts pgx.TxOptions
}

func (f *fakeTx) Prepare(_ context.Context, _, sql string) (*pgconn.StatementDescription, error) {
	st, ok := f.statements[sql]
	if !ok {
		return nil, errors.New("fake: unexpected statement " + sql)
	}
	if st.prepErr != nil {
		return nil, st.prepErr
	}
	return &pgconn.StatementDescription{SQL: sql, ParamOIDs: st.params, Fields: st.fields}, nil
}

func (f *fakeTx) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	f.execs = append(f.execs, sql)
	return pgconn.NewCommandTag("SET"), f.execErr
}

func (f *fakeTx) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	f.queries = append(f.queries, queryCall{sql: sql, args: args})
	if sql == typeQuery {
		literal, _ := args[0].(string)
		return f.typeRows(literal), nil
	}
	st := f.statements[sql]
	if st.execErr != nil {
		return nil, st.execErr
	}
	return &fakeRows{rows: st.rows, fields: st.fields, tag: st.tag}, nil
}

// typeRows answers the type query for the OIDs in an array literal.
func (f *fakeTx) typeRows(literal string) *fakeRows {
	var rows [][]any
	for part := range strings.SplitSeq(strings.Trim(literal, "{}"), ",") {
		oid, _ := strconv.ParseUint(part, 10, 32)
		if ti, ok := f.types[uint32(oid)]; ok {
			rows = append(rows, []any{uint32(oid), ti.name, ti.display, ti.array})
		}
	}
	return &fakeRows{rows: rows, tag: "SELECT"}
}

// QueryRow answers the first row of Query, or pgx.ErrNoRows.
func (f *fakeTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	rows, err := f.Query(ctx, sql, args...)
	return fakeRow{rows: rows, err: err}
}

// fakeRow is one row of a fake result.
type fakeRow struct {
	rows pgx.Rows
	err  error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if !r.rows.Next() {
		return pgx.ErrNoRows
	}
	return r.rows.Scan(dest...) //nolint:wrapcheck // the fake rows' own error, read by its type
}

func (f *fakeTx) Commit(context.Context) error {
	f.committed = true
	return f.commitErr
}

func (f *fakeTx) Rollback(context.Context) error {
	f.rolledBack = true
	return nil
}

// fakeRows serves a fixed result.
type fakeRows struct {
	rows   [][]any
	fields []pgconn.FieldDescription
	tag    string
	i      int
	err    error
}

func (*fakeRows) Close()                                         {}
func (r *fakeRows) Err() error                                   { return r.err }
func (r *fakeRows) CommandTag() pgconn.CommandTag                { return pgconn.NewCommandTag(r.tag) }
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return r.fields }
func (*fakeRows) RawValues() [][]byte                            { return nil }
func (*fakeRows) Conn() *pgx.Conn                                { return nil }
func (*fakeRows) TypeMap() *pgtype.Map                           { return pgtype.NewMap() }

func (r *fakeRows) Next() bool {
	if r.i >= len(r.rows) {
		return false
	}
	r.i++
	return true
}

func (r *fakeRows) Values() ([]any, error) { return r.rows[r.i-1], nil }

func (r *fakeRows) Scan(dest ...any) error {
	row := r.rows[r.i-1]
	for i, d := range dest {
		switch p := d.(type) {
		case *uint32:
			*p, _ = row[i].(uint32)
		case *string:
			*p, _ = row[i].(string)
		case *bool:
			*p, _ = row[i].(bool)
		case *int64:
			*p, _ = row[i].(int64)
		case *time.Time:
			*p, _ = row[i].(time.Time)
		}
	}
	return nil
}

// Built-in type OIDs the tests use.
const (
	oidBool    = 16
	oidInt4    = 23
	oidText    = 25
	oidJSONB   = 3802
	oidNumeric = 1700
	oidText1D  = 1009
	oidBytea   = 17
)

func fakeTypes() map[uint32]typeInfo {
	return map[uint32]typeInfo{
		oidBool:    {name: "bool", display: "boolean"},
		oidInt4:    {name: "int4", display: "integer"},
		oidText:    {name: "text", display: "text"},
		oidJSONB:   {name: "jsonb", display: "jsonb"},
		oidNumeric: {name: "numeric", display: "numeric"},
		oidText1D:  {name: "_text", display: "text[]", array: true},
		oidBytea:   {name: "bytea", display: "bytea"},
	}
}

func field(name string, oid uint32) pgconn.FieldDescription {
	return pgconn.FieldDescription{Name: name, DataTypeOID: oid}
}

// fakeToolkit is a toolkit serving one connection named "db" on tx.
func fakeToolkit(cfg Config, tx *fakeTx) *Toolkit {
	cfg.ConnectionName = "db"
	return &Toolkit{
		name: "db", defaultName: "db",
		connections: map[string]*conn{"db": {
			cfg: cfg, types: newTypeCache(),
			begin: func(_ context.Context, o pgx.TxOptions) (pgx.Tx, error) { tx.opts = o; return tx, nil },
		}},
	}
}

func baseConfig() Config {
	return Config{
		Host: "db", Port: DefaultPort, Database: "app", User: "app", SSLMode: SSLModeDisable,
		MaxConns: DefaultMaxConns, StatementTimeout: DefaultStatementTimeout, MaxRows: DefaultMaxRows,
	}
}
