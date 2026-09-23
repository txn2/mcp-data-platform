//go:build integration

package acceptance

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"
)

// Issue #1849: platform.export wrote csv, json, jsonl, parquet, markdown,
// text, html and jsx, so a report consumed as a spreadsheet could only be
// several CSVs the reader reassembled.
//
// What these hold, against the running platform: a saved script exporting
// format="xlsx" writes one portal asset served as an Excel workbook, whose two
// sheets carry typed cells (numbers as numbers, a date as a date), a bold
// header, a frozen pane and a column width, and whose currency "1234.50" is
// the exact decimal 1234.50 shown as 1,234.50; a draft reports the sheet
// count, the rows in each sheet and the size; and an invalid sheet name fails
// the run with a message naming the name and the rule.
//
// Wire forms: manage_script's `command`, `name`, `description` and `source`
// are typed string and `params` an array of objects; run_script's `name` is a
// string, `args` an object and `wait_seconds` an integer; manage_asset's
// `action` and `asset_id` are strings. Each is sent in that one form. The
// workbook body is Starlark source inside `source`, not a JSON parameter.

// issue1849Source exports the two-sheet workbook the criteria read back. %s
// is the output name.
const issue1849Source = `
out = platform.export(%q, {
    "sheets": [
        {"name": "Summary", "title": "Sales by region",
         "columns": ["region", "sales", "net", "day"],
         "rows": [{"region": "west", "sales": 1200, "net": "1234.50", "day": "2026-09-01"},
                  {"region": "east", "sales": 34, "net": 99, "day": "2026-09-02"}],
         "column_types": {"sales": "integer", "net": "currency", "day": "date"},
         "freeze": "A3", "widths": {"region": 30}},
        {"name": "By day", "rows": [{"day": "2026-09-01", "count": 3}]},
    ],
}, format="xlsx")
print(out["sheets"])
`

// xlsxContentType1849 is the media type an Excel workbook is served under.
const xlsxContentType1849 = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"

// saveScript1849 creates a script this file owns and deletes it afterwards.
func saveScript1849(t *testing.T, c *client, name, source string) {
	t.Helper()
	c.call("manage_script", map[string]any{
		"command": "create", "name": name, "source": source,
		"description": "Acceptance #1849: platform.export writes an Excel workbook.",
		"params":      []any{map[string]any{"name": "day", "type": "string"}},
	})
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
}

// assetBytes1849 reads an asset's stored content through the portal route the
// viewer and its download read, returning the served type and the bytes.
func assetBytes1849(t *testing.T, c *client, assetID string) (string, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(c.ctx, http.MethodGet, c.base+"/api/v1/portal/assets/"+assetID+"/content", http.NoBody)
	if err != nil {
		t.Fatalf("building the content request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET the asset content: %v", err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close after read
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("reading the asset content: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET the asset content: status %d: %s", res.StatusCode, body)
	}
	// A saved download opens by name: the output is named without an
	// extension, and the served filename carries .xlsx.
	if cd := res.Header.Get("Content-Disposition"); !strings.HasSuffix(strings.TrimSuffix(cd, `"`), ".xlsx") {
		t.Errorf("the download is named %q; want a .xlsx filename", cd)
	}
	return res.Header.Get("Content-Type"), body
}

// cell1849 reads one cell, raw (the stored value) or formatted (as shown).
func cell1849(t *testing.T, f *excelize.File, sheet, ref string, raw bool) string {
	t.Helper()
	v, err := f.GetCellValue(sheet, ref, excelize.Options{RawCellValue: raw})
	if err != nil {
		t.Fatalf("reading %s!%s: %v", sheet, ref, err)
	}
	return v
}

func TestIssue1849_ATwoSheetWorkbookWithTypedCellsAndExactCurrency(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	name := "acc-1849-" + stamp
	saveScript1849(t, c, name, fmt.Sprintf(issue1849Source, "acc-1849-"+stamp))

	out := c.call("run_script", map[string]any{"name": name, "args": map[string]any{}, "wait_seconds": 120})
	if status, _ := out["status"].(string); status != "succeeded" {
		t.Fatalf("the run did not succeed: %v", out)
	}
	if log, _ := out["log"].(string); !strings.Contains(log, `[{"name": "Summary", "rows": 2}, {"name": "By day", "rows": 1}]`) {
		t.Errorf("the export record handed the script does not report its sheets: log = %q", log)
	}
	outputs, _ := out["outputs"].([]any)
	if len(outputs) != 1 {
		t.Fatalf("outputs = %v; want the one workbook", out["outputs"])
	}
	output, _ := outputs[0].(map[string]any)
	assetID, _ := output["asset_id"].(string)
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": assetID}) })
	if output["format"] != "xlsx" || output["row_count"] != float64(3) {
		t.Errorf("the output = %v; want format xlsx and 3 rows across the sheets", output)
	}

	contentType, data := assetBytes1849(t, c, assetID)
	if contentType != xlsxContentType1849 {
		t.Errorf("the workbook is served as %q; want %q", contentType, xlsxContentType1849)
	}
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("the stored bytes are not a workbook excelize opens: %v", err)
	}
	defer func() { _ = f.Close() }()

	if got := f.GetSheetList(); len(got) != 2 || got[0] != "Summary" || got[1] != "By day" {
		t.Errorf("sheets = %v; want [Summary By day]", got)
	}
	if got := cell1849(t, f, "Summary", "A1", true); got != "Sales by region" {
		t.Errorf("the title row = %q", got)
	}
	styleID, err := f.GetCellStyle("Summary", "B2")
	if err != nil {
		t.Fatalf("the header style: %v", err)
	}
	if style, err := f.GetStyle(styleID); err != nil || style.Font == nil || !style.Font.Bold {
		t.Errorf("the header row is not bold: %+v (%v)", style, err)
	}

	// Typed cells: a number cell carries no t attribute (n), a text cell is a
	// string.
	for _, ref := range []string{"B3", "C3", "D3"} {
		if typ, err := f.GetCellType("Summary", ref); err != nil || typ != excelize.CellTypeUnset {
			t.Errorf("Summary!%s is not a number cell: type %v (%v)", ref, typ, err)
		}
	}
	if typ, _ := f.GetCellType("Summary", "A3"); typ != excelize.CellTypeSharedString {
		t.Errorf("Summary!A3 is not a text cell: type %v", typ)
	}
	if got := cell1849(t, f, "Summary", "C3", true); got != "1234.50" {
		t.Errorf("the currency's stored value is %q; want the exact decimal 1234.50", got)
	}
	if got := cell1849(t, f, "Summary", "C3", false); got != "1,234.50" {
		t.Errorf("the currency shows as %q; want 1,234.50", got)
	}
	if got := cell1849(t, f, "Summary", "C4", false); got != "0.99" {
		t.Errorf("integer cents 99 show as %q; want 0.99", got)
	}
	if got := cell1849(t, f, "Summary", "B3", false); got != "1,200" {
		t.Errorf("the integer shows as %q; want 1,200", got)
	}
	if got := cell1849(t, f, "Summary", "D3", false); got != "2026-09-01" {
		t.Errorf("the date shows as %q; want 2026-09-01", got)
	}
	if got := cell1849(t, f, "Summary", "D3", true); got != "46266" {
		t.Errorf("the date's stored value is %q; want the Excel day 46266", got)
	}

	panes, err := f.GetPanes("Summary")
	if err != nil || !panes.Freeze || panes.YSplit != 2 || panes.TopLeftCell != "A3" {
		t.Errorf("the frozen pane = %+v (%v); want the title and header rows frozen above A3", panes, err)
	}
	if width, err := f.GetColWidth("Summary", "A"); err != nil || width != 30 {
		t.Errorf("column A width = %v (%v); want 30", width, err)
	}
	rows, err := f.GetRows("By day")
	if err != nil || len(rows) != 2 || strings.Join(rows[0], ",") != "day,count" {
		t.Errorf("the second sheet = %v (%v); want its header in the order the script wrote", rows, err)
	}
}

func TestIssue1849_ADraftReportsSheetsRowsAndSize(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	ran := c.call("manage_script", map[string]any{
		"command": "run_draft", "name": "acc-1849-draft-" + stamp,
		"source": fmt.Sprintf(issue1849Source, "acc-1849-draft-"+stamp),
	})
	if status, _ := ran["status"].(string); status != "succeeded" {
		t.Fatalf("the draft did not succeed: %v", ran)
	}
	exports, _ := ran["exports"].([]any)
	if len(exports) != 1 {
		t.Fatalf("exports = %v; want the one workbook", ran["exports"])
	}
	rec, _ := exports[0].(map[string]any)
	if rec["preview"] != true || rec["format"] != "xlsx" || rec["row_count"] != float64(3) {
		t.Errorf("the draft's record = %v; want a preview of an xlsx with 3 rows", rec)
	}
	if size, _ := rec["bytes"].(float64); size <= 0 {
		t.Errorf("the draft reports no size: %v", rec)
	}
	sheets, _ := rec["sheets"].([]any)
	want := []string{"Summary:2", "By day:1"}
	if len(sheets) != len(want) {
		t.Fatalf("sheets = %v; want %v", rec["sheets"], want)
	}
	for i, s := range sheets {
		sheet, _ := s.(map[string]any)
		if got := fmt.Sprintf("%v:%v", sheet["name"], sheet["rows"]); got != want[i] {
			t.Errorf("sheet %d = %s; want %s", i+1, got, want[i])
		}
	}
}

func TestIssue1849_AnInvalidSheetNameFailsTheRunNamingIt(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	name := "acc-1849-bad-" + stamp
	saveScript1849(t, c, name, fmt.Sprintf(`platform.export(%q, {"sheets": [{"name": "Q3/Q4", "columns": ["a"], "rows": [[1]]}]}, format="xlsx")`+"\n",
		"acc-1849-bad-"+stamp))

	out := c.call("run_script", map[string]any{"name": name, "args": map[string]any{}, "wait_seconds": 120})
	if status, _ := out["status"].(string); status != "failed" {
		t.Fatalf("a workbook with an invalid sheet name must fail the run, got: %v", out)
	}
	errText, _ := out["error"].(string)
	for _, want := range []string{"platform.export", `sheet name "Q3/Q4"`, "which Excel does not allow in a sheet name"} {
		if !strings.Contains(errText, want) {
			t.Errorf("the failure %q does not say %q", errText, want)
		}
	}
	if outputs, _ := out["outputs"].([]any); len(outputs) != 0 {
		t.Errorf("a refused workbook wrote outputs: %v", outputs)
	}
}
