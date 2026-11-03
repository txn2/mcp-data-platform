package postgres

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// The isolation levels a script's transaction takes.
const (
	IsolationReadCommitted  = "read_committed"
	IsolationRepeatableRead = "repeatable_read"
	IsolationSerializable   = "serializable"
)

// isolationLevels maps each level a script names to pgx's.
var isolationLevels = map[string]pgx.TxIsoLevel{
	"":                      pgx.ReadCommitted,
	IsolationReadCommitted:  pgx.ReadCommitted,
	IsolationRepeatableRead: pgx.RepeatableRead,
	IsolationSerializable:   pgx.Serializable,
}

// The reasons a transaction ended before its script ended it, as the next
// call on it reads them.
const (
	endedCommitted    = "it was committed"
	endedCommitFailed = "its commit failed, so nothing in it was kept"
	endedRolledBack   = "it was rolled back"
	endedRunEnded     = "the run that opened it ended"
	endedStmtFailed   = "a statement in it failed, and PostgreSQL ends a transaction at its first failure"
	endedShutdown     = "the replica is shutting down"
	endedIdleFmt      = "it went %s without a statement (transaction_idle_timeout)"
	idleServerMargin  = 5 * time.Second
	endRollbackLimit  = 5 * time.Second
)

// How a script transaction ended, as appdb_transactions_total labels it.
const (
	txCommitted   = "committed"
	txRolledBack  = "rolled_back"
	txRetried     = "retried"
	txIdleTimeout = "idle_timeout"
)

// TxOptions is how a script's transaction is opened (#2402).
type TxOptions struct {
	Connection string
	// Isolation is read_committed (the default), repeatable_read or
	// serializable.
	Isolation string
	// ReadOnly opens a READ ONLY transaction, the only kind a read_only
	// connection opens.
	ReadOnly bool
	// Attempt numbers the tries of a retried transaction from 1; a later try
	// is counted as a retry.
	Attempt int
}

// Tx is one transaction a script holds across its calls (#2402): opened by
// platform.transaction, used by tx.query, tx.execute and tx.batch, and
// committed when the script's function returns. It is held by the run that
// opened it and never by an agent.
//
// It holds a pooled connection for its whole life, so it ends on its own when
// it should not keep one: when the run that opened it ends (finished, failed,
// timed out, shed by admission, drained at shutdown), when it goes
// transaction_idle_timeout without a statement, when a statement in it fails,
// and when the toolkit closes. Each rolls it back, and the next call on it
// fails naming the reason.
type Tx struct {
	c        *conn
	tx       pgx.Tx
	readOnly bool
	idle     time.Duration

	mu     sync.Mutex
	ended  string
	timer  *time.Timer
	unhook func() bool
	forget func()
}

// Begin opens a transaction on an application database. ctx is the run's: the
// transaction is rolled back when it ends.
func (t *Toolkit) Begin(ctx context.Context, o TxOptions) (*Tx, error) {
	c, ok := t.lookup(o.Connection)
	if !ok {
		return nil, &RefusedError{Code: CodeUnknownConnection, msg: fmt.Sprintf(
			"%s: no application database named %q", errPrefix, o.Connection)}
	}
	level, ok := isolationLevels[o.Isolation]
	if !ok {
		return nil, &RefusedError{Code: CodeInvalidStatement, msg: fmt.Sprintf(
			"%s: isolation %q is not one of %s, %s or %s", errPrefix, o.Isolation,
			IsolationReadCommitted, IsolationRepeatableRead, IsolationSerializable)}
	}
	if c.cfg.ReadOnly && !o.ReadOnly {
		return nil, &RefusedError{Code: CodeReadOnly, msg: fmt.Sprintf(
			"%s: connection %q is read_only; open the transaction with read_only = True", errPrefix, o.Connection)}
	}
	access := pgx.ReadWrite
	if o.ReadOnly {
		access = pgx.ReadOnly
	}
	tx, err := c.open(ctx, pgx.TxOptions{IsoLevel: level, AccessMode: access})
	observe(c.cfg.ConnectionName, err)
	if err != nil {
		return nil, c.failure(ctx, err)
	}
	if o.Attempt > 1 {
		c.rec().RecordAppDBTransaction(ctx, c.cfg.ConnectionName, txRetried)
	}
	idle := c.cfg.TransactionIdle
	if idle <= 0 {
		idle = DefaultTransactionIdle
	}
	x := &Tx{c: c, tx: tx, readOnly: o.ReadOnly, idle: idle}
	x.timer = time.AfterFunc(x.idle, func() { x.end(fmt.Sprintf(endedIdleFmt, x.idle), txIdleTimeout) })
	x.unhook = context.AfterFunc(ctx, func() { x.end(endedRunEnded, txRolledBack) })
	x.forget = t.hold(x)
	return x, nil
}

// open begins the transaction and bounds it in the same round trip: every
// statement by the connection's statement_timeout, and the session by the
// idle bound plus a margin, so the server ends a transaction the platform
// somehow did not.
func (c *conn) open(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error) {
	opts.BeginQuery = beginSQL(opts, c.cfg.StatementTimeout, max(c.cfg.TransactionIdle, DefaultTransactionIdle)+idleServerMargin)
	return c.begin(ctx, opts)
}

// Connection is the connection the transaction is on.
func (x *Tx) Connection() string { return x.c.cfg.ConnectionName }

// Query runs one statement in the transaction and keeps up to the limit's
// rows. The rest are read and dropped rather than canceled at the server: a
// canceled statement fails, and a failure ends the transaction.
func (x *Tx) Query(ctx context.Context, s Statement) (*Result, error) {
	limit := x.c.cfg.MaxRows
	if s.Limit > 0 && s.Limit < limit {
		limit = s.Limit
	}
	return x.statement(ctx, s, limit, drainWriteRows)
}

// Execute runs one statement in the transaction. Its RETURNING rows are read
// to the end and the ones past max_rows dropped.
func (x *Tx) Execute(ctx context.Context, s Statement) (*Result, error) {
	if x.readOnly {
		return nil, &RefusedError{Code: CodeReadOnly, msg: errPrefix + ": the transaction is read_only; tx.execute writes nothing in it"}
	}
	return x.statement(ctx, s, x.c.cfg.MaxRows, drainWriteRows)
}

// statement runs one statement in the transaction.
func (x *Tx) statement(ctx context.Context, s Statement, limit int, stop cancelOnCap) (*Result, error) {
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
	ctx, cancel := context.WithTimeout(ctx, effectiveTimeout(x.c.cfg, s.TimeoutSeconds)+graceAfterTimeout)
	defer cancel()
	set, err := x.c.runStatement(ctx, x.tx, stmt, limit, stop)
	observe(x.c.cfg.ConnectionName, err)
	if err != nil {
		x.failed(err)
		return nil, x.c.failure(ctx, err)
	}
	return x.result(set, limit), nil
}

// Batch runs statements in the transaction pipelined: sent together and
// answered in one round trip, none reading another's result. Each result is
// read to its end and the rows past max_rows dropped.
func (x *Tx) Batch(ctx context.Context, stmts []Statement) ([]*Result, error) {
	bound, err := bindAll(ctx, stmts)
	if err != nil {
		return nil, err
	}
	if err := x.enter(); err != nil {
		return nil, err
	}
	defer x.leave()
	ctx, cancel := context.WithTimeout(ctx, x.c.cfg.StatementTimeout+graceAfterTimeout)
	defer cancel()
	sets, err := x.c.runBatch(ctx, x.tx, bound, x.c.cfg.MaxRows)
	observe(x.c.cfg.ConnectionName, err)
	if err != nil {
		x.failed(err)
		return nil, x.c.failure(ctx, err)
	}
	out := make([]*Result, len(sets))
	for i, set := range sets {
		out[i] = x.result(set, x.c.cfg.MaxRows)
	}
	return out, nil
}

func (x *Tx) result(set rowSet, limit int) *Result {
	return &Result{
		Connection: x.c.cfg.ConnectionName, Command: set.command, RowsAffected: set.affected,
		Columns: set.columns, Rows: set.rows, Truncated: set.truncated, Limit: limit,
	}
}

// Commit commits the transaction. A serialization failure or deadlock found
// at commit is the commit's error, which the script's retry reads.
func (x *Tx) Commit(ctx context.Context) error {
	if err := x.enter(); err != nil {
		return err
	}
	defer x.leave()
	err := x.tx.Commit(ctx)
	observe(x.c.cfg.ConnectionName, err)
	if err != nil {
		x.settle(endedCommitFailed)
	} else {
		x.settle(endedCommitted)
	}
	if err != nil {
		x.c.rec().RecordAppDBTransaction(ctx, x.c.cfg.ConnectionName, txRolledBack)
		return x.c.failure(ctx, err)
	}
	x.c.rec().RecordAppDBTransaction(ctx, x.c.cfg.ConnectionName, txCommitted)
	return nil
}

// Rollback rolls the transaction back. Rolling back a transaction that already
// ended does nothing.
func (x *Tx) Rollback() {
	x.end(endedRolledBack, txRolledBack)
}

// Abandon rolls the transaction back because the toolkit or the run is going
// away.
func (x *Tx) Abandon() {
	x.end(endedShutdown, txRolledBack)
}

// enter claims the transaction for one call: it fails when the transaction
// ended, and stops the idle clock while the call runs.
func (x *Tx) enter() error {
	x.mu.Lock()
	if x.ended != "" {
		reason := x.ended
		x.mu.Unlock()
		return &FailedError{Code: CodeTransactionEnded, Summary: fmt.Sprintf(
			"%s: the transaction on connection %q is over: %s", errPrefix, x.c.cfg.ConnectionName, reason)}
	}
	x.timer.Stop()
	return nil
}

// leave ends a call: the idle clock starts again unless the transaction ended.
func (x *Tx) leave() {
	if x.ended == "" {
		x.timer.Reset(x.idle)
	}
	x.mu.Unlock()
}

// failed ends the transaction after a statement in it failed. Called with the
// lock held.
func (x *Tx) failed(err error) {
	// A statement the platform refused before sending it left the
	// transaction as it was.
	if _, refused := errors.AsType[*refusedInputError](err); refused {
		return
	}
	x.rollbackLocked(endedStmtFailed)
	x.c.rec().RecordAppDBTransaction(context.Background(), x.c.cfg.ConnectionName, txRolledBack)
}

// settle marks the transaction over after a commit. Called with the lock held.
func (x *Tx) settle(reason string) {
	x.ended = reason
	x.release()
}

// end rolls the transaction back for reason unless it already ended.
func (x *Tx) end(reason, outcome string) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.ended != "" {
		return
	}
	x.rollbackLocked(reason)
	x.c.rec().RecordAppDBTransaction(context.Background(), x.c.cfg.ConnectionName, outcome)
}

// rollbackLocked rolls back and marks the transaction over. Called with the
// lock held.
func (x *Tx) rollbackLocked(reason string) {
	x.ended = reason
	ctx, cancel := context.WithTimeout(context.Background(), endRollbackLimit)
	defer cancel()
	_ = x.tx.Rollback(ctx)
	x.release()
}

// release lets go of everything that would end the transaction later.
func (x *Tx) release() {
	x.timer.Stop()
	if x.unhook != nil {
		x.unhook()
	}
	if x.forget != nil {
		x.forget()
	}
}

// hold registers an open transaction so Close rolls it back, and returns the
// function that unregisters it.
func (t *Toolkit) hold(x *Tx) func() {
	t.openMu.Lock()
	defer t.openMu.Unlock()
	if t.open == nil {
		t.open = make(map[*Tx]struct{})
	}
	t.open[x] = struct{}{}
	return func() {
		t.openMu.Lock()
		defer t.openMu.Unlock()
		delete(t.open, x)
	}
}

// abandonOpen rolls back every transaction still open, before the pools close:
// a pool's Close waits for every connection a transaction holds.
func (t *Toolkit) abandonOpen() {
	t.openMu.Lock()
	open := make([]*Tx, 0, len(t.open))
	for x := range t.open {
		open = append(open, x)
	}
	t.openMu.Unlock()
	for _, x := range open {
		x.Abandon()
	}
}

// Batch runs statements on a connection pipelined in one transaction of their
// own, which commits only when every statement in it succeeded (#2402). On a
// read_only connection the transaction is READ ONLY and rolled back.
func (t *Toolkit) Batch(ctx context.Context, connection string, stmts []Statement) ([]*Result, error) {
	c, ok := t.lookup(connection)
	if !ok {
		return nil, &RefusedError{Code: CodeUnknownConnection, msg: fmt.Sprintf(
			"%s: no application database named %q", errPrefix, connection)}
	}
	bound, err := bindAll(ctx, stmts)
	if err != nil {
		return nil, err
	}
	access := pgx.ReadWrite
	if c.cfg.ReadOnly {
		access = pgx.ReadOnly
	}
	ctx, cancel := context.WithTimeout(ctx, c.cfg.StatementTimeout+graceAfterTimeout)
	defer cancel()
	out, err := c.inTx(ctx, pgx.TxOptions{AccessMode: access}, c.cfg.StatementTimeout, !c.cfg.ReadOnly, func(tx pgx.Tx) (any, error) {
		return c.runBatch(ctx, tx, bound, c.cfg.MaxRows)
	})
	observe(c.cfg.ConnectionName, err)
	if err != nil {
		return nil, c.failure(ctx, err)
	}
	sets, _ := out.([]rowSet)
	results := make([]*Result, len(sets))
	for i, set := range sets {
		results[i] = &Result{
			Connection: connection, Command: set.command, RowsAffected: set.affected,
			Columns: set.columns, Rows: set.rows, Truncated: set.truncated, Limit: c.cfg.MaxRows,
		}
	}
	return results, nil
}

// MaxBatch is the most statements one batch may carry.
const MaxBatch = 1000

// bindAll binds each statement's params.
func bindAll(ctx context.Context, stmts []Statement) ([]boundStatement, error) {
	if len(stmts) == 0 {
		return nil, &RefusedError{Code: CodeInvalidStatement, msg: errPrefix + ": the batch holds no statement"}
	}
	if len(stmts) > MaxBatch {
		return nil, &RefusedError{Code: CodeInvalidStatement, msg: fmt.Sprintf(
			"%s: the batch holds %d statements, more than %d", errPrefix, len(stmts), MaxBatch)}
	}
	out := make([]boundStatement, len(stmts))
	for i, s := range stmts {
		if s.SQL == "" {
			return nil, &RefusedError{Code: CodeInvalidStatement, msg: fmt.Sprintf("%s: statement %d of the batch is empty", errPrefix, i+1)}
		}
		b, err := bindValues(ctx, s.SQL, s.Params)
		if err != nil {
			return nil, &RefusedError{Code: CodeInvalidStatement, msg: fmt.Sprintf("%s: statement %d of the batch: %v", errPrefix, i+1, err)}
		}
		out[i] = b
	}
	return out, nil
}
