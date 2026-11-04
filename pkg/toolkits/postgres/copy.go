package postgres

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// CopyIn is one copy_in call (#2453): rows written into a table's columns
// through PostgreSQL's COPY protocol.
type CopyIn struct {
	Connection string
	// Table is the table, schema-qualified or not ("orders",
	// "shop.orders"). It and Columns are quoted as identifiers, never
	// written into the statement as given.
	Table   string
	Columns []string
	// Rows are the values, each row in Columns' order, in the JSON forms a
	// bound parameter takes.
	Rows [][]any
}

// The formats copy_out writes.
const (
	CopyFormatCSV  = "csv"
	CopyFormatText = "text"
)

// CopyIn writes in's rows in a transaction of its own and commits it,
// returning the rows written. It runs as the persona's role with the caller's
// claims set, as every statement does.
func (t *Toolkit) CopyIn(ctx context.Context, in CopyIn) (int64, error) {
	c, ok := t.lookup(in.Connection)
	if !ok {
		return 0, &RefusedError{Code: CodeUnknownConnection, msg: fmt.Sprintf("%s: no application database named %q", errPrefix, in.Connection)}
	}
	if c.cfg.ReadOnly {
		return 0, &RefusedError{Code: CodeReadOnly, msg: fmt.Sprintf("%s: connection %q is read_only and copies nothing in", errPrefix, in.Connection)}
	}
	if err := c.checkCopyIn(in); err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, c.cfg.StatementTimeout+graceAfterTimeout)
	defer cancel()
	out, err := c.inTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadWrite}, c.cfg.StatementTimeout, true, func(tx pgx.Tx) (any, error) {
		return c.copyIn(ctx, tx, in)
	})
	observe(c.cfg.ConnectionName, err)
	if err != nil {
		return 0, c.failure(ctx, err)
	}
	n, _ := out.(int64)
	return n, nil
}

// CopyIn writes in's rows in the transaction; they commit with it.
func (x *Tx) CopyIn(ctx context.Context, in CopyIn) (int64, error) {
	if x.readOnly {
		return 0, &RefusedError{Code: CodeReadOnly, msg: errPrefix + ": the transaction is read_only; tx.copy_in writes nothing in it"}
	}
	if err := x.c.checkCopyIn(in); err != nil {
		return 0, err
	}
	if err := x.enter(); err != nil {
		return 0, err
	}
	defer x.leave()
	if err := x.usableAfterFailure(); err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, x.c.cfg.StatementTimeout+graceAfterTimeout)
	defer cancel()
	n, err := x.c.copyIn(ctx, x.tx, in)
	observe(x.c.cfg.ConnectionName, err)
	if err != nil {
		x.failed(err)
		return 0, x.c.failure(ctx, err)
	}
	return n, nil
}

// checkCopyIn refuses a call before anything is sent: no table or columns, a
// row of the wrong width, more rows than copy_max_rows.
func (c *conn) checkCopyIn(in CopyIn) error {
	refuse := func(format string, a ...any) error {
		return &RefusedError{Code: CodeInvalidStatement, msg: errPrefix + ": " + fmt.Sprintf(format, a...)}
	}
	if _, err := tableIdentifier(in.Table); err != nil {
		return refuse("%v", err)
	}
	if len(in.Columns) == 0 {
		return refuse("copy_in names no columns")
	}
	for _, col := range in.Columns {
		if strings.TrimSpace(col) == "" {
			return refuse("copy_in names an empty column")
		}
	}
	if int64(len(in.Rows)) > c.cfg.CopyMaxRows {
		return refuse("copy_in holds %d rows, more than copy_max_rows (%d) on connection %q; copy in smaller calls",
			len(in.Rows), c.cfg.CopyMaxRows, c.cfg.ConnectionName)
	}
	for i, row := range in.Rows {
		if len(row) != len(in.Columns) {
			return refuse("row %d holds %d values and copy_in names %d columns", i+1, len(row), len(in.Columns))
		}
	}
	return nil
}

// tableIdentifier reads "table" or "schema.table".
func tableIdentifier(table string) (pgx.Identifier, error) {
	parts := strings.Split(strings.TrimSpace(table), ".")
	if len(parts) > 2 || parts[0] == "" || parts[len(parts)-1] == "" {
		return nil, fmt.Errorf("table %q is not a table or a schema.table name", table)
	}
	return pgx.Identifier(parts), nil
}

// copyIn encodes the rows by their columns' types, refuses them past
// copy_max_bytes, and sends them through COPY FROM STDIN.
func (c *conn) copyIn(ctx context.Context, tx pgx.Tx, in CopyIn) (int64, error) {
	ident, _ := tableIdentifier(in.Table)
	cols := make([]string, len(in.Columns))
	for i, col := range in.Columns {
		cols[i] = pgx.Identifier{col}.Sanitize()
	}
	colList := strings.Join(cols, ", ")
	sd, err := c.describe(ctx, tx, "SELECT "+colList+" FROM "+ident.Sanitize()+" LIMIT 0")
	if err != nil {
		return 0, err
	}
	oids := make([]uint32, len(sd.Fields))
	for i, f := range sd.Fields {
		oids[i] = f.DataTypeOID
	}
	types, err := c.typesOf(ctx, tx, oids)
	if err != nil {
		return 0, err
	}
	buf, err := c.encodeCopy(in.Rows, types)
	if err != nil {
		return 0, err
	}
	tag, err := tx.Conn().PgConn().CopyFrom(ctx, buf, "COPY "+ident.Sanitize()+" ("+colList+") FROM STDIN")
	if err != nil {
		return 0, err //nolint:wrapcheck // read by its type
	}
	return tag.RowsAffected(), nil
}

// encodeCopy writes rows in COPY's text format, each value encoded by its
// column's type as a bound parameter is, refusing them past copy_max_bytes.
func (c *conn) encodeCopy(rows [][]any, types []typeInfo) (*bytes.Buffer, error) {
	var buf bytes.Buffer
	for i, row := range rows {
		values, err := encodeArgs(row, types)
		if err != nil {
			return nil, refuse(fmt.Errorf("row %d: %w", i+1, err))
		}
		writeTextRecord(&buf, values)
		if int64(buf.Len()) > c.cfg.CopyMaxBytes {
			return nil, refuse(fmt.Errorf("copy_in is more than copy_max_bytes (%d) on connection %q; copy in smaller calls",
				c.cfg.CopyMaxBytes, c.cfg.ConnectionName))
		}
	}
	return &buf, nil
}

// CopyOutResult is what one copy_out wrote.
type CopyOutResult struct {
	Rows    int64
	Bytes   int64
	Columns []Column
}

// CopyOut writes the rows of s's query to w (#2453), as CSV with a header or
// in COPY's text format, in a READ ONLY transaction. The rows stream from the
// server to w a row at a time and are never held together. maxBytes lowers
// copy_max_bytes; a result past either bound, or past copy_max_rows, fails.
//
// The query is read through the extended protocol rather than COPY (query) TO
// STDOUT, which takes no parameters: its values stay bound. Each value is the
// text PostgreSQL wrote for it, which is what COPY writes.
func (t *Toolkit) CopyOut(ctx context.Context, s Statement, format string, w io.Writer, maxBytes int64) (*CopyOutResult, error) {
	if format != CopyFormatCSV && format != CopyFormatText {
		return nil, &RefusedError{Code: CodeInvalidStatement, msg: fmt.Sprintf("%s: format %q is not %s or %s", errPrefix, format, CopyFormatCSV, CopyFormatText)}
	}
	c, stmt, err := t.prepare(ctx, s)
	if err != nil {
		return nil, err
	}
	limit := c.cfg.CopyMaxBytes
	if maxBytes > 0 && maxBytes < limit {
		limit = maxBytes
	}
	timeout := effectiveTimeout(c.cfg, s.TimeoutSeconds)
	ctx, cancel := context.WithTimeout(ctx, timeout+graceAfterTimeout)
	defer cancel()
	out, err := c.inTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly}, timeout, false, func(tx pgx.Tx) (any, error) {
		return c.copyOut(ctx, tx, stmt, copySink{w: w, format: format, limit: limit, rows: c.cfg.CopyMaxRows})
	})
	observe(c.cfg.ConnectionName, err)
	if err != nil {
		return nil, c.failure(ctx, err)
	}
	res, _ := out.(*CopyOutResult)
	return res, nil
}

// copySink is where copy_out writes and the bounds it writes within.
type copySink struct {
	w      io.Writer
	format string
	limit  int64
	rows   int64
}

func (c *conn) copyOut(ctx context.Context, tx pgx.Tx, stmt boundStatement, sink copySink) (*CopyOutResult, error) {
	if err := refuseTransactionControl(stmt.sql); err != nil {
		return nil, refuse(err)
	}
	sd, err := c.describe(ctx, tx, stmt.sql)
	if err != nil {
		return nil, err
	}
	if len(sd.ParamOIDs) != len(stmt.args) {
		return nil, refuse(fmt.Errorf("the statement takes %d parameters and params holds %d", len(sd.ParamOIDs), len(stmt.args)))
	}
	types, err := c.typesOf(ctx, tx, sd.ParamOIDs)
	if err != nil {
		return nil, err
	}
	args, err := encodeArgs(stmt.args, types)
	if err != nil {
		return nil, refuse(err)
	}
	values, err := textValues(args)
	if err != nil {
		return nil, refuse(err)
	}
	res := &CopyOutResult{Columns: make([]Column, len(sd.Fields))}
	for i, f := range sd.Fields {
		res.Columns[i] = Column{Name: f.Name}
	}
	pc := tx.Conn()
	ctx = c.traceStart(ctx, pc, sd.SQL)
	err = streamRows(pc.PgConn().ExecPrepared(ctx, sd.Name, values, nil, nil), res, sink)
	c.traceEnd(ctx, pc, err)
	if err != nil {
		return nil, err
	}
	return res, nil
}

// rowSource is a result read a row at a time: a *pgconn.ResultReader.
type rowSource interface {
	NextRow() bool
	Values() [][]byte
	Close() (pgconn.CommandTag, error)
}

// streamRows writes the header and each row of rr to the sink in its format,
// within its byte and row bounds, counting what it wrote into res.
func streamRows(rr rowSource, res *CopyOutResult, sink copySink) error {
	w := copyWriter{res: res, sink: sink}
	fields := make([]any, len(res.Columns))
	for i, col := range res.Columns {
		fields[i] = col.Name
	}
	if err := w.emit(fields); err != nil {
		_, _ = rr.Close()
		return err
	}
	for rr.NextRow() {
		if res.Rows == sink.rows {
			_, _ = rr.Close()
			return refuse(fmt.Errorf("the result is more than copy_max_rows (%d) rows; narrow the query or raise copy_max_rows on the connection", sink.rows))
		}
		textFields(rr.Values(), fields)
		if err := w.emit(fields); err != nil {
			_, _ = rr.Close()
			return err
		}
		res.Rows++
	}
	_, err := rr.Close()
	return err //nolint:wrapcheck // read by its type
}

// textFields fills fields with a row's values as text, a null as nil.
func textFields(values [][]byte, fields []any) {
	for i, v := range values {
		if v == nil {
			fields[i] = nil
			continue
		}
		fields[i] = string(v)
	}
}

// copyWriter writes copy_out's records to its sink, counting the bytes.
type copyWriter struct {
	res  *CopyOutResult
	sink copySink
	line bytes.Buffer
}

// emit writes one record, refusing it past the sink's byte bound.
func (w *copyWriter) emit(fields []any) error {
	w.line.Reset()
	if w.sink.format == CopyFormatCSV {
		writeCSVRecord(&w.line, fields)
	} else {
		writeTextRecord(&w.line, fields)
	}
	if w.res.Bytes+int64(w.line.Len()) > w.sink.limit {
		return refuse(fmt.Errorf("the result is more than %d bytes, the most copy_out writes; narrow the query or "+
			"raise copy_max_bytes on the connection", w.sink.limit))
	}
	n, err := w.sink.w.Write(w.line.Bytes())
	w.res.Bytes += int64(n)
	return err //nolint:wrapcheck // the writer's own failure
}

// writeTextRecord writes one row in COPY's text format: fields separated by a
// tab, null as \N, a backslash, tab, newline or carriage return escaped.
func writeTextRecord(b *bytes.Buffer, fields []any) {
	for i, f := range fields {
		if i > 0 {
			_ = b.WriteByte('\t')
		}
		s, ok := f.(string)
		if !ok {
			_, _ = b.WriteString(`\N`)
			continue
		}
		_, _ = b.WriteString(textEscaper.Replace(s))
	}
	_ = b.WriteByte('\n')
}

//nolint:gochecknoglobals // a fixed escaper
var textEscaper = strings.NewReplacer(`\`, `\\`, "\t", `\t`, "\n", `\n`, "\r", `\r`)

// writeCSVRecord writes one row as COPY's CSV does: null as nothing, an empty
// string as "", a field holding a comma, quote, newline or carriage return
// quoted with its quotes doubled.
func writeCSVRecord(b *bytes.Buffer, fields []any) {
	for i, f := range fields {
		if i > 0 {
			_ = b.WriteByte(',')
		}
		s, ok := f.(string)
		if !ok {
			continue
		}
		if s == "" || strings.ContainsAny(s, ",\"\n\r") {
			_, _ = b.WriteString(`"` + strings.ReplaceAll(s, `"`, `""`) + `"`)
			continue
		}
		_, _ = b.WriteString(s)
	}
	_ = b.WriteByte('\n')
}
