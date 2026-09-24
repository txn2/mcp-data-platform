package scriptout

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"slices"
	"strings"
)

// Formats an output may be appended to across calls (#1861). Both are
// sequences of independent records, so a page serialized on its own is the
// same bytes it would be inside the whole: a CSV page after the first drops
// its header, and a JSON-lines page is its lines.
const (
	formatCSV   = "csv"
	formatJSONL = "jsonl"
)

// AppendFormats lists the formats Spool accepts, for the refusal that names
// them.
var AppendFormats = []string{formatCSV, formatJSONL}

// Spool is one output a run appends to across platform.export calls (#1861):
// each page is serialized as it arrives and only the bytes are kept, so a
// script can page an API or a warehouse into one file, and one table, without
// holding every page as Starlark values -- which cost roughly seventy times
// their serialized size. The output is written once, when the run finishes.
type Spool struct {
	name, format string
	// columns is the header a CSV output was started with, which every later
	// page is written under.
	columns []string
	data    bytes.Buffer
	rows    int
	ident   Identity
}

// NewSpool starts an appended output in format, refusing a format whose pages
// cannot be joined as bytes.
func NewSpool(name, format string) (*Spool, error) {
	if !slices.Contains(AppendFormats, format) {
		return nil, fmt.Errorf("output %q: append=True writes %s, whose pages join as bytes; %q does not. "+
			"Append as jsonl and register it as a table, or collect the rows and export once",
			name, strings.Join(AppendFormats, " or "), format)
	}
	return &Spool{name: name, format: format}, nil
}

// Append serializes one page of rows, projected onto columns, onto the output.
func (s *Spool) Append(columns []string, rows []any) error {
	if s.format == formatCSV {
		if s.columns == nil {
			s.columns = columns
		} else if extra := missingFrom(s.columns, columns); len(extra) > 0 {
			return fmt.Errorf("output %q: this page has column(s) %s that the output's header, set by its first page, "+
				"does not; a CSV file has one header, so give every page the same columns",
				s.name, strings.Join(extra, ", "))
		}
		columns = s.columns
	}
	data, ident, err := Rows(s.name, s.format, columns, rows)
	if err != nil {
		return err
	}
	if s.format == formatCSV && s.data.Len() > 0 {
		if data, err = withoutHeader(data); err != nil {
			return fmt.Errorf("output %q: %w", s.name, err)
		}
	}
	if s.data.Len()+len(data) > MaxBytes {
		return fmt.Errorf("output %q would be %d bytes, over the %d-byte limit; write the rest to a second output",
			s.name, s.data.Len()+len(data), MaxBytes)
	}
	_, _ = s.data.Write(data)
	s.rows += len(rows)
	s.ident = ident
	return nil
}

// Rows is how many rows the output holds so far.
func (s *Spool) Rows() int { return s.rows }

// Bytes is the output's size so far.
func (s *Spool) Bytes() int { return s.data.Len() }

// Data is the whole output and the identity it is stored under. A spool that
// was appended nothing is an empty file in its format.
func (s *Spool) Data() ([]byte, Identity, error) {
	if s.ident.ContentType == "" {
		_, ident, err := Rows(s.name, s.format, s.columns, nil)
		if err != nil {
			return nil, Identity{}, err
		}
		s.ident = ident
	}
	return s.data.Bytes(), s.ident, nil
}

// withoutHeader drops the first record of a CSV page. The header is parsed
// rather than cut at the first newline, because a quoted column name may hold
// one.
func withoutHeader(page []byte) ([]byte, error) {
	r := csv.NewReader(bytes.NewReader(page))
	r.FieldsPerRecord = -1
	if _, err := r.Read(); err != nil {
		return nil, fmt.Errorf("reading the page's header: %w", err)
	}
	return page[r.InputOffset():], nil
}

// missingFrom lists the names in got that header does not have.
func missingFrom(header, got []string) []string {
	var out []string
	for _, name := range got {
		if !slices.Contains(header, name) {
			out = append(out, name)
		}
	}
	return out
}
