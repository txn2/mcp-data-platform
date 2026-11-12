package trino

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// tokKind is a SQL token's kind, as the location check reads it.
type tokKind int

const (
	tokWord tokKind = iota
	tokIdent
	tokString
	tokNumber
	tokSym
)

// The two-character symbols the check reads, and the widths of a Unicode
// string's \XXXX and \+XXXXXX escapes.
const (
	symArrow     = "=>"
	symConcat    = "||"
	unicodeShort = 4
	unicodeLong  = 6
)

// sqlToken is one token: a word (lower-cased), a quoted name (unquoted and
// lower-cased), a string (its value; plain when written as '...'), a number
// or a symbol.
type sqlToken struct {
	kind  tokKind
	text  string
	value string
	plain bool
}

// lexer reads Trino SQL into tokens, skipping comments.
type lexer struct {
	sql string
	i   int
	out []sqlToken
}

// lexSQL reads Trino SQL into tokens: strings with ” escapes, U&'...' strings
// with their \XXXX and \+XXXXXX escapes, quoted names with "" escapes, words,
// numbers and symbols, comments skipped. A string, name or block comment that
// never ends is refused rather than read as ending at the end of the text.
func lexSQL(sql string) ([]sqlToken, error) {
	l := &lexer{sql: sql}
	for l.i < len(l.sql) {
		if err := l.step(); err != nil {
			return nil, err
		}
	}
	return l.out, nil
}

// step reads what starts at the lexer's position.
func (l *lexer) step() error {
	rest := l.sql[l.i:]
	switch {
	case strings.ContainsRune(" \t\n\r\f", rune(rest[0])):
		l.i++
	case strings.HasPrefix(rest, "--"):
		if end := strings.IndexByte(rest, '\n'); end >= 0 {
			l.i += end + 1
		} else {
			l.i = len(l.sql)
		}
	case strings.HasPrefix(rest, "/*"):
		end := strings.Index(rest[2:], "*/")
		if end < 0 {
			return errUnreadableSQL
		}
		l.i += end + len("/**/")
	case rest[0] == '\'' || rest[0] == '"' || unicodeString(rest):
		return l.quotedToken()
	default:
		l.bareToken()
	}
	return nil
}

// unicodeString reports whether rest starts a U&'...' string.
func unicodeString(rest string) bool {
	return len(rest) > 2 && (rest[0] == 'U' || rest[0] == 'u') && rest[1] == '&' && rest[2] == '\''
}

// quotedToken reads a string, a Unicode string or a quoted name.
func (l *lexer) quotedToken() error {
	at, tok := l.i, sqlToken{kind: tokString, plain: true}
	switch l.sql[at] {
	case '"':
		tok = sqlToken{kind: tokIdent}
	case '\'':
	default:
		tok.plain, at = false, at+len("U&")
	}
	v, next, ok := quoted(l.sql, at, l.sql[at])
	if !ok {
		return errUnreadableSQL
	}
	switch {
	case tok.kind == tokIdent:
		tok.text = strings.ToLower(v)
	case tok.plain:
		tok.value = v
	default:
		tok.value = unicodeEscapes(v)
	}
	l.out, l.i = append(l.out, tok), next
	return nil
}

// bareToken reads a number, a word or a symbol.
func (l *lexer) bareToken() {
	rest := l.sql[l.i:]
	c := rest[0]
	switch {
	case c >= '0' && c <= '9':
		n := runOf(rest, func(b byte) bool { return isWordByte(b) || b == '.' })
		l.emit(sqlToken{kind: tokNumber, text: rest[:n]}, n)
	case isWordByte(c) || c >= utf8.RuneSelf:
		n := runOf(rest, func(b byte) bool { return isWordByte(b) || b >= utf8.RuneSelf })
		l.emit(sqlToken{kind: tokWord, text: strings.ToLower(rest[:n])}, n)
	case strings.HasPrefix(rest, symArrow) || strings.HasPrefix(rest, symConcat):
		l.emit(sqlToken{kind: tokSym, text: rest[:len(symArrow)]}, len(symArrow))
	default:
		l.emit(sqlToken{kind: tokSym, text: rest[:1]}, 1)
	}
}

func (l *lexer) emit(t sqlToken, width int) {
	l.out, l.i = append(l.out, t), l.i+width
}

// runOf is the length of the run of bytes at the start of s that in accepts.
func runOf(s string, in func(byte) bool) int {
	n := 0
	for n < len(s) && in(s[n]) {
		n++
	}
	return n
}

// quoted reads a quoted run starting at sql[at] == q, a doubled q standing for
// one, and returns its value and the index after it.
func quoted(sql string, at int, q byte) (value string, next int, ok bool) {
	var b []byte
	for i := at + 1; i < len(sql); i++ {
		if sql[i] != q {
			b = append(b, sql[i])
			continue
		}
		if i+1 < len(sql) && sql[i+1] == q {
			b = append(b, q)
			i++
			continue
		}
		return string(b), i + 1, true
	}
	return "", len(sql), false
}

// unicodeEscapes decodes a U&'...' string's \XXXX and \+XXXXXX escapes with the
// default escape character. A string written with UESCAPE is decoded wrongly
// here, which is safe: a Unicode string is never accepted where a location
// is read.
func unicodeEscapes(s string) string {
	var b []byte
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' {
			b = append(b, s[i])
			continue
		}
		width, start := unicodeShort, i+1
		if start < len(s) && s[start] == '+' {
			width, start = unicodeLong, start+1
		}
		if start+width <= len(s) {
			if r, ok := unicodeRune(s[start : start+width]); ok {
				b = append(b, r...)
				i = start + width - 1
				continue
			}
		}
		b = append(b, s[i])
	}
	return string(b)
}

// unicodeRune is the character a run of four or six hex digits names, read
// as Go reads \u and \U escapes.
func unicodeRune(hex string) (string, bool) {
	prefix := `\u`
	if len(hex) == unicodeLong {
		prefix = `\U00`
	}
	s, err := strconv.Unquote(`"` + prefix + hex + `"`)
	return s, err == nil
}

func isWordByte(c byte) bool {
	return c == '_' || c == '$' || c == '@' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}
