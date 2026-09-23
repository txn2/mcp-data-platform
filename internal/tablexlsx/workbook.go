// Package tablexlsx writes an Excel workbook from a workbook described as
// data (#1849): named sheets of columns and rows, a small vocabulary of column
// types, and the few presentation facts a report needs -- a bold header row,
// column widths, a frozen pane and an optional title row. It is deliberately
// not a styling API: a sheet says what its columns ARE, and the writer decides
// how each type is shown.
//
// Numbers the script computed exactly stay exact. A currency or decimal cell
// is written from its decimal text into the cell's value, so "1234.50" is the
// number the file carries, not the nearest float64 to it. See cells.go.
package tablexlsx

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/xuri/excelize/v2"
)

// ContentType is the media type of an Excel workbook.
const ContentType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"

// Extension is the file extension of an Excel workbook.
const Extension = ".xlsx"

// ColumnType is one entry of the vocabulary a sheet's column_types uses.
type ColumnType string

// The column types a sheet may declare. A column that declares none is written
// from its values: text as text, a number as a number, a bool as a bool.
const (
	TypeString   ColumnType = "string"
	TypeInteger  ColumnType = "integer"
	TypeDecimal  ColumnType = "decimal"
	TypeCurrency ColumnType = "currency"
	TypeDate     ColumnType = "date"
	TypeDateTime ColumnType = "datetime"
	TypePercent  ColumnType = "percent"
)

// columnTypes is the vocabulary, in the order a refusal lists it.
var columnTypes = []ColumnType{
	TypeString, TypeInteger, TypeDecimal, TypeCurrency, TypeDate, TypeDateTime, TypePercent,
}

// Limits a workbook is held to before a byte is written.
const (
	// MaxSheets bounds the sheets of one workbook. Excel has no fixed limit;
	// a workbook with more sheets than this is several outputs.
	MaxSheets = 255
	// MaxCells bounds the cells of one workbook, across its sheets. The
	// writer holds the workbook in memory while it builds it, so the bound is
	// on what is built, not only on the bytes the result compresses to.
	MaxCells = 2_000_000
	// maxSheetName is Excel's limit on a sheet name, in UTF-16 units.
	maxSheetName = excelize.MaxSheetNameLength
	// maxCellText is Excel's limit on the text of one cell, in UTF-16 units.
	// excelize truncates longer text silently; it is refused here instead.
	maxCellText = excelize.TotalCellChars
)

// Keys a body and a sheet may carry. Anything else is refused, so a
// misspelled "column_type" fails loudly instead of being ignored.
const (
	keySheets      = "sheets"
	keyName        = "name"
	keyColumns     = "columns"
	keyRows        = "rows"
	keyColumnTypes = "column_types"
	keyWidths      = "widths"
	keyFreeze      = "freeze"
	keyTitle       = "title"
)

var sheetKeys = map[string]bool{
	keyName: true, keyColumns: true, keyRows: true, keyColumnTypes: true,
	keyWidths: true, keyFreeze: true, keyTitle: true,
}

// illegalSheetChars are the characters Excel refuses in a sheet name.
const illegalSheetChars = `[]:*?/\`

// Workbook is a parsed, validated workbook description.
type Workbook struct {
	Sheets []Sheet
}

// Sheet is one sheet of a workbook: its columns in order, each row's values
// in that order, and the presentation facts it declared.
type Sheet struct {
	Name    string
	Title   string
	Columns []string
	// Types is the declared type of each column that declared one.
	Types map[string]ColumnType
	// Widths is the declared width of each column that declared one, in
	// Excel's character units.
	Widths map[string]float64
	// Freeze is the top-left cell of the scrolling pane, "" for none.
	Freeze string
	// Rows holds each row's values, positional against Columns.
	Rows [][]any
}

// SheetShape is what a report says about one sheet: its name and how many
// data rows it holds (the header and title rows are not counted).
type SheetShape struct {
	Name string `json:"name"`
	Rows int    `json:"rows"`
}

// Shape reports each sheet's name and data row count, in order.
func (w *Workbook) Shape() []SheetShape {
	out := make([]SheetShape, 0, len(w.Sheets))
	for _, s := range w.Sheets {
		out = append(out, SheetShape{Name: s.Name, Rows: len(s.Rows)})
	}
	return out
}

// RowCount is the data rows across every sheet.
func (w *Workbook) RowCount() int {
	n := 0
	for _, s := range w.Sheets {
		n += len(s.Rows)
	}
	return n
}

// Parse reads a workbook description: a map with one key, "sheets", a list
// of sheet maps. Values are the plain Go values a JSON decode or a script's
// conversion produces (string, int64, float64, bool, nil, []any,
// map[string]any).
//
// columnsFor supplies the column order for a sheet whose description names
// no "columns": the caller that still holds the rows in their written order
// (a script's dicts keep insertion order; a Go map does not) reads it there.
// It may be nil, in which case such a sheet is refused.
func Parse(body any, columnsFor func(sheet int) []string) (*Workbook, error) {
	top, ok := body.(map[string]any)
	if !ok {
		return nil, fmt.Errorf(`an xlsx body is a dict {"sheets": [...]}, got %s`, describe(body))
	}
	for key := range top {
		if key != keySheets {
			return nil, fmt.Errorf(`unknown key %q in the xlsx body; it takes "sheets", a list of sheets`, key)
		}
	}
	list, ok := top[keySheets].([]any)
	if !ok || len(list) == 0 {
		return nil, errors.New(`an xlsx body needs "sheets", a non-empty list of sheets`)
	}
	if len(list) > MaxSheets {
		return nil, fmt.Errorf("the workbook has %d sheets, more than the %d a workbook may hold", len(list), MaxSheets)
	}
	b := builder{book: &Workbook{Sheets: make([]Sheet, 0, len(list))}, seen: map[string]string{}}
	for i, raw := range list {
		var derived []string
		if columnsFor != nil {
			derived = columnsFor(i)
		}
		if err := b.add(i, raw, derived); err != nil {
			return nil, err
		}
	}
	return b.book, nil
}

// builder accumulates a workbook's sheets and the limits that span them.
type builder struct {
	book  *Workbook
	seen  map[string]string
	cells int
}

// add parses one sheet and admits it: a name no earlier sheet has, ignoring
// case as Excel compares them, and room under the workbook's cell bound.
func (b *builder) add(i int, raw any, derived []string) error {
	sheet, err := parseSheet(i, raw, derived)
	if err != nil {
		return err
	}
	folded := strings.ToLower(sheet.Name)
	if prior, dup := b.seen[folded]; dup {
		return fmt.Errorf("sheet name %q is used twice (as %q before it); sheet names are unique, ignoring case", sheet.Name, prior)
	}
	b.seen[folded] = sheet.Name
	if b.cells += len(sheet.Columns) * (len(sheet.Rows) + 1); b.cells > MaxCells {
		return fmt.Errorf("the workbook holds more than %d cells; aggregate in SQL or write fewer rows", MaxCells)
	}
	b.book.Sheets = append(b.book.Sheets, sheet)
	return nil
}

// parseSheet reads and checks one sheet description.
func parseSheet(i int, raw any, derived []string) (Sheet, error) {
	m, ok := raw.(map[string]any)
	if !ok {
		return Sheet{}, fmt.Errorf("sheet %d is %s; each sheet is a dict with a name, columns and rows", i+1, describe(raw))
	}
	for key := range m {
		if !sheetKeys[key] {
			return Sheet{}, fmt.Errorf("sheet %d: unknown key %q; a sheet takes name, columns, rows, column_types, widths, freeze and title", i+1, key)
		}
	}
	name, err := optionalString(m, keyName, fmt.Sprintf("sheet %d", i+1))
	if err != nil {
		return Sheet{}, err
	}
	if err := CheckSheetName(name); err != nil {
		return Sheet{}, fmt.Errorf("sheet %d: %w", i+1, err)
	}
	sheet := Sheet{Name: name}
	if err := sheet.readLayout(m, derived); err != nil {
		return Sheet{}, err
	}
	if err := sheet.readRows(m[keyRows]); err != nil {
		return Sheet{}, err
	}
	return sheet, nil
}

// readLayout reads a sheet's columns and its presentation facts.
func (s *Sheet) readLayout(m map[string]any, derived []string) error {
	columns, err := readColumns(s.Name, m[keyColumns], derived)
	if err != nil {
		return err
	}
	s.Columns = columns
	if s.Types, err = readTypes(s.Name, m[keyColumnTypes], columns); err != nil {
		return err
	}
	if s.Widths, err = readWidths(s.Name, m[keyWidths], columns); err != nil {
		return err
	}
	if s.Title, err = optionalString(m, keyTitle, fmt.Sprintf("sheet %q", s.Name)); err != nil {
		return err
	}
	if err := checkText(s.Title); err != nil {
		return fmt.Errorf("sheet %q: the title %w", s.Name, err)
	}
	if s.Freeze, err = optionalString(m, keyFreeze, fmt.Sprintf("sheet %q", s.Name)); err != nil {
		return err
	}
	return s.checkFreeze()
}

// CheckSheetName holds a sheet name to Excel's rules: 1 to 31 characters
// (UTF-16 units, as Excel counts them), none of []:*?/\, not beginning or
// ending with an apostrophe, and no character Excel cannot store.
func CheckSheetName(name string) error {
	switch {
	case name == "":
		return errors.New(`a sheet needs a "name" of 1 to 31 characters`)
	case utf16Len(name) > maxSheetName:
		return fmt.Errorf("sheet name %q is %d characters, over Excel's limit of %d", name, utf16Len(name), maxSheetName)
	case strings.ContainsAny(name, illegalSheetChars):
		return fmt.Errorf("sheet name %q contains one of %s, which Excel does not allow in a sheet name", name, illegalSheetChars)
	case strings.HasPrefix(name, "'") || strings.HasSuffix(name, "'"):
		return fmt.Errorf("sheet name %q begins or ends with an apostrophe, which Excel does not allow", name)
	}
	if err := checkText(name); err != nil {
		return fmt.Errorf("sheet name %q %w", name, err)
	}
	return nil
}

// readColumns reads a sheet's column list, falling back to the order the
// caller derived from the rows.
func readColumns(sheet string, raw any, derived []string) ([]string, error) {
	var columns []string
	switch list := raw.(type) {
	case nil:
		columns = derived
	case []any:
		columns = make([]string, 0, len(list))
		for _, c := range list {
			name, ok := c.(string)
			if !ok {
				return nil, fmt.Errorf("sheet %q: columns must be strings, got %s", sheet, describe(c))
			}
			columns = append(columns, name)
		}
	default:
		return nil, fmt.Errorf("sheet %q: columns must be a list of names, got %s", sheet, describe(raw))
	}
	if len(columns) == 0 {
		return nil, fmt.Errorf(`sheet %q has no columns: name them in "columns", or give rows as dicts`, sheet)
	}
	if len(columns) > excelize.MaxColumns {
		return nil, fmt.Errorf("sheet %q has %d columns, more than Excel's %d", sheet, len(columns), excelize.MaxColumns)
	}
	return columns, checkColumnNames(sheet, columns)
}

// checkColumnNames refuses an empty, repeated or unstorable column name.
func checkColumnNames(sheet string, columns []string) error {
	seen := make(map[string]bool, len(columns))
	for _, c := range columns {
		if c == "" {
			return fmt.Errorf("sheet %q: a column name is empty", sheet)
		}
		if seen[c] {
			return fmt.Errorf("sheet %q: column %q is named twice", sheet, c)
		}
		seen[c] = true
		if err := checkText(c); err != nil {
			return fmt.Errorf("sheet %q: column name %q %w", sheet, c, err)
		}
	}
	return nil
}

// readTypes reads column_types: a dict of column name to a type in the
// vocabulary.
func readTypes(sheet string, raw any, columns []string) (map[string]ColumnType, error) {
	entries, err := columnMap(sheet, keyColumnTypes, raw, columns)
	if err != nil {
		return nil, err
	}
	out := make(map[string]ColumnType, len(entries))
	for column, v := range entries {
		name, _ := v.(string)
		t := ColumnType(name)
		if !knownType(t) {
			return nil, fmt.Errorf("sheet %q: column %q has type %s; the types are %s",
				sheet, column, describe(v), typeList())
		}
		out[column] = t
	}
	return out, nil
}

// readWidths reads widths: a dict of column name to a width in Excel's
// character units, above 0 and at most 255.
func readWidths(sheet string, raw any, columns []string) (map[string]float64, error) {
	entries, err := columnMap(sheet, keyWidths, raw, columns)
	if err != nil {
		return nil, err
	}
	out := make(map[string]float64, len(entries))
	for column, v := range entries {
		var width float64
		switch n := v.(type) {
		case int64:
			width = float64(n)
		case float64:
			width = n
		default:
			return nil, fmt.Errorf("sheet %q: the width of column %q must be a number, got %s", sheet, column, describe(v))
		}
		if !(width > 0 && width <= excelize.MaxColumnWidth) {
			return nil, fmt.Errorf("sheet %q: the width of column %q is %v; a width is above 0 and at most %d",
				sheet, column, v, excelize.MaxColumnWidth)
		}
		out[column] = width
	}
	return out, nil
}

// columnMap reads a dict keyed by column name, refusing a key that names no
// column.
func columnMap(sheet, key string, raw any, columns []string) (map[string]any, error) {
	if raw == nil {
		return map[string]any{}, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("sheet %q: %s must be a dict keyed by column name, got %s", sheet, key, describe(raw))
	}
	known := make(map[string]bool, len(columns))
	for _, c := range columns {
		known[c] = true
	}
	for column := range m {
		if !known[column] {
			return nil, fmt.Errorf("sheet %q: %s names column %q, which the sheet does not have; its columns are %s",
				sheet, key, column, strings.Join(columns, ", "))
		}
	}
	return m, nil
}

// checkFreeze holds freeze to a cell reference below or right of A1.
func (s *Sheet) checkFreeze() error {
	if s.Freeze == "" {
		return nil
	}
	col, row, err := excelize.CellNameToCoordinates(s.Freeze)
	if err != nil {
		return fmt.Errorf(`sheet %q: freeze %q is not a cell reference; name the first cell that scrolls, such as "A2" to keep the header row in view`,
			s.Name, s.Freeze)
	}
	if col == 1 && row == 1 {
		return fmt.Errorf(`sheet %q: freeze "A1" freezes nothing; name the first cell that scrolls, such as "A2"`, s.Name)
	}
	s.Freeze = strings.ToUpper(s.Freeze)
	return nil
}

// readRows reads a sheet's rows: each a dict, projected onto the columns, or
// a list, positional against them. A dict's keys the columns do not name are
// not written: columns chooses and orders what the sheet shows.
func (s *Sheet) readRows(raw any) error {
	if raw == nil {
		return nil
	}
	list, ok := raw.([]any)
	if !ok {
		return fmt.Errorf("sheet %q: rows must be a list, got %s", s.Name, describe(raw))
	}
	if limit := excelize.TotalRows - s.headerRows(); len(list) > limit {
		return fmt.Errorf("sheet %q has %d rows, more than the %d Excel can hold beneath its header", s.Name, len(list), limit)
	}
	s.Rows = make([][]any, 0, len(list))
	for i, r := range list {
		row, err := s.project(r)
		if err != nil {
			return fmt.Errorf("sheet %q, row %d: %w", s.Name, i+1, err)
		}
		s.Rows = append(s.Rows, row)
	}
	return nil
}

// project lays one row out against the sheet's columns.
func (s *Sheet) project(r any) ([]any, error) {
	switch row := r.(type) {
	case map[string]any:
		out := make([]any, len(s.Columns))
		for i, c := range s.Columns {
			out[i] = row[c]
		}
		return out, nil
	case []any:
		if len(row) != len(s.Columns) {
			return nil, fmt.Errorf("the row has %d values and the sheet has %d columns", len(row), len(s.Columns))
		}
		return row, nil
	default:
		return nil, fmt.Errorf("a row is a dict or a list, got %s", describe(r))
	}
}

// headerRows is how many rows sit above the data: the header, and the title
// when there is one.
func (s *Sheet) headerRows() int {
	if s.Title != "" {
		return 2 //nolint:mnd // a title row and a header row
	}
	return 1
}

// optionalString reads a string-valued key, "" when absent.
func optionalString(m map[string]any, key, where string) (string, error) {
	raw, present := m[key]
	if !present || raw == nil {
		return "", nil
	}
	s, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("the %s of %s must be a string, got %s", key, where, describe(raw))
	}
	return s, nil
}

// checkText refuses text Excel cannot store: a character XML 1.0 does not
// allow (a control character other than tab, line feed and carriage return,
// or U+FFFE and U+FFFF), text that is not valid UTF-8, and text longer than
// a cell holds. The error completes a sentence naming what was checked.
func checkText(s string) error {
	if !utf8.ValidString(s) {
		return errors.New("is not valid UTF-8")
	}
	for i, r := range s {
		if illegalRune(r) {
			return fmt.Errorf("contains the control character U+%04X at byte %d, which an Excel file cannot hold; remove it before exporting", r, i)
		}
	}
	if n := utf16Len(s); n > maxCellText {
		return fmt.Errorf("is %d characters, over the %d an Excel cell holds", n, maxCellText)
	}
	return nil
}

// The characters XML 1.0 excludes above the control range.
const (
	firstPrintable = 0x20
	nonCharFFFE    = 0xFFFE
	nonCharFFFF    = 0xFFFF
)

// illegalRune reports a character XML 1.0 has no representation for.
func illegalRune(r rune) bool {
	if r < firstPrintable {
		return r != '\t' && r != '\n' && r != '\r'
	}
	return r == nonCharFFFE || r == nonCharFFFF
}

// utf16Len is a string's length in UTF-16 units, the way Excel counts.
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}

// knownType reports whether t is in the vocabulary.
func knownType(t ColumnType) bool {
	return slices.Contains(columnTypes, t)
}

// typeList names the vocabulary for a refusal.
func typeList() string {
	names := make([]string, 0, len(columnTypes))
	for _, t := range columnTypes {
		names = append(names, string(t))
	}
	return strings.Join(names, ", ")
}

// describe names a value's shape for a refusal in the words a script author
// uses.
func describe(v any) string {
	switch t := v.(type) {
	case nil:
		return "None"
	case string:
		return fmt.Sprintf("the string %q", t)
	case bool:
		return "a bool"
	case int64, int:
		return fmt.Sprintf("the integer %v", t)
	case float64:
		return fmt.Sprintf("the float %v", t)
	case []any:
		return "a list"
	case map[string]any:
		return "a dict"
	default:
		return fmt.Sprintf("a %T", v)
	}
}
