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
// statement itself, in a statement of a dollar-quoted body it carries (a DO
// block, a function's body), or in a string literal that reads as one once
// unquoted (a role given a password inside an EXECUTE literal, #2750). It returns the
// object the statement acts on ("CREATE ROLE"), or BuiltAtRunTime for a
// statement or body that builds SQL at run time naming a role and PASSWORD,
// which cannot be read. The migration review's password_literal rule and
// RedactPasswords both read a statement by it, so what the review refuses is
// what a stored copy has redacted.
func PasswordLiteral(stmt string) (object string, ok bool) {
	if buildsCredential(stmt) {
		return BuiltAtRunTime, true
	}
	return credentialIn(stmt, 0)
}

// credentialIn is PasswordLiteral at a body depth: the statement itself, then
// each literal it holds, a dollar-quoted body read as a body (and refused
// whole when it builds a credential at run time), a string literal read as
// the SQL it stands for.
func credentialIn(stmt string, depth int) (string, bool) {
	if object, _, ok := passwordLiterals(stmt, 0, len(stmt), depth > 0); ok {
		return object, true
	}
	if depth >= maxBodyDepth {
		return "", false
	}
	for _, l := range lexemes(stmt, 0, len(stmt)) {
		if !l.literal {
			continue
		}
		inner := literalText(stmt, l)
		if stmt[l.start] == '$' && buildsCredential(inner) {
			return BuiltAtRunTime, true
		}
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
// A statement that sets no password itself has each dollar-quoted body and
// string literal it carries read the same way, so a role created inside a DO
// block, a function's body or an EXECUTE '...' is redacted as one created at
// the top level; a string literal is written back quoted. A statement or body
// that builds SQL at run time naming a role and PASSWORD has every literal it
// holds replaced, since which one carries the password cannot be read
// (#2750).
func RedactPasswords(sql string) string {
	return redactAt(sql, 0)
}

// redactAt is RedactPasswords at a body depth.
func redactAt(sql string, depth int) string {
	if !containsFold(sql, loginOption) {
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
// sql[r[0]:r[1]]: its password literals; every literal it holds when it
// builds a credential at run time; or, for a statement that sets no password
// itself, each dollar-quoted body and string literal redacted in turn.
func replacementsIn(sql string, r [2]int, depth int) []replacement {
	var out []replacement
	if _, spans, ok := passwordLiterals(sql, r[0], r[1], depth > 0); ok {
		for _, sp := range spans {
			out = append(out, replacement{span: sp, text: RedactedLiteral})
		}
		return out
	}
	if buildsCredential(sql[r[0]:r[1]]) {
		return everyLiteral(sql, r[0], r[1])
	}
	if depth >= maxBodyDepth {
		return nil
	}
	for _, l := range lexemes(sql, r[0], r[1]) {
		if rep, ok := redactedLiteral(sql, l, depth); ok {
			out = append(out, rep)
		}
	}
	return out
}

// redactedLiteral is the literal l redacted at the next depth, when that
// changes it: a dollar-quoted body's content (every literal of it when the
// body builds a credential at run time), or a string literal's text written
// back quoted.
func redactedLiteral(sql string, l lexeme, depth int) (replacement, bool) {
	if !l.literal {
		return replacement{}, false
	}
	if sql[l.start] != '$' {
		inner := unquote(sql, l)
		redacted := redactAt(inner, depth+1)
		return replacement{start: l.start, end: l.end, text: requote(redacted)}, redacted != inner
	}
	bodies := dollarBodies(sql, l.start, l.end)
	if len(bodies) == 0 {
		return replacement{}, false
	}
	inner := sql[bodies[0].start:bodies[0].end]
	redacted := redactAt(inner, depth+1)
	if buildsCredential(inner) {
		redacted = redactEvery(inner)
	}
	return replacement{span: bodies[0], text: redacted}, redacted != inner
}

// everyLiteral replaces every literal of sql[start:end] with RedactedLiteral.
func everyLiteral(sql string, start, end int) []replacement {
	var out []replacement
	for _, l := range lexemes(sql, start, end) {
		if !l.literal {
			continue
		}
		if n := len(out); n > 0 && out[n-1].end == l.start {
			out[n-1].end = l.end
			continue
		}
		out = append(out, replacement{start: l.start, end: l.end, text: RedactedLiteral})
	}
	return out
}

// redactEvery is sql with every literal replaced by RedactedLiteral.
func redactEvery(sql string) string {
	var b strings.Builder
	last := 0
	for _, rep := range everyLiteral(sql, 0, len(sql)) {
		_, _ = b.WriteString(sql[last:rep.start])
		_, _ = b.WriteString(rep.text)
		last = rep.end
	}
	_, _ = b.WriteString(sql[last:])
	return b.String()
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
			spans = appendSpan(spans, span{start: l.start, end: l.end})
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

// appendSpan adds sp to spans, joined to the last one when the two touch:
// two literals written with nothing between them (an empty literal then a
// dollar-quoted one) are
// replaced by one RedactedLiteral, since two would read back as one literal
// holding a doubled quote.
func appendSpan(spans []span, sp span) []span {
	if n := len(spans); n > 0 && spans[n-1].end == sp.start {
		spans[n-1].end = sp.end
		return spans
	}
	return append(spans, sp)
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
