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
	} {
		t.Run(tc.name, func(t *testing.T) {
			defect := InspectCSV(tc.content)
			require.NotNil(t, defect)
			assert.Equal(t, tc.want, defect.Encoding)
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
