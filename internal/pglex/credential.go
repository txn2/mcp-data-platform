package pglex

import "strings"

// RedactedLiteral is what RedactPasswords writes in place of a password
// literal: a string literal still, so the statement reads as SQL.
const RedactedLiteral = "'[REDACTED]'"

// passwordObjects are the objects a statement gives a password to, by the
// word that opens the statement: CREATE ROLE, USER (USER MAPPING too) and
// GROUP take a PASSWORD option, ALTER ROLE and USER (USER MAPPING too) set
// one.
var passwordObjects = map[string]map[string]bool{
	"create": {"role": true, "user": true, "group": true},
	"alter":  {"role": true, "user": true},
}

// span is where a literal stands in the text, end exclusive.
type span struct{ start, end int }

// lexeme is one token of a statement and where it stands in the text.
type lexeme struct {
	word       string // the word lower-cased, or a quoted identifier as written; "" otherwise
	literal    bool   // a string literal or a dollar-quoted body
	start, end int
}

// PasswordLiteral reports whether stmt, one statement, writes a credential: a
// role, user, group or user mapping given a PASSWORD other than NULL. It
// returns the object the statement acts on ("CREATE ROLE"). The migration
// review's password_literal rule and RedactPasswords both read a statement
// by it, so what the review refuses is what a stored copy has redacted.
func PasswordLiteral(stmt string) (object string, ok bool) {
	object, _, ok = passwordLiterals(stmt, 0, len(stmt))
	return object, ok
}

// RedactPasswords returns sql with every literal after the PASSWORD of a
// statement PasswordLiteral reports replaced by RedactedLiteral, and any other
// text as it is. Every literal after the PASSWORD is replaced, not only the
// first: what follows it (VALID UNTIL, a user mapping's later options) is
// read no further than that.
func RedactPasswords(sql string) string {
	if !containsFold(sql, "password") {
		return sql
	}
	var b strings.Builder
	last := 0
	for _, r := range statementRanges(sql) {
		_, spans, ok := passwordLiterals(sql, r[0], r[1])
		if !ok {
			continue
		}
		for _, sp := range spans {
			_, _ = b.WriteString(sql[last:sp.start])
			_, _ = b.WriteString(RedactedLiteral)
			last = sp.end
		}
	}
	if last == 0 {
		return sql
	}
	_, _ = b.WriteString(sql[last:])
	return b.String()
}

// passwordLiterals reads the statement sql[start:end] for PasswordLiteral and
// returns the spans of the literals after its PASSWORD.
func passwordLiterals(sql string, start, end int) (object string, spans []span, ok bool) {
	ls := lexemes(sql, start, end)
	if len(ls) < 2 || !passwordObjects[ls[0].word][ls[1].word] {
		return "", nil, false
	}
	p := -1
	for i := 2; i < len(ls); i++ {
		if ls[i].word == "password" {
			p = i
			break
		}
	}
	if p < 0 || (p+1 < len(ls) && ls[p+1].word == "null") {
		return "", nil, false
	}
	for _, l := range ls[p+1:] {
		if l.literal {
			spans = append(spans, span{start: l.start, end: l.end})
		}
	}
	return strings.ToUpper(ls[0].word + " " + ls[1].word), spans, true
}

// lexemes tokenizes sql[start:end], dropping whitespace and comments.
func lexemes(sql string, start, end int) []lexeme {
	var out []lexeme
	for i := start; i < end; {
		l, next, keep := lexemeAt(sql, i, end)
		if keep {
			out = append(out, l)
		}
		i = next
	}
	return out
}

// lexemeAt reads the token at i: what it is, where the next starts, and
// whether it is kept.
func lexemeAt(sql string, i, end int) (l lexeme, next int, keep bool) {
	c := sql[i]
	switch {
	case isSpace(c):
		return lexeme{}, i + 1, false
	case SkipComment(sql, i) > i:
		return lexeme{}, min(SkipComment(sql, i), end), false
	case opensLiteral(sql, i):
		next = min(SkipLexeme(sql, i), end)
		return lexeme{literal: true, start: i, end: next}, next, true
	case c == '"':
		next = min(SkipString(sql, i, false), end)
		return lexeme{word: sql[i+1 : max(i+1, next-1)], start: i, end: next}, next, true
	case IsIdentByte(c):
		next = wordEnd(sql, i, end)
		return lexeme{word: strings.ToLower(sql[i:next]), start: i, end: next}, next, true
	}
	return lexeme{start: i, end: i + 1}, i + 1, true
}

// containsFold reports whether s holds word (lower-case ASCII) in any case,
// without the copy strings.ToLower makes: the audit sanitizer asks it of every
// string argument of every call.
func containsFold(s, word string) bool {
	for i := 0; i+len(word) <= len(s); i++ {
		if strings.EqualFold(s[i:i+len(word)], word) {
			return true
		}
	}
	return false
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

// opensLiteral reports whether a string literal or a dollar-quoted body
// starts at i.
func opensLiteral(sql string, i int) bool {
	return sql[i] == '\'' || (sql[i] == '$' && SkipDollarQuoted(sql, i) > i)
}

// wordEnd is past the unquoted word starting at i.
func wordEnd(sql string, i, end int) int {
	for i < end && (IsIdentByte(sql[i]) || sql[i] == '$') {
		i++
	}
	return i
}
