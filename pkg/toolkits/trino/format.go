package trino

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	textColumnSeparator = "  "
	textNewline         = "\n"
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

// newFormatter returns a Formatter for the given format name.
// Supported formats: csv, json, markdown, text.
func newFormatter(format string) (Formatter, error) {
	switch format {
	case "csv":
		return &csvFormatter{}, nil
	case "json":
		return &jsonFormatter{}, nil
	case "markdown":
		return &markdownFormatter{}, nil
	case "text":
		return &textFormatter{}, nil
	default:
		return nil, fmt.Errorf("unsupported format: %q (must be csv, json, markdown, or text)", format)
	}
}

// --- CSV Formatter ---

type csvFormatter struct{}

func (*csvFormatter) ContentType() string   { return "text/csv" } //nolint:revive // implements Formatter
func (*csvFormatter) FileExtension() string { return ".csv" }     //nolint:revive // implements Formatter

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
				record[i] = escapeCSVCell(formatValue(row[i]))
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

// escapeCSVCell prevents formula injection by prefixing cells that start
// with characters Excel interprets as formula indicators.
func escapeCSVCell(s string) string {
	if s == "" {
		return s
	}
	r, _ := utf8.DecodeRuneInString(s)
	switch r {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + s
	}
	return s
}

// --- JSON Formatter ---

type jsonFormatter struct{}

func (*jsonFormatter) ContentType() string   { return "application/json" } //nolint:revive // implements Formatter
func (*jsonFormatter) FileExtension() string { return ".json" }            //nolint:revive // implements Formatter

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

// --- Markdown Formatter ---

type markdownFormatter struct{}

func (*markdownFormatter) ContentType() string   { return "text/markdown" } //nolint:revive // implements Formatter
func (*markdownFormatter) FileExtension() string { return ".md" }           //nolint:revive // implements Formatter

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
