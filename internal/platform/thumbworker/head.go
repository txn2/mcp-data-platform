package thumbworker

import "github.com/txn2/mcp-data-platform/internal/thumbtypes"

const (
	// headRecords is how many records of a table the tile page is handed.
	//
	// It draws ten, and takes its columns from the header row. The margin is
	// for the records a cut has to skip past to reach ten complete ones --
	// a field holding a line break spans several lines of the file -- and for
	// a leading comment or blank line an export writes before the header.
	headRecords = 64
	// headBytes bounds the prefix whatever the records say.
	//
	// A file whose quoting is unbalanced -- a bare quote in an unquoted field
	// -- reads as one record to the end of the document, and without this the
	// whole document would travel to the renderer as a JavaScript string,
	// which is the cost the source bound exists to refuse. 256 KiB is far more
	// than 64 records of any table a person reads.
	headBytes = 256 << 10
	// quote is the character that opens and closes a field holding a
	// delimiter or a line break. It is the same in CSV and TSV, and the tile
	// page parses both with the same parser.
	quote = '"'
)

// headFor is the bytes a tile of this document is drawn from: the head of the
// file for the families drawn from their head, the whole document otherwise.
func headFor(contentType string, content []byte) []byte {
	if !thumbtypes.DrawnFromHead(contentType) {
		return content
	}
	return head(content)
}

// head is the first headRecords records of a delimited file, cut at a record
// boundary so the parser on the other side never sees half a record.
//
// A line break inside a quoted field is part of its record rather than the end
// of one, which is why this tracks quoting rather than counting newlines: a
// cut made on a line break inside a field would hand the tile page a record
// ending on an unbalanced quote, and every field after it would land in the
// wrong column -- the same misreading a line-based reader makes of such a file
// (internal/platform/tablecsv).
//
// Two doubled quotes inside a quoted field are an escaped quote; toggling on
// each of them in turn leaves the state where it was, which is what makes the
// toggle exact without a case for it.
//
// All three line endings end a record, because the parser on the other side
// detects all three: a spreadsheet that writes a bare carriage return would
// otherwise have no boundary to cut on and be cut at headBytes instead
// (internal/platform/tablecsv names the same family of files).
//
// A document with no record boundary inside headBytes is cut at headBytes.
// Its header row alone is longer than that, so there is no tile of its rows to
// preserve; what it gets is a tile of as much of that row as the renderer can
// be handed.
func head(content []byte) []byte {
	window := content
	if len(window) > headBytes {
		window = window[:headBytes]
	}
	records, inQuote := 0, false
	for i := 0; i < len(window); i++ {
		if window[i] == quote {
			inQuote = !inQuote
			continue
		}
		end, ok := recordEnd(window, i)
		if inQuote || !ok {
			continue
		}
		i = end
		records++
		if records == headRecords {
			return window[:i+1]
		}
	}
	return window
}

// recordEnd reports whether the byte at i ends a record and, when it does, the
// index of the ending's last byte. CRLF is one ending rather than two, so a
// cut after it never leaves a stray newline opening the next record.
func recordEnd(window []byte, i int) (end int, ok bool) {
	switch window[i] {
	case '\n':
		return i, true
	case '\r':
		if i+1 < len(window) && window[i+1] == '\n' {
			return i + 1, true
		}
		return i, true
	}
	return i, false
}
