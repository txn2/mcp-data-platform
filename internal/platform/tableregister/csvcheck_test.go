package tableregister

import (
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"

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
	assert.Nil(t, InspectCSV([]byte("store,note\nMünchen,café ☕\n")),
		"multi-byte UTF-8 is UTF-8; only the encodings a table cannot read are named")
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
		// says is the phrase the refusal has to carry. A file identified by a
		// byte-order mark is told what it looks like; one identified only by
		// the NUL in it is told about the NUL, since naming an encoding on
		// that evidence alone would be a guess.
		says string
	}{
		{"utf-16 little-endian", utf16LE("name,note\nalice,ok\n"), encodingUTF16, encodingUTF16},
		{"utf-16 big-endian", append([]byte{0xFE, 0xFF}, 0x00, 'a', 0x00, ','), encodingUTF16, encodingUTF16},
		{"utf-32 little-endian", []byte{0xFF, 0xFE, 0x00, 0x00, 'a', 0, 0, 0}, encodingUTF32, encodingUTF32},
		{"utf-32 big-endian", []byte{0x00, 0x00, 0xFE, 0xFF, 0, 0, 0, 'a'}, encodingUTF32, encodingUTF32},
		{"no mark, but not single-byte text", []byte("name\x00,note\x00\n\xff"), encodingWide, "NUL byte"},
		// A NUL is valid UTF-8, so a markless wide encoding whose content is
		// all ASCII passes a UTF-8 test with a NUL beside every character.
		// Nothing else in the file says what it is, so it is named for what it
		// is not (#1447).
		{
			"utf-16 little-endian with no byte-order mark", utf16LEUnmarked("name,note\nalice,ok\n"),
			encodingWide, "NUL byte",
		},
		// A carriage return is one byte here and part of a wider unit there,
		// so the line endings are left alone rather than guessed at.
		{
			"utf-16 little-endian, carriage-return endings", utf16LE("name,note\ralice,ok\r"),
			encodingUTF16, encodingUTF16,
		},
		// A Windows ending in a wide encoding puts a carriage return beside a
		// NUL, which a single-byte reader takes for a line break inside a
		// cell. Nothing is said about it: the encoding is the whole answer.
		{
			"utf-16 little-endian, windows line endings", utf16LE("name,note\r\nalice,ok\r\n"),
			encodingUTF16, encodingUTF16,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defect := InspectCSV(tc.content)
			require.NotNil(t, defect)
			assert.Equal(t, tc.want, defect.Encoding)
			assert.Empty(t, defect.LineEndings, "nothing is claimed about a file that is not single-byte text")
			assert.Zero(t, defect.Rows, "nor how many of its rows are torn")
			assert.Empty(t, defect.Columns,
				"nor what its columns are called, which would be named out of bytes read in the wrong encoding")
			assert.NotContains(t, defect.Reason(), "\x00", "so no NUL reaches the refusal a person reads")
			assert.False(t, defect.Correctable(), "the platform does not convert this itself")
			assert.Contains(t, defect.Reason(), tc.says)

			_, _, err := NormalizeCSV(tc.content)
			require.Error(t, err, "and it refuses to rewrite the file into mojibake")
			assert.ErrorIs(t, err, ErrRefused)
			assert.Contains(t, err.Error(), "re-export it as UTF-8 CSV")
		})
	}
}

// utf16LE encodes text the way a spreadsheet's "Unicode Text" export does:
// little-endian, with a byte-order mark.
func utf16LE(text string) []byte {
	return append([]byte{0xFF, 0xFE}, utf16LEUnmarked(text)...)
}

// utf16LEUnmarked is the same encoding with no byte-order mark, which is what
// an export written for a reader that was told the encoding looks like: no
// byte in it announces what it is. Each code unit is masked to a byte rather
// than converted, so the two halves are the ones the encoding puts there and
// not whatever a narrowing conversion happens to keep.
func utf16LEUnmarked(text string) []byte {
	units := utf16.Encode([]rune(text))
	out := make([]byte, 0, 2*len(units))
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
	assert.False(t, (&CSVDefect{Encoding: encodingUnidentified}).Correctable())
}

// TestInspectCSV_ByteWindows1252DoesNotDefineIsNotCalledWindows1252 covers the
// five byte values that code page assigns no character to. The x/text decoder
// emits a replacement mark for each of them and returns no error, so calling
// the file windows-1252 would have it converted "cleanly" into a file with
// replacement marks in it and the conversion reported as a repair (#1448).
func TestInspectCSV_ByteWindows1252DoesNotDefineIsNotCalledWindows1252(t *testing.T) {
	for _, undefined := range undefinedWindows1252 {
		t.Run(fmt.Sprintf("%#x", undefined), func(t *testing.T) {
			// The 0xE9 is a byte windows-1252 does define, so the file is one
			// this path would otherwise have converted. Its lines end in a
			// bare carriage return, a defect of its own, so a reading taken
			// below the encoding would have something to report.
			content := []byte("a,b\r1,caf\xe9\r2,x\r")
			content[len(content)-2] = undefined

			defect := InspectCSV(content)
			require.NotNil(t, defect)
			assert.Equal(t, encodingUnidentified, defect.Encoding)
			assert.False(t, defect.Correctable(), "the platform does not convert what it cannot identify")
			assert.Contains(t, defect.Reason(), "a byte windows-1252 does not define")
			assert.NotContains(t, defect.Reason(), "�",
				"and no replacement mark of its own reaches the sentence a person reads")
			assert.Empty(t, defect.LineEndings,
				"nothing under the encoding is claimed, since every reader below it is a single-byte one")
			assert.Zero(t, defect.Rows)
			assert.Empty(t, defect.Columns)

			_, report, err := NormalizeCSV(content)
			require.Error(t, err, "so the file is never rewritten")
			assert.ErrorIs(t, err, ErrRefused)
			assert.Contains(t, err.Error(), "re-export it as UTF-8 CSV")
			assert.Empty(t, report.FromEncoding, "and nothing claims a conversion happened")
		})
	}
}

// TestInspectCSV_AnUndefinedByteInsideAValidUTF8SequenceIsNotAnEncodingDefect
// covers the order the two tests run in. All five of those byte values are
// also continuation bytes, so any of them can be the second or third byte of a
// character a UTF-8 file legitimately holds -- U+0801 is E0 A0 81. The UTF-8
// test therefore has to answer first, or a file that is exactly what the
// platform asks for would be refused for carrying the character it holds.
func TestInspectCSV_AnUndefinedByteInsideAValidUTF8SequenceIsNotAnEncodingDefect(t *testing.T) {
	for _, r := range []rune{'ࠁ', 'ࠍ', 'ࠏ', 'ࠐ', 'ࠝ'} {
		t.Run(strconv.QuoteRune(r), func(t *testing.T) {
			content := []byte("store_id,note\n101," + string(r) + "\n")
			require.True(t, utf8.Valid(content))
			require.Contains(t, string(content), string(r))

			assert.Nil(t, InspectCSV(content), "a UTF-8 file with nothing else wrong with it registers as it is")

			out, report, err := NormalizeCSV(content)
			require.NoError(t, err)
			assert.Empty(t, report.FromEncoding, "and no conversion is claimed over it")
			assert.Contains(t, string(out), string(r), "with the character it holds still in it")
		})
	}
}

// TestNormalizeCSV_ConvertsTheDefinedBytesAroundTheUndefinedOnes: the refusal
// above is the five values and not the block they sit in. 0x80, 0x8E, 0x9E and
// 0x9F sit beside them and all have characters, so a file made only of those
// still converts and is still reported as windows-1252.
func TestNormalizeCSV_ConvertsTheDefinedBytesAroundTheUndefinedOnes(t *testing.T) {
	out, report, err := NormalizeCSV([]byte("store_id,note\n101,\x80 \x8e \x9e \x9f\n"))
	require.NoError(t, err)

	assert.Equal(t, encodingWindows1252, report.FromEncoding)
	assert.Contains(t, string(out), "€ Ž ž Ÿ")
	assert.NotContains(t, string(out), "�", "no replacement character survives the conversion")
}

// The offer and the correction used to answer different questions. Correctable
// inspected the encoding and nothing else, while NormalizeCSV went on to
// refuse a file whose records did not all have the header's fields and one it
// could not parse to the end. A caller who was told to register again asking
// for the correction did so and got a second, different refusal naming a
// problem the first had not mentioned (#1449). The tests below are that the
// inspection now settles both, and that neither condition refuses a file that
// nothing else is wrong with.

// TestInspectCSV_ARaggedRecordWithdrawsTheOffer: the file has a torn row, so
// it is refused either way; what changed is that the refusal states the field
// counts and no correction is offered for it.
func TestInspectCSV_ARaggedRecordWithdrawsTheOffer(t *testing.T) {
	defect := InspectCSV([]byte("a,b\n1,\"x\ny\"\n2\n"))
	require.NotNil(t, defect)

	assert.Equal(t, 1, defect.Rows, "the torn row is still what was found")
	assert.Equal(t, 2, defect.HeaderFields)
	assert.Equal(t, []string{"record 2 has 1"}, defect.Ragged)
	assert.False(t, defect.Correctable())

	reason := defect.Reason()
	assert.Contains(t, reason, "line break inside a cell", "both findings are stated")
	assert.Contains(t, reason, "the header's 2 fields (record 2 has 1)")
	assert.Equal(t, "Correct it where it was written and upload it again.", defect.remedy(),
		"and the person is not told to re-export bytes that are already UTF-8")

	_, _, err := NormalizeCSV([]byte("a,b\n1,\"x\ny\"\n2\n"))
	require.Error(t, err, "which is the same answer the correction would have given")
	assert.Contains(t, err.Error(), "the header's 2 fields (record 2 has 1)")
}

// TestInspectCSV_AParseThatStopsShortWithdrawsTheOffer. The correction rewrites
// every record, so it cannot be made over a file whose records cannot all be
// read; ReadAll fails where the scan merely stops.
func TestInspectCSV_AParseThatStopsShortWithdrawsTheOffer(t *testing.T) {
	const body = "a,b\n1,\"x\ny\"\n2,he\"llo\n"

	defect := InspectCSV([]byte(body))
	require.NotNil(t, defect)

	assert.Equal(t, 1, defect.Rows, "read up to the record it could not parse")
	assert.Contains(t, defect.Unreadable, "bare \" in non-quoted-field")
	assert.False(t, defect.Correctable())
	assert.Contains(t, defect.Reason(), "not readable as a CSV all the way through")
	assert.Empty(t, defect.Ragged, "the parse error is reported ahead of counts it never reached")

	_, _, err := NormalizeCSV([]byte(body))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not readable as a CSV all the way through")
}

// TestInspectCSV_AHeaderTheReaderCannotParseWithdrawsTheOfferToo. The scan
// reads the header before its loop, and a failure there used to return
// claiming nothing -- which left the offer standing for a file whose defect
// was settled before the scan ran, an encoding or carriage-return endings, and
// which the correction then refused on its own ReadAll.
func TestInspectCSV_AHeaderTheReaderCannotParseWithdrawsTheOfferToo(t *testing.T) {
	// windows-1252 bytes, so the file has a defect the scan is not the source
	// of, and a bare quote in the header, so the scan's first read fails.
	const body = "Caf\xe9,He said \"hi\"\nx,y\n"

	defect := InspectCSV([]byte(body))
	require.NotNil(t, defect)

	assert.Equal(t, encodingWindows1252, defect.Encoding)
	assert.Contains(t, defect.Unreadable, "bare \" in non-quoted-field")
	assert.False(t, defect.Correctable())
	assert.Contains(t, defect.Reason(), "not readable as a CSV all the way through")

	_, _, err := NormalizeCSV([]byte(body))
	require.Error(t, err, "which is the answer the correction would have given")
	assert.Contains(t, err.Error(), "not readable as a CSV all the way through")
}

// TestInspectCSV_NeitherConditionIsADefectOnItsOwn. A ragged file, and one the
// reader gives up on partway, register today. Both are reasons not to offer a
// correction and neither is a reason to refuse a file nothing else is wrong
// with, which is the invariant the scan tolerated a parse failure for in the
// first place.
func TestInspectCSV_NeitherConditionIsADefectOnItsOwn(t *testing.T) {
	assert.Nil(t, InspectCSV([]byte("a,b\n1,2\n3\n")), "ragged, and readable by a line-based reader")
	assert.Nil(t, InspectCSV([]byte("a,b\n1,2\n3,he\"llo\n")), "unparseable partway, and every record on its own line")
	assert.Nil(t, InspectCSV([]byte("a,he said \"hi\"\n1,2\n")), "and the same where the header is the record it stops on")
}

// TestInspectCSV_AConsistentFileWithATornRowIsStillOffered is the other half:
// the condition added above must not withdraw the correction from the shape it
// exists for.
func TestInspectCSV_AConsistentFileWithATornRowIsStillOffered(t *testing.T) {
	defect := InspectCSV([]byte(spreadsheetCSV))
	require.NotNil(t, defect)

	assert.True(t, defect.Correctable())
	assert.Empty(t, defect.Ragged)
	assert.Empty(t, defect.Unreadable)
	assert.NotContains(t, defect.Reason(), "the header's")

	out, report, err := NormalizeCSV([]byte(spreadsheetCSV))
	require.NoError(t, err, "and it still corrects")
	assert.Equal(t, 2, report.RowsRepaired)
	assert.NotContains(t, string(out), "Suite 4\n")
}

// TestInspectCSV_NamesAtMostFiveRaggedRecords. A file whose every record is
// ragged is not a CSV with a few bad rows, and printing all of them buries the
// sentence that says so. The bound is the one checkFieldCounts uses, so the
// two refusals name the same records.
func TestInspectCSV_NamesAtMostFiveRaggedRecords(t *testing.T) {
	body := "a,b\n1,\"x\ny\"\n" + strings.Repeat("9\n", maxNamedRecords+3)

	defect := InspectCSV([]byte(body))
	require.NotNil(t, defect)
	assert.Len(t, defect.Ragged, maxNamedRecords)
	assert.Equal(t, "record 2 has 1", defect.Ragged[0])
	assert.False(t, defect.Correctable(), "every record beyond the bound is still ragged")

	_, _, err := NormalizeCSV([]byte(body))
	require.Error(t, err)
	for _, named := range defect.Ragged {
		assert.Contains(t, err.Error(), named, "the correction names the same records")
	}
}

// TestCSVDefect_RemedyMatchesWhatIsWrong. Bytes the platform cannot read are
// re-exported; records it will not adjust are fixed where the file was
// written. Telling somebody to re-export a file whose bytes were never the
// problem sends them at the wrong thing.
func TestCSVDefect_RemedyMatchesWhatIsWrong(t *testing.T) {
	assert.Equal(t, "Re-export it as UTF-8 CSV and upload that.",
		(&CSVDefect{Encoding: encodingUTF16}).remedy())
	assert.Equal(t, "Correct it where it was written and upload it again.",
		(&CSVDefect{HeaderFields: 2, Ragged: []string{"record 1 has 1"}}).remedy())
	assert.Equal(t, "Correct it where it was written and upload it again.",
		(&CSVDefect{Unreadable: "parse error"}).remedy())
}

// TestCSVDefect_CorrectableIsEveryRuleTheCorrectionApplies. Correctable is the
// question NormalizeCSV answers, asked before the offer is made; each of the
// three things that refuse there refuses here.
func TestCSVDefect_CorrectableIsEveryRuleTheCorrectionApplies(t *testing.T) {
	assert.True(t, (&CSVDefect{Rows: 1}).Correctable())
	assert.False(t, (&CSVDefect{Rows: 1, Encoding: encodingWide}).Correctable())
	assert.False(t, (&CSVDefect{Rows: 1, Ragged: []string{"record 1 has 1"}}).Correctable())
	assert.False(t, (&CSVDefect{Rows: 1, Unreadable: "parse error"}).Correctable())
}
