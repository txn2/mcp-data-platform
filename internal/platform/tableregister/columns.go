package tableregister

import (
	"bytes"
	"encoding/csv"
	"strings"
	"unicode"

	"github.com/txn2/mcp-data-platform/internal/platform/tablecsv"
)

// maxColumns caps how wide a registered table can be. A header longer than
// this is a file whose first line is data, not a header, far more often than
// it is a real table.
const maxColumns = 512

// ReadHeaderColumns parses the first line of a CSV and returns the columns a
// table registered over it declares.
//
// A blank column name is filled in positionally rather than refused: a
// trailing comma or an unnamed index column is ordinary in an exported CSV,
// and a table that refuses to exist over it helps nobody. A duplicate name is
// suffixed for the same reason -- the file is what it is, and the column still
// has to be addressable.
func ReadHeaderColumns(content []byte) ([]Column, error) {
	reader := csv.NewReader(bytes.NewReader(content))
	// A ragged file still has a header; the count here must not be pinned to
	// whatever the first record happened to hold.
	reader.FieldsPerRecord = -1

	record, err := reader.Read()
	if err != nil {
		return nil, ErrEmptyHeader
	}
	if len(record) == 0 {
		return nil, ErrEmptyHeader
	}
	if len(record) > maxColumns {
		return nil, refusedf("the file's first line has %d fields, more than the %d a registered table may declare",
			len(record), maxColumns)
	}

	if allBlank(record) {
		return nil, ErrEmptyHeader
	}
	return tablecsv.ColumnsFrom(record), nil
}

// allBlank reports whether every field of the header was empty, which is a
// file that begins with a blank line rather than one with unnamed columns.
func allBlank(record []string) bool {
	for _, f := range record {
		if strings.TrimSpace(f) != "" {
			return false
		}
	}
	return true
}

// QuoteIdentifier renders a name as a Trino delimited identifier.
//
// Every identifier the registrar puts in a statement goes through this,
// including ones it derived itself: a column name comes from the first line of
// a file somebody uploaded, and the DDL is assembled as text because Trino has
// no parameter binding for identifiers. Doubling the quote is what closes
// that.
func QuoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// QuoteLiteral renders a value as a Trino string literal, for the table
// properties -- the external location -- that are values rather than names.
func QuoteLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

// maxTableNameLength bounds a generated table name. It is well inside what
// Trino accepts and leaves room for the persona prefix.
const maxTableNameLength = 96

// nameSeparator joins the parts of a generated identifier and is what a run of
// unusable characters collapses to.
const nameSeparator = "_"

// SlugifyTableName turns a filename or a person's suggestion into a table name
// Trino accepts unquoted: lowercase, alphanumeric and underscore, never
// leading with a digit.
func SlugifyTableName(raw string) string {
	// A filename's extension is not part of what the table is called.
	if idx := strings.LastIndex(raw, "."); idx > 0 {
		raw = raw[:idx]
	}

	slug := strings.Trim(collapseToIdentifier(raw), nameSeparator)
	if slug == "" {
		return ""
	}
	// An identifier may not lead with a digit, and a name that did would have
	// to be quoted everywhere it was written.
	if slug[0] >= '0' && slug[0] <= '9' {
		slug = "t" + nameSeparator + slug
	}
	if len(slug) > maxTableNameLength {
		slug = strings.Trim(slug[:maxTableNameLength], nameSeparator)
	}
	return slug
}

// collapseToIdentifier lowercases, keeps letters and digits, and turns every
// run of anything else into one separator.
func collapseToIdentifier(raw string) string {
	var b strings.Builder
	separated := false
	for _, r := range strings.ToLower(raw) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			separated = false
		case !separated && b.Len() > 0:
			b.WriteString(nameSeparator)
			separated = true
		}
	}
	return b.String()
}

// PrefixedTableName is the name a registration takes: the persona, then the
// slug.
//
// The scratch schema is one shared workspace -- everyone granted the
// connection sees every table in it, and resource and asset permissions are
// not carried into Trino. The prefix is not a boundary and does not pretend to
// be one; it is what keeps two people who both registered "vendors" from
// colliding on the name, and what tells a reader of the schema whose working
// table they are looking at.
func PrefixedTableName(persona, slug string) string {
	prefix := SlugifyTableName(persona)
	if prefix == "" || strings.HasPrefix(slug, prefix+nameSeparator) {
		return slug
	}
	return prefix + nameSeparator + slug
}
