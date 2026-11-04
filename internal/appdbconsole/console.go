package appdbconsole

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/txn2/mcp-data-platform/internal/pglex"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// Console bounds.
const (
	// MaxConsoleStatements is the most statements one console request runs.
	MaxConsoleStatements = 50
	// MaxConsoleSQLBytes is the most SQL text one console request carries.
	MaxConsoleSQLBytes = 1 << 20
)

// ConsoleStatement is one statement and the values bound to its
// placeholders: a list binds $1, $2, ... and an object binds @name.
type ConsoleStatement struct {
	SQL    string `json:"sql"`
	Params any    `json:"params,omitempty"`
}

// ConsoleRequest is what an administrator runs in the console (#2427): one
// statement, several separated by semicolons in sql, or a list. Every
// statement runs in one transaction, READ ONLY unless ReadOnly is false.
type ConsoleRequest struct {
	SQL        string             `json:"sql,omitempty"`
	Params     any                `json:"params,omitempty"`
	Statements []ConsoleStatement `json:"statements,omitempty"`
	// ReadOnly runs the transaction READ ONLY; nil is true. A write needs it
	// turned off for the request that makes it.
	ReadOnly *bool `json:"read_only,omitempty"`
}

// StatementResult is what one statement returned.
type StatementResult struct {
	Index        int               `json:"index"`
	Command      string            `json:"command"`
	RowsAffected int64             `json:"rows_affected"`
	Columns      []postgres.Column `json:"columns"`
	Rows         [][]any           `json:"rows"`
	Truncated    bool              `json:"truncated"`
}

// ConsoleError is the statement the database refused. Summary is the
// value-free account the audit row and the logs may carry; Detail is what the
// server said, DETAIL and HINT included, which can repeat a value and goes to
// the administrator alone.
type ConsoleError struct {
	Index   int    `json:"index"`
	Code    string `json:"code"`
	Summary string `json:"summary"`
	Detail  string `json:"detail,omitempty"`
}

// ConsoleResult is what a console request did.
type ConsoleResult struct {
	Connection string `json:"connection"`
	ReadOnly   bool   `json:"read_only"`
	// Committed is true when a read-write transaction committed.
	Committed bool              `json:"committed"`
	Results   []StatementResult `json:"results"`
	// Error is the statement that failed, after which nothing more ran and
	// the transaction rolled back.
	Error *ConsoleError `json:"error,omitempty"`
}

// Tx is the transaction a console request runs in: the postgres toolkit's.
type Tx interface {
	Query(ctx context.Context, s postgres.Statement) (*postgres.Result, error)
	Commit(ctx context.Context) error
	Rollback()
}

// Transactor opens a console request's transaction.
type Transactor interface {
	ConsoleTx(ctx context.Context, connection string, readOnly bool) (Tx, error)
}

// Toolkit is the postgres toolkit as a Transactor.
type Toolkit struct{ *postgres.Toolkit }

// ConsoleTx implements Transactor.
func (t Toolkit) ConsoleTx(ctx context.Context, connection string, readOnly bool) (Tx, error) {
	tx, err := t.Begin(ctx, postgres.TxOptions{Connection: connection, ReadOnly: readOnly})
	if err != nil {
		return nil, err //nolint:wrapcheck // the toolkit's value-free summary is the text
	}
	return tx, nil
}

// Parse is the statements a request runs, split from sql when it carries no
// list, checked against the console's bounds.
func (r ConsoleRequest) Parse() ([]ConsoleStatement, error) {
	stmts := slices.Clone(r.Statements)
	if len(stmts) == 0 {
		parts := pglex.Split(r.SQL)
		if len(parts) > 1 && r.Params != nil {
			return nil, errors.New("params bind one statement; send several as statements, each with its params")
		}
		for _, p := range parts {
			stmts = append(stmts, ConsoleStatement{SQL: p, Params: r.Params})
		}
	}
	size := 0
	for _, s := range stmts {
		size += len(s.SQL)
	}
	switch {
	case len(stmts) == 0:
		return nil, errors.New("there is no statement to run")
	case len(stmts) > MaxConsoleStatements:
		return nil, fmt.Errorf("a request runs at most %d statements", MaxConsoleStatements)
	case size > MaxConsoleSQLBytes:
		return nil, fmt.Errorf("a request carries at most %d bytes of SQL", MaxConsoleSQLBytes)
	}
	return stmts, nil
}

// IsReadOnly is whether the request runs READ ONLY.
func (r ConsoleRequest) IsReadOnly() bool { return r.ReadOnly == nil || *r.ReadOnly }

// RunConsole runs a console request on connection as the connection's own
// login, through its pool, in one transaction.
func RunConsole(ctx context.Context, kit Transactor, connection string, req ConsoleRequest) (*ConsoleResult, error) {
	stmts, err := req.Parse()
	if err != nil {
		return nil, &RefusedError{msg: err.Error()}
	}
	out := &ConsoleResult{Connection: connection, ReadOnly: req.IsReadOnly(), Results: []StatementResult{}}
	tx, err := kit.ConsoleTx(ctx, connection, out.ReadOnly)
	if err != nil {
		return nil, err //nolint:wrapcheck // the toolkit's value-free summary is the text
	}
	defer tx.Rollback()
	for i, s := range stmts {
		res, err := tx.Query(ctx, postgres.Statement{Connection: connection, SQL: s.SQL, Params: s.Params})
		if err != nil {
			out.Error = consoleError(i, err)
			return out, nil
		}
		out.Results = append(out.Results, StatementResult{
			Index: i, Command: res.Command, RowsAffected: res.RowsAffected,
			Columns: res.Columns, Rows: res.Rows, Truncated: res.Truncated,
		})
	}
	if !out.ReadOnly {
		if err := tx.Commit(ctx); err != nil {
			out.Error = consoleError(len(stmts), err)
			return out, nil
		}
		out.Committed = true
	}
	return out, nil
}

// consoleError reads a failed statement.
func consoleError(i int, err error) *ConsoleError {
	e := &ConsoleError{Index: i, Code: postgres.CodeStatementFailed, Summary: err.Error()}
	if f, ok := errors.AsType[*postgres.FailedError](err); ok {
		e.Code, e.Detail = f.Code, f.CallerText()
	}
	if r, ok := errors.AsType[*postgres.RefusedError](err); ok {
		e.Code = r.Code
	}
	return e
}

// ParamShape is a bound parameter as the audit row records it: its name or
// position and the JSON type of its value, never the value.
type ParamShape struct {
	Name     string `json:"name,omitempty"`
	Position int    `json:"position,omitempty"`
	Type     string `json:"type"`
}

// Shapes reads the parameters of a statement into their shapes.
func Shapes(params any) []ParamShape {
	out := []ParamShape{}
	switch p := params.(type) {
	case []any:
		for i, v := range p {
			out = append(out, ParamShape{Position: i + 1, Type: jsonType(v)})
		}
	case map[string]any:
		names := make([]string, 0, len(p))
		for k := range p {
			names = append(names, k)
		}
		slices.Sort(names)
		for _, k := range names {
			out = append(out, ParamShape{Name: k, Type: jsonType(p[k])})
		}
	}
	return out
}

// jsonType names the JSON type of a decoded value.
func jsonType(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return "number"
	}
}
