package tablexlsx

import (
	"bytes"
	"maps"
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"
)

// twoSheets is the workbook the round-trip tests read back: a typed summary
// sheet with a title, a freeze and a width, and an untyped second sheet
// written from positional rows.
func twoSheets() map[string]any {
	return map[string]any{"sheets": []any{
		map[string]any{
			"name":    "Summary",
			"title":   "Sales by region",
			"columns": []any{"region", "sales", "net", "share", "day", "at"},
			"rows": []any{
				map[string]any{
					"region": "west", "sales": int64(1200), "net": "1234.50", "share": 0.125,
					"day": "2026-09-01", "at": "2026-09-01T12:00:00Z", "ignored": "not a column",
				},
				map[string]any{
					"region": "east", "sales": "34", "net": int64(-5), "share": "0.5",
					"day": "2026-09-02", "at": "2026-09-02 06:00:00", "ignored": nil,
				},
			},
			"column_types": map[string]any{
				"region": "string", "sales": "integer", "net": "currency", "share": "percent",
				"day": "date", "at": "datetime",
			},
			"freeze": "b3",
			"widths": map[string]any{"region": int64(30), "net": 12.5},
		},
		map[string]any{
			"name":    "By day",
			"columns": []any{"day", "count", "ok", "tags"},
			"rows": []any{
				[]any{"mon", int64(3), true, []any{"a", "b"}},
				[]any{"tue", 2.5, false, nil},
			},
		},
	}}
}

func openBook(t *testing.T, data []byte) *excelize.File {
	t.Helper()
	f, err := excelize.OpenReader(bytes.NewReader(data))
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func raw(t *testing.T, f *excelize.File, sheet, cell string) string {
	t.Helper()
	v, err := f.GetCellValue(sheet, cell, excelize.Options{RawCellValue: true})
	require.NoError(t, err)
	return v
}

func numFmtOf(t *testing.T, f *excelize.File, cell string) (builtin int, custom string) {
	t.Helper()
	id, err := f.GetCellStyle("Summary", cell)
	require.NoError(t, err)
	style, err := f.GetStyle(id)
	require.NoError(t, err)
	if style.CustomNumFmt != nil {
		custom = *style.CustomNumFmt
	}
	return style.NumFmt, custom
}

func TestWrite_TwoSheetRoundTrip(t *testing.T) {
	book, err := Parse(twoSheets(), nil)
	require.NoError(t, err)
	assert.Equal(t, []SheetShape{{Name: "Summary", Rows: 2}, {Name: "By day", Rows: 2}}, book.Shape())
	assert.Equal(t, 4, book.RowCount())

	data, err := book.Write(1 << 20)
	require.NoError(t, err)
	f := openBook(t, data)
	assert.Equal(t, []string{"Summary", "By day"}, f.GetSheetList())

	// Title row, then the bold header row.
	assert.Equal(t, "Sales by region", raw(t, f, "Summary", "A1"))
	merged, err := f.GetMergeCells("Summary")
	require.NoError(t, err)
	require.Len(t, merged, 1)
	assert.Equal(t, "A1:F1", merged[0].GetStartAxis()+":"+merged[0].GetEndAxis())
	assert.Equal(t, "region", raw(t, f, "Summary", "A2"))
	headerStyle, err := f.GetCellStyle("Summary", "C2")
	require.NoError(t, err)
	hs, err := f.GetStyle(headerStyle)
	require.NoError(t, err)
	require.NotNil(t, hs.Font)
	assert.True(t, hs.Font.Bold, "the header row is bold")

	// Typed data cells: numbers are numbers, text is text.
	assert.Equal(t, "west", raw(t, f, "Summary", "A3"))
	typ, err := f.GetCellType("Summary", "A3")
	require.NoError(t, err)
	assert.Equal(t, excelize.CellTypeSharedString, typ)
	for _, cell := range []string{"B3", "C3", "D3", "E3", "F3", "B4"} {
		typ, err := f.GetCellType("Summary", cell)
		require.NoError(t, err)
		assert.Equal(t, excelize.CellTypeUnset, typ, "%s is a number cell (no t attribute means n)", cell)
	}
	assert.Equal(t, "1200", raw(t, f, "Summary", "B3"))
	assert.Equal(t, "34", raw(t, f, "Summary", "B4"))
	assert.Equal(t, "1234.50", raw(t, f, "Summary", "C3"), "the currency is the exact decimal the script wrote")
	assert.Equal(t, "-0.05", raw(t, f, "Summary", "C4"), "integer cents become exact decimal text")
	assert.Equal(t, "0.125", raw(t, f, "Summary", "D3"))
	assert.Equal(t, "46266", raw(t, f, "Summary", "E3"), "2026-09-01 is Excel day 46266")
	assert.Equal(t, "46266.5", raw(t, f, "Summary", "F3"))
	assert.Equal(t, "46267.25", raw(t, f, "Summary", "F4"))

	// Formats: shown values and number formats per type.
	shown, err := f.GetCellValue("Summary", "C3")
	require.NoError(t, err)
	assert.Equal(t, "1,234.50", shown)
	shownDate, err := f.GetCellValue("Summary", "E3")
	require.NoError(t, err)
	assert.Equal(t, "2026-09-01", shownDate)
	id, _ := numFmtOf(t, f, "B3")
	assert.Equal(t, numFmtThousands, id)
	id, _ = numFmtOf(t, f, "C4")
	assert.Equal(t, numFmtThousandsDecimal, id)
	id, _ = numFmtOf(t, f, "D3")
	assert.Equal(t, numFmtPercent, id)
	_, custom := numFmtOf(t, f, "E3")
	assert.Equal(t, "yyyy-mm-dd", custom)
	_, custom = numFmtOf(t, f, "F3")
	assert.Equal(t, "yyyy-mm-dd hh:mm:ss", custom)

	// Widths and the frozen pane.
	w, err := f.GetColWidth("Summary", "A")
	require.NoError(t, err)
	assert.InDelta(t, 30.0, w, 0.001)
	w, err = f.GetColWidth("Summary", "C")
	require.NoError(t, err)
	assert.InDelta(t, 12.5, w, 0.001)
	panes, err := f.GetPanes("Summary")
	require.NoError(t, err)
	assert.True(t, panes.Freeze)
	assert.Equal(t, 1, panes.XSplit)
	assert.Equal(t, 2, panes.YSplit)
	assert.Equal(t, "B3", panes.TopLeftCell)

	// The untyped sheet writes values as what they are.
	assert.Equal(t, "day", raw(t, f, "By day", "A1"))
	assert.Equal(t, "3", raw(t, f, "By day", "B2"))
	assert.Equal(t, "2.5", raw(t, f, "By day", "B3"))
	typ, err = f.GetCellType("By day", "C2")
	require.NoError(t, err)
	assert.Equal(t, excelize.CellTypeBool, typ)
	assert.Equal(t, `["a","b"]`, raw(t, f, "By day", "D2"))
	assert.Empty(t, raw(t, f, "By day", "D3"), "None is an empty cell")
	noPanes, err := f.GetPanes("By day")
	require.NoError(t, err)
	assert.False(t, noPanes.Freeze)

	props, err := f.GetDocProps()
	require.NoError(t, err)
	assert.Equal(t, creator, props.Creator)
}

func TestWrite_IsDeterministic(t *testing.T) {
	a, err := Parse(twoSheets(), nil)
	require.NoError(t, err)
	b, err := Parse(twoSheets(), nil)
	require.NoError(t, err)
	first, err := a.Write(1 << 20)
	require.NoError(t, err)
	second, err := b.Write(1 << 20)
	require.NoError(t, err)
	assert.Equal(t, first, second, "the same workbook is the same bytes, so a draft measures what a run writes")
}

func TestWrite_OversizedIsRefused(t *testing.T) {
	book, err := Parse(twoSheets(), nil)
	require.NoError(t, err)
	_, err = book.Write(100)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "over the 100-byte limit")
}

func TestWrite_ColumnsFromTheCaller(t *testing.T) {
	body := map[string]any{"sheets": []any{map[string]any{
		"name": "Only", "rows": []any{map[string]any{"b": int64(1), "a": int64(2)}},
	}}}
	book, err := Parse(body, func(int) []string { return []string{"b", "a"} })
	require.NoError(t, err)
	assert.Equal(t, []string{"b", "a"}, book.Sheets[0].Columns)

	_, err = Parse(body, nil)
	require.ErrorContains(t, err, "has no columns")
}

func TestWrite_HeaderOnlySheetAndSingleColumnTitle(t *testing.T) {
	book, err := Parse(map[string]any{"sheets": []any{map[string]any{
		"name": "Empty", "columns": []any{"a"}, "title": "Nothing yet", "freeze": "A3",
		"column_types": map[string]any{"a": "currency"},
	}}}, nil)
	require.NoError(t, err)
	data, err := book.Write(1 << 20)
	require.NoError(t, err)
	f := openBook(t, data)
	assert.Equal(t, "Nothing yet", raw(t, f, "Empty", "A1"))
	assert.Equal(t, "a", raw(t, f, "Empty", "A2"))
	merged, err := f.GetMergeCells("Empty")
	require.NoError(t, err)
	assert.Empty(t, merged, "a one-column title needs no merge")
	panes, err := f.GetPanes("Empty")
	require.NoError(t, err)
	assert.Equal(t, 0, panes.XSplit)
	assert.Equal(t, 2, panes.YSplit)
}

func TestFreeze_ColumnsOnly(t *testing.T) {
	book, err := Parse(map[string]any{"sheets": []any{map[string]any{
		"name": "S", "columns": []any{"a", "b"}, "freeze": "B1",
	}}}, nil)
	require.NoError(t, err)
	data, err := book.Write(1 << 20)
	require.NoError(t, err)
	panes, err := openBook(t, data).GetPanes("S")
	require.NoError(t, err)
	assert.Equal(t, 1, panes.XSplit)
	assert.Equal(t, 0, panes.YSplit)
	assert.Equal(t, "topRight", panes.ActivePane)
}

func TestCheckSheetName(t *testing.T) {
	for name, want := range map[string]string{
		"":                               "1 to 31 characters",
		strings.Repeat("x", 32):          "over Excel's limit of 31",
		"Q3 [draft]":                     "which Excel does not allow",
		"a/b":                            "which Excel does not allow",
		"what?":                          "which Excel does not allow",
		"'quoted":                        "apostrophe",
		"bell\x07":                       "control character U+0007",
		string([]byte{0xff}):             "not valid UTF-8",
		strings.Repeat("\U0001F600", 16): "over Excel's limit of 31", // 32 UTF-16 units
	} {
		err := CheckSheetName(name)
		require.Error(t, err, "%q", name)
		assert.Contains(t, err.Error(), want, "%q", name)
	}
	for _, ok := range []string{"Summary", strings.Repeat("x", 31), "By day (UTC)", "Ventas é"} {
		assert.NoError(t, CheckSheetName(ok), ok)
	}
}

func TestParse_Refusals(t *testing.T) {
	sheet := func(extra map[string]any) map[string]any {
		s := map[string]any{"name": "S", "columns": []any{"a"}, "rows": []any{map[string]any{"a": "x"}}}
		maps.Copy(s, extra)
		return map[string]any{"sheets": []any{s}}
	}
	cases := map[string]struct {
		body any
		want string
	}{
		"not a dict":          {[]any{}, `an xlsx body is a dict`},
		"unknown top key":     {map[string]any{"sheets": []any{}, "sheet": 1}, `unknown key "sheet"`},
		"no sheets":           {map[string]any{"sheets": []any{}}, `non-empty list of sheets`},
		"sheet not a dict":    {map[string]any{"sheets": []any{"S"}}, `sheet 1 is the string "S"`},
		"unknown sheet key":   {sheet(map[string]any{"column_type": map[string]any{}}), `unknown key "column_type"`},
		"name not a string":   {sheet(map[string]any{"name": int64(3)}), `the name of sheet 1 must be a string`},
		"illegal name":        {sheet(map[string]any{"name": "a:b"}), `which Excel does not allow`},
		"long name":           {sheet(map[string]any{"name": strings.Repeat("n", 40)}), `over Excel's limit of 31`},
		"columns not a list":  {sheet(map[string]any{"columns": "a"}), `columns must be a list`},
		"column not a string": {sheet(map[string]any{"columns": []any{int64(1)}}), `columns must be strings`},
		"empty column":        {sheet(map[string]any{"columns": []any{""}}), `a column name is empty`},
		"duplicate column":    {sheet(map[string]any{"columns": []any{"a", "a"}}), `column "a" is named twice`},
		"control in column":   {sheet(map[string]any{"columns": []any{"a\x01"}}), `control character U+0001`},
		"unknown type":        {sheet(map[string]any{"column_types": map[string]any{"a": "money"}}), `the types are string, integer`},
		"type of no column":   {sheet(map[string]any{"column_types": map[string]any{"b": "string"}}), `names column "b", which the sheet does not have`},
		"types not a dict":    {sheet(map[string]any{"column_types": []any{}}), `column_types must be a dict`},
		"width not a number":  {sheet(map[string]any{"widths": map[string]any{"a": "wide"}}), `must be a number`},
		"width too large":     {sheet(map[string]any{"widths": map[string]any{"a": int64(300)}}), `at most 255`},
		"width zero":          {sheet(map[string]any{"widths": map[string]any{"a": 0.0}}), `above 0`},
		"freeze not a cell":   {sheet(map[string]any{"freeze": "top"}), `is not a cell reference`},
		"freeze A1":           {sheet(map[string]any{"freeze": "A1"}), `freezes nothing`},
		"freeze not a string": {sheet(map[string]any{"freeze": int64(2)}), `the freeze of sheet "S" must be a string`},
		"title control":       {sheet(map[string]any{"title": "a\x00b"}), `the title contains the control character U+0000`},
		"rows not a list":     {sheet(map[string]any{"rows": map[string]any{}}), `rows must be a list`},
		"row not a row":       {sheet(map[string]any{"rows": []any{"x"}}), `row 1: a row is a dict or a list`},
		"short list row":      {sheet(map[string]any{"rows": []any{[]any{}}}), `the row has 0 values and the sheet has 1 columns`},
		"duplicate sheet": {map[string]any{"sheets": []any{
			map[string]any{"name": "Sales", "columns": []any{"a"}},
			map[string]any{"name": "SALES", "columns": []any{"a"}},
		}}, `sheet name "SALES" is used twice`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(tc.body, nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestParse_TooManySheetsAndCells(t *testing.T) {
	sheets := make([]any, MaxSheets+1)
	for i := range sheets {
		sheets[i] = map[string]any{}
	}
	_, err := Parse(map[string]any{"sheets": sheets}, nil)
	require.ErrorContains(t, err, "more than the 255")

	rows := make([]any, MaxCells/2)
	for i := range rows {
		rows[i] = []any{nil, nil}
	}
	_, err = Parse(map[string]any{"sheets": []any{map[string]any{
		"name": "Big", "columns": []any{"a", "b"}, "rows": rows,
	}}}, nil)
	require.ErrorContains(t, err, "more than 2000000 cells")
}

func TestWrite_ValueRefusals(t *testing.T) {
	cases := map[string]struct {
		typ  string
		v    any
		want string
	}{
		"control char in text":      {"", "line\x0bbreak", "control character U+000B"},
		"control char in string":    {"string", "a\x1fb", "control character U+001F"},
		"U+FFFE":                    {"", "a￾b", "control character U+FFFE"},
		"overlong text":             {"string", strings.Repeat("x", maxCellText+1), "over the 32767 an Excel cell holds"},
		"currency float":            {"currency", 1234.5, "the float 1234.5 is not exact"},
		"currency bool":             {"currency", true, "or integer cents, got a bool"},
		"currency not decimal":      {"currency", "1,234.50", `"1,234.50" is not a decimal number`},
		"currency too precise":      {"currency", "12345678901234.567", "17 significant digits"},
		"integer fraction":          {"integer", 1.5, "whole numbers of at most 15 digits"},
		"integer text":              {"integer", "twelve", `got "twelve"`},
		"integer too long":          {"integer", int64(1_000_000_000_000_000), "more than the 15 digits"},
		"integer bool":              {"integer", false, "got a bool"},
		"decimal list":              {"decimal", []any{}, "got a list"},
		"decimal NaN":               {"decimal", math.NaN(), "not a number a cell can hold"},
		"inferred inf":              {"", math.Inf(1), "not a number a cell can hold"},
		"date not text":             {"date", int64(20260901), "got the integer 20260901"},
		"date malformed":            {"date", "09/01/2026", `"09/01/2026" is not a date`},
		"date too early":            {"date", "1900-02-28", "outside the dates Excel shows exactly"},
		"datetime not text":         {"datetime", 1.5, "got the float 1.5"},
		"datetime malformed":        {"datetime", "tomorrow", `"tomorrow" is not a date and time`},
		"datetime out of range":     {"datetime", "0001-01-01T00:00:00", "outside the dates Excel shows exactly"},
		"string of unencodable map": {"string", map[string]any{"f": math.Inf(1)}, "cannot be written as text"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			sheet := map[string]any{"name": "S", "columns": []any{"v"}, "rows": []any{[]any{tc.v}}}
			if tc.typ != "" {
				sheet["column_types"] = map[string]any{"v": tc.typ}
			}
			book, err := Parse(map[string]any{"sheets": []any{sheet}}, nil)
			require.NoError(t, err)
			_, err = book.Write(1 << 20)
			require.Error(t, err)
			assert.Contains(t, err.Error(), `sheet "S", row 1, column "v"`)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestCells_Accepted(t *testing.T) {
	cases := []struct {
		typ  ColumnType
		v    any
		want string
	}{
		{TypeCurrency, "1234.50", "1234.50"},
		{TypeCurrency, " 7 ", "7"},
		{TypeCurrency, int64(123450), "1234.50"},
		{TypeCurrency, int64(math.MinInt64), "-92233720368547758.08"},
		{TypeDecimal, "0.000000000000001", "0.000000000000001"},
		{TypeDecimal, int64(42), "42"},
		{TypeDecimal, 2.75, "2.75"},
		{TypePercent, "0.5", "0.5"},
		{TypeInteger, 12.0, "12"},
		{TypeInteger, " -3 ", "-3"},
		{TypeString, int64(5), "5"},
		{TypeString, 1.5, "1.5"},
		{TypeString, true, "true"},
		{TypeString, map[string]any{"k": "v"}, `{"k":"v"}`},
		{TypeDate, "9999-12-31", "2958465"},
		{TypeDateTime, "2026-09-01T18:00:00.000+02:00", "46266.75"},
		{TypeDateTime, "2026-09-01", "46266"},
		{"", "tab\tand\nnewline", "tab\tand\nnewline"},
	}
	for _, tc := range cases {
		c, err := toCell(tc.typ, tc.v)
		if tc.typ == TypeCurrency && tc.v == int64(math.MinInt64) {
			// The cents render exactly; the digits are more than Excel keeps.
			assert.Equal(t, "-92233720368547758.08", centsText(math.MinInt64))
			require.Error(t, err)
			continue
		}
		require.NoError(t, err, "%s %v", tc.typ, tc.v)
		assert.Equal(t, tc.want, c.text, "%s %v", tc.typ, tc.v)
	}
	empty, err := toCell(TypeCurrency, nil)
	require.NoError(t, err)
	assert.Equal(t, cellEmpty, empty.kind)
}

func TestSignificantDigits(t *testing.T) {
	for s, want := range map[string]int{
		"1234.50": 5, "-0.05": 1, "007": 1, "0": 0, "100": 3, "123456789012345": 15,
	} {
		assert.Equal(t, want, significantDigits(s), s)
	}
}

func TestDescribe(t *testing.T) {
	for _, tc := range []struct {
		v    any
		want string
	}{
		{nil, "None"},
		{"x", `the string "x"`},
		{true, "a bool"},
		{int64(3), "the integer 3"},
		{2.5, "the float 2.5"},
		{[]any{}, "a list"},
		{map[string]any{}, "a dict"},
		{int8(1), "a int8"},
	} {
		assert.Equal(t, tc.want, describe(tc.v))
	}
}
