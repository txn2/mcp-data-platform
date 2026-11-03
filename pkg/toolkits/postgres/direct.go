package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Statement is one statement a script or an endpoint runs on an application
// database (#2428). The script host calls the toolkit directly rather than
// through an MCP tool: an application's request path runs many statements and
// cannot pay a tool call, its middleware and an audit row for each.
type Statement struct {
	Connection string
	// SQL is one statement. Placeholders are $1, $2, ... when Params is a
	// list and @name when it is a map.
	SQL string
	// Params are the values bound to the placeholders: a []any binds $n in
	// order, a map[string]any binds @name. Values are JSON-compatible (string,
	// json.Number or a Go number, bool, nil, []any, map[string]any) and are
	// read by the type PostgreSQL gives each parameter.
	Params any
	// Limit caps the rows read; zero or more than the connection's max_rows
	// is the connection's max_rows.
	Limit int
	// TimeoutSeconds lowers the connection's statement_timeout for this
	// statement; zero keeps it.
	TimeoutSeconds int
}

// Result is what a statement returned.
type Result struct {
	Connection string
	// Command is the command tag's first word: SELECT, INSERT, UPDATE.
	Command      string
	RowsAffected int64
	Columns      []Column
	Rows         [][]any
	// Truncated is whether the statement returned more rows than Limit.
	Truncated bool
	Limit     int
}

// RefusedError is the toolkit's refusal of a statement before or instead of
// the database's answer: an unknown connection, a write on a read_only
// connection, params that do not fit the statement. Its text names no bound
// value.
type RefusedError struct {
	// Code is the platform error code a script branches on.
	Code string
	msg  string
}

// Error implements error.
func (e *RefusedError) Error() string { return e.msg }

// Error codes a direct statement is refused with.
const (
	CodeUnknownConnection = "unknown_connection"
	CodeReadOnly          = "read_only_connection"
	CodeInvalidStatement  = "invalid_statement"
	CodeStatementFailed   = "statement_failed"
	CodeUnavailable       = "database_unavailable"
	CodePoolWait          = "pool_wait_timeout"
	CodeTransactionEnded  = "transaction_ended"
)

// FailedError is a statement the database refused or could not run. Error()
// is the value-free summary (the SQLSTATE and the objects the server named),
// which is what a run's error, its audit record and the logs may carry.
// Statement holds what the server said, DETAIL included, which can repeat a
// bound value and so is handed to the script alone.
type FailedError struct {
	Code string
	// Summary is the value-free account of the failure, and the error's text.
	Summary   string
	Statement *StatementError
}

// Error implements error.
func (e *FailedError) Error() string { return e.Summary }

// CallerText is the server's own account of the failure, DETAIL and HINT
// included, for the script that ran the statement and nothing else.
func (e *FailedError) CallerText() string {
	if e.Statement == nil {
		return e.Summary
	}
	return callerText(e.Statement)
}

// Query runs one statement in a READ ONLY transaction and reads up to the
// limit's rows. A statement that writes fails in that transaction.
func (t *Toolkit) Query(ctx context.Context, s Statement) (*Result, error) {
	c, stmt, err := t.prepare(ctx, s)
	if err != nil {
		return nil, err
	}
	limit := c.cfg.MaxRows
	if s.Limit > 0 && s.Limit < limit {
		limit = s.Limit
	}
	return c.run(ctx, stmt, runPlan{seconds: s.TimeoutSeconds, access: pgx.ReadOnly, limit: limit, stop: stopReadAtCap})
}

// Execute runs one statement in a transaction of its own and commits it. Its
// RETURNING rows are read to the end and the ones past max_rows dropped.
func (t *Toolkit) Execute(ctx context.Context, s Statement) (*Result, error) {
	c, stmt, err := t.prepare(ctx, s)
	if err != nil {
		return nil, err
	}
	if c.cfg.ReadOnly {
		return nil, &RefusedError{Code: CodeReadOnly, msg: fmt.Sprintf(
			"%s: connection %q is read_only and runs no platform.execute; use platform.query to read", errPrefix, s.Connection)}
	}
	return c.run(ctx, stmt, runPlan{seconds: s.TimeoutSeconds, access: pgx.ReadWrite, commit: true, limit: c.cfg.MaxRows, stop: drainWriteRows})
}

// prepare finds the connection and binds the params.
func (t *Toolkit) prepare(ctx context.Context, s Statement) (*conn, boundStatement, error) {
	if s.SQL == "" {
		return nil, boundStatement{}, &RefusedError{Code: CodeInvalidStatement, msg: errPrefix + ": the statement is empty"}
	}
	c, ok := t.lookup(s.Connection)
	if !ok {
		return nil, boundStatement{}, &RefusedError{Code: CodeUnknownConnection, msg: fmt.Sprintf(
			"%s: no application database named %q", errPrefix, s.Connection)}
	}
	stmt, err := bindValues(ctx, s.SQL, s.Params)
	if err != nil {
		return nil, boundStatement{}, &RefusedError{Code: CodeInvalidStatement, msg: errPrefix + ": " + err.Error()}
	}
	return c, stmt, nil
}

// runPlan is how one statement runs: its timeout, its transaction's access
// mode, whether that transaction commits, and how its rows are read.
type runPlan struct {
	seconds int
	access  pgx.TxAccessMode
	commit  bool
	limit   int
	stop    cancelOnCap
}

// run executes stmt in one transaction bounded by the effective timeout.
func (c *conn) run(ctx context.Context, stmt boundStatement, p runPlan) (*Result, error) {
	timeout := effectiveTimeout(c.cfg, p.seconds)
	ctx, cancel := context.WithTimeout(ctx, timeout+graceAfterTimeout)
	defer cancel()
	set, err := c.inTx(ctx, pgx.TxOptions{AccessMode: p.access}, timeout, p.commit, func(tx pgx.Tx) (any, error) {
		return c.runStatement(ctx, tx, stmt, p.limit, p.stop)
	})
	observe(c.cfg.ConnectionName, err)
	if err != nil {
		return nil, c.failure(ctx, err)
	}
	rs, _ := set.(rowSet)
	return &Result{
		Connection: c.cfg.ConnectionName, Command: rs.command, RowsAffected: rs.affected,
		Columns: rs.columns, Rows: rs.rows, Truncated: rs.truncated, Limit: p.limit,
	}, nil
}

// failure reads err into the error a script is handed, naming the statement
// of a batch that failed.
func (c *conn) failure(ctx context.Context, err error) error {
	out := c.failureOf(ctx, err)
	if b, ok := errors.AsType[*batchError](err); ok {
		if f, ok := errors.AsType[*FailedError](out); ok {
			f.Summary = fmt.Sprintf("statement %d of the batch: %s", b.n, f.Summary)
		}
	}
	return out
}

// failureOf reads err into the error a script is handed.
func (c *conn) failureOf(ctx context.Context, err error) error {
	if refused, ok := errors.AsType[*refusedInputError](err); ok {
		return &RefusedError{Code: CodeInvalidStatement, msg: errPrefix + ": " + refused.err.Error()}
	}
	if waited, ok := errors.AsType[*poolWaitError](err); ok {
		return &FailedError{Code: CodePoolWait, Summary: waited.Error()}
	}
	// A connection the server refused (a wrong password is 28P01) carries a
	// PostgreSQL error inside pgconn's ConnectError; it is the connection that
	// failed, not a statement.
	if se := statementError(err); se != nil && !isConnectError(err) {
		return &FailedError{Code: CodeStatementFailed, Summary: se.summary(), Statement: se}
	}
	name := c.cfg.ConnectionName
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return &FailedError{Code: CodeStatementFailed, Summary: fmt.Sprintf(
			"%s: the statement on connection %q ran out of time", errPrefix, name)}
	}
	return &FailedError{Code: CodeUnavailable, Summary: fmt.Sprintf(
		"%s: connection %q could not reach its database: %s", errPrefix, name, scrubDriverError(err))}
}
