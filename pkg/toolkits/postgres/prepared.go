package postgres

// The statements the statement cache prepared and the batches sent pipelined,
// both sent through pgconn below pgx's query path. They run only on a live
// physical connection, so their tests are the RealDB tests in
// postgres_realdb_integration_test.go and txn_realdb_integration_test.go,
// which produce no unit coverage profile; the encoding and naming they share
// lives in wire.go and is unit-tested there.

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// readPrepared runs a statement the cache prepared and maps its rows.
//
// It is sent through pgconn rather than pgx's query path because pgx asks for
// binary results of a prepared statement whose column types it knows, and a
// type outside the mapping (interval, inet, money) is read as the text the
// server sent for it (mapRow). Every parameter is already text (encodeArgs)
// and every result is asked for as text, which is what the exec-mode path
// sends and receives.
func (c *conn) readPrepared(ctx context.Context, tx pgx.Tx, sd *pgconn.StatementDescription, args []any, p readPlan) (rowSet, error) {
	values, err := textValues(args)
	if err != nil {
		return rowSet{}, refuse(err)
	}
	qctx, cancel := context.WithCancel(ctx)
	defer cancel()
	pc := tx.Conn()
	qctx = c.traceStart(qctx, pc, sd.SQL)
	rr := pc.PgConn().ExecPrepared(qctx, sd.Name, values, nil, nil)
	set, err := collect(pgx.RowsFromResultReader(pc.TypeMap(), rr), cancel, p)
	if err == nil {
		set.command, set.affected = commandOf(rr.Close())
	}
	c.traceEnd(qctx, pc, err)
	return set, err
}

// runBatch runs bound statements in tx pipelined: each is described (from the
// cache when it is on), then all are sent together and answered in one round
// trip. Every result is read to its end and the rows past limit dropped.
func (c *conn) runBatch(ctx context.Context, tx pgx.Tx, stmts []boundStatement, limit int) ([]rowSet, error) {
	plans, values, err := c.planBatch(ctx, tx, stmts, limit)
	if err != nil {
		return nil, err
	}
	batch := &pgconn.Batch{}
	for i, stmt := range stmts {
		if c.cache != nil {
			batch.ExecPrepared(plans[i].name, values[i], nil, nil)
		} else {
			batch.ExecParams(stmt.sql, values[i], nil, nil, nil)
		}
	}
	pc := tx.Conn()
	ctx = c.traceStart(ctx, pc, batchOperation)
	mrr := pc.PgConn().ExecBatch(ctx, batch)
	sets := make([]rowSet, 0, len(stmts))
	for i := range stmts {
		if !mrr.NextResult() {
			break
		}
		rr := mrr.ResultReader()
		set, err := collect(pgx.RowsFromResultReader(pc.TypeMap(), rr), func() {}, plans[i].read)
		if err != nil {
			_ = mrr.Close()
			c.traceEnd(ctx, pc, err)
			return nil, &batchError{n: i + 1, err: err}
		}
		set.command, set.affected = commandOf(rr.Close())
		sets = append(sets, set)
	}
	err = mrr.Close()
	c.traceEnd(ctx, pc, err)
	if err != nil {
		return nil, &batchError{n: len(sets) + 1, err: err}
	}
	return sets, nil
}

// planBatch describes each statement and encodes its values for the types
// the server described.
func (c *conn) planBatch(ctx context.Context, tx pgx.Tx, stmts []boundStatement, limit int) ([]batchPlan, [][][]byte, error) {
	descs, oids, err := c.describeBatch(ctx, tx, stmts)
	if err != nil {
		return nil, nil, err
	}
	types, err := c.typesOf(ctx, tx, oids)
	if err != nil {
		return nil, nil, err
	}
	plans := make([]batchPlan, len(stmts))
	values := make([][][]byte, len(stmts))
	for i, sd := range descs {
		n := len(sd.ParamOIDs) + len(sd.Fields)
		if plans[i], values[i], err = planOne(i, sd, stmts[i].args, types[:n], limit); err != nil {
			return nil, nil, err
		}
		types = types[n:]
	}
	return plans, values, nil
}

// describeBatch describes each statement of a batch, and lists every
// parameter and column type it names, in order.
func (c *conn) describeBatch(ctx context.Context, tx pgx.Tx, stmts []boundStatement) ([]*pgconn.StatementDescription, []uint32, error) {
	descs := make([]*pgconn.StatementDescription, len(stmts))
	var oids []uint32
	for i, stmt := range stmts {
		if err := refuseTransactionControl(stmt.sql); err != nil {
			return nil, nil, refuse(fmt.Errorf(batchStmtFmt, i+1, err))
		}
		sd, err := c.describe(ctx, tx, stmt.sql)
		if err != nil {
			return nil, nil, fmt.Errorf(batchStmtFmt, i+1, err)
		}
		if len(sd.ParamOIDs) != len(stmt.args) {
			return nil, nil, refuse(fmt.Errorf("statement %d of the batch takes %d parameters and params holds %d", i+1, len(sd.ParamOIDs), len(stmt.args)))
		}
		descs[i] = sd
		oids = append(oids, sd.ParamOIDs...)
		for _, f := range sd.Fields {
			oids = append(oids, f.DataTypeOID)
		}
	}
	return descs, oids, nil
}
