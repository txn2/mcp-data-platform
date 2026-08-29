// Package tablecsv inspects and corrects a CSV file before a table is
// registered over it: the defects a query engine cannot read through (a
// carriage-return line ending, a line break inside a cell, a legacy code
// page), whether each is correctable, the reason and remedy a refusal names,
// and the normalization that writes the corrected version. It knows nothing
// about registrations; tableregister asks it and acts on the answer.
package tablecsv

import (
	"bytes"
	"encoding/csv"
	"errors"
	"io"
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
// the conversion cannot invent a character that was not there. That holds for
// the 251 bytes windows-1252 defines and not for the other five, which is why
// a file carrying one of those is encodingUnidentified rather than this.
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
	// encodingUnidentified is what non-UTF-8 bytes are called when one of them
	// is a value windows-1252 leaves undefined. The byte-for-byte mapping the
	// conversion rests on does not reach those five values, so the file is in
	// something else, and nothing in it says what.
	encodingUnidentified = "an encoding this platform cannot identify"
)

// lineEndingsCR names the line endings of a file whose lines end in a bare
// carriage return -- the classic Mac ending some spreadsheet exports still
// write. Every reader on this path splits records on "\n" and none of them
// splits on "\r", so such a file is one record to all of them.
const lineEndingsCR = "carriage return"

// Defect is why a CSV cannot be read as a table the way it is stored. A nil
// *Defect means it can.
type Defect struct {
	// Rows is how many records carry a field with a line break inside it.
	Rows int `json:"rows,omitempty"`
	// Columns names the columns those line breaks are in, in header order.
	Columns []string `json:"columns,omitempty"`
	// Encoding names what the bytes appear to be when they are not readable as
	// UTF-8 text, and is empty when they are. A NUL byte is one of the ways
	// they are not, whether or not the rest of the file is valid UTF-8.
	Encoding string `json:"encoding,omitempty"`
	// LineEndings names what the file's lines end in when that is not
	// something a line-based reader splits on, and is empty when it is.
	LineEndings string `json:"line_endings,omitempty"`
	// HeaderFields is how many fields the header row declares, and is what a
	// ragged record is short or long of.
	HeaderFields int `json:"header_fields,omitempty"`
	// Ragged names the records whose field count differs from the header's,
	// in file order and at most maxNamedRecords of them.
	Ragged []string `json:"ragged_records,omitempty"`
	// Unreadable is the parse error that stopped a read of this file before
	// its end, and is empty when every record parsed.
	Unreadable string `json:"unreadable,omitempty"`
}

// Inspect reports why a CSV cannot be read by a line-based reader, or nil
// when it can.
//
// Three conditions refuse. Lines that end in something the reader does not
// split on run the records that end in one together into a single record, a
// line break inside a field tears the record, and bytes that are not UTF-8
// reach every cell as replacement characters. None of them is visible in the result of a query over the table,
// so all three are answered before one exists.
//
// The scan records two further things that do not refuse a file by
// themselves: records whose field count differs from the header's, and a parse
// error that stops the read before the end. A file carrying only one of those
// registers today and goes on registering, but neither can be put right by the
// correction, so a defect found alongside one of them is refused rather than
// offered a repair that would then decline (#1449).
//
// The line endings are settled first and the record scan runs over the
// translated bytes, because the scan is itself a line-based reader: over a
// carriage-return file it sees one record, reports the file as a single torn
// row, and names columns made out of the header line joined to the line after
// it.
func Inspect(content []byte) *Defect {
	defect := Defect{Encoding: sourceEncoding(content)}
	// A file in an encoding the platform does not read is reported on its
	// encoding alone. Everything below is a single-byte reader, and over a
	// wide encoding it reads bytes that are not the characters the file holds:
	// it takes a carriage return that is half of a wider unit for a line break
	// inside a cell, and it names the column that break is in out of the
	// NUL-laden bytes around it. Both would go into the refusal a person
	// reads, and neither can be checked against the file.
	if !defect.convertibleEncoding() {
		return &defect
	}
	content, defect.LineEndings = withLineFeeds(content)
	defect.scanRecords(content)
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

// undefinedWindows1252 are the five byte values windows-1252 assigns no
// character to. The decoder does not report them: it emits U+FFFD for each and
// returns a nil error, so a file holding one converts without complaint into a
// file carrying replacement marks where those bytes were. The source is
// checked for them here instead.
var undefinedWindows1252 = []byte{0x81, 0x8D, 0x8F, 0x90, 0x9D}

// hasUndefinedWindows1252 reports whether the content holds a byte
// windows-1252 leaves undefined.
func hasUndefinedWindows1252(content []byte) bool {
	for _, b := range undefinedWindows1252 {
		if bytes.IndexByte(content, b) >= 0 {
			return true
		}
	}
	return false
}

// sourceEncoding names what a file's bytes appear to be, or "" when they are
// valid UTF-8 with no NUL in them.
//
// The UTF-8 test runs last, because passing it does not rule a wide encoding
// out. A NUL is valid UTF-8, so a UTF-16 or UTF-32 file of ASCII content
// written without a byte-order mark passes that test with a NUL beside every
// character, and would reach Trino as a table whose column names and cells
// carry them.
//
// The byte-order marks are read first and widest first, since a UTF-32LE mark
// begins with a UTF-16LE one and a UTF-32BE mark begins with the NUL the case
// below it looks for. A file with no mark and a NUL byte in it is not
// single-byte text either, whatever it is: a CSV in a code page has no NUL in
// it, and treating one as windows-1252 would turn every character into two.
//
// windows-1252 is the last answer rather than the fallback for everything that
// is not UTF-8. It is reached only when every byte is one that code page
// defines, because the five it does not define decode to a replacement mark
// without the decoder saying so, and a file holding one of them is in some
// other encoding (#1448).
func sourceEncoding(content []byte) string {
	switch {
	case bytes.HasPrefix(content, []byte{0xFF, 0xFE, 0x00, 0x00}),
		bytes.HasPrefix(content, []byte{0x00, 0x00, 0xFE, 0xFF}):
		return encodingUTF32
	case bytes.HasPrefix(content, []byte{0xFF, 0xFE}), bytes.HasPrefix(content, []byte{0xFE, 0xFF}):
		return encodingUTF16
	case bytes.IndexByte(content, 0) >= 0:
		return encodingWide
	case utf8.Valid(content):
		return ""
	case hasUndefinedWindows1252(content):
		return encodingUnidentified
	default:
		return encodingWindows1252
	}
}

// Correctable reports whether the platform can produce a corrected version of
// this file itself.
//
// It is the same question Normalize answers, asked before the offer is
// made rather than after it is taken up. Three things say no: bytes in an
// encoding the platform does not convert, because everything else about the
// file is read through that encoding and would be corrected into mojibake; a
// record whose field count differs from the header's, because padding a short
// one invents data and truncating a long one discards it; and a parse that
// does not reach the end of the file, because the correction has to read every
// record to write it back.
func (d *Defect) Correctable() bool {
	return d.convertibleEncoding() && len(d.Ragged) == 0 && d.Unreadable == ""
}

// convertibleEncoding reports whether the platform can read this file's bytes
// as the characters they stand for. It is the encoding half of Correctable,
// asked on its own by the two places that have nothing but an encoding to go
// on: the inspection, before it has read a record, and the conversion itself.
func (d *Defect) convertibleEncoding() bool {
	return d.Encoding == "" || d.Encoding == encodingWindows1252
}

// remedy is what has to happen to a file the platform cannot correct itself.
// Bytes it cannot read are re-exported; records that do not match the header,
// or that it cannot parse through, are fixed where the file was written,
// because only whoever wrote it knows what the missing field held.
func (d *Defect) Remedy() string {
	if !d.convertibleEncoding() {
		return "Re-export it as UTF-8 CSV and upload that."
	}
	return "Correct it where it was written and upload it again."
}

// scanRecords reads the file the way the correction would and records what it
// finds: the records carrying a field with a line break in them, the columns
// those breaks are in, the records whose field count differs from the
// header's, and the parse error that ended the read early.
//
// A parse failure stops the scan rather than failing it: what has been read so
// far is still evidence, and a file this reader cannot finish is one that
// registers today, so refusing it on that ground alone would take away a
// registration that works. It is kept rather than dropped because the
// correction reads the same file with the same reader, and cannot rewrite what
// it cannot read.
func (d *Defect) scanRecords(content []byte) {
	reader := newCSVReader(content)

	header, err := reader.Read()
	if err != nil {
		// On the same terms as the loop below. A header the reader cannot
		// parse is a file the correction cannot read either, and the defect
		// this scan found nothing about was settled before it ran -- an
		// encoding, or carriage-return endings -- so the offer would still be
		// made and still be refused a second time.
		d.noteUnreadable(err)
		return
	}
	names := headerLabels(header)
	d.HeaderFields = len(header)

	hit := map[int]bool{}
	record, number := header, 0
	for err == nil {
		if recordBreaks(record, hit) {
			d.Rows++
		}
		// The header is the width every record is measured against, so it is
		// counted as a torn row like any other and compared with nothing.
		if number > 0 && len(record) != d.HeaderFields {
			d.noteRagged(number, len(record))
		}
		number++
		record, err = reader.Read()
	}
	d.noteUnreadable(err)
	d.Columns = labelsFor(names, hit)
}

// noteUnreadable records the error that ended a read, unless it was the end of
// the file. Only a parse failure is one the correction would meet as well.
func (d *Defect) noteUnreadable(err error) {
	if !errors.Is(err, io.EOF) {
		d.Unreadable = err.Error()
	}
}

// noteRagged records a record whose field count differs from the header's, up
// to the number a refusal lists. Beyond that the record is still ragged and
// the file is still refused; only its name is dropped.
func (d *Defect) noteRagged(number, fields int) {
	if len(d.Ragged) >= maxNamedRecords {
		return
	}
	d.Ragged = append(d.Ragged, raggedRecordLabel(number, fields))
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
func (d *Defect) Reason() string {
	parts := make([]string, 0, 4)
	if d.LineEndings != "" {
		parts = append(parts,
			"this file's lines end in a "+d.LineEndings+" rather than a newline, and a table splits records on"+
				" the newline, so the records ending in one would be run together into a single row")
	}
	if d.Rows > 0 {
		part := plural(d.Rows, "row", "rows") + " in this file " +
			verb(d.Rows, "has", "have") + " a line break inside a cell"
		if len(d.Columns) > 0 {
			part += " (in " + JoinAnd(d.Columns) + ")"
		}
		parts = append(parts,
			part+", and a table reads a line break as the end of the row, so each of those rows would be torn into fragments")
	}
	if part := d.encodingReason(); part != "" {
		parts = append(parts, part)
	}
	// Last, because it is the reason the defects above it are not being
	// offered a correction rather than a defect of its own.
	if part := d.uncorrectableReason(); part != "" {
		parts = append(parts, part)
	}
	return strings.Join(parts, "; ") + "."
}

// encodingReason says what the file's bytes appear to be and what a table
// would make of them, or is empty when they are UTF-8 text.
func (d *Defect) encodingReason() string {
	switch {
	case d.Encoding == encodingWindows1252:
		return "this file's bytes are not UTF-8 and read as " + d.Encoding +
			", so the characters outside plain ASCII would arrive in the table as replacement marks"
	case d.Encoding == encodingUnidentified:
		// Named for what it is not, like the markless wide case below it: the
		// evidence is a byte windows-1252 has no character for, which rules
		// that code page out without saying what the file is instead.
		return "this file's bytes are not UTF-8 and hold a byte windows-1252 does not define, so they are in" +
			" some other encoding, and reading them as windows-1252 would put a replacement mark" +
			" where each of those bytes is"
	case d.Encoding == encodingWide:
		// This label is reached on the NUL alone, so the NUL is what the
		// sentence reports. "These bytes are not UTF-8" would be false for a
		// markless UTF-16 export of ASCII, whose bytes are valid UTF-8, and
		// naming an encoding on that evidence would be a guess.
		return "this file has a NUL byte in it, which is not text, and a table would read it as part of" +
			" whatever column name or cell it falls in"
	case d.Encoding != "":
		return "this file's bytes look like " + d.Encoding +
			", which a table would read as one wrong character per byte"
	default:
		return ""
	}
}

// uncorrectableReason says why the platform will not produce a corrected
// version of this file, and is empty when it will. Only the record structure
// answers here: an encoding the platform cannot convert is already stated by
// encodingReason, and is the one case the scan does not run for.
//
// A parse that did not finish is reported ahead of the field counts, because
// the records it never reached are not in the ragged list either.
func (d *Defect) uncorrectableReason() string {
	switch {
	case d.Unreadable != "":
		return unreadableClause(d.Unreadable)
	case len(d.Ragged) > 0:
		return raggedClause(d.HeaderFields, d.Ragged)
	default:
		return ""
	}
}

// raggedRecordLabel names one record by its position among the data records
// and the field count it turned out to have.
func raggedRecordLabel(number, fields int) string {
	return "record " + strconv.Itoa(number) + " has " + strconv.Itoa(fields)
}

// raggedClause states that a file's records do not all have the header's
// fields and why the platform will not adjust them. It is the one wording for
// the condition, so the inspection that declines to offer a correction and the
// correction that refuses to make one say the same thing about the same file.
//
// Its subject is "it", so it reads after a sentence that has already named the
// file. Both of its callers give it one.
func raggedClause(headerFields int, ragged []string) string {
	return "its records do not all have the header's " + strconv.Itoa(headerFields) +
		" fields (" + JoinAnd(ragged) + "), and filling in a short record would invent data while" +
		" dropping a field from a long one would lose some"
}

// unreadableClause states that a read of the file stopped before its end. A
// correction rewrites every record, so it cannot be made over a file whose
// records cannot all be read. Its subject is "it", like raggedClause above.
func unreadableClause(parseErr string) string {
	return "it is not readable as a CSV all the way through (" + parseErr + ")"
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

// Normalize rewrites a CSV so a line-based reader gets the records the file
// actually holds: UTF-8 with no byte-order mark, one record per newline, and
// every field on one line.
//
// It is a decode and a re-emit, never a repair of the record structure. A
// record whose field count differs from the header is refused rather than
// adjusted: padding a short record invents data and truncating a long one
// discards it, and neither is a correction the platform can make on somebody's
// behalf.
func Normalize(content []byte) ([]byte, NormalizeReport, error) {
	decoded, from, err := decodeToUTF8(content)
	if err != nil {
		return nil, NormalizeReport{}, err
	}
	// Before anything is counted, for the same reason Inspect does it: this
	// reader splits on "\n" too, so a carriage-return file would be read here
	// as the single record it is not, and written back out as one.
	decoded, endings := withLineFeeds(decoded)
	records, err := newCSVReader(decoded).ReadAll()
	if err != nil {
		return nil, NormalizeReport{}, uncorrectablef(
			"this file cannot be corrected because %s", unreadableClause(err.Error()))
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
		return nil, NormalizeReport{}, uncorrectablef("this file's corrected form could not be written: %s", err.Error())
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
	if !(&Defect{Encoding: from}).convertibleEncoding() {
		return nil, "", uncorrectablef(
			"this file's bytes look like %s, which cannot be converted here without"+
				" guessing at it; re-export it as UTF-8 CSV and upload that", from)
	}
	converted, err := charmap.Windows1252.NewDecoder().Bytes(content)
	if err != nil {
		return nil, "", uncorrectablef("this file's bytes are neither UTF-8 nor %s, so it cannot be corrected: %s",
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
			ragged = append(ragged, raggedRecordLabel(i+1, len(record)))
		}
	}
	if len(ragged) == 0 {
		return nil
	}
	return uncorrectablef("this file cannot be corrected because %s, so it has to be fixed where it was written",
		raggedClause(want, ragged))
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
