package thumbworker

import (
	"fmt"
	"strings"
	"testing"
)

// csvOf is a table of n data rows under one header, each row naming its own
// number so a prefix can be told from a truncation.
func csvOf(n int) string {
	rows := make([]string, 0, n+1)
	rows = append(rows, "id,name")
	for i := 1; i <= n; i++ {
		rows = append(rows, fmt.Sprintf("%d,row %d", i, i))
	}
	return strings.Join(rows, "\n") + "\n"
}

// TestHeadCutsAtARecordBoundary. The tile page parses what it is handed, so a
// prefix that ends mid-record would put every field of that record in the
// wrong column.
func TestHeadCutsAtARecordBoundary(t *testing.T) {
	got := string(head([]byte(csvOf(500))))
	if want := csvOf(headRecords - 1); got != want {
		t.Fatalf("head kept %d bytes, want the first %d records (%d bytes)", len(got), headRecords, len(want))
	}
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("the prefix does not end on a record boundary: %q", got[max(0, len(got)-40):])
	}
}

// TestHeadKeepsTheHeaderAndTheRowsATileDraws. A tile is the header row and ten
// rows; a prefix that lost either is a tile of nothing.
func TestHeadKeepsTheHeaderAndTheRowsATileDraws(t *testing.T) {
	got := string(head([]byte(csvOf(500))))
	if !strings.HasPrefix(got, "id,name\n") {
		t.Fatalf("the prefix does not open with the header row: %q", got[:min(40, len(got))])
	}
	if !strings.Contains(got, "10,row 10\n") {
		t.Error("the prefix does not reach the tenth row, which is the last one a tile draws")
	}
}

// TestHeadReturnsAShortDocumentUnchanged. Cutting is what a large file needs;
// a small one must draw exactly as it did before, byte for byte.
func TestHeadReturnsAShortDocumentUnchanged(t *testing.T) {
	for _, in := range []string{"", "id,name\n1,one\n", "id,name\n1,no trailing newline", csvOf(headRecords - 2)} {
		if got := string(head([]byte(in))); got != in {
			t.Errorf("head(%q) = %q, want it unchanged", in, got)
		}
	}
}

// TestHeadCountsARecordNotALine. A line break inside a quoted field belongs to
// its record. Counting lines would cut such a file in the middle of a field,
// on an unbalanced quote -- the misreading a line-based reader makes of a file
// like this (#1441).
func TestHeadCountsARecordNotALine(t *testing.T) {
	records := make([]string, 0, 201)
	records = append(records, "id,note")
	for i := 1; i <= 200; i++ {
		records = append(records, fmt.Sprintf("%d,\"line one\nline two\nline three\"", i))
	}
	got := string(head([]byte(strings.Join(records, "\n") + "\n")))
	if strings.Count(got, "\n") <= headRecords {
		t.Errorf("the prefix holds %d lines; a record spanning three lines means far more than %d",
			strings.Count(got, "\n"), headRecords)
	}
	if strings.Count(got, `"`)%2 != 0 {
		t.Error("the prefix ends inside a quoted field; every field after it would land in the wrong column")
	}
	// The cut is still where the records run out, not at the byte bound.
	if len(got) >= headBytes {
		t.Errorf("the prefix is %d bytes; these records fit inside %d", len(got), headBytes)
	}
}

// TestHeadIsBoundedWhateverTheQuotingSays. An unbalanced quote makes the rest
// of the document one record, and the byte bound is what keeps that document
// from traveling to the renderer whole -- which is the cost the source bound
// exists to refuse.
func TestHeadIsBoundedWhateverTheQuotingSays(t *testing.T) {
	doc := "id,note\n1,\"unterminated\n" + strings.Repeat("filler,filler\n", 200_000)
	got := head([]byte(doc))
	if len(got) > headBytes {
		t.Fatalf("head kept %d bytes of a document with an unbalanced quote, want at most %d", len(got), headBytes)
	}
	if len(got) != headBytes {
		t.Errorf("head kept %d bytes, want the whole %d-byte window: there is no record boundary in it", len(got), headBytes)
	}
}

// TestHeadForAppliesToTablesAlone. Every other family is laid out in full, so
// a prefix of one is a document with its tail cut off.
func TestHeadForAppliesToTablesAlone(t *testing.T) {
	doc := []byte(csvOf(500))
	for _, ct := range []string{"text/csv", "text/tab-separated-values; charset=utf-8"} {
		if got := headFor(ct, doc); len(got) >= len(doc) {
			t.Errorf("headFor(%q) kept the whole document", ct)
		}
	}
	for _, ct := range []string{"text/markdown", "text/html", "application/json", "application/pdf", "text/plain"} {
		if got := headFor(ct, doc); len(got) != len(doc) {
			t.Errorf("headFor(%q) kept %d of %d bytes, want the whole document", ct, len(got), len(doc))
		}
	}
}

// TestHeadCutsOnEveryLineEnding. The parser on the other side detects CRLF, LF
// and a bare carriage return, so all three end a record here. A spreadsheet
// that writes CR-only endings -- the classic Mac ending some exports still
// produce -- would otherwise have no boundary to cut on at all.
func TestHeadCutsOnEveryLineEnding(t *testing.T) {
	for _, tc := range []struct{ name, ending string }{
		{"LF", "\n"},
		{"CRLF", "\r\n"},
		{"CR", "\r"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := make([]string, 0, 501)
			rows = append(rows, "id,name")
			for i := 1; i <= 500; i++ {
				rows = append(rows, fmt.Sprintf("%d,row %d", i, i))
			}
			doc := strings.Join(rows, tc.ending) + tc.ending
			got := string(head([]byte(doc)))
			want := strings.Join(rows[:headRecords], tc.ending) + tc.ending
			if got != want {
				t.Fatalf("head kept %d bytes, want the first %d records (%d bytes)", len(got), headRecords, len(want))
			}
		})
	}
}
