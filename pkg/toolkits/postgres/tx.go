package postgres

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// inTx runs fn in a transaction with every statement bounded by timeout, and
// commits when commit is set and fn succeeded; otherwise it rolls back. A
// query's READ ONLY transaction is always rolled back: it changed nothing.
func (c *conn) inTx(ctx context.Context, opts pgx.TxOptions, timeout time.Duration, commit bool, fn func(pgx.Tx) (any, error)) (any, error) {
	opts.BeginQuery = beginSQL(opts, timeout, 0)
	tx, err := c.begin(ctx, opts)
	if err != nil {
		return nil, err //nolint:wrapcheck // read by its type
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
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

// refusedInputError is the toolkit's own refusal of a statement or its params,
// made before or instead of the database's answer.
type refusedInputError struct{ err error }

func (r *refusedInputError) Error() string { return r.err.Error() }
func (r *refusedInputError) Unwrap() error { return r.err }

// refuse marks err as a refusal of the caller's input.
func refuse(err error) error { return &refusedInputError{err: err} }

// poolWaitError is a statement that found no free pooled connection within
// the connection's pool_wait_timeout.
type poolWaitError struct {
	connection string
	maxConns   int
	wait       time.Duration
}

func (e *poolWaitError) Error() string {
	return fmt.Sprintf("%s: connection %q had no free pooled connection within %s (pool_wait_timeout); "+
		"all %d of its connections on this replica are in use (max_conns)", errPrefix, e.connection, e.wait, e.maxConns)
}

// acquire takes a connection from pool. pool_wait_timeout bounds the wait
// for a connection other calls hold; a pool with room opens a new one, and
// that is bounded by the connect timeout instead, so a slow connect is never
// reported as a full pool.
func (c *conn) acquire(ctx context.Context, pool *pgxpool.Pool) (*pgxpool.Conn, error) {
	waitCtx, cancel := context.WithTimeout(ctx, c.cfg.PoolWait)
	pc, err := pool.Acquire(waitCtx)
	timedOut := waitCtx.Err() != nil && ctx.Err() == nil
	cancel()
	if err == nil || !timedOut {
		return pc, err //nolint:wrapcheck // read by its type
	}
	if s := pool.Stat(); s.AcquiredConns() < s.MaxConns() {
		openCtx, cancelOpen := context.WithTimeout(ctx, DefaultConnectTimeout)
		defer cancelOpen()
		return pool.Acquire(openCtx) //nolint:wrapcheck // read by its type
	}
	c.rec().RecordAppDBPoolWaitTimeout(ctx, c.cfg.ConnectionName)
	return nil, &poolWaitError{connection: c.cfg.ConnectionName, maxConns: c.cfg.MaxConns, wait: c.cfg.PoolWait}
}

// poolBegin takes a connection from the pool, waiting at most pool_wait_timeout
// for one, and opens a transaction on it. The connection goes back to the pool
// when the transaction ends.
//
// The pool is the one the identity on ctx runs as (#2403), and the
// transaction's first statement sets that identity's claims.
func (c *conn) poolBegin(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error) {
	id := IdentityOf(ctx)
	pool, err := c.poolFor(id.Persona)
	if err != nil {
		return nil, err
	}
	pc, err := c.acquire(ctx, pool)
	if err != nil {
		return nil, err
	}
	tx, err := pc.BeginTx(ctx, opts)
	if err != nil {
		pc.Release()
		return nil, err //nolint:wrapcheck // read by its type
	}
	rtx := &releasingTx{Tx: tx, release: pc.Release}
	// Sent below pgx's query path, so the statement tracer neither counts
	// nor times it as one of the caller's.
	if _, err := tx.Conn().PgConn().ExecParams(ctx, claimsSQL, id.claims(), nil, nil, nil).Close(); err != nil {
		_ = rtx.Rollback(context.WithoutCancel(ctx))
		return nil, err //nolint:wrapcheck // read by its type
	}
	return rtx, nil
}

// releasingTx returns its connection to the pool when it ends.
type releasingTx struct {
	pgx.Tx
	once    sync.Once
	release func()
}

// Commit implements pgx.Tx.
func (r *releasingTx) Commit(ctx context.Context) error {
	err := r.Tx.Commit(ctx)
	r.once.Do(r.release)
	return err //nolint:wrapcheck // read by its type
}

// Rollback implements pgx.Tx.
func (r *releasingTx) Rollback(ctx context.Context) error {
	err := r.Tx.Rollback(ctx)
	r.once.Do(r.release)
	return err //nolint:wrapcheck // read by its type
}
