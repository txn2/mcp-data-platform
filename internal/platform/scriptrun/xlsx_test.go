package scriptrun

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"

	"github.com/txn2/mcp-data-platform/internal/tablexlsx"
)

// xlsxSource exports a two-sheet workbook with a currency column and prints
// what the export record reported back to the script.
const xlsxSource = `
out = platform.export("sales", {
    "sheets": [
        {"name": "Summary", "title": "Q3 sales",
         "columns": ["region", "sales", "net"],
         "rows": [{"region": "west", "sales": 12, "net": "1234.50"},
                  {"region": "east", "sales": 7, "net": 99}],
         "column_types": {"net": "currency", "sales": "integer"},
         "freeze": "A3", "widths": {"region": 30}},
        {"name": "By day", "rows": [{"day": "2026-09-01", "count": 3}]},
    ],
}, format="xlsx")
print(out["format"], out["row_count"], out["sheets"], out["bytes"] > 0)
`

// TestExport_XLSXDraftReportsSheetsRowsAndSize pins what a draft reports for
// a workbook: the sheet count and rows per sheet, and the size the platform
// run would write -- measured by the same serializer, so the two agree.
func TestExport_XLSXDraftReportsSheetsRowsAndSize(t *testing.T) {
	draft, err := draftPublish(t, xlsxSource)
	require.NoError(t, err)
	require.Len(t, draft.Exports, 1)
	rec := draft.Exports[0]
	assert.True(t, rec.Preview)
	assert.Equal(t, "xlsx", rec.Format)
	assert.Equal(t, 3, rec.RowCount)
	assert.False(t, rec.Document)
	assert.Equal(t, []tablexlsx.SheetShape{{Name: "Summary", Rows: 2}, {Name: "By day", Rows: 1}}, rec.Sheets)
	assert.Contains(t, draft.Log, `xlsx 3 [{"name": "Summary", "rows": 2}, {"name": "By day", "rows": 1}] True`)

	exporter := &recordingExporter{}
	_, err = exporterRun(t, xlsxSource, exporter)
	require.NoError(t, err)
	require.Len(t, exporter.requests, 1)
	data, identity, err := FormatOutput(exporter.requests[0])
	require.NoError(t, err)
	assert.Equal(t, len(data), rec.Bytes, "a draft measures the bytes a platform run writes")
	assert.Equal(t, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", identity.ContentType)
	assert.Equal(t, ".xlsx", identity.Extension)
}

// TestExport_XLSXThroughTheEngine runs the script as a platform run and reads
// the workbook the writer would store: two sheets, typed cells, the exact
// currency, and a column order taken from the script's own dict keys.
func TestExport_XLSXThroughTheEngine(t *testing.T) {
	exporter := &recordingExporter{}
	result, err := exporterRun(t, xlsxSource, exporter)
	require.NoError(t, err)
	require.Len(t, exporter.requests, 1)
	req := exporter.requests[0]
	assert.Nil(t, req.Rows)
	assert.Nil(t, req.Body)
	assert.Equal(t, 3, req.RowCount())
	require.Len(t, result.Exports, 1)
	assert.Equal(t, "asset_1", result.Exports[0].AssetID)
	assert.Len(t, result.Exports[0].Sheets, 2)

	data, _, err := FormatOutput(req)
	require.NoError(t, err)
	f, err := excelize.OpenReader(bytes.NewReader(data))
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	assert.Equal(t, []string{"Summary", "By day"}, f.GetSheetList())

	net, err := f.GetCellValue("Summary", "C3", excelize.Options{RawCellValue: true})
	require.NoError(t, err)
	assert.Equal(t, "1234.50", net)
	shown, err := f.GetCellValue("Summary", "C3")
	require.NoError(t, err)
	assert.Equal(t, "1,234.50", shown)
	cents, err := f.GetCellValue("Summary", "C4")
	require.NoError(t, err)
	assert.Equal(t, "0.99", cents, "an integer currency value is cents")

	header, err := f.GetRows("By day")
	require.NoError(t, err)
	assert.Equal(t, []string{"day", "count"}, header[0], "columns follow the order the script wrote")
}

func TestExport_XLSXRefusals(t *testing.T) {
	cases := map[string]string{
		`platform.export("r", {"sheets": [{"name": "Q3/Q4", "columns": ["a"]}]}, format="xlsx")`:                                       `sheet name "Q3/Q4" contains one of`,
		`platform.export("r", {"sheets": [{"name": "` + "abcdefghijklmnopqrstuvwxyz0123456" + `", "columns": ["a"]}]}, format="xlsx")`: `over Excel's limit of 31`,
		`platform.export("r", {"sheets": [{"name": "A", "columns": ["a"]}, {"name": "a", "columns": ["b"]}]}, format="xlsx")`:          `is used twice`,
		`platform.export("r", {"sheets": [{"columns": ["a"]}]}, format="xlsx")`:                                                        `a sheet needs a "name"`,
		`platform.export("r", {"sheets": [{"name": "S", "rows": [{"v": "a\x01b"}]}]}, format="xlsx")`:                                  `control character U+0001`,
		`platform.export("r", {"sheets": [{"name": "S", "rows": [{"v": 1.5}]}], "x": 1}, format="xlsx")`:                               `unknown key "x"`,
		`platform.export("r", [{"a": 1}], format="xlsx")`:                                                                              `format "xlsx" takes a dict`,
		`platform.export("r", "<p>hi</p>", format="xlsx")`:                                                                             `format "xlsx" takes a dict`,
		`platform.export("r", {"sheets": [{"name": "S", "rows": [{"net": 1.5}]}]}, format="xlsx")`:                                     ``,
	}
	for source, want := range cases {
		_, err := draftPublish(t, source)
		if want == "" {
			require.NoError(t, err, "an undeclared float column is written as a number")
			continue
		}
		require.Error(t, err, source)
		assert.Contains(t, err.Error(), "platform.export", source)
		assert.Contains(t, err.Error(), want, source)
	}

	// A currency float fails the run at the write, naming where it was.
	_, err := Run(context.Background(), Options{
		Source: `platform.export("r", {"sheets": [{"name": "S", "rows": [{"net": 1.5}], "column_types": {"net": "currency"}}]}, format="xlsx")`,
		Name:   "test", RunID: "dpx_1", FireTime: fireTime, Caller: &recordingCaller{},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `output "r": sheet "S", row 1, column "net"`)
	assert.Contains(t, err.Error(), "is not exact")
}
