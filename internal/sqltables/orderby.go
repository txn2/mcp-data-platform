package sqltables

import (
	"strings"
	"unicode"
)

// HasTopLevelOrderBy reports whether a statement orders its own result: an
// ORDER BY outside every parenthesis, so not a subquery's, a CTE's or a window
// function's, none of which fix the order of the rows the statement returns.
// String literals, quoted identifiers and comments are skipped. A statement
// without one returns its rows in whatever order the engine produced them,
// which is what makes a limited subset of it arbitrary (#2057).
func HasTopLevelOrderBy(sql string) bool {
	words := topLevelWords(sql)
	for i := 0; i+1 < len(words); i++ {
		if words[i] == "order" && words[i+1] == "by" {
			return true
		}
	}
	return false
}

// topLevelWords is the lowercased words of sql at parenthesis depth zero,
// outside literals, quoted identifiers and comments.
func topLevelWords(sql string) []string {
	var words []string
	depth, start := 0, -1
	flush := func(end int) {
		if start >= 0 && depth == 0 {
			words = append(words, strings.ToLower(sql[start:end]))
		}
		start = -1
	}
	for i := 0; i < len(sql); i++ {
		if skip := skipQuotedOrComment(sql, i); skip > i {
			flush(i)
			i = skip - 1
			continue
		}
		if isWordByte(sql[i]) {
			if start < 0 {
				start = i
			}
			continue
		}
		flush(i)
		depth = depthAfter(depth, sql[i])
	}
	flush(len(sql))
	return words
}

// isWordByte reports whether c is part of a word.
func isWordByte(c byte) bool {
	return c == '_' || unicode.IsLetter(rune(c)) || unicode.IsDigit(rune(c))
}

// depthAfter is the parenthesis depth after c, never below zero.
func depthAfter(depth int, c byte) int {
	switch c {
	case '(':
		return depth + 1
	case ')':
		return max(depth-1, 0)
	default:
		return depth
	}
}

// skipQuotedOrComment returns the index just past the literal, quoted
// identifier or comment starting at i, or i when none starts there. One that
// never ends runs to the end of sql. Comments are skipped the way leadingWord
// skips them.
func skipQuotedOrComment(sql string, i int) int {
	if sql[i] == '\'' || sql[i] == '"' {
		return skipQuoted(sql, i, sql[i])
	}
	rest, skipped, ok := skipComment(sql[i:])
	switch {
	case !skipped:
		return i
	case !ok:
		return len(sql)
	default:
		return len(sql) - len(rest)
	}
}

// skipQuoted returns the index just past the quoted run opening at i, where a
// doubled quote is an escaped one.
func skipQuoted(sql string, i int, quote byte) int {
	for j := i + 1; j < len(sql); j++ {
		if sql[j] != quote {
			continue
		}
		if j+1 < len(sql) && sql[j+1] == quote {
			j++
			continue
		}
		return j + 1
	}
	return len(sql)
}
