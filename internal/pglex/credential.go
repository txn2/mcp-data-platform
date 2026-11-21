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

// maxBodyDepth bounds how deep a dollar-quoted body inside a body is read.
const maxBodyDepth = 4

// PasswordLiteral reports whether stmt, one statement, writes a credential: a
// role, user, group or user mapping given a PASSWORD other than NULL, in the
// statement itself or in a statement of a dollar-quoted body it carries (a DO
// block, a function's body). It returns the object the statement acts on
// ("CREATE ROLE"). The migration review's password_literal rule and
// RedactPasswords both read a statement by it, so what the review refuses is
// what a stored copy has redacted.
func PasswordLiteral(stmt string) (object string, ok bool) {
	return credentialIn(stmt, 0)
}

// credentialIn is PasswordLiteral at a body depth.
func credentialIn(stmt string, depth int) (string, bool) {
	if object, _, ok := passwordLiterals(stmt, 0, len(stmt), depth > 0); ok {
		return object, true
	}
	if depth >= maxBodyDepth {
		return "", false
	}
	for _, body := range dollarBodies(stmt, 0, len(stmt)) {
		inner := stmt[body.start:body.end]
		for _, r := range statementRanges(inner) {
			if object, ok := credentialIn(inner[r[0]:r[1]], depth+1); ok {
				return object, true
			}
		}
	}
	return "", false
}

// RedactPasswords returns sql with every literal after the PASSWORD of a
// statement PasswordLiteral reports replaced by RedactedLiteral, and any other
// text as it is. Every literal after the PASSWORD is replaced, not only the
// first: what follows it (VALID UNTIL, a user mapping's later options) is
// read no further than that.
//
// A statement that sets no password itself has each dollar-quoted body it
// carries read the same way, so a role created inside a DO block or a
// function's body is redacted as one created at the top level.
func RedactPasswords(sql string) string {
	return redactAt(sql, 0)
}

// redactAt is RedactPasswords at a body depth.
func redactAt(sql string, depth int) string {
	if !containsFold(sql, "password") {
		return sql
	}
	var b strings.Builder
	last := 0
	for _, r := range statementRanges(sql) {
		for _, rep := range replacementsIn(sql, r, depth) {
			_, _ = b.WriteString(sql[last:rep.start])
			_, _ = b.WriteString(rep.text)
			last = rep.end
		}
	}
	if last == 0 {
		return sql
	}
	_, _ = b.WriteString(sql[last:])
	return b.String()
}

// replacement is text to write in place of sql[start:end].
type replacement struct {
	span
	text string
}

// replacementsIn is what RedactPasswords writes over in the statement
// sql[r[0]:r[1]]: its password literals, or, for a statement that sets no
// password itself, each dollar-quoted body redacted in turn.
func replacementsIn(sql string, r [2]int, depth int) []replacement {
	var out []replacement
	if _, spans, ok := passwordLiterals(sql, r[0], r[1], depth > 0); ok {
		for _, sp := range spans {
			out = append(out, replacement{span: sp, text: RedactedLiteral})
		}
		return out
	}
	if depth >= maxBodyDepth {
		return nil
	}
	for _, body := range dollarBodies(sql, r[0], r[1]) {
		inner := sql[body.start:body.end]
		if redacted := redactAt(inner, depth+1); redacted != inner {
			out = append(out, replacement{span: body, text: redacted})
		}
	}
	return out
}

// passwordLiterals reads the statement sql[start:end] for PasswordLiteral and
// returns the spans of the literals after its PASSWORD. In a body (inBody) the
// statement may open with procedural words before the one that sets a
// password (BEGIN, IF ... THEN), so it is found wherever it starts.
func passwordLiterals(sql string, start, end int, inBody bool) (object string, spans []span, ok bool) {
	ls, found := credentialStatement(lexemes(sql, start, end), inBody)
	if !found {
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

// credentialStatement is ls from the word that opens a statement giving a
// password (CREATE ROLE, ALTER USER ...): the first two words, or anywhere in
// a body statement (inBody), which may open with procedural words.
func credentialStatement(ls []lexeme, inBody bool) ([]lexeme, bool) {
	for k := 0; k+1 < len(ls); k++ {
		if passwordObjects[ls[k].word][ls[k+1].word] {
			return ls[k:], true
		}
		if !inBody {
			break
		}
	}
	return nil, false
}

// dollarBodies is where the content of each dollar-quoted body in
// sql[start:end] stands, between its opening and closing tags.
func dollarBodies(sql string, start, end int) []span {
	var out []span
	for _, l := range lexemes(sql, start, end) {
		if !l.literal || sql[l.start] != '$' {
			continue
		}
		tagEnd := strings.IndexByte(sql[l.start+1:l.end], '$')
		if tagEnd < 0 {
			continue
		}
		tag := l.start + tagEnd + 2 // past the opening tag's closing $
		if closing := l.end - (tag - l.start); closing >= tag {
			out = append(out, span{start: tag, end: closing})
		}
	}
	return out
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
