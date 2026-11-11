package sqlident

import "strings"

// ReadsEveryColumn reports whether a statement selects every column of a
// table it reads: a `*` projection, bare (`SELECT *`, `SELECT a, *`) or
// qualified (`t.*`). A star inside parentheses (`count(*)`) and a
// multiplication (`a * b`) read no column by themselves and do not count, and
// a star inside a string literal, a quoted identifier or a comment is not
// read at all.
//
// It is lexical, like Identifiers, and errs toward true: the PII consent check
// counts every PII column of a table when this holds, and a missed star would
// let a query read personal data without the prompt (#2586).
func ReadsEveryColumn(sql string) bool {
	text := blankLiterals(sql)
	for i := 0; i < len(text); i++ {
		if text[i] == '*' && starIsProjection(text, i) {
			return true
		}
	}
	return false
}

// starIsProjection reports whether the star at i is a projection, judged by
// what precedes it: a qualifier's dot, a select list's comma, or the SELECT
// (DISTINCT, ALL) that opens the list.
func starIsProjection(text string, i int) bool {
	j := i - 1
	for j >= 0 && isSpace(text[j]) {
		j--
	}
	if j < 0 {
		return false
	}
	switch text[j] {
	case '.', ',':
		return true
	}
	if !isIdentChar(text[j]) {
		return false
	}
	end := j + 1
	for j >= 0 && isIdentChar(text[j]) {
		j--
	}
	switch strings.ToLower(text[j+1 : end]) {
	case "select", "distinct", "all":
		return true
	}
	return false
}

// blankLiterals is sql with every string literal, quoted identifier and
// comment replaced by spaces, so a star or a word inside one is never read.
func blankLiterals(sql string) string {
	out := []byte(sql)
	n := len(sql)
	blank := func(from, to int) {
		for k := from; k < to && k < n; k++ {
			out[k] = ' '
		}
	}
	for pos := 0; pos < n; {
		var next int
		switch {
		case sql[pos] == '\'':
			next = skipSingleQuoted(sql, pos, n)
		case sql[pos] == '"':
			_, next = readDoubleQuoted(sql, pos, n)
		case isBlockCommentStart(sql, pos, n):
			next = skipBlockComment(sql, pos, n)
		case isLineCommentStart(sql, pos, n):
			next = skipLineComment(sql, pos, n)
		default:
			pos++
			continue
		}
		blank(pos, next)
		pos = next
	}
	return string(out)
}

func isSpace(ch byte) bool {
	return ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' || ch == '\f'
}
