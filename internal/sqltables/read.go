package sqltables

import "strings"

// readKinds are the statement kinds that read and change nothing: queries
// (SELECT, WITH, VALUES, TABLE), SHOW and DESCRIBE. EXPLAIN reads too, unless
// it is EXPLAIN ANALYZE, which runs the statement it explains (IsRead).
//
//nolint:gochecknoglobals // a read-only lookup set.
var readKinds = map[string]bool{
	wordSelect: true, wordWith: true, wordValues: true, wordTable: true,
	wordShow: true, wordDescribe: true,
}

// The words that open a statement that only reads (wordSelect is argfrom's).
const (
	wordWith     = "with"
	wordValues   = "values"
	wordTable    = "table"
	wordShow     = "show"
	wordDescribe = "describe"
)

// wordExplain opens an EXPLAIN, which reads unless it runs what it explains.
const wordExplain = "explain"

// IsRead reports whether sql is a statement that only reads. It is an
// allow-list: a statement whose kind is not a read kind is not a read,
// whatever it is, so a statement kind added to the engine later is refused
// by a read-only gate until it is listed here. EXPLAIN is a read when what it
// explains is not run: EXPLAIN ANALYZE runs it, so EXPLAIN ANALYZE, and an
// EXPLAIN whose explained statement is EXPLAIN ANALYZE or ANALYZE, is not.
func IsRead(sql string) bool {
	word, rest := nextWord(sql)
	if readKinds[word] {
		return true
	}
	if word != wordExplain {
		return false
	}
	for {
		rest = skipExplainOptions(rest)
		next, after := nextWord(rest)
		switch next {
		case "analyze", "":
			return false
		case wordExplain:
			rest = after
		default:
			return true
		}
	}
}

// skipExplainOptions removes an EXPLAIN option list ("(TYPE IO, FORMAT
// JSON)") from the front of s. The options are identifiers and keywords, so
// the list ends at the first closing parenthesis.
func skipExplainOptions(s string) string {
	t, ok := skipBlank(s)
	if !ok {
		return ""
	}
	if !strings.HasPrefix(t, "(") {
		return s
	}
	if _, after, ok := strings.Cut(t, ")"); ok {
		return after
	}
	return ""
}

// skipBlank removes whitespace and comments from the front of s; ok is false
// for a comment that never ends.
func skipBlank(s string) (string, bool) {
	for {
		s = strings.TrimLeft(s, " \t\r\n")
		after, skipped, ok := skipComment(s)
		if !ok {
			return "", false
		}
		if !skipped {
			return s, true
		}
		s = after
	}
}
