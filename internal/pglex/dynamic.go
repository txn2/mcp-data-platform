package pglex

import "strings"

// BuiltAtRunTime is the object PasswordLiteral reports for SQL that is
// built at run time and gives a password (#2750): an EXECUTE of anything but
// one literal, or a dblink call with an argument that is not a literal, in a
// unit that names a role, user or group and PASSWORD. Such SQL cannot be read
// statically, so it is refused rather than passed.
const BuiltAtRunTime = "SQL built at run time"

// Words PostgreSQL runs SQL from a string with.
const (
	wordExecute    = "execute"
	wordDblink     = "dblink"
	wordDblinkExec = "dblink_exec"
	loginOption    = "password"
	quote          = "'"
)

// StringLiterals returns the text each string literal in sql stands for once
// unquoted (a doubled quote read as one, and in an E'...' string an escaped
// quote too), in order: the SQL a body passes to EXECUTE or dblink_exec as a
// literal. A dollar-quoted body is not among them.
func StringLiterals(sql string) []string {
	var out []string
	for _, l := range lexemes(sql, 0, len(sql)) {
		if l.literal && sql[l.start] == '\'' {
			out = append(out, unquote(sql, l))
		}
	}
	return out
}

// unquote is the text the single-quoted literal l stands for. A doubled quote
// is one quote; in an E'...' string an escaped quote is one quote and every
// other backslash sequence is kept as written, so requote writes the literal
// back meaning what it did.
func unquote(sql string, l lexeme) string {
	content := sql[l.start+1 : max(l.start+1, l.end-1)]
	if l.end-l.start < 2 || sql[l.end-1] != '\'' {
		content = sql[l.start+1 : l.end] // a literal that never ends
	}
	escape := IsEString(sql, l.start)
	var b strings.Builder
	for i := 0; i < len(content); i++ {
		text, skip := unquoteAt(content, i, escape)
		_, _ = b.WriteString(text)
		i += skip
	}
	return b.String()
}

// unquoteAt is what the byte at i of a literal's content stands for, and how
// many bytes after it that reading took.
func unquoteAt(content string, i int, escape bool) (text string, skip int) {
	if i+1 >= len(content) {
		return content[i : i+1], 0
	}
	switch c, next := content[i], content[i+1]; {
	case escape && c == '\\' && next == '\'':
		return quote, 1
	case escape && c == '\\':
		return content[i : i+2], 1
	case c == '\'' && next == '\'':
		return quote, 1
	}
	return content[i : i+1], 0
}

// requote writes text back as a single-quoted literal's content: each quote
// doubled. A backslash sequence unquote kept is kept, so an E'...' literal
// reads as it did.
func requote(text string) string {
	return quote + strings.ReplaceAll(text, quote, quote+quote) + quote
}

// BuildsSQL reports whether unit runs SQL it builds at run time: an EXECUTE
// whose argument is anything but one literal (format(...), a concatenation, a
// variable), or a dblink or dblink_exec call with an argument that is not a
// literal. EXECUTE as a privilege (GRANT EXECUTE ON) or a trigger's EXECUTE
// FUNCTION is not one.
func BuildsSQL(unit string) bool {
	for _, r := range statementRanges(unit) {
		ls := withoutStringPrefixes(lexemes(unit, r[0], r[1]))
		for k, l := range ls {
			switch l.word {
			case wordExecute:
				if executesBuilt(ls, k) {
					return true
				}
			case wordDblink, wordDblinkExec:
				if callsWithBuilt(unit, ls, k) {
					return true
				}
			}
		}
	}
	return false
}

// withoutStringPrefixes drops the E of each E'...' literal, which the lexer
// reads as a word of its own, so the literal is one argument.
func withoutStringPrefixes(ls []lexeme) []lexeme {
	out := ls[:0:0]
	for i, l := range ls {
		if (l.word == "e") && i+1 < len(ls) && ls[i+1].literal && ls[i+1].start == l.end {
			continue
		}
		out = append(out, l)
	}
	return out
}

// executesBuilt reports whether the EXECUTE at k runs anything but one
// literal: what follows it up to USING, INTO or the end of the statement.
func executesBuilt(ls []lexeme, k int) bool {
	if k > 0 && (ls[k-1].word == "grant" || ls[k-1].word == "revoke") {
		return false
	}
	arg := executeArgument(ls, k)
	if len(arg) == 0 || notDynamic[arg[0].word] {
		return false
	}
	return len(arg) != 1 || !arg[0].literal
}

// notDynamic are the words after EXECUTE that make it a privilege or a
// trigger's action rather than a statement run from a string.
var notDynamic = map[string]bool{"on": true, "function": true, "procedure": true}

// executeArgument is what the EXECUTE at k runs: the lexemes after it up to
// USING, INTO or the end of the statement.
func executeArgument(ls []lexeme, k int) []lexeme {
	end := k + 1
	for end < len(ls) && ls[end].word != "using" && ls[end].word != "into" {
		end++
	}
	return ls[k+1 : end]
}

// callsWithBuilt reports whether the dblink call at k passes an argument that
// is not a literal.
func callsWithBuilt(sql string, ls []lexeme, k int) bool {
	if k+1 >= len(ls) || sql[ls[k+1].start] != '(' {
		return false
	}
	for _, l := range ls[k+2:] {
		switch {
		case sql[l.start] == ')' && !l.literal && l.word == "":
			return false
		case l.literal, l.word == "" && sql[l.start] == ',':
		default:
			return true
		}
	}
	return true
}

// Words is every word of unit in order, lower-cased, with the words of each
// literal it holds read in place: the SQL a unit builds at run time is in its
// literals, split across them and the code that joins them. A literal's words
// are read to maxBodyDepth.
func Words(unit string) []string {
	return wordsAt(unit, 0)
}

func wordsAt(unit string, depth int) []string {
	var out []string
	for _, l := range lexemes(unit, 0, len(unit)) {
		switch {
		case l.word != "":
			out = append(out, l.word)
		case l.literal && depth < maxBodyDepth:
			out = append(out, wordsAt(literalText(unit, l), depth+1)...)
		}
	}
	return out
}

// literalText is what the literal l holds: a single-quoted literal unquoted,
// a dollar-quoted body as written.
func literalText(sql string, l lexeme) string {
	if sql[l.start] == '\'' {
		return unquote(sql, l)
	}
	if bodies := dollarBodies(sql, l.start, l.end); len(bodies) > 0 {
		return sql[bodies[0].start:bodies[0].end]
	}
	return ""
}

// buildsCredential reports whether unit builds SQL at run time that gives a
// password: BuildsSQL, and among its words PASSWORD and a role, user or group
// a statement gives one to (CREATE ROLE, ALTER USER ...).
func buildsCredential(unit string) bool {
	if !containsFold(unit, loginOption) || !BuildsSQL(unit) {
		return false
	}
	words := Words(unit)
	named, object := false, false
	for i, w := range words {
		named = named || w == loginOption
		object = object || (i+1 < len(words) && passwordObjects[w][words[i+1]])
	}
	return named && object
}
