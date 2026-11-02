package postgres

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/txn2/mcp-data-platform/internal/sqltables"
)

// Column is one result column: its name and the type PostgreSQL gave it.
type Column struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// rowSet is what one statement returned.
type rowSet struct {
	columns   []Column
	rows      [][]any
	truncated bool
	command   string
	affected  int64
}

// cancelOnCap selects how a statement whose rows pass the cap is stopped. A
// read is canceled at the server, so a cap of 100 on a table of millions reads
// 101 rows. A write is never canceled once it runs: its RETURNING rows are
// read to the end and the ones past the cap dropped.
type cancelOnCap bool

const (
	stopReadAtCap  cancelOnCap = true
	drainWriteRows cancelOnCap = false
)

// graceAfterTimeout is how long past the statement timeout the call's own
// context lasts, so the server's statement_timeout, which names the failure,
// fires before the client gives up on the connection.
const graceAfterTimeout = 2 * time.Second

// effectiveTimeout is the bound of one call: the connection's statement
// timeout, lowered by the caller's timeout_seconds.
func effectiveTimeout(cfg Config, seconds int) time.Duration {
	if seconds > 0 {
		if d := time.Duration(seconds) * time.Second; d < cfg.StatementTimeout {
			return d
		}
	}
	return cfg.StatementTimeout
}

// transactionControl are the leading keywords of a statement that begins,
// ends or splits a transaction. The tools hold the transaction themselves, so
// such a statement would commit part of a statements list or end the READ ONLY
// transaction a query runs in.
var transactionControl = map[string]bool{
	"begin": true, "start": true, "commit": true, "end": true, "rollback": true, "abort": true,
	"savepoint": true, "release": true, "prepare": true,
}

// refuseTransactionControl refuses a statement whose first word is one of
// transactionControl.
func refuseTransactionControl(sql string) error {
	word := sqltables.StatementKind(sql)
	if word == sqltables.KindOther {
		word = firstWord(sql)
	}
	if transactionControl[word] {
		return fmt.Errorf("a %s statement is not run: the tool holds the transaction, and a statements list "+
			"commits only when every statement in it succeeds", strings.ToUpper(word))
	}
	return nil
}

// firstWord is the lowercased first word of sql after whitespace, comments and
// opening parentheses.
func firstWord(sql string) string {
	s := strings.TrimLeft(sql, " \t\r\n(")
	for {
		switch {
		case strings.HasPrefix(s, "--"):
			if i := strings.IndexByte(s, '\n'); i >= 0 {
				s = strings.TrimLeft(s[i+1:], " \t\r\n(")
				continue
			}
			return ""
		case strings.HasPrefix(s, "/*"):
			if i := strings.Index(s, "*/"); i >= 0 {
				s = strings.TrimLeft(s[i+2:], " \t\r\n(")
				continue
			}
			return ""
		}
		end := strings.IndexFunc(s, func(r rune) bool {
			return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z')
		})
		if end < 0 {
			end = len(s)
		}
		return strings.ToLower(s[:end])
	}
}

// setTimeoutSQL bounds every statement of the transaction it runs in: the
// third argument of set_config makes the setting local to the transaction, as
// SET LOCAL does, and the value is a bound parameter.
const setTimeoutSQL = "SELECT set_config('statement_timeout', $1, true)"

// setLocalTimeout bounds every statement of the transaction tx is.
func setLocalTimeout(ctx context.Context, tx pgx.Tx, d time.Duration) error {
	_, err := tx.Exec(ctx, setTimeoutSQL, strconv.FormatInt(d.Milliseconds(), decimal))
	if err != nil {
		return fmt.Errorf("setting the statement timeout: %w", err)
	}
	return nil
}

// runStatement runs one bound statement in tx and reads up to limit rows.
//
// The statement is described first, which gives the type of every parameter
// and every result column before it runs, so each value is encoded for the
// type the server will read it as (encodeArgs) and each column is mapped by
// its own type. The extended protocol carries exactly one statement: a string
// holding two is refused by the server.
func (c *conn) runStatement(ctx context.Context, tx pgx.Tx, stmt boundStatement, limit int, stop cancelOnCap) (rowSet, error) {
	if err := refuseTransactionControl(stmt.sql); err != nil {
		return rowSet{}, refuse(err)
	}
	sd, err := tx.Prepare(ctx, "", stmt.sql)
	if err != nil {
		return rowSet{}, err //nolint:wrapcheck // a PostgreSQL error is read by its type
	}
	if len(sd.ParamOIDs) != len(stmt.args) {
		return rowSet{}, refuse(fmt.Errorf("the statement takes %d parameters and params holds %d", len(sd.ParamOIDs), len(stmt.args)))
	}
	fieldOIDs := make([]uint32, len(sd.Fields))
	for i, f := range sd.Fields {
		fieldOIDs[i] = f.DataTypeOID
	}
	types, err := c.typesOf(ctx, tx, append(append([]uint32{}, sd.ParamOIDs...), fieldOIDs...))
	if err != nil {
		return rowSet{}, err
	}
	paramTypes, colTypes := types[:len(sd.ParamOIDs)], types[len(sd.ParamOIDs):]
	args, err := encodeArgs(stmt.args, paramTypes)
	if err != nil {
		return rowSet{}, refuse(err)
	}
	set := rowSet{columns: make([]Column, len(sd.Fields)), rows: make([][]any, 0)}
	for i, f := range sd.Fields {
		set.columns[i] = Column{Name: f.Name, Type: colTypes[i].display}
	}
	return read(ctx, tx, stmt.sql, args, readPlan{set: set, types: colTypes, limit: limit, stop: stop})
}

// typesOf resolves oids in order.
func (c *conn) typesOf(ctx context.Context, tx pgx.Tx, oids []uint32) ([]typeInfo, error) {
	if len(oids) == 0 {
		return nil, nil
	}
	known, err := c.types.resolve(ctx, tx, oids)
	if err != nil {
		return nil, err
	}
	out := make([]typeInfo, len(oids))
	for i, oid := range oids {
		out[i] = known[oid]
	}
	return out, nil
}

// readPlan is how one statement's rows are read.
type readPlan struct {
	set   rowSet
	types []typeInfo
	limit int
	stop  cancelOnCap
}

// read executes the statement and maps its rows.
func read(ctx context.Context, tx pgx.Tx, sql string, args []any, p readPlan) (rowSet, error) {
	qctx, cancel := context.WithCancel(ctx)
	defer cancel()
	rows, err := tx.Query(qctx, sql, args...)
	if err != nil {
		return rowSet{}, err //nolint:wrapcheck // read by its type
	}
	defer rows.Close()
	set := p.set
	for rows.Next() {
		if len(set.rows) == p.limit {
			set.truncated = true
			if p.stop == stopReadAtCap {
				cancel()
				rows.Close()
				return set, nil
			}
			continue
		}
		row, err := mapRow(rows, p.types)
		if err != nil {
			return rowSet{}, err
		}
		set.rows = append(set.rows, row)
	}
	if err := rows.Err(); err != nil {
		return rowSet{}, err //nolint:wrapcheck // read by its type
	}
	tag := rows.CommandTag()
	set.command, set.affected = strings.Fields(tag.String() + " ")[0], tag.RowsAffected()
	return set, nil
}

// mapRow decodes one row and maps each value by its column's type. A scalar
// type outside the mapping (interval, inet, money, an enum, a range) is the
// text the server sent for it, which results carry as text (the pool runs in
// exec mode), rather than the driver's own rendering of it.
func mapRow(rows pgx.Rows, types []typeInfo) ([]any, error) {
	values, err := rows.Values()
	if err != nil {
		return nil, fmt.Errorf("decoding a row: %w", err)
	}
	raw := rows.RawValues()
	out := make([]any, len(values))
	for i, v := range values {
		if !types[i].array && !mappedTypes[types[i].name] && i < len(raw) && raw[i] != nil {
			out[i] = string(raw[i])
			continue
		}
		out[i] = jsonValue(v, types[i].name)
	}
	return out, nil
}
