package postgres

import (
	"context"
	"fmt"
	"strconv"
	"sync"

	"github.com/jackc/pgx/v5"
)

// Cursor reads one query's result in pages from a server-side cursor (#2454),
// so a script processes a result larger than max_rows holding one page at a
// time. It is declared in the script's transaction (tx.query_iter) or in a
// READ ONLY transaction of its own (platform.query_iter), which it ends when
// it closes.
type Cursor struct {
	c        *conn
	tx       pgx.Tx
	x        *Tx
	name     string
	pageRows int
	columns  []Column
	types    []typeInfo

	mu     sync.Mutex
	read   int64
	closed bool
	unhook func() bool
}

// OpenCursor declares a cursor over s in a READ ONLY transaction of its own.
// ctx is the run's: the transaction ends when it does.
func (t *Toolkit) OpenCursor(ctx context.Context, s Statement, pageRows int) (*Cursor, error) {
	c, stmt, err := t.prepare(ctx, s)
	if err != nil {
		return nil, err
	}
	tx, err := c.open(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	observe(c.cfg.ConnectionName, err)
	if err != nil {
		return nil, c.failure(ctx, err)
	}
	k := &Cursor{c: c, tx: tx, name: "mcp_cursor", pageRows: pageSize(c.cfg, pageRows)}
	if err := k.declare(ctx, stmt, s.TimeoutSeconds); err != nil {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return nil, c.failure(ctx, err)
	}
	k.unhook = context.AfterFunc(ctx, k.Close)
	return k, nil
}

// OpenCursor declares a cursor over s in the transaction. It ends with the
// transaction, and a failure reading it ends the transaction as a failed
// statement does.
func (x *Tx) OpenCursor(ctx context.Context, s Statement, pageRows int) (*Cursor, error) {
	if s.SQL == "" {
		return nil, &RefusedError{Code: CodeInvalidStatement, msg: errPrefix + ": the statement is empty"}
	}
	stmt, err := bindValues(ctx, s.SQL, s.Params)
	if err != nil {
		return nil, &RefusedError{Code: CodeInvalidStatement, msg: errPrefix + ": " + err.Error()}
	}
	if err := x.enter(); err != nil {
		return nil, err
	}
	defer x.leave()
	if err := x.usableAfterFailure(); err != nil {
		return nil, err
	}
	x.cursors++
	k := &Cursor{c: x.c, tx: x.tx, x: x, name: "mcp_cursor_" + strconv.Itoa(x.cursors), pageRows: pageSize(x.c.cfg, pageRows)}
	if err := k.declare(ctx, stmt, s.TimeoutSeconds); err != nil {
		x.failed(err)
		return nil, x.c.failure(ctx, err)
	}
	return k, nil
}

// pageSize is the rows one page reads: what the caller asked for, the
// connection's max_rows when it asked for none, never above MaxMaxRows.
func pageSize(cfg Config, asked int) int {
	if asked <= 0 {
		return cfg.MaxRows
	}
	return min(asked, MaxMaxRows)
}

// declare describes the query, which types its parameters and columns, and
// declares the cursor over it with the parameters bound.
func (k *Cursor) declare(ctx context.Context, stmt boundStatement, seconds int) error {
	if err := refuseTransactionControl(stmt.sql); err != nil {
		return refuse(err)
	}
	ctx, cancel := context.WithTimeout(ctx, effectiveTimeout(k.c.cfg, seconds)+graceAfterTimeout)
	defer cancel()
	sd, err := k.c.describe(ctx, k.tx, stmt.sql)
	if err != nil {
		return err
	}
	if len(sd.ParamOIDs) != len(stmt.args) {
		return refuse(fmt.Errorf("the statement takes %d parameters and params holds %d", len(sd.ParamOIDs), len(stmt.args)))
	}
	fieldOIDs := make([]uint32, len(sd.Fields))
	for i, f := range sd.Fields {
		fieldOIDs[i] = f.DataTypeOID
	}
	oids := append(append([]uint32{}, sd.ParamOIDs...), fieldOIDs...)
	types, err := k.c.typesOf(ctx, k.tx, oids)
	if err != nil {
		return err
	}
	args, err := encodeArgs(stmt.args, types[:len(sd.ParamOIDs)])
	if err != nil {
		return refuse(err)
	}
	k.types = types[len(sd.ParamOIDs):]
	k.columns = make([]Column, len(sd.Fields))
	for i, f := range sd.Fields {
		k.columns[i] = Column{Name: f.Name, Type: k.types[i].display}
	}
	// The cursor's name is the platform's; the query is the caller's, its
	// values bound, never written into the text.
	_, err = k.tx.Exec(ctx, "DECLARE "+k.name+" NO SCROLL CURSOR FOR "+stmt.sql, args...)
	return err //nolint:wrapcheck // read by its type
}

// IterLimitError is a loop that read past the connection's iter_max_rows.
type IterLimitError struct {
	Connection string
	Limit      int64
}

func (e *IterLimitError) Error() string {
	return fmt.Sprintf("%s: the loop read more than iter_max_rows (%d) rows from connection %q; narrow the query, "+
		"or raise iter_max_rows on the connection", errPrefix, e.Limit, e.Connection)
}

// Next reads the next page. done is true when the result has no rows after
// this page; a page past iter_max_rows is refused.
func (k *Cursor) Next(ctx context.Context) (page *Result, done bool, err error) {
	if k.x != nil {
		if err := k.x.enter(); err != nil {
			return nil, true, err
		}
		defer k.x.leave()
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.closed {
		return nil, true, &FailedError{Code: CodeTransactionEnded, Summary: errPrefix + ": the cursor is closed"}
	}
	ctx, cancel := context.WithTimeout(ctx, k.c.cfg.StatementTimeout+graceAfterTimeout)
	defer cancel()
	set, err := read(ctx, k.tx, "FETCH FORWARD "+strconv.Itoa(k.pageRows)+" FROM "+k.name, nil, readPlan{
		set: rowSet{columns: k.columns, rows: make([][]any, 0)}, types: k.types, limit: k.pageRows, stop: drainWriteRows,
	})
	observe(k.c.cfg.ConnectionName, err)
	if err != nil {
		if k.x != nil {
			k.x.failed(err)
		}
		return nil, true, k.c.failure(ctx, err)
	}
	k.read += int64(len(set.rows))
	if k.read > k.c.cfg.IterMaxRows {
		return nil, true, &IterLimitError{Connection: k.c.cfg.ConnectionName, Limit: k.c.cfg.IterMaxRows}
	}
	return &Result{Connection: k.c.cfg.ConnectionName, Command: "FETCH", Columns: k.columns, Rows: set.rows, Limit: k.pageRows},
		len(set.rows) < k.pageRows, nil
}

// Columns is the result's columns, known once the cursor is declared.
func (k *Cursor) Columns() []Column { return k.columns }

// Close closes the cursor, and ends the transaction it opened for itself.
// Closing a closed cursor does nothing.
func (k *Cursor) Close() {
	if k.x != nil {
		if k.x.enter() != nil {
			return
		}
		defer k.x.leave()
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.closed {
		return
	}
	k.closed = true
	if k.unhook != nil {
		k.unhook()
	}
	ctx, cancel := context.WithTimeout(context.Background(), endRollbackLimit)
	defer cancel()
	if k.x == nil {
		_ = k.tx.Rollback(ctx)
		return
	}
	// In the script's transaction the cursor is closed only while it exists
	// and the transaction can run a statement: a savepoint rolled back over
	// its DECLARE has closed it already, and a CLOSE of a cursor that does
	// not exist would fail the transaction.
	if k.x.ended != "" || k.x.aborted != nil {
		return
	}
	var open bool
	if err := k.tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_catalog.pg_cursors WHERE name = $1)`, k.name).Scan(&open); err == nil && open {
		_, _ = k.tx.Exec(ctx, "CLOSE "+k.name)
	}
}
