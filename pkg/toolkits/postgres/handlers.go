package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// QueryInput is postgres_query's arguments.
type QueryInput struct {
	Connection     string          `json:"connection"`
	SQL            string          `json:"sql"`
	Params         json.RawMessage `json:"params,omitempty"`
	Limit          int             `json:"limit,omitempty"`
	TimeoutSeconds int             `json:"timeout_seconds,omitempty"`
}

// QueryOutput is postgres_query's result.
type QueryOutput struct {
	Connection string   `json:"connection"`
	Columns    []Column `json:"columns"`
	Rows       [][]any  `json:"rows"`
	RowCount   int      `json:"row_count"`
	// Truncated is whether the statement returned more rows than Limit.
	Truncated bool `json:"truncated"`
	Limit     int  `json:"limit"`
}

// maxStatements is the longest statements list one call runs.
const maxStatements = 100

func (t *Toolkit) handleQuery(ctx context.Context, _ *mcp.CallToolRequest, in QueryInput) (*mcp.CallToolResult, any, error) {
	c, refusal := t.resolve(in.Connection, in.SQL)
	if refusal != nil {
		return refusal, nil, nil
	}
	stmt, err := bindParams(ctx, in.SQL, in.Params)
	if err != nil {
		return inputError(err), nil, nil
	}
	limit := c.cfg.MaxRows
	if in.Limit > 0 && in.Limit < limit {
		limit = in.Limit
	}
	timeout := effectiveTimeout(c.cfg, in.TimeoutSeconds)
	ctx, cancel := context.WithTimeout(ctx, timeout+graceAfterTimeout)
	defer cancel()

	set, err := c.inTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly}, timeout, false, func(tx pgx.Tx) (any, error) {
		return c.runStatement(ctx, tx, stmt, limit, stopReadAtCap)
	})
	observe(c.cfg.ConnectionName, err)
	if err != nil {
		return failure(ctx, c.cfg.ConnectionName, err, nil), nil, nil
	}
	rs, _ := set.(rowSet)
	out := &QueryOutput{
		Connection: c.cfg.ConnectionName,
		Columns:    rs.columns,
		Rows:       rs.rows,
		RowCount:   len(rs.rows),
		Truncated:  rs.truncated,
		Limit:      limit,
	}
	return toolkit.JSONResult(out), out, nil
}

func (t *Toolkit) handleExecute(ctx context.Context, _ *mcp.CallToolRequest, in ExecuteInput) (*mcp.CallToolResult, any, error) {
	stmts, err := executeStatements(in)
	if err != nil {
		return inputError(err), nil, nil
	}
	c, refusal := t.resolve(in.Connection, stmts[0].SQL)
	if refusal != nil {
		return refusal, nil, nil
	}
	if c.cfg.ReadOnly {
		return readOnlyRefusal(c.cfg.ConnectionName), nil, nil
	}
	bound := make([]boundStatement, len(stmts))
	for i, s := range stmts {
		if bound[i], err = bindParams(ctx, s.SQL, s.Params); err != nil {
			return inputError(fmt.Errorf("statement %d: %w", i, err)), nil, nil
		}
	}
	timeout := effectiveTimeout(c.cfg, in.TimeoutSeconds)
	ctx, cancel := context.WithTimeout(ctx, timeout*time.Duration(len(bound))+graceAfterTimeout)
	defer cancel()

	var failedAt *int
	results, err := c.inTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadWrite}, timeout, true, func(tx pgx.Tx) (any, error) {
		out := make([]StatementResult, 0, len(bound))
		for i, s := range bound {
			rs, err := c.runStatement(ctx, tx, s, c.cfg.MaxRows, drainWriteRows)
			if err != nil {
				failedAt = &i
				return nil, err
			}
			out = append(out, StatementResult{Command: rs.command, RowsAffected: rs.affected, Columns: rs.columns, Rows: rs.rows, Truncated: rs.truncated})
		}
		return out, nil
	})
	observe(c.cfg.ConnectionName, err)
	if err != nil {
		return failure(ctx, c.cfg.ConnectionName, err, failedAt), nil, nil
	}
	list, _ := results.([]StatementResult)
	out := &ExecuteOutput{Connection: c.cfg.ConnectionName, Committed: true, Statements: list}
	return toolkit.JSONResult(out), out, nil
}

// executeStatements is postgres_execute's statements: sql and params as a list
// of one, or the statements list.
func executeStatements(in ExecuteInput) ([]StatementInput, error) {
	switch {
	case in.SQL != "" && len(in.Statements) > 0:
		return nil, errors.New("give sql or statements, not both")
	case in.SQL != "":
		return []StatementInput{{SQL: in.SQL, Params: in.Params}}, nil
	case len(in.Statements) == 0:
		return nil, errors.New("sql or statements is required")
	case len(in.Params) > 0:
		return nil, errors.New("params goes inside each statement of a statements list")
	case len(in.Statements) > maxStatements:
		return nil, fmt.Errorf("at most %d statements in one call", maxStatements)
	}
	for i, s := range in.Statements {
		if s.SQL == "" {
			return nil, fmt.Errorf("statement %d has no sql", i)
		}
	}
	return in.Statements, nil
}

// resolve finds the connection a call names and refuses an empty statement.
func (t *Toolkit) resolve(name, sql string) (*conn, *mcp.CallToolResult) {
	if name == "" {
		return nil, missingParameter("connection")
	}
	if sql == "" {
		return nil, missingParameter("sql")
	}
	c, ok := t.lookup(name)
	if !ok {
		return nil, (&toolError{
			code: codeNotFound, category: categoryNotFound,
			message: fmt.Sprintf("postgres: no connection named %q", name),
			hint:    "Call list_connections and use the name of a connection of kind postgres.",
		}).result()
	}
	return c, nil
}

// inTx runs fn in a transaction with every statement bounded by timeout, and
// commits when commit is set and fn succeeded; otherwise it rolls back. A
// query's READ ONLY transaction is always rolled back: it changed nothing.
func (c *conn) inTx(ctx context.Context, opts pgx.TxOptions, timeout time.Duration, commit bool, fn func(pgx.Tx) (any, error)) (any, error) {
	tx, err := c.begin(ctx, opts)
	if err != nil {
		return nil, err //nolint:wrapcheck // read by its type
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := setLocalTimeout(ctx, tx, timeout); err != nil {
		return nil, err
	}
	out, err := fn(tx)
	if err != nil {
		return nil, err
	}
	if commit {
		if err := tx.Commit(ctx); err != nil {
			return nil, err //nolint:wrapcheck // read by its type
		}
	}
	return out, nil
}

// failure is a failed call's result. A refusal the toolkit made of the
// statement or its params is the caller's input; a PostgreSQL error, a
// connection failure or a spent timeout goes through failureResult.
func failure(ctx context.Context, connection string, err error, stmt *int) *mcp.CallToolResult {
	if refused, ok := errors.AsType[*refusedInputError](err); ok {
		if stmt != nil {
			return inputError(fmt.Errorf("statement %d: %w", *stmt, refused.err))
		}
		return inputError(refused.err)
	}
	return failureResult(ctx, connection, err, stmt)
}

// refusedInputError is the toolkit's own refusal of a statement or its params,
// made before or instead of the database's answer.
type refusedInputError struct{ err error }

func (r *refusedInputError) Error() string { return r.err.Error() }
func (r *refusedInputError) Unwrap() error { return r.err }

// refuse marks err as a refusal of the caller's input.
func refuse(err error) error { return &refusedInputError{err: err} }

// inputError is a refusal of the call's arguments.
func inputError(err error) *mcp.CallToolResult {
	return (&toolError{
		code: codeInvalidStatement, category: categoryClientInput,
		message: "postgres: " + err.Error(),
		hint:    "Correct the statement or its params and retry.",
	}).result()
}

func readOnlyRefusal(connection string) *mcp.CallToolResult {
	return (&toolError{
		code: codeReadOnly, category: categoryClientInput,
		message: fmt.Sprintf("postgres: connection %q is read_only and runs no postgres_execute", connection),
		hint:    "Use postgres_query to read, or a connection that is not read_only to write.",
	}).result()
}
