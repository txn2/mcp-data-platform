package scriptout

import (
	"fmt"

	"go.starlark.net/starlark"

	"github.com/txn2/mcp-data-platform/internal/platform/starlarkconv"
	"github.com/txn2/mcp-data-platform/internal/tablexlsx"
)

// FormatXLSX is the export format that writes an Excel workbook (#1849).
// Its body is neither rows nor a string: it is a dict describing the sheets.
const FormatXLSX = "xlsx"

// WorkbookArg reads platform.export's body for the xlsx format, a dict
// {"sheets": [...]}, into a checked workbook.
//
// A sheet that names no "columns" takes them from its rows' keys, in the
// order the script wrote them: the Starlark dicts still hold that order, and
// the Go maps they convert to do not, so it is read here, before conversion.
func WorkbookArg(v starlark.Value) (*tablexlsx.Workbook, error) {
	dict, ok := v.(*starlark.Dict)
	if !ok {
		return nil, fmt.Errorf(`format %q takes a dict {"sheets": [{"name": ..., "columns": [...], "rows": [...]}]}, got %s`,
			FormatXLSX, v.Type())
	}
	body, err := starlarkconv.FromStarlark(dict)
	if err != nil {
		return nil, fmt.Errorf("the xlsx body: %w", err)
	}
	orders := sheetColumnOrders(dict)
	book, err := tablexlsx.Parse(body, func(i int) []string {
		if i < len(orders) {
			return orders[i]
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("the xlsx body: %w", err)
	}
	return book, nil
}

// SheetsValue renders a workbook's shape as the list an export record hands
// the script: one {"name": ..., "rows": ...} dict per sheet, in order.
func SheetsValue(shapes []tablexlsx.SheetShape) *starlark.List {
	out := make([]starlark.Value, 0, len(shapes))
	for _, shape := range shapes {
		d := starlark.NewDict(2) //nolint:mnd // name and rows
		_ = d.SetKey(starlark.String("name"), starlark.String(shape.Name))
		_ = d.SetKey(starlark.String("rows"), starlark.MakeInt(shape.Rows))
		out = append(out, d)
	}
	return starlark.NewList(out)
}

// sheetColumnOrders reads, per sheet, the column order of its rows as the
// script wrote them. A sheet that is not a dict, or has no rows, has none.
func sheetColumnOrders(body *starlark.Dict) [][]string {
	sheets, found, _ := body.Get(starlark.String("sheets"))
	list, ok := sheets.(*starlark.List)
	if !found || !ok {
		return nil
	}
	out := make([][]string, list.Len())
	for i := range list.Len() {
		sheet, ok := list.Index(i).(*starlark.Dict)
		if !ok {
			continue
		}
		if rows, found, _ := sheet.Get(starlark.String("rows")); found {
			out[i] = starlarkconv.ColumnOrder(rows)
		}
	}
	return out
}
