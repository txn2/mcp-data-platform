// Package pglex reads PostgreSQL SQL text lexically: where its top-level
// statements end, and where a string literal, a quoted identifier, a
// dollar-quoted body or a comment starts and stops. It parses nothing; the
// application database kind's statement checks, its migration lint and the
// admin console's statement lists read SQL through it (#2410, #2427).
package pglex

import "strings"

// Split cuts SQL into its top-level statements at the semicolons that end
// them, skipping a semicolon inside a string literal (with an E-string's
// backslash escapes), a quoted identifier, a dollar-quoted body (a
// function's, a DO block's), a comment, or a SQL-standard function body
// (BEGIN ATOMIC ... END, whose statements end in semicolons of their own).
// Each statement is trimmed; an empty one is dropped.
func Split(sql string) []string {
	out := []string{}
	start, depth := 0, 0
	for i := 0; i < len(sql); {
		if next := SkipLexeme(sql, i); next > i {
			i = next
			continue
		}
		if word := wordAt(sql, i); word != "" {
			depth = blockDepth(sql, i+len(word), word, depth)
			i += len(word)
			continue
		}
		if sql[i] == ';' && depth == 0 {
			out = appendStatement(out, sql[start:i])
			start = i + 1
		}
		i++
	}
	return appendStatement(out, sql[start:])
}

// wordAt is the unquoted word starting at i, or "" when none starts there.
func wordAt(sql string, i int) string {
	if !IsIdentByte(sql[i]) || (i > 0 && (IsIdentByte(sql[i-1]) || sql[i-1] == '$')) {
		return ""
	}
	j := i
	for j < len(sql) && (IsIdentByte(sql[j]) || sql[j] == '$') {
		j++
	}
	return sql[i:j]
}

// blockDepth is how deep in SQL-standard function bodies the text after word
// is, as psql counts it: BEGIN ATOMIC opens a body, inside one CASE opens an
// expression END closes, and END closes the innermost.
func blockDepth(sql string, after int, word string, depth int) int {
	switch {
	case strings.EqualFold(word, "begin") && strings.EqualFold(nextWord(sql, after), "atomic"):
		return depth + 1
	case depth > 0 && strings.EqualFold(word, "case"):
		return depth + 1
	case depth > 0 && strings.EqualFold(word, "end"):
		return depth - 1
	}
	return depth
}

// nextWord is the word after i, past whitespace and comments.
func nextWord(sql string, i int) string {
	for i < len(sql) {
		switch {
		case sql[i] == ' ' || sql[i] == '\t' || sql[i] == '\n' || sql[i] == '\r':
			i++
		case SkipComment(sql, i) > i:
			i = SkipComment(sql, i)
		default:
			return wordAt(sql, i)
		}
	}
	return ""
}

func appendStatement(out []string, s string) []string {
	if s = strings.TrimSpace(s); s != "" {
		out = append(out, s)
	}
	return out
}

// SkipLexeme returns the index just past the literal, quoted identifier,
// dollar-quoted body or comment starting at i, or i when none starts there.
// One that never ends runs to the end of sql.
func SkipLexeme(sql string, i int) int {
	switch sql[i] {
	case '\'':
		return SkipString(sql, i, IsEString(sql, i))
	case '"':
		return SkipString(sql, i, false)
	case '$':
		return SkipDollarQuoted(sql, i)
	}
	return SkipComment(sql, i)
}

// IsEString reports whether the quote at i opens an E'...' string, whose
// backslashes escape.
func IsEString(sql string, i int) bool {
	return i > 0 && (sql[i-1] == 'E' || sql[i-1] == 'e') && (i < 2 || !IsIdentByte(sql[i-2]))
}

// SkipComment returns the index just past a comment starting at i, or i.
func SkipComment(sql string, i int) int {
	switch {
	case strings.HasPrefix(sql[i:], "--"):
		if j := strings.IndexByte(sql[i:], '\n'); j >= 0 {
			return i + j + 1
		}
		return len(sql)
	case strings.HasPrefix(sql[i:], "/*"):
		return skipBlockComment(sql, i)
	}
	return i
}

// SkipString skips a quoted run where a doubled quote is an escaped one and,
// in an E-string, a backslash escapes the next byte.
func SkipString(sql string, i int, backslash bool) int {
	quote := sql[i]
	for j := i + 1; j < len(sql); j++ {
		switch {
		case backslash && sql[j] == '\\':
			j++
		case sql[j] != quote:
		case j+1 < len(sql) && sql[j+1] == quote:
			j++
		default:
			return j + 1
		}
	}
	return len(sql)
}

// skipBlockComment skips a /* */ comment, which nests in PostgreSQL.
func skipBlockComment(sql string, i int) int {
	depth := 0
	for j := i; j+1 < len(sql); j++ {
		switch {
		case sql[j] == '/' && sql[j+1] == '*':
			depth++
			j++
		case sql[j] == '*' && sql[j+1] == '/':
			depth--
			j++
			if depth == 0 {
				return j + 1
			}
		}
	}
	return len(sql)
}

// SkipDollarQuoted skips $tag$ ... $tag$, or returns i when $ starts no
// dollar quote (a positional parameter, $1).
func SkipDollarQuoted(sql string, i int) int {
	end := strings.IndexByte(sql[i+1:], '$')
	if end < 0 {
		return i
	}
	tag := sql[i : i+end+2]
	for k := 1; k < len(tag)-1; k++ {
		if !IsIdentByte(tag[k]) || (k == 1 && tag[k] >= '0' && tag[k] <= '9') {
			return i
		}
	}
	if closing := strings.Index(sql[i+len(tag):], tag); closing >= 0 {
		return i + len(tag) + closing + len(tag)
	}
	return len(sql)
}

// IsIdentByte reports whether c may appear in an unquoted identifier.
func IsIdentByte(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}
