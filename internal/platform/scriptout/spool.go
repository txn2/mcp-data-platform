package scriptout

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"slices"
	"strings"

	"go.starlark.net/starlark"

	"github.com/txn2/mcp-data-platform/pkg/contenttype"
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
	// copied is a spool holding copy_out's bytes, written whole, rather than
	// an appended output's pages (#2453).
	copied bool
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

// Copied reports whether the spool holds copy_out's bytes rather than an
// appended output's pages.
func (s *Spool) Copied() bool { return s.copied }

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

// Copied is the bytes platform.copy_out read from an application database
// (#2453): already serialized, held outside the script's values, and handed
// to the output writer whole. As a Starlark value it is only what the
// binding passes on to the export path; a script never holds one.
type Copied struct {
	Data   []byte
	Rows   int
	Format string
}

// CopyFormatText is COPY's text format, stored as tab-separated values.
const CopyFormatText = "text"

// Spool is the output the copied bytes are written as: CSV, or COPY's text
// format as tab-separated values.
func (c *Copied) Spool(name string) *Spool {
	s := &Spool{name: name, format: formatCSV, rows: c.Rows, ident: Identity{ContentType: contenttype.CSV, Extension: "csv"}, copied: true}
	if c.Format == CopyFormatText {
		s.ident = Identity{ContentType: contenttype.TSV, Extension: "tsv"}
	}
	// The bytes are the output's own, handed over rather than copied: an
	// output may be 100 MB.
	s.data = *bytes.NewBuffer(c.Data)
	return s
}

// String implements starlark.Value.
func (c *Copied) String() string { return fmt.Sprintf("<copied %d rows>", c.Rows) }

// Type implements starlark.Value.
func (*Copied) Type() string { return "copied" }

// Freeze implements starlark.Value: the bytes are never changed.
func (*Copied) Freeze() {}

// Truth implements starlark.Value.
func (*Copied) Truth() starlark.Bool { return starlark.True }

// Hash implements starlark.Value: copied bytes are not a dict key.
func (*Copied) Hash() (uint32, error) { return 0, errors.New("unhashable type: copied") }
