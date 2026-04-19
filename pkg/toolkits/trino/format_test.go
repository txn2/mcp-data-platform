package trino

import (
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

func TestCSVFormatterFormulaEscaping(t *testing.T) {
	f := &csvFormatter{}
	columns := []string{"value"}
	rows := [][]any{
		{"=SUM(A1:A10)"},
		{"+cmd|' /C calc'!A0"},
		{"-1+1"},
		{"@import('evil')"},
		{"safe value"},
	}

	data, err := f.Format(columns, rows)
	require.NoError(t, err)

	output := string(data)
	assert.Contains(t, output, "'=SUM")
	assert.Contains(t, output, "'+cmd")
	assert.Contains(t, output, "'-1+1")
	assert.Contains(t, output, "'@import")
	assert.Contains(t, output, "safe value")
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

func TestEscapeCSVCell(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"normal", "normal"},
		{"=formula", "'=formula"},
		{"+cmd", "'+cmd"},
		{"-1", "'-1"},
		{"@import", "'@import"},
		{"\ttab", "'\ttab"},
		{"\rcr", "'\rcr"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.want, escapeCSVCell(tt.input))
		})
	}
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
