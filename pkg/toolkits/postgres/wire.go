package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// batchError is a failure of one statement of a batch, numbered from 1.
type batchError struct {
	n   int
	err error
}

func (e *batchError) Error() string { return fmt.Sprintf("statement %d of the batch: %v", e.n, e.err) }
func (e *batchError) Unwrap() error { return e.err }

// batchOperation is what a batch's span and statement series are named by.
const batchOperation = "BATCH"

// batchPlan is how one statement of a batch is sent and read.
type batchPlan struct {
	name string
	read readPlan
}

// batchStmtFmt names the statement of a batch an error is about.
const batchStmtFmt = "statement %d of the batch: %w"

// planOne encodes one statement's values and lays out how its rows are read.
// types are its parameters' types, then its columns'.
func planOne(i int, sd *pgconn.StatementDescription, args []any, types []typeInfo, limit int) (batchPlan, [][]byte, error) {
	if len(types) != len(sd.ParamOIDs)+len(sd.Fields) {
		return batchPlan{}, nil, fmt.Errorf("statement %d of the batch: %d types for %d parameters and %d columns",
			i+1, len(types), len(sd.ParamOIDs), len(sd.Fields))
	}
	paramTypes, colTypes := types[:len(sd.ParamOIDs)], types[len(sd.ParamOIDs):]
	encoded, err := encodeArgs(args, paramTypes)
	if err != nil {
		return batchPlan{}, nil, refuse(fmt.Errorf(batchStmtFmt, i+1, err))
	}
	values, err := textValues(encoded)
	if err != nil {
		return batchPlan{}, nil, refuse(fmt.Errorf(batchStmtFmt, i+1, err))
	}
	set := rowSet{columns: make([]Column, 0, len(sd.Fields)), rows: make([][]any, 0)}
	for j, ti := range colTypes {
		if j < len(sd.Fields) {
			set.columns = append(set.columns, Column{Name: sd.Fields[j].Name, Type: ti.display})
		}
	}
	return batchPlan{name: sd.Name, read: readPlan{set: set, types: colTypes, limit: limit, stop: drainWriteRows}}, values, nil
}

// textValues is each encoded argument as the text the wire carries, nil for
// NULL.
func textValues(args []any) ([][]byte, error) {
	out := make([][]byte, len(args))
	for i, a := range args {
		switch v := a.(type) {
		case nil:
		case string:
			out[i] = []byte(v)
		default:
			return nil, fmt.Errorf("params: $%d is a %T, not text", i+1, a)
		}
	}
	return out, nil
}

// commandOf is a finished result's command word and the rows it affected.
func commandOf(tag pgconn.CommandTag, _ error) (word string, affected int64) {
	word = tag.String()
	for i, r := range word {
		if r == ' ' {
			word = word[:i]
			break
		}
	}
	return word, tag.RowsAffected()
}

// traceStart and traceEnd report a statement sent through pgconn to the
// connection's tracer, as pgx reports the ones it sends.
func (c *conn) traceStart(ctx context.Context, pc *pgx.Conn, sql string) context.Context {
	if c.tracer == nil {
		return ctx
	}
	return c.tracer.TraceQueryStart(ctx, pc, pgx.TraceQueryStartData{SQL: sql})
}

func (c *conn) traceEnd(ctx context.Context, pc *pgx.Conn, err error) {
	if c.tracer != nil {
		c.tracer.TraceQueryEnd(ctx, pc, pgx.TraceQueryEndData{Err: err})
	}
}
