package tableregister

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/tablecsv"
)

// The sentence a correction is summarized by, over the report tablecsv
// renders; it lives here because the summary is the registrar's wording.

func TestNormalizeCSV_CorrectsTheLineEndingsAndTheEncodingTogether(t *testing.T) {
	// 0xA9 is the copyright sign in windows-1252 and is not valid UTF-8.
	body := []byte("store_id,note\r101,15% \xa9 ACME\r")

	defect := tablecsv.Inspect(body)
	require.NotNil(t, defect)
	assert.Equal(t, "carriage return", defect.LineEndings)
	assert.Equal(t, "windows-1252", defect.Encoding)
	assert.True(t, defect.Correctable())
	assert.Contains(t, defect.Reason(), "carriage return")
	assert.Contains(t, defect.Reason(), "not UTF-8")

	out, report, err := tablecsv.Normalize(body)
	require.NoError(t, err)
	assert.Equal(t, "carriage return", report.FromLineEndings)
	assert.Equal(t, "windows-1252", report.FromEncoding)
	assert.Equal(t, "store_id,note\n101,15% © ACME\n", string(out))
	assert.Equal(t,
		"rewrote the carriage return line endings as newlines and converted the text from windows-1252 to UTF-8",
		repairSummary(report))
}

// TestRepairSummary_SaysWhatChanged renders each correction in the words the
// person who uploaded the file reads them in.
func TestRepairSummary_SaysWhatChanged(t *testing.T) {
	assert.Equal(t, "put 3 rows back onto one line",
		repairSummary(tablecsv.NormalizeReport{RowsRepaired: 3}))
	assert.Equal(t, "converted the text from windows-1252 to UTF-8",
		repairSummary(tablecsv.NormalizeReport{FromEncoding: "windows-1252"}))
	assert.Equal(t, "put 1 row back onto one line and converted the text from windows-1252 to UTF-8",
		repairSummary(tablecsv.NormalizeReport{RowsRepaired: 1, FromEncoding: "windows-1252"}))
	assert.Equal(t, "rewrote the carriage return line endings as newlines",
		repairSummary(tablecsv.NormalizeReport{FromLineEndings: "carriage return"}))
	assert.Equal(t, "rewrote it as a plain UTF-8 CSV", repairSummary(tablecsv.NormalizeReport{}))

	report := &RepairReport{NormalizeReport: tablecsv.NormalizeReport{RowsRepaired: 2}, Version: 3}
	assert.Contains(t, report.Summary(), "version 3")
	assert.Contains(t, report.Summary(), "put 2 rows back onto one line")
	assert.Empty(t, (*RepairReport)(nil).Summary(), "no correction says nothing")
}
