package tablexlsx

import (
	"errors"
	"fmt"

	"github.com/xuri/excelize/v2"
)

// Built-in Excel number formats the column types are shown with.
const (
	numFmtThousands        = 3  // #,##0
	numFmtThousandsDecimal = 4  // #,##0.00
	numFmtPercent          = 10 // 0.00%
	titleFontSize          = 14
)

// titleCell is where a sheet's title is written.
const titleCell = "A1"

// creator is the author a workbook's properties name.
const creator = "mcp-data-platform"

// Custom number formats for the date types: ISO order, which reads the same
// in every locale the file is opened in.
var (
	dateFormat     = "yyyy-mm-dd"
	dateTimeFormat = "yyyy-mm-dd hh:mm:ss"
)

// typeStyles is how each column type is shown. decimal and string take the
// General format, which shows the value as written.
var typeStyles = map[ColumnType]*excelize.Style{
	TypeInteger:  {NumFmt: numFmtThousands},
	TypeCurrency: {NumFmt: numFmtThousandsDecimal},
	TypePercent:  {NumFmt: numFmtPercent},
	TypeDate:     {CustomNumFmt: &dateFormat},
	TypeDateTime: {CustomNumFmt: &dateTimeFormat},
}

// styles holds the style ids registered in one file.
type styles struct {
	header int
	title  int
	byType map[ColumnType]int
}

// newStyles registers the styles a workbook uses in f, in vocabulary order
// rather than map order, so the same workbook is the same bytes.
func newStyles(f *excelize.File) (styles, error) {
	st := styles{byType: make(map[ColumnType]int, len(typeStyles))}
	var errs []error
	add := func(style *excelize.Style) int {
		id, err := f.NewStyle(style)
		errs = append(errs, err)
		return id
	}
	st.header = add(&excelize.Style{Font: &excelize.Font{Bold: true}})
	st.title = add(&excelize.Style{Font: &excelize.Font{Bold: true, Size: titleFontSize}})
	for _, t := range columnTypes {
		if style, ok := typeStyles[t]; ok {
			st.byType[t] = add(style)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return styles{}, fmt.Errorf("registering the workbook styles: %w", err)
	}
	return st, nil
}

// Write renders the workbook as an .xlsx file and refuses a result larger
// than maxBytes. A value a column cannot hold fails the write naming its
// sheet, row and column, and nothing is returned.
func (w *Workbook) Write(maxBytes int) ([]byte, error) {
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	if err := f.SetDocProps(&excelize.DocProperties{Creator: creator}); err != nil {
		return nil, fmt.Errorf("writing the workbook properties: %w", err)
	}
	st, err := newStyles(f)
	if err != nil {
		return nil, err
	}
	first := f.GetSheetName(0)
	for i := range w.Sheets {
		s := &w.Sheets[i]
		if i == 0 {
			err = f.SetSheetName(first, s.Name)
		} else {
			_, err = f.NewSheet(s.Name)
		}
		if err != nil {
			return nil, fmt.Errorf("sheet %q: %w", s.Name, err)
		}
		if err := s.write(f, st); err != nil {
			return nil, err
		}
	}
	buf, err := f.WriteToBuffer()
	if err != nil {
		return nil, fmt.Errorf("writing the workbook: %w", err)
	}
	if buf.Len() > maxBytes {
		return nil, fmt.Errorf("the workbook is %d bytes, over the %d-byte limit; aggregate in SQL or write fewer rows", buf.Len(), maxBytes)
	}
	return buf.Bytes(), nil
}

// write lays one sheet out in f: the title row, the header row, the data
// rows, then each column's style and width and the frozen pane.
func (s *Sheet) write(f *excelize.File, st styles) error {
	row := 1
	if s.Title != "" {
		if err := s.writeTitle(f, st); err != nil {
			return err
		}
		row++
	}
	header := make([]any, len(s.Columns))
	for j, column := range s.Columns {
		header[j] = column
	}
	err := errors.Join(
		f.SetSheetRow(s.Name, ref(0, row), &header),
		f.SetCellStyle(s.Name, ref(0, row), ref(len(s.Columns)-1, row), st.header),
	)
	if err != nil {
		return fmt.Errorf("sheet %q: writing the header row: %w", s.Name, err)
	}
	if err := s.writeRows(f, row+1); err != nil {
		return err
	}
	if err := s.styleColumns(f, st, row+1); err != nil {
		return err
	}
	return s.freeze(f)
}

// writeTitle writes the title in A1, bold and larger, across the columns.
func (s *Sheet) writeTitle(f *excelize.File, st styles) error {
	errs := []error{
		f.SetCellStr(s.Name, titleCell, s.Title),
		f.SetCellStyle(s.Name, titleCell, titleCell, st.title),
	}
	if len(s.Columns) > 1 {
		errs = append(errs, f.MergeCell(s.Name, titleCell, ref(len(s.Columns)-1, 1)))
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("sheet %q: writing the title: %w", s.Name, err)
	}
	return nil
}

// writeRows writes each data row from firstRow down.
func (s *Sheet) writeRows(f *excelize.File, firstRow int) error {
	for i, values := range s.Rows {
		for j, v := range values {
			column := s.Columns[j]
			c, err := toCell(s.Types[column], v)
			if err != nil {
				return fmt.Errorf("sheet %q, row %d, column %q: %w", s.Name, i+1, column, err)
			}
			if err := setCell(f, s.Name, ref(j, firstRow+i), c); err != nil {
				return fmt.Errorf("sheet %q, row %d, column %q: %w", s.Name, i+1, column, err)
			}
		}
	}
	return nil
}

// setCell stores one converted value.
func setCell(f *excelize.File, sheet, at string, c cell) error {
	switch c.kind {
	case cellText:
		return f.SetCellStr(sheet, at, c.text) //nolint:wrapcheck // the caller names the sheet, row and column
	case cellNumber:
		// The decimal text goes into the cell's value as written; see cellNumber.
		return f.SetCellDefault(sheet, at, c.text) //nolint:wrapcheck // as above
	case cellBool:
		return f.SetCellBool(sheet, at, c.flag) //nolint:wrapcheck // as above
	default:
		return nil
	}
}

// styleColumns applies each declared column's number format to its data
// cells, and each declared width.
func (s *Sheet) styleColumns(f *excelize.File, st styles, firstRow int) error {
	var errs []error
	for j, column := range s.Columns {
		if id, ok := st.byType[s.Types[column]]; ok && len(s.Rows) > 0 {
			errs = append(errs, f.SetCellStyle(s.Name, ref(j, firstRow), ref(j, firstRow+len(s.Rows)-1), id))
		}
		if width, ok := s.Widths[column]; ok {
			name, _ := excelize.ColumnNumberToName(j + 1)
			errs = append(errs, f.SetColWidth(s.Name, name, name, width))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("sheet %q: styling the columns: %w", s.Name, err)
	}
	return nil
}

// freeze keeps the rows above and the columns left of the Freeze cell in
// view while the rest scrolls.
func (s *Sheet) freeze(f *excelize.File) error {
	if s.Freeze == "" {
		return nil
	}
	col, row, _ := excelize.CellNameToCoordinates(s.Freeze) // checked by Parse
	pane := "bottomRight"
	switch {
	case col == 1:
		pane = "bottomLeft"
	case row == 1:
		pane = "topRight"
	}
	err := f.SetPanes(s.Name, &excelize.Panes{
		Freeze: true, XSplit: col - 1, YSplit: row - 1, TopLeftCell: s.Freeze, ActivePane: pane,
	})
	if err != nil {
		return fmt.Errorf("sheet %q: freezing at %s: %w", s.Name, s.Freeze, err)
	}
	return nil
}

// ref names the cell at a zero-based column and a one-based row.
func ref(col, row int) string {
	name, _ := excelize.CoordinatesToCellName(col+1, row)
	return name
}
