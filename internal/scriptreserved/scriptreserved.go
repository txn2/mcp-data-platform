// Package scriptreserved answers one question about a Starlark parse error:
// is it a reserved word used as a name, and if so which word (#1823)?
//
// The parser's message at "def load(...)" is "not an identifier", reported at
// the token after the word, which is true and names neither the word nor the
// mistake. This package finds the word from the error and the source line, so
// the validator can say which word it was and what to do about it.
package scriptreserved

import (
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"go.starlark.net/syntax"
)

// words are the words the Starlark scanner reads as keywords, so none of them
// can name a function, a parameter, a variable or an attribute.
//
// The set is read off the library's own token range rather than written out
// here, so it is the scanner's set whatever version is vendored: the keywords
// run from AND to WHILE and the reserved Python words from AS to YIELD. A token
// in that range whose text is not a single word ("not in") is not a name
// anybody could write, and is left out.
var words = func() []string {
	var out []string
	for t := syntax.AND; t <= syntax.YIELD; t++ {
		if w := t.String(); isIdentifier(w) {
			out = append(out, w)
		}
	}
	slices.Sort(out)
	return out
}()

// usedAsValue matches the parser's message for a keyword where an expression
// belongs: "x = load", "for load in rows".
var usedAsValue = regexp.MustCompile(`^got (\w+), want primary expression$`)

// Words returns the reserved words in sorted order.
func Words() []string {
	return slices.Clone(words)
}

// Misused reports the reserved word a parse error is about, or false when the
// error is about something else.
//
// There are three shapes: the word where a name is declared (a def, a
// parameter, an attribute), where the parser says "not an identifier" at the
// token after it; the word where a value is read, which the parser names; and
// "load" at the start of an assignment, which the parser takes for the load
// statement and answers with the parenthesis it wanted.
func Misused(source string, e syntax.Error) (string, bool) {
	line := sourceLine(source, int(e.Pos.Line))
	word := ""
	switch {
	case e.Msg == "not an identifier":
		word = wordBefore(line, int(e.Pos.Col))
	case usedAsValue.MatchString(e.Msg):
		word = usedAsValue.FindStringSubmatch(e.Msg)[1]
	case strings.HasSuffix(e.Msg, "want '('") && startsWithLoadName(line):
		word = "load"
	}
	return word, slices.Contains(words, word)
}

// sourceLine returns one 1-based line of the source, or "" when there is none.
func sourceLine(source string, n int) string {
	lines := strings.Split(source, "\n")
	if n < 1 || n > len(lines) {
		return ""
	}
	return lines[n-1]
}

// wordBefore returns the identifier that ends before a 1-based rune column,
// skipping the blanks between them.
func wordBefore(line string, col int) string {
	runes := []rune(line)
	end := min(max(col-1, 0), len(runes))
	for end > 0 && (runes[end-1] == ' ' || runes[end-1] == '\t') {
		end--
	}
	start := end
	for start > 0 && isIdentRune(runes[start-1]) {
		start--
	}
	return string(runes[start:end])
}

// startsWithLoadName reports whether a line begins with the word load used as
// something other than the load statement, which is always load( ... ).
func startsWithLoadName(line string) bool {
	rest, ok := strings.CutPrefix(strings.TrimLeft(line, " \t"), "load")
	if !ok || rest == "" {
		return false
	}
	next, _ := utf8.DecodeRuneInString(rest)
	return !isIdentRune(next) && !strings.HasPrefix(strings.TrimLeft(rest, " \t"), "(")
}

// isIdentifier reports whether s is a single Starlark identifier.
func isIdentifier(s string) bool {
	return s != "" && strings.IndexFunc(s, func(r rune) bool { return !isIdentRune(r) }) < 0
}

// isIdentRune reports whether r can appear in a Starlark identifier.
func isIdentRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}
