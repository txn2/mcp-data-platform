package postgres

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
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

// poolBegin takes a connection from the pool, waiting at most pool_wait_timeout
// for one, and opens a transaction on it. The connection goes back to the pool
// when the transaction ends.
func (c *conn) poolBegin(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error) {
	waitCtx, cancel := context.WithTimeout(ctx, c.cfg.PoolWait)
	pc, err := c.pool.Acquire(waitCtx)
	timedOut := waitCtx.Err() != nil && ctx.Err() == nil
	cancel()
	if err != nil {
		if timedOut {
			c.rec().RecordAppDBPoolWaitTimeout(ctx, c.cfg.ConnectionName)
			return nil, &poolWaitError{connection: c.cfg.ConnectionName, maxConns: c.cfg.MaxConns, wait: c.cfg.PoolWait}
		}
		return nil, err //nolint:wrapcheck // read by its type
	}
	tx, err := pc.BeginTx(ctx, opts)
	if err != nil {
		pc.Release()
		return nil, err //nolint:wrapcheck // read by its type
	}
	return &releasingTx{Tx: tx, release: pc.Release}, nil
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
