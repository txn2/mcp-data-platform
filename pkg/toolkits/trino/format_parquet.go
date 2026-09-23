package trino

import (
	"fmt"
	"strings"

	trinoclient "github.com/txn2/mcp-trino/pkg/client"

	"github.com/txn2/mcp-data-platform/internal/tableparquet"
	"github.com/txn2/mcp-data-platform/internal/tabletype"
)

// TypedFormatter is a Formatter that writes each column as its own type, and
// so needs the types. trino_export hands it the types Trino reported for the
// query's columns; a writer with no types (a managed script's rows) calls
// Format, which infers them.
type TypedFormatter interface {
	Formatter
	FormatTyped(columns []tabletype.Column, rows [][]any) ([]byte, error)
}

// parquetFormatter writes a Parquet file (#1833): typed, columnar and
// compressed, and registrable with manage_table, which declares the file's
// columns from its footer.
type parquetFormatter struct{}

func (*parquetFormatter) ContentType() string   { return tableparquet.ContentType } //nolint:revive // implements Formatter
func (*parquetFormatter) FileExtension() string { return tableparquet.Extension }   //nolint:revive // implements Formatter

// FormatTyped writes rows under the types given.
func (*parquetFormatter) FormatTyped(columns []tabletype.Column, rows [][]any) ([]byte, error) {
	b, err := tableparquet.Write(columns, rows)
	if err != nil {
		return nil, fmt.Errorf("writing Parquet: %w", err)
	}
	return b, nil
}

// Format writes rows with no declared types, inferring each column's from its
// values under the rules a JSON-lines registration types its columns by
// (tabletype.Inferrer), so a script's Parquet file and its JSON-lines file
// declare the same table.
func (f *parquetFormatter) Format(columns []string, rows [][]any) ([]byte, error) {
	normalized, err := normalizeRows(rows, len(columns))
	if err != nil {
		return nil, err
	}
	infer := tabletype.NewInferrer()
	for i, row := range normalized {
		if err := infer.Observe(columns, row); err != nil {
			return nil, fmt.Errorf("row %d: %w", i+1, err)
		}
	}
	inferred, err := infer.Columns()
	if err != nil {
		return nil, fmt.Errorf("typing the columns: %w", err)
	}
	return f.FormatTyped(inferred, normalized)
}

// normalizeRows turns a writer's values into the ones the inference reads, by
// the JSON a JSON-lines file would hold them as: a number keeps its digits, a
// map becomes an object with its keys in order, and a value JSON cannot carry
// is refused the way the JSON-lines writer refuses it.
func normalizeRows(rows [][]any, width int) ([][]any, error) {
	out := make([][]any, len(rows))
	for i, row := range rows {
		values := make([]any, width)
		for j := range width {
			if j >= len(row) || row[j] == nil {
				continue
			}
			text, err := marshalJSONL(row[j])
			if err != nil {
				return nil, fmt.Errorf("row %d, column %d: %w", i+1, j+1, err)
			}
			v, err := tabletype.DecodeJSON(text)
			if err != nil {
				return nil, fmt.Errorf("row %d, column %d: %w", i+1, j+1, err)
			}
			values[j] = v
		}
		out[i] = values
	}
	return out, nil
}

// storageLosses collects the columns a Parquet file cannot keep everything of,
// at any depth. A nested value loses the same things a top-level one does, and
// a note that named only the top level would read as though the rest kept
// everything.
type storageLosses struct {
	zoned []string // a timestamp whose zone is not kept
	text  []string // a type with no Parquet form, written as text
	fine  []string // a timestamp finer than the microsecond a table reads
}

// walk records what the type at name loses.
func (l *storageLosses) walk(name string, t tabletype.Type) {
	switch t.Kind {
	case tabletype.TimestampTZ:
		l.zoned = append(l.zoned, name)
	case tabletype.Text:
		l.text = append(l.text, name+" ("+strings.ToLower(t.Source)+")")
	case tabletype.Timestamp:
		if t.Precision > tabletype.DeclaredTimestampPrecision {
			l.fine = append(l.fine, name)
		}
	case tabletype.Array, tabletype.Map:
		l.walk(name+"[]", *t.Elem)
	case tabletype.Row:
		for _, f := range t.Fields {
			l.walk(name+"."+f.Name, f.Type)
		}
	}
}

// columnTypes reads the types Trino reported for a query's columns. A scalar
// is reported by its bare name with its precision and scale beside it; a
// nested type by its whole signature.
func columnTypes(cols []trinoclient.ColumnInfo) ([]tabletype.Column, error) {
	out := make([]tabletype.Column, 0, len(cols))
	for _, c := range cols {
		t, err := tabletype.Parse(c.Type)
		if err != nil {
			return nil, fmt.Errorf("the column %q: %w", c.Name, err)
		}
		out = append(out, tabletype.Column{Name: c.Name, Type: t.WithPrecision(int(c.Precision), int(c.Scale))})
	}
	return out, nil
}

// storageNote says what a Parquet file could not keep of a query's columns: a
// timestamp's zone, a timestamp finer than a microsecond, and a type with no
// Parquet form of its own, which is written as text so the file still
// registers.
//
// Every column the file loses something of is named. A column whose loss went
// unnamed beside one that was named reads as a column that lost nothing.
func storageNote(columns []tabletype.Column) string {
	var losses storageLosses
	for _, c := range columns {
		losses.walk(c.Name, c.Type)
	}
	zoned, text, fine := losses.zoned, losses.text, losses.fine
	var parts []string
	if len(zoned) > 0 {
		parts = append(parts, "Parquet keeps the instant of a timestamp with a time zone and not the zone, so "+
			strings.Join(zoned, ", ")+" read back in UTC.")
	}
	if len(fine) > 0 {
		parts = append(parts, "A registered table reads a timestamp at microseconds, so these are written to the "+
			"microsecond and their finer digits are dropped: "+strings.Join(fine, ", ")+".")
	}
	if len(text) > 0 {
		parts = append(parts, "Parquet has no type these read back exactly as, so they are written as text: "+
			strings.Join(text, ", ")+".")
	}
	return strings.Join(parts, " ")
}
