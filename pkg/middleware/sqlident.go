package middleware

import "strings"

// ExtractIdentifiers lexes a SQL string and returns all identifiers found,
// lowercased. This is dialect-agnostic because it operates at the lexical
// level (tokenization), not at the grammar level (parsing). String literals,
// block comments, and line comments are skipped entirely. Double-quoted
// identifiers are extracted and lowercased.
//
// The function runs in O(n) time with a single pass over the input.
func ExtractIdentifiers(sql string) map[string]bool {
	ids := make(map[string]bool)
	n := len(sql)
	pos := 0

	for pos < n {
		pos = lexOne(sql, pos, n, ids)
	}

	return ids
}

// lexOne processes one token at position pos and returns the next position.
// Identifiers found are added to ids.
func lexOne(sql string, pos, n int, ids map[string]bool) int {
	ch := sql[pos]

	if ch == '\'' {
		return skipSingleQuoted(sql, pos, n)
	}
	if ch == '"' {
		return lexDoubleQuoted(sql, pos, n, ids)
	}
	if isBlockCommentStart(sql, pos, n) {
		return skipBlockComment(sql, pos, n)
	}
	if isLineCommentStart(sql, pos, n) {
		return skipLineComment(sql, pos, n)
	}
	if isIdentStart(ch) {
		return lexBareword(sql, pos, n, ids)
	}
	return pos + 1
}

// lexDoubleQuoted reads a double-quoted identifier and adds it to ids.
func lexDoubleQuoted(sql string, pos, n int, ids map[string]bool) int {
	id, next := readDoubleQuoted(sql, pos, n)
	if id != "" {
		ids[strings.ToLower(id)] = true
	}
	return next
}

// lexBareword reads a bareword identifier and adds it to ids.
func lexBareword(sql string, pos, n int, ids map[string]bool) int {
	word, next := readBareword(sql, pos, n)
	ids[strings.ToLower(word)] = true
	return next
}

// isBlockCommentStart returns true if pos is at the start of a block comment.
func isBlockCommentStart(sql string, pos, n int) bool {
	return sql[pos] == '/' && pos+1 < n && sql[pos+1] == '*'
}

// isLineCommentStart returns true if pos is at the start of a line comment.
func isLineCommentStart(sql string, pos, n int) bool {
	return sql[pos] == '-' && pos+1 < n && sql[pos+1] == '-'
}

// skipSingleQuoted advances past a single-quoted string literal, handling
// escaped quotes (”).
func skipSingleQuoted(sql string, pos, n int) int {
	pos++ // skip opening quote
	for pos < n {
		if sql[pos] == '\'' {
			pos++
			// '' is an escaped quote inside the literal
			if pos < n && sql[pos] == '\'' {
				pos++
				continue
			}
			return pos
		}
		pos++
	}
	return pos
}

// readDoubleQuoted reads a double-quoted identifier and returns it along
// with the position after the closing quote. Double-quotes inside are
// escaped as "".
func readDoubleQuoted(sql string, pos, n int) (id string, next int) {
	pos++ // skip opening quote
	var b strings.Builder
	for pos < n {
		if sql[pos] == '"' {
			pos++
			// "" is an escaped double-quote inside the identifier
			if pos < n && sql[pos] == '"' {
				b.WriteByte('"')
				pos++
				continue
			}
			return b.String(), pos
		}
		b.WriteByte(sql[pos])
		pos++
	}
	return b.String(), pos
}

// skipBlockComment advances past a /* ... */ block comment.
func skipBlockComment(sql string, pos, n int) int {
	pos += 2 // skip /*
	for pos+1 < n {
		if sql[pos] == '*' && sql[pos+1] == '/' {
			return pos + 2
		}
		pos++
	}
	return n
}

// skipLineComment advances past a -- line comment to end of line.
func skipLineComment(sql string, pos, n int) int {
	pos += 2 // skip --
	for pos < n && sql[pos] != '\n' {
		pos++
	}
	return pos
}

// readBareword reads an unquoted identifier (letters, digits, underscores).
func readBareword(sql string, pos, n int) (word string, next int) {
	start := pos
	for pos < n && isIdentChar(sql[pos]) {
		pos++
	}
	return sql[start:pos], pos
}

// isIdentStart returns true if ch can start an identifier (letter or underscore).
func isIdentStart(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_'
}

// isIdentChar returns true if ch can continue an identifier.
func isIdentChar(ch byte) bool {
	return isIdentStart(ch) || (ch >= '0' && ch <= '9')
}
