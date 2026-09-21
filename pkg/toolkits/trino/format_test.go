package trino

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewFormatter(t *testing.T) {
	tests := []struct {
		format  string
		wantErr bool
		wantCT  string
		wantExt string
	}{
		{"csv", false, "text/csv", ".csv"},
		{"json", false, "application/json", ".json"},
		{"markdown", false, "text/markdown", ".md"},
		{"text", false, "text/plain", ".txt"},
		{"jsonl", false, "application/x-ndjson", ".jsonl"},
		{"xml", true, "", ""},
		{"", true, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.format, func(t *testing.T) {
			f, err := newFormatter(tt.format)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantCT, f.ContentType())
			assert.Equal(t, tt.wantExt, f.FileExtension())
		})
	}
}

func TestCSVFormatter(t *testing.T) {
	f := &csvFormatter{}
	columns := []string{"name", "age", "city"}
	rows := [][]any{
		{"Alice", 30, "New York"},
		{"Bob", 25, "London"},
	}

	data, err := f.Format(columns, rows)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	require.Len(t, lines, 3)
	assert.Equal(t, "name,age,city", lines[0])
	assert.Equal(t, "Alice,30,New York", lines[1])
	assert.Equal(t, "Bob,25,London", lines[2])
}

// TestCSVFormatterRoundTripsEveryValue holds #1818: the bytes a CSV export
// stores parse back to exactly the values the query returned. The corpus is the
// values a formula guard rewrote (a leading '=', '+', '-', '@', tab, CR) beside
// the ones RFC 4180 quoting must carry: quotes, separators, line breaks,
// backslashes and non-ASCII text.
func TestCSVFormatterRoundTripsEveryValue(t *testing.T) {
	values := []string{
		"=SUM(A1:A10)", "+cmd|' /C calc'!A0", "-AbCdEfGhIj", "-5", "@import",
		"\tleading tab", "\rleading cr", `say "hi"`, `""`, "a,b", "line\nbreak",
		`back\slash`, `trailing \`, `q"\`, "caf\u00e9 \U0001F600 \uE000", "safe value", "",
	}
	rows := make([][]any, len(values))
	for i, v := range values {
		rows[i] = []any{i, v}
	}
	data, err := (&csvFormatter{}).Format([]string{"id", "value"}, rows)
	require.NoError(t, err)

	records, err := csv.NewReader(bytes.NewReader(data)).ReadAll()
	require.NoError(t, err)
	require.Len(t, records, len(values)+1)
	for i, v := range values {
		assert.Equal(t, v, records[i+1][1], "row %d", i)
	}
}

func TestCSVFormatterNullHandling(t *testing.T) {
	f := &csvFormatter{}
	columns := []string{"a", "b"}
	rows := [][]any{
		{nil, "value"},
		{"value", nil},
	}

	data, err := f.Format(columns, rows)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	assert.Equal(t, ",value", lines[1])
	assert.Equal(t, "value,", lines[2])
}

func TestCSVFormatterEmptyRows(t *testing.T) {
	f := &csvFormatter{}
	data, err := f.Format([]string{"a", "b"}, nil)
	require.NoError(t, err)
	assert.Equal(t, "a,b\n", string(data))
}

func TestJSONFormatter(t *testing.T) {
	f := &jsonFormatter{}
	columns := []string{"name", "age"}
	rows := [][]any{
		{"Alice", float64(30)},
		{"Bob", float64(25)},
	}

	data, err := f.Format(columns, rows)
	require.NoError(t, err)

	assert.Contains(t, string(data), `"columns"`)
	assert.Contains(t, string(data), `"data"`)
	assert.Contains(t, string(data), `"row_count": 2`)
	assert.Contains(t, string(data), `"Alice"`)
}

func TestJSONFormatterNullValues(t *testing.T) {
	f := &jsonFormatter{}
	columns := []string{"val"}
	rows := [][]any{
		{nil},
	}

	data, err := f.Format(columns, rows)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"val": null`)
}

func TestJSONFormatterEmpty(t *testing.T) {
	f := &jsonFormatter{}
	data, err := f.Format([]string{"a"}, nil)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"row_count": 0`)
	assert.Contains(t, string(data), `"data": []`)
}

func TestMarkdownFormatter(t *testing.T) {
	f := &markdownFormatter{}
	columns := []string{"name", "age"}
	rows := [][]any{
		{"Alice", 30},
		{"Bob", 25},
	}

	data, err := f.Format(columns, rows)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	require.Len(t, lines, 4)
	assert.Equal(t, "| name | age |", lines[0])
	assert.Equal(t, "| --- | --- |", lines[1])
	assert.Equal(t, "| Alice | 30 |", lines[2])
	assert.Equal(t, "| Bob | 25 |", lines[3])
}

func TestMarkdownFormatterPipeEscaping(t *testing.T) {
	f := &markdownFormatter{}
	columns := []string{"value"}
	rows := [][]any{
		{"foo|bar"},
	}

	data, err := f.Format(columns, rows)
	require.NoError(t, err)
	assert.Contains(t, string(data), `foo\|bar`)
}

func TestTextFormatter(t *testing.T) {
	f := &textFormatter{}
	columns := []string{"name", "age"}
	rows := [][]any{
		{"Alice", 30},
		{"Bob", 25},
	}

	data, err := f.Format(columns, rows)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	require.Len(t, lines, 4)
	// Header
	assert.Contains(t, lines[0], "name")
	assert.Contains(t, lines[0], "age")
	// Separator
	assert.True(t, strings.Contains(lines[1], "----"))
	// Data
	assert.Contains(t, lines[2], "Alice")
	assert.Contains(t, lines[3], "Bob")
}

func TestTextFormatterAlignment(t *testing.T) {
	f := &textFormatter{}
	columns := []string{"short", "longcolumnname"}
	rows := [][]any{
		{"a", "b"},
	}

	data, err := f.Format(columns, rows)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	// The header columns should be padded to their respective widths
	assert.Equal(t, "short  longcolumnname", lines[0])
}

func TestFormatValue(t *testing.T) {
	assert.Equal(t, "", formatValue(nil))
	assert.Equal(t, "hello", formatValue("hello"))
	assert.Equal(t, "42", formatValue(42))
	assert.Equal(t, "3.14", formatValue(3.14))
	assert.Equal(t, "true", formatValue(true))
}

func TestFormatterShortRow(t *testing.T) {
	// Row has fewer values than columns — should not panic
	f := &csvFormatter{}
	columns := []string{"a", "b", "c"}
	rows := [][]any{
		{"only-one"},
	}

	data, err := f.Format(columns, rows)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	assert.Equal(t, "only-one,,", lines[1])
}

// hostileStrings is the corpus #1820 names: every string a lossless path has to
// carry exactly, whichever reader it ends at.
var hostileStrings = []string{
	`plain`, `say "hi"`, `""`, `back\slash`, `trailing\`, "cr\rhere", "lf\nhere", "crlf\r\nhere",
	"-1+1", "=SUM(A1)", "+44", "@user", "tab\there", "emoji \U0001F600", "pua \ue000\U000F0000",
	"ls\u2028ps\u2029", "<a>&amp;", "", " lead and trail ", "nul\x00byte", `{"json":"inside"}`, "a,b,c",
}

func TestJSONLFormatter_EveryStringRoundTripsExactly(t *testing.T) {
	rows := make([][]any, 0, len(hostileStrings))
	for i, s := range hostileStrings {
		rows = append(rows, []any{int64(i), s})
	}
	out, err := (&jsonlFormatter{}).Format([]string{"id", "text"}, rows)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	require.Len(t, lines, len(hostileStrings), "one line per row, whatever the values hold")
	for i, line := range lines {
		var rec map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &rec), "line %d", i+1)
		assert.Equal(t, hostileStrings[i], rec["text"], "row %d", i+1)
	}
}

func TestJSONLFormatter_KeepsColumnOrderNullsAndNesting(t *testing.T) {
	out, err := (&jsonlFormatter{}).Format(
		[]string{"z", "a", "nested", "missing"},
		[][]any{{"1", nil, map[string]any{"k": []any{1, "<x>"}}}},
	)
	require.NoError(t, err)
	assert.Equal(t, `{"z":"1","a":null,"nested":"{\"k\":[1,\"<x>\"]}","missing":null}`+"\n", string(out),
		"columns keep their order, a nil stays null, a nested value is its JSON text, and a short row fills with null")
}

func TestJSONLFormatter_RefusesInvalidUTF8(t *testing.T) {
	_, err := (&jsonlFormatter{}).Format([]string{"v"}, [][]any{{"ok"}, {"bad\xffbyte"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "row 2")
	assert.Contains(t, err.Error(), `column "v"`)
	assert.Contains(t, err.Error(), "not valid UTF-8")

	_, err = (&jsonlFormatter{}).Format([]string{"v"}, [][]any{{[]any{map[string]any{"k\xff": 1}}}})
	require.Error(t, err, "a bad key inside a nested value is refused too")

	_, err = (&jsonlFormatter{}).Format([]string{"bad\xffname"}, [][]any{{1}})
	require.Error(t, err, "a column name is written as a key and is held to the same rule")
}

func TestJSONLFormatter_NoRowsIsEmpty(t *testing.T) {
	out, err := (&jsonlFormatter{}).Format([]string{"a"}, nil)
	require.NoError(t, err)
	assert.Empty(t, out)
}
