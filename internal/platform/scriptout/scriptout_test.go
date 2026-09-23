package scriptout

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"
	"go.starlark.net/starlark"
	"go.starlark.net/syntax"

	"github.com/txn2/mcp-data-platform/internal/tablexlsx"
	"github.com/txn2/mcp-data-platform/pkg/contenttype"
)

// TestTabular_ProjectsRowsOntoTheColumnOrder pins the projection every tabular
// output format shares.
func TestTabular_ProjectsRowsOntoTheColumnOrder(t *testing.T) {
	rows := tabular([]string{"a", "b"}, []any{
		map[string]any{"b": 2, "a": 1},
		map[string]any{"a": 3},
		"not a dict",
	})
	assert.Equal(t, [][]any{{1, 2}, {3, nil}, {nil, nil}}, rows,
		"a missing column is an empty cell, not a shifted row")
}

func TestRows(t *testing.T) {
	data, identity, err := Rows("daily", "csv", []string{"a", "b"}, []any{map[string]any{"b": int64(2), "a": "x"}})
	require.NoError(t, err)
	assert.Equal(t, "a,b\nx,2\n", string(data))
	assert.Equal(t, "text/csv", identity.ContentType)
	assert.Equal(t, ".csv", identity.Extension)

	_, _, err = Rows("daily", "avro", nil, nil)
	require.ErrorContains(t, err, `output "daily"`)
	_, _, err = Rows("daily", "json", []string{"a"}, []any{map[string]any{"a": make(chan int)}})
	require.ErrorContains(t, err, `formatting output "daily"`)
}

func TestDocument(t *testing.T) {
	data, identity, err := Document("doc", "jsx", "export default () => null")
	require.NoError(t, err)
	assert.Equal(t, "export default () => null", string(data))
	assert.Equal(t, "text/jsx", identity.ContentType)

	_, _, err = Document("doc", "csv", "a,b")
	require.ErrorContains(t, err, `output "doc": format "csv" is serialized from rows`)
	assert.Contains(t, err.Error(), "[html jsx markdown text]")
	_, _, err = Document("doc", "html", strings.Repeat("x", MaxBytes+1))
	require.ErrorContains(t, err, "over the")
}

func TestDataPayload(t *testing.T) {
	out, err := DataPayload("dash", map[string]any{"a": "<b>"})
	require.NoError(t, err)
	assert.NotContains(t, string(out), "<", "no string can close the <script> element the region sits in")
	_, err = DataPayload("dash", map[string]any{"c": make(chan int)})
	require.ErrorContains(t, err, `output "dash": the data cannot be serialized`)
	_, err = DataPayload("dash", strings.Repeat("y", MaxBytes))
	require.ErrorContains(t, err, "over the")
}

// eval evaluates one Starlark expression, the way a script's argument arrives.
func eval(t *testing.T, expr string) starlark.Value {
	t.Helper()
	v, err := starlark.EvalOptions(&syntax.FileOptions{}, &starlark.Thread{}, "test", expr, nil)
	require.NoError(t, err)
	return v
}

// TestWorkbookArg_ColumnOrderFromTheScript pins that a sheet naming no
// columns takes them in the order the script wrote its dict keys, which only
// the Starlark value still holds.
func TestWorkbookArg_ColumnOrderFromTheScript(t *testing.T) {
	book, err := WorkbookArg(eval(t, `{"sheets": [
		{"name": "One", "rows": [{"zeta": 1, "alpha": "1234.50"}, {"mid": True}]},
		{"name": "Two", "columns": ["x"], "rows": [[1]]},
		{"name": "Three", "columns": ["y"]},
	]}`))
	require.NoError(t, err)
	assert.Equal(t, []string{"zeta", "alpha", "mid"}, book.Sheets[0].Columns)
	assert.Equal(t, []string{"x"}, book.Sheets[1].Columns)
	assert.Equal(t, []tablexlsx.SheetShape{{Name: "One", Rows: 2}, {Name: "Two", Rows: 1}, {Name: "Three", Rows: 0}}, book.Shape())

	data, identity, err := Workbook("report", book)
	require.NoError(t, err)
	assert.Equal(t, contenttype.XLSX, identity.ContentType, "the platform's one name for the type")
	assert.Equal(t, contenttype.Extension(contenttype.XLSX), identity.Extension)
	f, err := excelize.OpenReader(bytes.NewReader(data))
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	assert.Equal(t, []string{"One", "Two", "Three"}, f.GetSheetList())
	header, err := f.GetRows("One")
	require.NoError(t, err)
	assert.Equal(t, []string{"zeta", "alpha", "mid"}, header[0])
}

func TestWorkbookArg_Refusals(t *testing.T) {
	cases := map[string]string{
		`[{"name": "x"}]`: `format "xlsx" takes a dict`,
		`"sheets"`:        `format "xlsx" takes a dict`,
		`{"sheets": [{"name": "a/b", "columns": []}]}`: `the xlsx body: sheet 1: sheet name "a/b"`,
		`{"sheets": "none"}`:                           `the xlsx body: an xlsx body needs "sheets"`,
		`{"sheets": [1], "extra": len}`:                `the xlsx body: key "extra"`,
		`{"sheets": ["S"]}`:                            `sheet 1 is the string "S"`,
	}
	for expr, want := range cases {
		_, err := WorkbookArg(eval(t, expr))
		require.Error(t, err, expr)
		assert.Contains(t, err.Error(), want, expr)
	}
}

func TestWorkbook_RefusalNamesTheOutput(t *testing.T) {
	book, err := WorkbookArg(eval(t, `{"sheets": [{"name": "S", "columns": ["v"], "rows": [["bell\x07"]]}]}`))
	require.NoError(t, err)
	_, _, err = Workbook("report", book)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `output "report": sheet "S", row 1, column "v"`)
	assert.Contains(t, err.Error(), "control character U+0007")
}

func TestSheetsValue(t *testing.T) {
	v := SheetsValue([]tablexlsx.SheetShape{{Name: "A", Rows: 3}})
	assert.Equal(t, `[{"name": "A", "rows": 3}]`, v.String())
	assert.Equal(t, "[]", SheetsValue(nil).String())
}
