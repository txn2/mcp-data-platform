package tableregister

import (
	"bytes"
	"encoding/csv"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
)

// A registered table is read by Trino's Hive CSV reader, which is line-based:
// the text input format splits records on "\n" before the quote-aware serde
// sees them. A quoted line break therefore tears one record into fragments,
// the first of which ends on an unbalanced quote, and every field after it
// lands in the wrong column. The table is created, the row is recorded and the
// query returns rows, so nothing downstream can tell that the file was read
// wrongly -- which is why the file is inspected here, before the DDL runs.
//
// The bytes needed to see it are already in hand: contentFor reads the whole
// object, because the header row is taken from it.

// bomUTF8 is the byte-order mark many spreadsheet tools lead a UTF-8 export
// with. It is content to a line-based reader and would become part of the
// first column's name, so a correction drops it.
const bomUTF8 = "\ufeff"

// The encodings a CSV that is not UTF-8 turns out to be.
//
// windows-1252 is the case that reaches the platform: a spreadsheet exported
// on a machine with a legacy code page, whose printable range every
// Latin-script export of that kind stays inside. It is the only one the
// platform converts, because a single-byte code page maps byte for byte and
// the conversion cannot invent a character that was not there.
//
// The rest are named so a refusal can say what the file appears to be. None of
// them is converted: a wide encoding decoded as a code page produces a
// character per byte, which is mojibake in every cell, and writing that back
// as a corrected version of somebody's file would be worse than refusing.
const (
	encodingWindows1252 = "windows-1252"
	encodingUTF16       = "UTF-16"
	encodingUTF32       = "UTF-32"
	// encodingWide is what bytes with no byte-order mark and a NUL in them are
	// called: not text in any single-byte encoding, and not identifiable
	// beyond that.
	encodingWide = "a multi-byte encoding"
)

// lineEndingsCR names the line endings of a file whose lines end in a bare
// carriage return -- the classic Mac ending some spreadsheet exports still
// write. Every reader on this path splits records on "\n" and none of them
// splits on "\r", so such a file is one record to all of them.
const lineEndingsCR = "carriage return"

// CSVDefect is why a CSV cannot be read as a table the way it is stored. A nil
// *CSVDefect means it can.
type CSVDefect struct {
	// Rows is how many records carry a field with a line break inside it.
	Rows int `json:"rows,omitempty"`
	// Columns names the columns those line breaks are in, in header order.
	Columns []string `json:"columns,omitempty"`
	// Encoding names what the bytes appear to be when they are not UTF-8, and
	// is empty when they are.
	Encoding string `json:"encoding,omitempty"`
	// LineEndings names what the file's lines end in when that is not
	// something a line-based reader splits on, and is empty when it is.
	LineEndings string `json:"line_endings,omitempty"`
}

// InspectCSV reports why a CSV cannot be read by a line-based reader, or nil
// when it can.
//
// Three conditions refuse. Lines that end in something the reader does not
// split on run the records that end in one together into a single record, a
// line break inside a field tears the record, and bytes that are not UTF-8
// reach every cell as replacement characters. None of them is visible in the result of a query over the table,
// so all three are answered before one exists.
//
// The line endings are settled first and the record scan runs over the
// translated bytes, because the scan is itself a line-based reader: over a
// carriage-return file it sees one record, reports the file as a single torn
// row, and names columns made out of the header line joined to the line after
// it.
func InspectCSV(content []byte) *CSVDefect {
	defect := CSVDefect{Encoding: sourceEncoding(content)}
	// Only where the bytes are single-byte text. A carriage return in a wide
	// encoding is two bytes or more and the file is refused for its encoding
	// anyway, so translating one byte of it would be a guess.
	if defect.Correctable() {
		content, defect.LineEndings = withLineFeeds(content)
	}
	defect.Rows, defect.Columns = scanEmbeddedBreaks(content)
	if defect.Rows == 0 && defect.Encoding == "" && defect.LineEndings == "" {
		return nil
	}
	return &defect
}

// withLineFeeds returns the content with carriage-return line endings
// translated to "\n", and names the endings when it translated any.
//
// A carriage return that is not part of a "\r\n" is one of two things: the end
// of a record, which no reader on this path splits on, or a line break inside
// a cell, which is the defect the record scan is for. The bytes do not say
// which, so the parse does: the translation is kept only where it recovers
// records the reader could not see. Anything else is handed back exactly as it
// came in, so a file that reads correctly today keeps doing so.
func withLineFeeds(content []byte) (fed []byte, endings string) {
	fed, found := replaceLoneCarriageReturns(content)
	if !found || !recoversRecords(content, fed) {
		return content, ""
	}
	return fed, lineEndingsCR
}

// recoversRecords reports whether translating the carriage returns gave a
// line-based reader records it could not see before.
//
// Which measure answers that depends on what the file already is, and the two
// regimes need different ones.
//
// A file with no line feed anywhere is one record to every reader on this
// path, whatever is in it, and no ordinary CSV is written that way: a file
// that ends its records with newlines has newlines in it. Nothing there is
// ambiguous, so the plain count answers, and it has to -- a classic Mac file
// whose rows do not match its header would score no better on the header's
// width than the single record it starts as, and rejecting its translation
// hands it back to the reader that merges the whole file into one row. Kept,
// the same file is refused honestly by the field-count check instead.
//
// A file that already has newline-delimited records is the ambiguous one. A
// lone carriage return in it is as likely to be a break inside a cell, and
// splitting that cell raises the plain count exactly as a real line ending
// does. There the records are counted by the header's width, because a record
// recovered from a line ending has the columns the header declares and a
// fragment torn out of one cell does not.
func recoversRecords(content, fed []byte) bool {
	if bytes.IndexByte(content, '\n') < 0 {
		return recordCount(fed) > recordCount(content)
	}
	return wellFormedRecords(fed) > wellFormedRecords(content)
}

// recordCount is how many records a line-based reader finds in the content.
// Only the count is read, so the reader's own record buffer is reused.
func recordCount(content []byte) int {
	reader := newCSVReader(content)
	reader.ReuseRecord = true

	count := 0
	for {
		if _, err := reader.Read(); err != nil {
			return count
		}
		count++
	}
}

// replaceLoneCarriageReturns rewrites every carriage return that is not part
// of a "\r\n" as a line feed, and reports whether it found one. A "\r\n" is
// left alone: every reader on this path already folds it to a line feed, so it
// costs a file nothing. Content with none is returned untouched and uncopied.
func replaceLoneCarriageReturns(content []byte) (fed []byte, found bool) {
	fed = content
	for i := range content {
		if content[i] != '\r' || (i+1 < len(content) && content[i+1] == '\n') {
			continue
		}
		if !found {
			fed = make([]byte, len(content))
			copy(fed, content)
			found = true
		}
		fed[i] = '\n'
	}
	return fed, found
}

// wellFormedRecords is how many of the records a line-based reader finds in
// the content have the field count the first of them declares, the header
// counting as one of them.
//
// A parse it cannot finish counts what it read before it stopped, which is
// what the comparison in recoversRecords needs: a file the reader gives up on
// early counts low, and a translation that lets it read further counts high.
//
// Only the width of each record is read, so the reader's own buffer is reused
// rather than a slice allocated per record; the header's width is taken before
// the loop, since the reused slice is the one the header was returned in.
func wellFormedRecords(content []byte) int {
	reader := newCSVReader(content)
	reader.ReuseRecord = true

	header, err := reader.Read()
	if err != nil {
		return 0
	}
	want, count := len(header), 0
	for record := header; err == nil; record, err = reader.Read() {
		if len(record) == want {
			count++
		}
	}
	return count
}

// sourceEncoding names what a file's bytes appear to be, or "" when they are
// valid UTF-8.
//
// The byte-order marks are checked before anything else and widest first,
// since a UTF-32LE mark begins with a UTF-16LE one. A file with no mark and a
// NUL byte in it is not single-byte text either, whatever it is: a CSV in a
// code page has no NUL in it, and treating one as windows-1252 would turn
// every character into two.
func sourceEncoding(content []byte) string {
	switch {
	case utf8.Valid(content):
		return ""
	case bytes.HasPrefix(content, []byte{0xFF, 0xFE, 0x00, 0x00}),
		bytes.HasPrefix(content, []byte{0x00, 0x00, 0xFE, 0xFF}):
		return encodingUTF32
	case bytes.HasPrefix(content, []byte{0xFF, 0xFE}), bytes.HasPrefix(content, []byte{0xFE, 0xFF}):
		return encodingUTF16
	case bytes.IndexByte(content, 0) >= 0:
		return encodingWide
	default:
		return encodingWindows1252
	}
}

// Correctable reports whether the platform can produce a corrected version of
// this file itself. It cannot when the bytes are in an encoding it does not
// convert, because everything else about the file is read through that
// encoding and would be corrected into mojibake.
func (d *CSVDefect) Correctable() bool {
	return d.Encoding == "" || d.Encoding == encodingWindows1252
}

// scanEmbeddedBreaks counts the records carrying a field with a line break in
// it and names the columns those breaks are in.
//
// A parse failure stops the scan rather than failing it: what has been read so
// far is still evidence, and a file this reader cannot finish is one that
// registers today, so refusing it on that ground alone would take away a
// registration that works.
func scanEmbeddedBreaks(content []byte) (rows int, columns []string) {
	reader := newCSVReader(content)

	header, err := reader.Read()
	if err != nil {
		return 0, nil
	}
	names := headerLabels(header)

	hit := map[int]bool{}
	record := header
	for err == nil {
		if recordBreaks(record, hit) {
			rows++
		}
		record, err = reader.Read()
	}
	return rows, labelsFor(names, hit)
}

// recordBreaks records which of a record's fields carry a line break and
// reports whether any did.
func recordBreaks(record []string, hit map[int]bool) bool {
	found := false
	for i, field := range record {
		if strings.ContainsAny(field, "\r\n") {
			hit[i] = true
			found = true
		}
	}
	return found
}

// newCSVReader is the one reader configuration this package parses with. The
// field count is unpinned because a ragged file still has a header, and it is
// not this reader's job to refuse one.
func newCSVReader(content []byte) *csv.Reader {
	reader := csv.NewReader(bytes.NewReader(content))
	reader.FieldsPerRecord = -1
	return reader
}

// headerLabels renders the header row as the names a person reads. They come
// from the same place the table's own column names do, so a refusal and the
// table it refused to create call a column the same thing.
func headerLabels(header []string) []string {
	columns := ColumnsFrom(header)
	names := make([]string, 0, len(columns))
	for _, c := range columns {
		names = append(names, c.Name)
	}
	return names
}

// labelsFor renders the flagged column indexes in header order.
func labelsFor(names []string, hit map[int]bool) []string {
	indexes := make([]int, 0, len(hit))
	for i := range hit {
		indexes = append(indexes, i)
	}
	sort.Ints(indexes)

	labels := make([]string, 0, len(indexes))
	for _, i := range indexes {
		if i < len(names) {
			labels = append(labels, names[i])
			continue
		}
		labels = append(labels, "column "+strconv.Itoa(i+1))
	}
	return labels
}

// Reason is what the person is told: what the file's lines end in when that is
// not a newline, how many rows carry a line break inside a cell, which columns
// those are in, and what the bytes appear to be when they are not UTF-8.
//
// The line endings come first, because they decide how the rest of the file is
// read.
func (d *CSVDefect) Reason() string {
	parts := make([]string, 0, 3)
	if d.LineEndings != "" {
		parts = append(parts,
			"this file's lines end in a "+d.LineEndings+" rather than a newline, and a table splits records on"+
				" the newline, so the records ending in one would be run together into a single row")
	}
	if d.Rows > 0 {
		part := plural(d.Rows, "row", "rows") + " in this file " +
			verb(d.Rows, "has", "have") + " a line break inside a cell"
		if len(d.Columns) > 0 {
			part += " (in " + joinAnd(d.Columns) + ")"
		}
		parts = append(parts,
			part+", and a table reads a line break as the end of the row, so each of those rows would be torn into fragments")
	}
	switch {
	case d.Encoding == encodingWindows1252:
		parts = append(parts,
			"this file's bytes are not UTF-8 and read as "+d.Encoding+
				", so the characters outside plain ASCII would arrive in the table as replacement marks")
	case d.Encoding != "":
		parts = append(parts,
			"this file's bytes are not UTF-8 and look like "+d.Encoding+
				", which a table would read as one wrong character per byte")
	}
	return strings.Join(parts, "; ") + "."
}

// plural renders a count with the noun that agrees with it.
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// verb agrees with the same count.
func verb(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// NormalizeReport is what a correction changed.
type NormalizeReport struct {
	// RowsRepaired is how many records had a line break taken out of a field.
	RowsRepaired int `json:"rows_repaired"`
	// FromEncoding names the encoding the bytes were converted from, and is
	// empty when they were already UTF-8.
	FromEncoding string `json:"from_encoding,omitempty"`
	// FromLineEndings names the line endings the file was rewritten from, and
	// is empty when its records already ended in a newline.
	FromLineEndings string `json:"from_line_endings,omitempty"`
}

// NormalizeCSV rewrites a CSV so a line-based reader gets the records the file
// actually holds: UTF-8 with no byte-order mark, one record per newline, and
// every field on one line.
//
// It is a decode and a re-emit, never a repair of the record structure. A
// record whose field count differs from the header is refused rather than
// adjusted: padding a short record invents data and truncating a long one
// discards it, and neither is a correction the platform can make on somebody's
// behalf.
func NormalizeCSV(content []byte) ([]byte, NormalizeReport, error) {
	decoded, from, err := decodeToUTF8(content)
	if err != nil {
		return nil, NormalizeReport{}, err
	}
	// Before anything is counted, for the same reason InspectCSV does it: this
	// reader splits on "\n" too, so a carriage-return file would be read here
	// as the single record it is not, and written back out as one.
	decoded, endings := withLineFeeds(decoded)
	records, err := newCSVReader(decoded).ReadAll()
	if err != nil {
		return nil, NormalizeReport{}, refusedf(
			"this file cannot be corrected because it is not readable as a CSV: %s", err.Error())
	}
	if len(records) == 0 {
		return nil, NormalizeReport{}, ErrEmptyHeader
	}
	if err := checkFieldCounts(records); err != nil {
		return nil, NormalizeReport{}, err
	}

	report := NormalizeReport{FromEncoding: from, FromLineEndings: endings}
	for _, record := range records {
		if flattenFields(record) {
			report.RowsRepaired++
		}
	}

	var out bytes.Buffer
	w := csv.NewWriter(&out)
	if err := w.WriteAll(records); err != nil {
		return nil, NormalizeReport{}, refusedf("this file's corrected form could not be written: %s", err.Error())
	}
	return out.Bytes(), report, nil
}

// decodeToUTF8 returns the content as UTF-8 text with any byte-order mark
// removed, and names the encoding it converted from.
func decodeToUTF8(content []byte) (decoded []byte, from string, err error) {
	from = sourceEncoding(content)
	if from == "" {
		return bytes.TrimPrefix(content, []byte(bomUTF8)), "", nil
	}
	// Only a single-byte code page is converted here. Decoding a wide encoding
	// as one produces a character per byte, and writing that back as a
	// corrected version of somebody's file would replace their data with
	// mojibake and report it as a repair.
	if !(&CSVDefect{Encoding: from}).Correctable() {
		return nil, "", refusedf(
			"this file's bytes are not UTF-8 and look like %s, which cannot be converted here without"+
				" guessing at it; re-export it as UTF-8 CSV and upload that", from)
	}
	converted, err := charmap.Windows1252.NewDecoder().Bytes(content)
	if err != nil {
		return nil, "", refusedf("this file's bytes are neither UTF-8 nor %s, so it cannot be corrected: %s",
			encodingWindows1252, err.Error())
	}
	return bytes.TrimPrefix(converted, []byte(bomUTF8)), encodingWindows1252, nil
}

// maxNamedRecords bounds how many ragged records a refusal lists. A file whose
// every record is ragged is not a CSV with a few bad rows, and printing all of
// them buries the sentence that says so.
const maxNamedRecords = 5

// checkFieldCounts refuses a file holding a record that does not have the
// header's fields.
func checkFieldCounts(records [][]string) error {
	want := len(records[0])
	var ragged []string
	for i, record := range records[1:] {
		if len(record) == want {
			continue
		}
		if len(ragged) < maxNamedRecords {
			ragged = append(ragged, "record "+strconv.Itoa(i+1)+" has "+strconv.Itoa(len(record)))
		}
	}
	if len(ragged) == 0 {
		return nil
	}
	return refusedf(
		"this file cannot be corrected because its records do not all have the header's %d fields (%s); "+
			"filling in a short record would invent data and dropping a field from a long one would lose some, "+
			"so the file has to be fixed where it was written",
		want, joinAnd(ragged))
}

// flattenFields puts every field of a record on one line and reports whether
// any of them had to move.
func flattenFields(record []string) bool {
	changed := false
	for i, field := range record {
		if !strings.ContainsAny(field, "\r\n") {
			continue
		}
		record[i] = collapseLineBreaks(field)
		changed = true
	}
	return changed
}

// collapseLineBreaks joins the lines a field was written across into one, each
// separated by a single space, which is what a multi-line address in one
// spreadsheet cell reads as on a line. The indentation a spreadsheet leaves on
// the continuation lines goes with the break that preceded it.
func collapseLineBreaks(field string) string {
	lines := strings.FieldsFunc(field, func(r rune) bool { return r == '\r' || r == '\n' })
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			kept = append(kept, trimmed)
		}
	}
	return strings.Join(kept, " ")
}
