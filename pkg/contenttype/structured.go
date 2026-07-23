package contenttype

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

// minDelimitedRows is the number of consistently-delimited lines a prefix must
// hold before it is classified as CSV or TSV. A header plus two data rows is
// enough structure that prose with incidental commas does not qualify.
const minDelimitedRows = 3

// minDelimitedColumns is the number of fields each delimited line must hold.
const minDelimitedColumns = 2

// minJSONTokens is the number of JSON tokens a prefix must yield before it is
// classified as JSON. A lone "{" is not evidence of a JSON document.
const minJSONTokens = 2

// detectStructuredText classifies textual content that http.DetectContentType
// reports as text/plain. It returns the canonical type of the first family
// that matches, or the empty string when the content has no recognized
// structure.
//
// Order matters: NDJSON is checked before JSON because a JSON decoder happily
// reads a newline-delimited stream as a sequence of values, and the delimited
// (CSV/TSV) checks come last because they are the loosest.
func detectStructuredText(prefix []byte) string {
	if !utf8.Valid(trimPartialRune(prefix)) {
		return ""
	}
	body := bytes.TrimLeft(prefix, " \t\r\n\uFEFF")
	if len(body) == 0 {
		return ""
	}

	switch {
	case looksLikeNDJSON(body):
		return NDJSON
	case looksLikeJSON(body):
		return JSON
	case looksLikeXML(body):
		return XML
	case looksLikeYAML(body):
		return YAML
	case looksDelimited(body, ','):
		return CSV
	case looksDelimited(body, '\t'):
		return TSV
	default:
		return ""
	}
}

// trimPartialRune drops a trailing byte sequence that is a valid UTF-8 prefix
// but was cut short by the sniff window, so a truncated multi-byte character
// does not make an otherwise-valid document look like binary.
func trimPartialRune(b []byte) []byte {
	for i := 0; i < utf8.UTFMax && i < len(b); i++ {
		trimmed := b[:len(b)-i]
		if r, size := utf8.DecodeLastRune(trimmed); r != utf8.RuneError || size > 1 {
			return trimmed
		}
	}
	return b
}

// looksLikeJSON reports whether body opens a JSON object or array whose tokens
// parse cleanly up to the end of the sniff window. Truncation at the window
// boundary counts as success: the document was well-formed as far as it was
// read.
func looksLikeJSON(body []byte) bool {
	if body[0] != '{' && body[0] != '[' {
		return false
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	tokens := 0
	for {
		_, err := dec.Token()
		switch {
		case err == nil:
			tokens++
			if tokens > maxScannedJSONTokens {
				return true
			}
		case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
			return tokens >= minJSONTokens
		default:
			return false
		}
	}
}

// maxScannedJSONTokens caps the token scan so a large prefix of a deeply
// nested document does not cost more than a constant amount of work.
const maxScannedJSONTokens = 4096

// looksLikeNDJSON reports whether body holds at least two complete lines, each
// a self-contained JSON object or array. The final line is ignored because the
// sniff window may have cut it in half.
func looksLikeNDJSON(body []byte) bool {
	if body[0] != '{' && body[0] != '[' {
		return false
	}
	lines := completeLines(body)
	if len(lines) < 2 {
		return false
	}
	for _, line := range lines {
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 {
			return false
		}
		if trimmed[0] != '{' && trimmed[0] != '[' {
			return false
		}
		if !json.Valid(trimmed) {
			return false
		}
	}
	return true
}

// completeLines splits body into the lines that are known to be whole, dropping
// the trailing fragment left by the sniff window. It returns at most
// maxScannedLines entries.
func completeLines(body []byte) [][]byte {
	idx := bytes.LastIndexByte(body, '\n')
	if idx < 0 {
		return nil
	}
	lines := bytes.Split(body[:idx], []byte("\n"))
	out := make([][]byte, 0, len(lines))
	for _, line := range lines {
		line = bytes.TrimRight(line, "\r")
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		out = append(out, line)
		if len(out) >= maxScannedLines {
			break
		}
	}
	return out
}

// maxScannedLines caps how many lines the line-oriented heuristics examine.
const maxScannedLines = 64

// activeRootTags lists the root element names looksLikeXML rejects. These are
// the roots of the active families (HTML fragments and SVG documents): routing
// them to the XML viewer would classify script-bearing markup into a family
// detection is not allowed to reach, so they stay plain text instead.
var activeRootTags = map[string]bool{
	"html": true, "head": true, "body": true, "div": true, "span": true,
	"p": true, "a": true, "img": true, "table": true, "script": true,
	"style": true, "ul": true, "ol": true, "li": true, "h1": true,
	"form": true, "input": true, "button": true, "section": true, "main": true,
	"svg": true,
}

// minElementLen is the shortest possible element start, "<a>".
const minElementLen = 3

// looksLikeXML reports whether body opens with an XML declaration or a
// non-HTML root element.
func looksLikeXML(body []byte) bool {
	if bytes.HasPrefix(body, []byte("<?xml")) {
		return true
	}
	if body[0] != '<' || len(body) < minElementLen {
		return false
	}
	if body[1] == '!' || body[1] == '?' || body[1] == '/' {
		return false
	}
	name := xmlRootName(body[1:])
	if name == "" {
		return false
	}
	return !activeRootTags[strings.ToLower(name)]
}

// xmlRootName returns the element name that starts rest, or the empty string
// when rest does not open a well-formed element.
func xmlRootName(rest []byte) string {
	end := bytes.IndexAny(rest, " \t\r\n/>")
	if end <= 0 {
		return ""
	}
	name := string(rest[:end])
	for i, r := range name {
		if !validNameRune(r, i == 0) {
			return ""
		}
	}
	return name
}

// xmlNamePunct is the punctuation an XML element name may contain.
const xmlNamePunct = "_:-."

// validNameRune reports whether r may appear in an XML element name. Digits are
// legal everywhere except the first position.
func validNameRune(r rune, first bool) bool {
	if isASCIILetter(r) || strings.ContainsRune(xmlNamePunct, r) {
		return true
	}
	return !first && isASCIIDigit(r)
}

func isASCIILetter(r rune) bool { return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') }

func isASCIIDigit(r rune) bool { return r >= '0' && r <= '9' }

// looksLikeYAML reports whether body opens with a YAML directive or document
// marker. Bare "key: value" text is deliberately not enough: it matches log
// lines and prose far too often, and misclassifying prose costs more than
// leaving a marker-less YAML document as plain text.
func looksLikeYAML(body []byte) bool {
	if bytes.HasPrefix(body, []byte("%YAML")) {
		return true
	}
	docMarker := []byte("---")
	if !bytes.HasPrefix(body, docMarker) {
		return false
	}
	rest := body[len(docMarker):]
	return len(rest) == 0 || rest[0] == '\n' || rest[0] == '\r' ||
		rest[0] == ' ' || rest[0] == '\t'
}

// looksDelimited reports whether body parses as a delimiter-separated table:
// at least minDelimitedRows complete lines, each holding the same number of
// fields, and at least minDelimitedColumns of them.
func looksDelimited(body []byte, delim rune) bool {
	lines := completeLines(body)
	if len(lines) < minDelimitedRows {
		return false
	}
	if !bytes.ContainsRune(lines[0], delim) {
		return false
	}

	reader := csv.NewReader(bytes.NewReader(bytes.Join(lines, []byte("\n"))))
	reader.Comma = delim
	reader.LazyQuotes = true
	reader.FieldsPerRecord = 0 // enforce consistency against the first record

	rows := 0
	for {
		rec, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return false
		}
		if len(rec) < minDelimitedColumns {
			return false
		}
		rows++
	}
	return rows >= minDelimitedRows
}
