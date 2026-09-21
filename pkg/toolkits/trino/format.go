package trino

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	textColumnSeparator = "  "
	textNewline         = "\n"

	// Output format names recognized by newFormatter.
	formatCSV      = "csv"
	formatJSON     = "json"
	formatMarkdown = "markdown"
	formatText     = "text"
	formatJSONL    = "jsonl"

	// File extensions and content types per format.
	extCSV      = ".csv"
	extJSON     = ".json"
	extMarkdown = ".md"
	extJSONL    = ".jsonl"

	contentTypeCSV   = "text/csv"
	contentTypeJSONL = "application/x-ndjson"
)

// Formatter converts query results into a specific output format.
type Formatter interface {
	// Format serializes columns and rows into the target format.
	Format(columns []string, rows [][]any) ([]byte, error)
	// ContentType returns the MIME type for the formatted output.
	ContentType() string
	// FileExtension returns the file extension (including dot) for the format.
	FileExtension() string
}

// NewFormatter returns a Formatter for the given format name.
// Supported formats: csv, json, jsonl, markdown, text.
//
// It is exported because trino_export is not the only writer of these formats:
// a managed script's platform.export writes the same four from rows it computed
// itself, and it writes them with this implementation rather than a second one
// that would drift. The format an author sees in a draft preview is therefore
// byte-for-byte the format a platform run persists.
func NewFormatter(format string) (Formatter, error) {
	return newFormatter(format)
}

// newFormatter returns a Formatter for the given format name.
func newFormatter(format string) (Formatter, error) {
	switch format {
	case formatCSV:
		return &csvFormatter{}, nil
	case formatJSON:
		return &jsonFormatter{}, nil
	case formatMarkdown:
		return &markdownFormatter{}, nil
	case formatText:
		return &textFormatter{}, nil
	case formatJSONL:
		return &jsonlFormatter{}, nil
	default:
		return nil, fmt.Errorf("unsupported format: %q (must be csv, json, jsonl, markdown, or text)", format)
	}
}

// --- CSV Formatter ---

// csvFormatter writes RFC 4180 CSV holding each value exactly as the query
// returned it (#1818). A stored CSV is a data file: a table is registered over
// it, a script reads it, another system loads it. Rewriting a value that starts
// with '=', '+', '-' or '@' for the benefit of a spreadsheet changed an
// identifier like "-AbC" into "'-AbC" for every one of those readers, and
// nothing reported the change.
type csvFormatter struct{}

func (*csvFormatter) ContentType() string   { return contentTypeCSV } //nolint:revive // implements Formatter
func (*csvFormatter) FileExtension() string { return extCSV }         //nolint:revive // implements Formatter

func (*csvFormatter) Format(columns []string, rows [][]any) ([]byte, error) { //nolint:revive // implements Formatter
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)

	if err := w.Write(columns); err != nil {
		return nil, fmt.Errorf("writing CSV header: %w", err)
	}

	record := make([]string, len(columns))
	for _, row := range rows {
		for i := range record {
			if i < len(row) {
				record[i] = formatValue(row[i])
			} else {
				record[i] = ""
			}
		}
		if err := w.Write(record); err != nil {
			return nil, fmt.Errorf("writing CSV row: %w", err)
		}
	}

	w.Flush()
	if err := w.Error(); err != nil {
		return nil, fmt.Errorf("flushing CSV: %w", err)
	}
	return buf.Bytes(), nil
}

// --- JSON Formatter ---

type jsonFormatter struct{}

func (*jsonFormatter) ContentType() string   { return "application/json" } //nolint:revive // implements Formatter
func (*jsonFormatter) FileExtension() string { return extJSON }            //nolint:revive // implements Formatter

func (*jsonFormatter) Format(columns []string, rows [][]any) ([]byte, error) { //nolint:revive // implements Formatter
	data := make([]map[string]any, len(rows))
	for i, row := range rows {
		m := make(map[string]any, len(columns))
		for j, col := range columns {
			if j < len(row) {
				m[col] = row[j]
			} else {
				m[col] = nil
			}
		}
		data[i] = m
	}

	out := map[string]any{
		"columns":   columns,
		"data":      data,
		"row_count": len(rows),
	}

	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshaling JSON: %w", err)
	}
	return b, nil
}

// --- JSON Lines Formatter ---

// jsonlFormatter writes one JSON object per line, keyed by column, in column
// order (#1820). It is the format a table registered over a stored file reads
// back exactly: every string survives, including the line breaks a CSV cell
// cannot carry past Trino's line-based CSV reader, and a null stays a null
// rather than becoming an empty string.
//
// A nested value (a list, a map, a Trino ARRAY or ROW) is written as its JSON
// text, a string. Trino's JSON reader fails every query on a table whose file
// holds a nested value in a column declared VARCHAR, and every registered
// column is VARCHAR; as text the value survives and json_parse reads it back.
//
// A string that is not valid UTF-8 is refused rather than written. encoding/json
// would replace its bad bytes with U+FFFD and report nothing, which is the
// silent change of data this format exists to rule out.
type jsonlFormatter struct{}

func (*jsonlFormatter) ContentType() string   { return contentTypeJSONL } //nolint:revive // implements Formatter
func (*jsonlFormatter) FileExtension() string { return extJSONL }         //nolint:revive // implements Formatter

func (*jsonlFormatter) Format(columns []string, rows [][]any) ([]byte, error) { //nolint:revive // implements Formatter
	var buf bytes.Buffer
	for i, row := range rows {
		if err := writeJSONLRecord(&buf, columns, row); err != nil {
			return nil, fmt.Errorf("row %d: %w", i+1, err)
		}
	}
	return buf.Bytes(), nil
}

// writeJSONLRecord writes one row as one line. The object is assembled by hand
// rather than marshaled from a map so the keys keep the column order the
// writer was given, which is what a person reading the file expects.
func writeJSONLRecord(buf *bytes.Buffer, columns []string, row []any) error {
	_ = buf.WriteByte('{')
	for j, col := range columns {
		if j > 0 {
			_ = buf.WriteByte(',')
		}
		var cell any
		if j < len(row) {
			cell = row[j]
		}
		if err := writeJSONLValue(buf, col); err != nil {
			return fmt.Errorf("column name %q: %w", col, err)
		}
		_ = buf.WriteByte(':')
		value, err := jsonlCell(cell)
		if err != nil {
			return fmt.Errorf("column %q: %w", col, err)
		}
		if err := writeJSONLValue(buf, value); err != nil {
			return fmt.Errorf("column %q: %w", col, err)
		}
	}
	_, _ = buf.WriteString("}\n")
	return nil
}

// jsonlCell returns the value a cell is written as: a scalar as itself, a
// nested value as its JSON text.
func jsonlCell(v any) (any, error) {
	switch v.(type) {
	case nil, string, bool, float32, float64, int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64, json.Number:
		return v, nil
	}
	text, err := marshalJSONL(v)
	if err != nil {
		return nil, err
	}
	return strings.TrimSuffix(string(text), "\n"), nil
}

// writeJSONLValue writes one JSON value without a trailing newline.
func writeJSONLValue(buf *bytes.Buffer, v any) error {
	text, err := marshalJSONL(v)
	if err != nil {
		return err
	}
	_, _ = buf.Write(bytes.TrimSuffix(text, []byte("\n")))
	return nil
}

// marshalJSONL encodes a value with HTML escaping off, so a file holding "<"
// reads as "<" to a person as well as to Trino, after refusing any string in
// it that is not valid UTF-8.
func marshalJSONL(v any) ([]byte, error) {
	if err := checkUTF8(v); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("encoding as JSON: %w", err)
	}
	return buf.Bytes(), nil
}

// errNotUTF8 refuses a string JSON could carry only by replacing its bytes.
var errNotUTF8 = errors.New("the value is not valid UTF-8 text, and JSON can only carry it by replacing bytes; " +
	"write it as hex or base64 instead")

// checkUTF8 refuses a value holding a string, or a map key, that is not valid
// UTF-8.
func checkUTF8(v any) error {
	switch val := v.(type) {
	case string:
		if !utf8.ValidString(val) {
			return errNotUTF8
		}
	case []any:
		return checkUTF8Each(val)
	case map[string]any:
		for key, item := range val {
			if !utf8.ValidString(key) {
				return errNotUTF8
			}
			if err := checkUTF8(item); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkUTF8Each checks every item of a list.
func checkUTF8Each(items []any) error {
	for _, item := range items {
		if err := checkUTF8(item); err != nil {
			return err
		}
	}
	return nil
}

// --- Markdown Formatter ---

type markdownFormatter struct{}

func (*markdownFormatter) ContentType() string   { return "text/markdown" } //nolint:revive // implements Formatter
func (*markdownFormatter) FileExtension() string { return extMarkdown }     //nolint:revive // implements Formatter

func (*markdownFormatter) Format(columns []string, rows [][]any) ([]byte, error) { //nolint:revive // implements Formatter
	var buf bytes.Buffer

	// Header row
	buf.WriteString("| ")
	buf.WriteString(strings.Join(columns, " | "))
	buf.WriteString(" |\n")

	// Separator row
	buf.WriteString("|")
	for range columns {
		buf.WriteString(" --- |")
	}
	buf.WriteString("\n")

	// Data rows
	for _, row := range rows {
		buf.WriteString("| ")
		vals := make([]string, len(columns))
		for i := range columns {
			if i < len(row) {
				// Escape pipe characters in values
				v := strings.ReplaceAll(formatValue(row[i]), "|", "\\|")
				vals[i] = v
			}
		}
		buf.WriteString(strings.Join(vals, " | "))
		buf.WriteString(" |\n")
	}

	return buf.Bytes(), nil
}

// --- Text Formatter ---

type textFormatter struct{}

func (*textFormatter) ContentType() string   { return "text/plain" } //nolint:revive // implements Formatter
func (*textFormatter) FileExtension() string { return ".txt" }       //nolint:revive // implements Formatter

func (*textFormatter) Format(columns []string, rows [][]any) ([]byte, error) { //nolint:revive // implements Formatter
	widths, strRows := textMeasure(columns, rows)
	var buf bytes.Buffer
	textWriteHeader(&buf, columns, widths)
	textWriteRows(&buf, strRows, widths)
	return buf.Bytes(), nil
}

func textMeasure(columns []string, rows [][]any) (widths []int, strRows [][]string) { //nolint:gocritic // named returns for clarity
	widths = make([]int, len(columns))
	for i, col := range columns {
		widths[i] = len(col)
	}
	strRows = make([][]string, len(rows))
	for i, row := range rows {
		strRow := make([]string, len(columns))
		for j := range columns {
			if j < len(row) {
				strRow[j] = formatValue(row[j])
			}
			if len(strRow[j]) > widths[j] {
				widths[j] = len(strRow[j])
			}
		}
		strRows[i] = strRow
	}
	return widths, strRows
}

func textWriteHeader(buf *bytes.Buffer, columns []string, widths []int) {
	for i, col := range columns {
		if i > 0 {
			buf.WriteString(textColumnSeparator)
		}
		buf.WriteString(padRight(col, widths[i]))
	}
	buf.WriteString(textNewline)
	for i, w := range widths {
		if i > 0 {
			buf.WriteString(textColumnSeparator)
		}
		buf.WriteString(strings.Repeat("-", w))
	}
	buf.WriteString(textNewline)
}

func textWriteRows(buf *bytes.Buffer, strRows [][]string, widths []int) {
	for _, strRow := range strRows {
		for i, val := range strRow {
			if i > 0 {
				buf.WriteString(textColumnSeparator)
			}
			buf.WriteString(padRight(val, widths[i]))
		}
		buf.WriteString(textNewline)
	}
}

// padRight pads a string with spaces to the given width.
func padRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}

// formatValue converts any value to its string representation.
func formatValue(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}
