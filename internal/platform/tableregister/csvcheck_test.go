package tableregister

import (
	"encoding/csv"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The defect this file is about does not surface as an error anywhere: Trino
// creates the table, the registration row is written, and the query returns
// rows. What it returns is a record torn into fragments on every line break a
// spreadsheet put inside a cell. So the assertions here are about the bytes --
// what is detected in them, and what the corrected form of them holds.

// spreadsheetCSV is the shape a spreadsheet export takes: a quoted multi-line
// address in one cell, and a quoted comma in another.
const spreadsheetCSV = "store_id,address,rebate\n" +
	"101,\"12 Mill Rd\nSuite 4\nPortland OR\",\"$156,142.58 \"\n" +
	"102,\"9 Bay St\nSeattle WA\",\"$2,000.00 \"\n" +
	"103,880 Pine St,15%\n"

func TestInspectCSV_LineSafeFilePasses(t *testing.T) {
	assert.Nil(t, InspectCSV([]byte(csvBody)))
	assert.Nil(t, InspectCSV([]byte("a,b\n\"x,y\",\"say \"\"hi\"\"\"\n")),
		"a quoted comma and a doubled quote are read correctly by a line-based reader")
}

// TestInspectCSV_NamesTheRowsAndTheColumns is the refusal a person reads: how
// many rows are torn, and which column tore them.
func TestInspectCSV_NamesTheRowsAndTheColumns(t *testing.T) {
	defect := InspectCSV([]byte(spreadsheetCSV))
	require.NotNil(t, defect)

	assert.Equal(t, 2, defect.Rows)
	assert.Equal(t, []string{"address"}, defect.Columns)
	assert.Empty(t, defect.Encoding, "these bytes are valid UTF-8")

	reason := defect.Reason()
	assert.Contains(t, reason, "2 rows")
	assert.Contains(t, reason, "address")
	assert.Contains(t, reason, "line break inside a cell")
}

// TestInspectCSV_ReportsBytesThatAreNotUTF8 is the second defect on the same
// path: those bytes reach every cell as replacement marks.
func TestInspectCSV_ReportsBytesThatAreNotUTF8(t *testing.T) {
	// 0xCA is a non-breaking space on a Mac Roman machine and is not valid
	// UTF-8; it is what a spreadsheet put in the cell reading "15%".
	defect := InspectCSV([]byte("store_id,rebate\n101,15%\xca\n"))
	require.NotNil(t, defect)

	assert.Zero(t, defect.Rows, "nothing is torn; only the encoding is wrong")
	assert.Equal(t, encodingWindows1252, defect.Encoding)
	assert.Contains(t, defect.Reason(), "not UTF-8")
}

func TestInspectCSV_EmptyFileHasNoDefectToName(t *testing.T) {
	assert.Nil(t, InspectCSV(nil))
	assert.Nil(t, InspectCSV([]byte("")))
}

// macCSV is a spreadsheet export whose lines end in a bare carriage return.
// Every reader on this path splits on "\n", so all three of its records are
// one line to all of them.
const macCSV = "store_id,address\r101,12 Mill Rd\r102,9 Oak St\r"

// TestInspectCSV_NamesCarriageReturnLineEndings is the defect a line-based
// reader hits before it hits any other: the file holds three records and the
// reader sees one, so a table over it has a single row holding the whole file.
func TestInspectCSV_NamesCarriageReturnLineEndings(t *testing.T) {
	defect := InspectCSV([]byte(macCSV))
	require.NotNil(t, defect)

	assert.Equal(t, lineEndingsCR, defect.LineEndings)
	assert.Zero(t, defect.Rows, "no cell holds a line break; the file's lines do not end in one")
	assert.Empty(t, defect.Columns, "and no column is named that the file does not have")
	assert.True(t, defect.Correctable(), "translating one line ending to another cannot lose anything")

	reason := defect.Reason()
	assert.Contains(t, reason, "carriage return")
	assert.Contains(t, reason, "run together into a single row")
}

// TestInspectCSV_CarriageReturnFileIsScannedAsTheRecordsItHolds: with the
// endings settled first, a cell that really does hold a line break is found in
// the record it is in, and named by the column it is in.
func TestInspectCSV_CarriageReturnFileIsScannedAsTheRecordsItHolds(t *testing.T) {
	defect := InspectCSV([]byte("store_id,address\r101,\"12 Mill Rd\rSuite 4\"\r102,9 Oak St\r"))
	require.NotNil(t, defect)

	assert.Equal(t, lineEndingsCR, defect.LineEndings)
	assert.Equal(t, 1, defect.Rows)
	assert.Equal(t, []string{"address"}, defect.Columns)
}

// TestInspectCSV_LeavesTheOrdinaryLineEndingsAlone. A newline is what the
// reader splits on, and a CRLF is folded to one by every CSV reader in this
// path, so neither is a defect and neither costs the person a correction.
func TestInspectCSV_LeavesTheOrdinaryLineEndingsAlone(t *testing.T) {
	assert.Nil(t, InspectCSV([]byte("store_id,address\r\n101,12 Mill Rd\r\n")),
		"windows line endings")
	assert.Nil(t, InspectCSV([]byte("store_id,address")), "a single line with no ending at all")
}

// TestInspectCSV_OneStrayNewlineDoesNotHideTheCarriageReturns. A file is not
// disqualified by holding a line feed somewhere -- a tool that appended one, a
// cell pasted in from a Windows source. What the file's lines end in is read
// from the records the translation recovers, not from whether any line feed is
// present, because the alternative registers the file whole and calls the
// correction that flattens it a repair.
func TestInspectCSV_OneStrayNewlineDoesNotHideTheCarriageReturns(t *testing.T) {
	body := []byte("store_id,address\r101,12 Mill Rd\r102,9 Oak St\n")

	defect := InspectCSV(body)
	require.NotNil(t, defect)
	assert.Equal(t, lineEndingsCR, defect.LineEndings)
	assert.Zero(t, defect.Rows)
	assert.Empty(t, defect.Columns, "no column the file does not have is named")

	out, report, err := NormalizeCSV(body)
	require.NoError(t, err)
	assert.Equal(t, lineEndingsCR, report.FromLineEndings)
	assert.Zero(t, report.RowsRepaired, "no record is flattened into the one before it")
	assert.Equal(t, "store_id,address\n101,12 Mill Rd\n102,9 Oak St\n", string(out))
}

// TestInspectCSV_CarriageReturnFileWhoseCellHoldsANewline. The record scan
// cannot even read this file until the endings are translated -- the first
// record ends on an unbalanced quote -- so before the translation it reported
// no defect at all and the file went on to be refused for having no header.
func TestInspectCSV_CarriageReturnFileWhoseCellHoldsANewline(t *testing.T) {
	body := []byte("store_id,address\r101,\"12 Mill Rd\nSuite 4\"\r102,9 Oak St\r")

	defect := InspectCSV(body)
	require.NotNil(t, defect)
	assert.Equal(t, lineEndingsCR, defect.LineEndings)
	assert.Equal(t, 1, defect.Rows)
	assert.Equal(t, []string{"address"}, defect.Columns)

	out, _, err := NormalizeCSV(body)
	require.NoError(t, err)
	assert.Equal(t, "store_id,address\n101,12 Mill Rd Suite 4\n102,9 Oak St\n", string(out))
}

// TestInspectCSV_LeavesACarriageReturnInsideACellAsWhatItIs. A lone carriage
// return that recovers no record of the header's width is a line break inside
// a cell, and saying the file's lines end in one would be telling the person
// something untrue about their file and offering them a correction that then
// refuses the fragment it made.
//
// The unquoted case is the one that decides the measure: quoting the cell puts
// the carriage return where no translation can split a record, so a rule that
// only counted records would pass on the quoted shape and still be wrong here.
func TestInspectCSV_LeavesACarriageReturnInsideACellAsWhatItIs(t *testing.T) {
	for _, tc := range []struct{ name, body, corrected string }{
		{
			"unquoted",
			"store_id,address\n101,12 Mill Rd\r9 Oak St\n102,4 Elm Ave\n",
			"store_id,address\n101,12 Mill Rd 9 Oak St\n102,4 Elm Ave\n",
		},
		{
			"quoted",
			"store_id,address\n101,\"12 Mill Rd\rSuite 4\"\n102,9 Oak St\n",
			"store_id,address\n101,12 Mill Rd Suite 4\n102,9 Oak St\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defect := InspectCSV([]byte(tc.body))
			require.NotNil(t, defect)

			assert.Empty(t, defect.LineEndings, "nothing about this file's lines is wrong")
			assert.Equal(t, 1, defect.Rows)
			assert.Equal(t, []string{"address"}, defect.Columns)
			assert.NotContains(t, defect.Reason(), "carriage return")

			out, report, err := NormalizeCSV([]byte(tc.body))
			require.NoError(t, err, "and the correction it offers goes through")
			assert.Equal(t, 1, report.RowsRepaired)
			assert.Empty(t, report.FromLineEndings)
			assert.Equal(t, tc.corrected, string(out))
		})
	}
}

// TestInspectCSV_CarriageReturnsAmongWindowsLineEndings. A CRLF is folded by
// every reader here and a lone carriage return among them is not, so the file
// is read as the records the lone ones end.
func TestInspectCSV_CarriageReturnsAmongWindowsLineEndings(t *testing.T) {
	defect := InspectCSV([]byte("store_id,address\r\n101,12 Mill Rd\r102,9 Oak St\r\n"))
	require.NotNil(t, defect)
	assert.Equal(t, lineEndingsCR, defect.LineEndings)
	assert.Zero(t, defect.Rows)

	out, _, err := NormalizeCSV([]byte("store_id,address\r\n101,12 Mill Rd\r102,9 Oak St\r\n"))
	require.NoError(t, err)
	assert.Equal(t, "store_id,address\n101,12 Mill Rd\n102,9 Oak St\n", string(out))
}

// TestInspectCSV_ACarriageReturnThatEndsNoRecord. Two shapes a spreadsheet
// leaves behind that recover nothing and must cost the person nothing: a
// carriage return doubled before a Windows ending, which is a stray character
// in the cell before it, and one closing a file that has no other line at all.
func TestInspectCSV_ACarriageReturnThatEndsNoRecord(t *testing.T) {
	doubled := InspectCSV([]byte("store_id,address\r\r\n101,12 Mill Rd\r\r\n"))
	require.NotNil(t, doubled)
	assert.Empty(t, doubled.LineEndings, "the record boundary was already there")
	assert.Equal(t, 2, doubled.Rows, "what is left is a stray character in the last cell of each")

	assert.Nil(t, InspectCSV([]byte("store_id,address\r")),
		"a lone header line, whose trailing carriage return the reader drops at the end of the file")
}

// TestNormalizeCSV_NeverMergesTheRecordsOfACarriageReturnFile is the property
// the whole translation exists for, over every classic-Mac shape of a few rows
// and a few fields: whatever else it does, a file whose records are separated
// by bare carriage returns is never written back out with fewer records than
// it has.
//
// The shapes that matter here are the ones whose rows do not match the header,
// because they are the ones a measure counting only header-width records
// scores no better after the translation than before. Rejecting the
// translation there hands the file back to the reader that merges the file
// whole; kept, the field-count check refuses it and says which record is
// wrong.
//
// Both a value set holding an unquoted comma and one holding none are swept,
// and the shapes that came through whole are counted. The comma is what
// produces the unquoted-comma-in-an-address case, and it also makes nearly
// every shape ragged: without a set that leaves uniform shapes behind, almost
// all of them land in the refusal arm, and a later change that pushed the last
// few over would leave the length assertion running on nothing while the test
// still passed.
func TestNormalizeCSV_NeverMergesTheRecordsOfACarriageReturnFile(t *testing.T) {
	for _, values := range [][]string{
		{"a", "b", "c", "1", "2", "3", "x, y"},
		{"a", "b", "c", "1", "2", "3"},
	} {
		preserved := 0
		for rows := 2; rows <= 5; rows++ {
			for headerWidth := 1; headerWidth <= 4; headerWidth++ {
				for rowWidth := 1; rowWidth <= 4; rowWidth++ {
					if normalizePreserves(t, macFile(values, rows, headerWidth, rowWidth), rows) {
						preserved++
					}
				}
			}
		}
		assert.Positive(t, preserved,
			"every shape was refused, so the assertion this test exists for ran on nothing")
	}
}

// normalizePreserves reports whether the body came through the correction with
// at least the records it holds, and fails the test if it came through with
// fewer. A refusal is neither: refusing a file whose records do not all have
// the header's fields is the honest answer, and merging them is not, which is
// the whole distinction being asserted.
func normalizePreserves(t *testing.T, body string, rows int) bool {
	t.Helper()

	out, _, err := NormalizeCSV([]byte(body))
	if err != nil {
		assert.ErrorIs(t, err, ErrRefused, body)
		return false
	}
	records, readErr := csv.NewReader(strings.NewReader(string(out))).ReadAll()
	require.NoError(t, readErr, body)
	return assert.GreaterOrEqual(t, len(records), rows,
		"%q was written back with fewer records than it holds", body)
}

// macFile builds a classic-Mac CSV: records separated by bare carriage
// returns, a header of one width and every row under it of another, so the
// shapes whose rows do not match their header are covered alongside those that
// do.
func macFile(values []string, rows, headerWidth, rowWidth int) string {
	lines := make([]string, 0, rows)
	for r := range rows {
		width := headerWidth
		if r > 0 {
			width = rowWidth
		}
		cells := make([]string, width)
		for c := range cells {
			cells[c] = values[(r*3+c)%len(values)]
		}
		lines = append(lines, strings.Join(cells, ","))
	}
	return strings.Join(lines, "\r") + "\r"
}

// TestInspectCSV_ACarriageReturnFileWhoseRowsDoNotMatchItsHeader is that
// property in the cases a person actually uploads: an unquoted comma in an
// address, and a header wider than the rows under it. Neither is corrected --
// the field-count check refuses both -- but both have to be seen as the
// several records they are first, because the alternative is one row holding
// the file and a refusal naming columns it does not have.
func TestInspectCSV_ACarriageReturnFileWhoseRowsDoNotMatchItsHeader(t *testing.T) {
	for _, tc := range []struct{ name, body, refusal string }{
		{"an unquoted comma in an address", "store_id,address\r101,12 Mill Rd, Suite 4\r", "record 1 has 3"},
		{"rows narrower than the header", "a,b\r1\r2\r3\r", "record 1 has 1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defect := InspectCSV([]byte(tc.body))
			require.NotNil(t, defect)
			assert.Equal(t, lineEndingsCR, defect.LineEndings)
			assert.Zero(t, defect.Rows, "the file is read as its records, not as one torn row")
			assert.Empty(t, defect.Columns, "so no column it does not have is named")

			_, _, err := NormalizeCSV([]byte(tc.body))
			require.Error(t, err, "and the correction refuses it rather than merging it")
			assert.ErrorIs(t, err, ErrRefused)
			assert.Contains(t, err.Error(), tc.refusal)
		})
	}
}

// TestInspectCSV_ASingleRecordWithNoLineEndingAtAll. A file with no newline is
// one record to every reader here, which is why the plain count decides it --
// but a carriage return that recovers no record is still a break inside a
// cell, and this file's is.
func TestInspectCSV_ASingleRecordWithNoLineEndingAtAll(t *testing.T) {
	defect := InspectCSV([]byte("\"12 Mill Rd\rSuite 4\",101"))
	require.NotNil(t, defect)

	assert.Empty(t, defect.LineEndings, "there is one record either way, so nothing was recovered")
	assert.Equal(t, 1, defect.Rows)

	out, report, err := NormalizeCSV([]byte("\"12 Mill Rd\rSuite 4\",101"))
	require.NoError(t, err)
	assert.Empty(t, report.FromLineEndings)
	assert.Equal(t, "12 Mill Rd Suite 4,101\n", string(out))
}

// TestNormalizeCSV_GivesEachRecordItsOwnLine is the acceptance assertion for a
// carriage-return file: the corrected form holds the records the file holds,
// not the one record a line-based reader found in it.
func TestNormalizeCSV_GivesEachRecordItsOwnLine(t *testing.T) {
	out, report, err := NormalizeCSV([]byte(macCSV))
	require.NoError(t, err)

	assert.Equal(t, lineEndingsCR, report.FromLineEndings)
	assert.Zero(t, report.RowsRepaired)
	assert.Equal(t, "store_id,address\n101,12 Mill Rd\n102,9 Oak St\n", string(out))
	assert.Nil(t, InspectCSV(out), "the corrected form has nothing left to correct")

	records, err := csv.NewReader(strings.NewReader(string(out))).ReadAll()
	require.NoError(t, err)
	assert.Len(t, records, 3, "the header and one row per source record")
}

// TestNormalizeCSV_CorrectsTheLineEndingsAndTheEncodingTogether. A spreadsheet
// that wrote one wrote the other, and the person is told about both in one
// sentence rather than being sent round the loop twice.
func TestNormalizeCSV_CorrectsTheLineEndingsAndTheEncodingTogether(t *testing.T) {
	// 0xA9 is the copyright sign in windows-1252 and is not valid UTF-8.
	body := []byte("store_id,note\r101,15% \xa9 ACME\r")

	defect := InspectCSV(body)
	require.NotNil(t, defect)
	assert.Equal(t, lineEndingsCR, defect.LineEndings)
	assert.Equal(t, encodingWindows1252, defect.Encoding)
	assert.True(t, defect.Correctable())
	assert.Contains(t, defect.Reason(), "carriage return")
	assert.Contains(t, defect.Reason(), "not UTF-8")

	out, report, err := NormalizeCSV(body)
	require.NoError(t, err)
	assert.Equal(t, lineEndingsCR, report.FromLineEndings)
	assert.Equal(t, encodingWindows1252, report.FromEncoding)
	assert.Equal(t, "store_id,note\n101,15% © ACME\n", string(out))
	assert.Equal(t,
		"rewrote the carriage return line endings as newlines and converted the text from windows-1252 to UTF-8",
		repairSummary(report))
}

// TestNormalizeCSV_CarriageReturnFileIsStillHeldToTheHeaderShape: the field
// count check is only meaningful once the file is read as the records it
// holds, and a ragged one is refused rather than corrected here too.
func TestNormalizeCSV_CarriageReturnFileIsStillHeldToTheHeaderShape(t *testing.T) {
	_, _, err := NormalizeCSV([]byte("a,b,c\r1,2,3\r4,5\r"))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRefused)
	assert.Contains(t, err.Error(), "record 2 has 2")
}

// TestNormalizeCSV_PutsEveryRecordOnOneLine is the acceptance assertion for the
// correction: one row per source record, every field in its declared column.
func TestNormalizeCSV_PutsEveryRecordOnOneLine(t *testing.T) {
	out, report, err := NormalizeCSV([]byte(spreadsheetCSV))
	require.NoError(t, err)

	assert.Equal(t, 2, report.RowsRepaired)
	assert.Empty(t, report.FromEncoding)
	assert.Nil(t, InspectCSV(out), "the corrected form has nothing left to correct")

	records, err := csv.NewReader(strings.NewReader(string(out))).ReadAll()
	require.NoError(t, err)
	require.Len(t, records, 4, "the header and one row per source record")
	assert.Equal(t, []string{"101", "12 Mill Rd Suite 4 Portland OR", "$156,142.58 "}, records[1])
	assert.Equal(t, []string{"102", "9 Bay St Seattle WA", "$2,000.00 "}, records[2])
	assert.Equal(t, []string{"103", "880 Pine St", "15%"}, records[3],
		"a record that was already on one line is untouched")
}

// TestNormalizeCSV_ConvertsBytesThatAreNotUTF8 covers the encoding half: the
// cell reads what it read in the source, with no replacement mark in it.
func TestNormalizeCSV_ConvertsBytesThatAreNotUTF8(t *testing.T) {
	// 0xA9 is the copyright sign in windows-1252 and is not valid UTF-8.
	out, report, err := NormalizeCSV([]byte("store_id,note\n101,15% \xa9 ACME\n"))
	require.NoError(t, err)

	assert.Equal(t, encodingWindows1252, report.FromEncoding)
	assert.Zero(t, report.RowsRepaired)
	assert.Contains(t, string(out), "15% © ACME")
	assert.NotContains(t, string(out), "�", "no replacement character survives the conversion")
}

// TestNormalizeCSV_DropsTheByteOrderMark: a BOM is content to a line-based
// reader and would otherwise lead the first column's first value.
func TestNormalizeCSV_DropsTheByteOrderMark(t *testing.T) {
	out, _, err := NormalizeCSV([]byte(bomUTF8 + "store_id,rebate\n101,\"a\nb\"\n"))
	require.NoError(t, err)
	assert.False(t, strings.HasPrefix(string(out), bomUTF8))
	assert.True(t, strings.HasPrefix(string(out), "store_id,"))
}

// TestNormalizeCSV_RefusesARecordThatIsNotTheHeaderShape: padding a short
// record invents data and truncating a long one discards it, so neither is a
// correction the platform makes on somebody's behalf.
func TestNormalizeCSV_RefusesARecordThatIsNotTheHeaderShape(t *testing.T) {
	_, _, err := NormalizeCSV([]byte("a,b,c\n1,2,3\n4,5\n"))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRefused)
	assert.Contains(t, err.Error(), "record 2 has 2")
	assert.Contains(t, err.Error(), "3 fields")
}

func TestNormalizeCSV_RefusesAFileWithNoHeader(t *testing.T) {
	_, _, err := NormalizeCSV(nil)
	assert.ErrorIs(t, err, ErrEmptyHeader)
}

// TestNormalizeCSV_LeavesALineSafeFileAlone: a file that needed nothing comes
// back reporting nothing, which is what keeps the success message silent.
func TestNormalizeCSV_LeavesALineSafeFileAlone(t *testing.T) {
	out, report, err := NormalizeCSV([]byte(csvBody))
	require.NoError(t, err)
	assert.Zero(t, report.RowsRepaired)
	assert.Empty(t, report.FromEncoding)
	assert.Equal(t, csvBody, string(out))
}

// TestCollapseLineBreaks_LeavesOneSpaceWhereTheBreakWas, whatever the break was
// made of: a CRLF, a run of them, or one with indentation after it.
func TestCollapseLineBreaks_LeavesOneSpaceWhereTheBreakWas(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"12 Mill Rd\nSuite 4", "12 Mill Rd Suite 4"},
		{"12 Mill Rd\r\nSuite 4", "12 Mill Rd Suite 4"},
		{"12 Mill Rd\n\n\nSuite 4", "12 Mill Rd Suite 4"},
		{"12 Mill Rd\n   Suite 4", "12 Mill Rd Suite 4"},
		{"no break at all", "no break at all"},
	} {
		assert.Equal(t, tc.want, collapseLineBreaks(tc.in), tc.in)
	}
}

// TestRepairSummary_SaysWhatChanged in the terms of the person whose file it
// is, since the summary is both the version-trail entry and what they are told.
func TestRepairSummary_SaysWhatChanged(t *testing.T) {
	assert.Equal(t, "put 3 rows back onto one line",
		repairSummary(NormalizeReport{RowsRepaired: 3}))
	assert.Equal(t, "converted the text from windows-1252 to UTF-8",
		repairSummary(NormalizeReport{FromEncoding: encodingWindows1252}))
	assert.Equal(t, "put 1 row back onto one line and converted the text from windows-1252 to UTF-8",
		repairSummary(NormalizeReport{RowsRepaired: 1, FromEncoding: encodingWindows1252}))
	assert.Equal(t, "rewrote the carriage return line endings as newlines",
		repairSummary(NormalizeReport{FromLineEndings: lineEndingsCR}))
	assert.Equal(t, "rewrote it as a plain UTF-8 CSV", repairSummary(NormalizeReport{}))

	report := &RepairReport{NormalizeReport: NormalizeReport{RowsRepaired: 2}, Version: 3}
	assert.Contains(t, report.Summary(), "version 3")
	assert.Contains(t, report.Summary(), "put 2 rows back onto one line")
	assert.Empty(t, (*RepairReport)(nil).Summary(), "no correction says nothing")
}

// TestInspectCSV_NamesAWideEncodingRatherThanGuessing. A spreadsheet's
// "Unicode Text" export is UTF-16, and every byte of it is valid windows-1252,
// so reading it as one produces a character per byte -- mojibake in every cell.
// Correcting a file into that and calling it a repair would be worse than
// refusing, so the encoding is named and the file is left alone.
func TestInspectCSV_NamesAWideEncodingRatherThanGuessing(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content []byte
		want    string
	}{
		{"utf-16 little-endian", utf16LE("name,note\nalice,ok\n"), encodingUTF16},
		{"utf-16 big-endian", append([]byte{0xFE, 0xFF}, 0x00, 'a', 0x00, ','), encodingUTF16},
		{"utf-32 little-endian", []byte{0xFF, 0xFE, 0x00, 0x00, 'a', 0, 0, 0}, encodingUTF32},
		{"utf-32 big-endian", []byte{0x00, 0x00, 0xFE, 0xFF, 0, 0, 0, 'a'}, encodingUTF32},
		{"no mark, but not single-byte text", []byte("name\x00,note\x00\n\xff"), encodingWide},
		// A carriage return is one byte here and part of a wider unit there,
		// so the line endings are left alone rather than guessed at.
		{"utf-16 little-endian, carriage-return endings", utf16LE("name,note\ralice,ok\r"), encodingUTF16},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defect := InspectCSV(tc.content)
			require.NotNil(t, defect)
			assert.Equal(t, tc.want, defect.Encoding)
			assert.Empty(t, defect.LineEndings, "nothing is claimed about a file that is not single-byte text")
			assert.False(t, defect.Correctable(), "the platform does not convert this itself")
			assert.Contains(t, defect.Reason(), tc.want)

			_, _, err := NormalizeCSV(tc.content)
			require.Error(t, err, "and it refuses to rewrite the file into mojibake")
			assert.ErrorIs(t, err, ErrRefused)
			assert.Contains(t, err.Error(), "re-export it as UTF-8 CSV")
		})
	}
}

// utf16LE encodes text the way a spreadsheet's "Unicode Text" export does:
// little-endian, with a byte-order mark. Each code unit is masked to a byte
// rather than converted, so the two halves are the ones the encoding puts
// there and not whatever a narrowing conversion happens to keep.
func utf16LE(text string) []byte {
	units := utf16.Encode([]rune(text))
	out := make([]byte, 0, 2+2*len(units))
	out = append(out, 0xFF, 0xFE)
	for _, unit := range units {
		out = append(out, byte(unit&0xFF), byte((unit>>8)&0xFF))
	}
	return out
}

// TestCSVDefect_Correctable: a single-byte code page maps byte for byte, so
// converting it cannot invent a character that was not in the file.
func TestCSVDefect_Correctable(t *testing.T) {
	assert.True(t, (&CSVDefect{}).Correctable(), "valid UTF-8 with only torn rows")
	assert.True(t, (&CSVDefect{Encoding: encodingWindows1252}).Correctable())
	assert.False(t, (&CSVDefect{Encoding: encodingUTF16}).Correctable())
	assert.False(t, (&CSVDefect{Encoding: encodingWide}).Correctable())
}
