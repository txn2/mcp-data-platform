package scriptlex

import "strings"

// CodeOnly returns source with the contents of every string literal and every
// comment replaced by spaces, keeping the quotes, the newlines and every byte
// offset. The author-facing lexical checks read it instead of the raw text, so
// a string whose content is `f`, or a SQL column named datetime, is not taken
// for code (#1853). An unterminated literal is masked to the end of its line
// (or of the source, for a triple-quoted one); the parser reports it.
func CodeOnly(source string) string {
	out := []byte(source)
	for i := 0; i < len(source); {
		switch source[i] {
		case '#':
			i = blankUntil(out, i+1, strings.IndexByte(source[i:], '\n'))
		case '"', '\'':
			i = maskString(source, out, i)
		default:
			i++
		}
	}
	return string(out)
}

// tripleQuote is the length of the quote that opens a multi-line string.
const tripleQuote = 3

// maskString blanks the literal whose opening quote is at start and returns
// the offset just past its closing quote. A backslash always takes the next
// byte with it, in a raw string too, which is how Starlark's scanner finds the
// end of one.
func maskString(source string, out []byte, start int) int {
	quote := source[start : start+1]
	if strings.HasPrefix(source[start:], strings.Repeat(quote, tripleQuote)) {
		quote = strings.Repeat(quote, tripleQuote)
	}
	triple := len(quote) == tripleQuote
	i := start + len(quote)
	for i < len(source) {
		switch {
		case strings.HasPrefix(source[i:], quote):
			return i + len(quote)
		case source[i] == '\\' && i+1 < len(source):
			blank(out, i)
			if source[i+1] != '\n' {
				blank(out, i+1)
			}
			i += 2
		case source[i] == '\n' && !triple:
			return i
		default:
			blank(out, i)
			i++
		}
	}
	return i
}

// blankUntil blanks out[from:] up to rel bytes past from-1 (the newline a
// comment ends at, which is kept), or to the end when rel is negative.
func blankUntil(out []byte, from, rel int) int {
	end := len(out)
	if rel >= 0 {
		end = from - 1 + rel
	}
	for i := from; i < end; i++ {
		blank(out, i)
	}
	return end
}

// blank replaces one byte with a space unless it is a newline, which a line
// number is counted from.
func blank(out []byte, i int) {
	if out[i] != '\n' {
		out[i] = ' '
	}
}
