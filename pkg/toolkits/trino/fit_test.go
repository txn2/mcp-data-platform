package trino

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	trinotools "github.com/txn2/mcp-trino/pkg/tools"
)

// queryResult builds a trino_query result the way the tool returns one:
// the structured output, and its text in the requested format. The text
// renderings follow mcp-trino's: an indented JSON document, or a header,
// one line per row and a footer. Every third row's note carries a comma
// and a line break, so the CSV cut has to follow quoting to count rows.
func queryResult(t *testing.T, rows int, format string) (*mcp.CallToolResult, trinotools.QueryOutput) {
	t.Helper()
	out := trinotools.QueryOutput{
		Columns:  []trinotools.QueryColumn{{Name: "id", Type: "integer"}, {Name: "note", Type: "varchar"}},
		RowCount: rows,
		Stats:    trinotools.QueryStats{RowCount: rows, DurationMs: 12},
	}
	for i := range rows {
		note := fmt.Sprintf("row %d %s", i, strings.Repeat("n", 40))
		if i%3 == 0 {
			note = fmt.Sprintf("row %d, split\nacross lines %s", i, strings.Repeat("n", 30))
		}
		out.Rows = append(out.Rows, map[string]any{"id": float64(i), "note": note})
	}
	var text string
	switch format {
	case formatCSV:
		lines := make([]string, 0, len(out.Rows))
		lines = append(lines, "id,note")
		for _, r := range out.Rows {
			note, _ := r["note"].(string)
			if strings.ContainsAny(note, ",\n") {
				note = `"` + note + `"`
			}
			lines = append(lines, fmt.Sprintf("%v,%s", r["id"], note))
		}
		text = strings.Join(lines, "\n") + fmt.Sprintf("\n\n# %d rows returned, executed in 12ms", rows)
	case formatMarkdown:
		lines := make([]string, 0, len(out.Rows))
		lines = append(lines, "| id | note |", "| --- | --- |")
		for _, r := range out.Rows {
			note, _ := r["note"].(string)
			lines = append(lines, fmt.Sprintf("| %v | %s |", r["id"], strings.ReplaceAll(note, "\n", " ")))
		}
		text = strings.Join(lines, "\n") + fmt.Sprintf("\n\n*%d rows returned, executed in 12ms*", rows)
	default:
		b, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		text = string(b)
	}
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}, StructuredContent: json.RawMessage(raw)}, out
}

// fittedOf decodes a fitted result's structured content.
func fittedOf(t *testing.T, res *mcp.CallToolResult) fittedQuery {
	t.Helper()
	raw, ok := res.StructuredContent.(json.RawMessage)
	if !ok {
		t.Fatalf("structured content is %T; want encoded JSON", res.StructuredContent)
	}
	var got fittedQuery
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("structured content: %v", err)
	}
	return got
}

func fitArgs(t *testing.T, format string) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(map[string]any{"sql": "SELECT id, note FROM t", "connection": "warehouse", "format": format, "limit": 500})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestFitResult_KeepsTheHeadOfTheRowsInTheCallersFormat: in every format a
// trino_query result past the budget keeps the most whole rows that fit, in
// the format asked for, says how many of how many, and steers to
// trino_export with the same query. The structured copy holds the same rows.
func TestFitResult_KeepsTheHeadOfTheRowsInTheCallersFormat(t *testing.T) {
	const budget = 2048
	for _, format := range []string{"", formatJSON, formatCSV, formatMarkdown} {
		t.Run("format="+format, func(t *testing.T) {
			tk := &Toolkit{exportDeps: &ExportDeps{}}
			res, whole := queryResult(t, 200, format)
			if !tk.FitResult(toolQuery, fitArgs(t, format), res, budget) {
				t.Fatal("FitResult declined a trino_query result")
			}
			text := firstText(res)
			if len(text) > budget {
				t.Errorf("text is %d characters; want it inside the %d budget", len(text), budget)
			}
			got := fittedOf(t, res)
			if !got.ResultTruncated || got.RowsShown == 0 || got.RowsShown >= 200 || len(got.Rows) != got.RowsShown {
				t.Fatalf("truncated=%v rows_shown=%d rows=%d; want a head of the 200 rows", got.ResultTruncated, got.RowsShown, len(got.Rows))
			}
			if got.RowCount != whole.RowCount {
				t.Errorf("row_count = %d; want the %d rows the query returned", got.RowCount, whole.RowCount)
			}
			if got.ExportArguments["sql"] != "SELECT id, note FROM t" || got.ExportArguments["connection"] != "warehouse" || got.ExportArguments["limit"] != float64(500) {
				t.Errorf("export_arguments = %v; want the same query", got.ExportArguments)
			}
			if !strings.Contains(got.Hint, fmt.Sprintf("%d of 200 rows", got.RowsShown)) || !strings.Contains(got.Hint, "trino_export") {
				t.Errorf("hint = %q", got.Hint)
			}
			// A row's note opens "row N " or "row N, ", so both spellings
			// of the first row withheld are looked for.
			last := fmt.Sprintf("row %d", got.RowsShown-1)
			next := func(sep string) bool { return strings.Contains(text, fmt.Sprintf("row %d%s", got.RowsShown, sep)) }
			if !strings.Contains(text, last) || next(" ") || next(",") {
				t.Errorf("text does not end at row %d:\n%s", got.RowsShown-1, text)
			}
		})
	}
}

// TestFitResult_WithoutTrinoExportNoSteer: with no trino_export on the
// deployment the result is still cut, but not told to use it.
func TestFitResult_WithoutTrinoExportNoSteer(t *testing.T) {
	tk := &Toolkit{}
	res, _ := queryResult(t, 200, formatJSON)
	if !tk.FitResult(toolQuery, fitArgs(t, formatJSON), res, 2048) {
		t.Fatal("FitResult declined")
	}
	got := fittedOf(t, res)
	if got.ExportArguments != nil || strings.Contains(got.Hint, "trino_export") {
		t.Errorf("hint=%q export=%v; want no steer to a tool this deployment lacks", got.Hint, got.ExportArguments)
	}
}

// TestFitResult_DeclinesWhatItCannotShape: another tool, unreadable output
// or arguments, a text that does not match the rows, a format it has no cut
// for, and a header alone past the budget are declined and left untouched.
func TestFitResult_DeclinesWhatItCannotShape(t *testing.T) {
	tk := &Toolkit{}
	cases := []struct {
		name   string
		tool   string
		format string
		args   json.RawMessage
		budget int
		edit   func(*mcp.CallToolResult)
	}{
		{"another tool", toolExecute, formatJSON, nil, 2048, nil},
		{"no structured output", toolQuery, formatJSON, nil, 2048, func(r *mcp.CallToolResult) { r.StructuredContent = nil }},
		{"arguments of another shape", toolQuery, formatJSON, json.RawMessage(`[1]`), 2048, nil},
		{"a text holding fewer rows", toolQuery, formatCSV, nil, 2048, func(r *mcp.CallToolResult) { r.Content = []mcp.Content{&mcp.TextContent{Text: "id,note\n1,x\n"}} }},
		{"markdown holding fewer lines", toolQuery, formatMarkdown, nil, 2048, func(r *mcp.CallToolResult) { r.Content = []mcp.Content{&mcp.TextContent{Text: "| id |\n"}} }},
		{"a format with no cut", toolQuery, "parquet", nil, 2048, nil},
		{"the header alone past the budget", toolQuery, formatJSON, nil, 10, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, _ := queryResult(t, 50, tc.format)
			if tc.edit != nil {
				tc.edit(res)
			}
			before := firstText(res)
			args := tc.args
			if args == nil {
				args = fitArgs(t, tc.format)
			}
			if tk.FitResult(tc.tool, args, res, tc.budget) {
				t.Fatal("FitResult fitted a result it cannot shape")
			}
			if firstText(res) != before {
				t.Error("a declined result was changed")
			}
		})
	}
}
