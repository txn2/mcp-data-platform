package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/pglex"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

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

// sessionControl are the leading keywords of a statement that changes what
// the platform keeps on a pooled connection: its prepared statements, which
// the statement cache owns (stmtcache.go).
var sessionControl = map[string]bool{"deallocate": true, "discard": true}

// refuseTransactionControl refuses a statement whose first word is one of
// transactionControl or sessionControl, works a savepoint, or is COPY (#2453):
// COPY FROM PROGRAM runs a program on the server for a user allowed to, and
// copy_in and copy_out are how rows are copied.
func refuseTransactionControl(sql string) error {
	word := sqltables.StatementKind(sql)
	if word == sqltables.KindOther {
		word = pglex.FirstWord(sql)
	}
	if word == "copy" {
		return errors.New("a COPY statement is not run: copy rows with platform.copy_in and platform.copy_out, which " +
			"keep their values bound and reach no file or program on the database server")
	}
	if savepointControl(word, sql) {
		return fmt.Errorf("a %s statement is not run: the platform names and closes savepoints; run the "+
			"statements in tx.savepoint(fn), which rolls back only fn's work when it fails", savepointWords(word))
	}
	if transactionControl[word] {
		return fmt.Errorf("a %s statement is not run: the platform holds the transaction; use "+
			"platform.transaction to run statements that commit together", strings.ToUpper(word))
	}
	if sessionControl[word] {
		return fmt.Errorf("a %s statement is not run: the platform resets each connection's session "+
			"when a call releases it", strings.ToUpper(word))
	}
	return nil
}

// savepointControl reports whether a statement starting with word works a
// savepoint: SAVEPOINT, RELEASE, or ROLLBACK TO (#2452).
func savepointControl(word, sql string) bool {
	switch word {
	case "savepoint", "release":
		return true
	case "rollback":
		rest := strings.Fields(strings.ToLower(sql))
		for i, w := range rest {
			if w == "to" && i > 0 {
				return true
			}
		}
	}
	return false
}

// savepointWords names the statement a savepoint refusal is about.
func savepointWords(word string) string {
	if word == "rollback" {
		return "ROLLBACK TO"
	}
	return strings.ToUpper(word)
}

// beginSQL begins a transaction in opts' mode with every statement of it
// bounded by timeout and, when idle is set, the session's idle time bounded
// by idle: one simple-protocol round trip in place of three. SET LOCAL makes
// both bounds the transaction's own. Every value written into the text is
// the platform's: a pgx isolation and access constant and two integers.
func beginSQL(opts pgx.TxOptions, timeout, idle time.Duration) string {
	var b strings.Builder
	_, _ = b.WriteString("BEGIN")
	if opts.IsoLevel != "" {
		_, _ = b.WriteString(" ISOLATION LEVEL " + strings.ToUpper(string(opts.IsoLevel)))
	}
	if opts.AccessMode != "" {
		_, _ = b.WriteString(" " + strings.ToUpper(string(opts.AccessMode)))
	}
	_, _ = b.WriteString("; SET LOCAL statement_timeout = " + strconv.FormatInt(timeout.Milliseconds(), decimal))
	// The interval text a result carries and the mapping reads (#2455) is
	// PostgreSQL's own style, whatever the database or role sets.
	_, _ = b.WriteString("; SET LOCAL IntervalStyle = 'postgres'")
	if idle > 0 {
		_, _ = b.WriteString("; SET LOCAL idle_in_transaction_session_timeout = " + strconv.FormatInt(idle.Milliseconds(), decimal))
	}
	return b.String()
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
	sd, err := c.describe(ctx, tx, stmt.sql)
	if err != nil {
		return rowSet{}, err
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
	plan := readPlan{set: set, types: colTypes, limit: limit, stop: stop}
	if c.cache == nil {
		return read(ctx, tx, stmt.sql, args, plan)
	}
	out, err := c.readPrepared(ctx, tx, sd, args, plan)
	if err != nil {
		c.cache.failed(tx, stmt.sql, err)
	}
	return out, err
}

// describe is the statement's description: prepared on the physical
// connection through the statement cache, or described anew when the cache is
// off.
func (c *conn) describe(ctx context.Context, tx pgx.Tx, sql string) (*pgconn.StatementDescription, error) {
	if c.cache != nil {
		return c.cache.describe(ctx, tx, sql)
	}
	sd, err := tx.Prepare(ctx, "", sql)
	if err != nil {
		return nil, err //nolint:wrapcheck // a PostgreSQL error is read by its type
	}
	return sd, nil
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
	return collect(rows, cancel, p)
}

// collect reads a statement's rows into its row set, stopping as p says.
func collect(rows pgx.Rows, cancel context.CancelFunc, p readPlan) (rowSet, error) {
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

// mapRow decodes one row and maps each value by its column's type. A
// structured type is read from the text the server sent into its structure
// (structuredValue); a scalar type outside the mapping (inet, money, an enum)
// is that text, which results carry as text (the pool runs in exec mode),
// rather than the driver's own rendering of it.
func mapRow(rows pgx.Rows, types []typeInfo) ([]any, error) {
	values, err := rows.Values()
	if err != nil {
		return nil, fmt.Errorf("decoding a row: %w", err)
	}
	raw := rows.RawValues()
	out := make([]any, len(values))
	for i, v := range values {
		if i < len(raw) && raw[i] != nil && structuredKind(&types[i]) {
			sv, err := structuredValue(&types[i], string(raw[i]))
			if err != nil {
				return nil, fmt.Errorf("decoding a %s value: %w", types[i].display, err)
			}
			out[i] = sv
			continue
		}
		if !types[i].array && !mappedTypes[types[i].name] && i < len(raw) && raw[i] != nil {
			out[i] = string(raw[i])
			continue
		}
		out[i] = jsonValue(v, types[i].name)
	}
	return out, nil
}
