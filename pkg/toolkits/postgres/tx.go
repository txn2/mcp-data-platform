package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

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

// refusedInputError is the toolkit's own refusal of a statement or its params,
// made before or instead of the database's answer.
type refusedInputError struct{ err error }

func (r *refusedInputError) Error() string { return r.err.Error() }
func (r *refusedInputError) Unwrap() error { return r.err }

// refuse marks err as a refusal of the caller's input.
func refuse(err error) error { return &refusedInputError{err: err} }
