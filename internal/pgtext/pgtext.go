// Package pgtext reads and writes the text forms PostgreSQL gives the values
// of its structured types (#2455): a range, a multirange, a composite row, an
// hstore and pgvector's vector. The application-database kind returns each as
// JSON a script reads, and accepts the same JSON as a bound parameter, which
// it writes back in these forms for the server to parse by the parameter's
// own type.
//
// It knows only the syntax. What a range's bounds or a row's fields mean is
// their element types', which the caller maps.
package pgtext

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Range is one range value. A nil bound is unbounded (infinite); Empty is the
// range with no points, whose bounds are not meaningful.
type Range struct {
	Empty    bool
	Lower    *string
	Upper    *string
	LowerInc bool
	UpperInc bool
}

// errSyntax is the refusal of a value that is not in the form its type writes.
var errSyntax = errors.New("pgtext: malformed value")

// ParseRange reads a range as PostgreSQL writes it: empty, or a bracket or
// parenthesis, the lower bound, a comma, the upper bound and a closing
// bracket or parenthesis, a missing bound being unbounded.
func ParseRange(s string) (Range, error) {
	r, rest, err := readRange(s)
	if err != nil {
		return Range{}, err
	}
	if rest != "" {
		return Range{}, fmt.Errorf("unexpected %q after the range: %w", rest, errSyntax)
	}
	return r, nil
}

func readRange(s string) (Range, string, error) {
	s = strings.TrimLeft(s, space)
	if strings.HasPrefix(strings.ToLower(s), "empty") {
		return Range{Empty: true}, s[len("empty"):], nil
	}
	if s == "" || (s[0] != '[' && s[0] != '(') {
		return Range{}, "", fmt.Errorf("a range opens with [ or (: %w", errSyntax)
	}
	r := Range{LowerInc: s[0] == '['}
	lower, rest, err := readItem(s[1:], comma)
	if err != nil {
		return Range{}, "", err
	}
	if !strings.HasPrefix(rest, comma) {
		return Range{}, "", fmt.Errorf("a range's bounds are separated by a comma: %w", errSyntax)
	}
	upper, rest, err := readUpper(rest[1:])
	if err != nil {
		return Range{}, "", err
	}
	r.Lower, r.Upper, r.UpperInc = lower, upper, rest[0] == ']'
	return r, rest[1:], nil
}

// readUpper reads a range's upper bound and checks the bracket that closes
// it, which rest then starts with.
func readUpper(s string) (upper *string, rest string, err error) {
	upper, rest, err = readItem(s, ")]")
	if err != nil {
		return nil, "", err
	}
	if rest == "" || (rest[0] != ')' && rest[0] != ']') {
		return nil, "", fmt.Errorf("a range closes with ] or ): %w", errSyntax)
	}
	return upper, rest, nil
}

// comma separates a range's bounds, a row's fields and an hstore's pairs, and
// space is what the forms allow around them.
const (
	comma = ","
	space = " "
)

// readItem reads one bound or field up to a character of stop: a quoted
// string (backslash escapes, a doubled quote is one quote), an unquoted run
// (backslash escapes), or nothing, which is nil.
func readItem(s, stop string) (value *string, rest string, err error) {
	r := itemReader{s: s}
	for ; r.i < len(s); r.i++ {
		if !r.quoted && strings.IndexByte(stop, s[r.i]) >= 0 {
			return item(r.b.String(), r.seen), s[r.i:], nil
		}
		if err := r.step(); err != nil {
			return nil, "", err
		}
	}
	if r.quoted {
		return nil, "", fmt.Errorf("an unclosed quote: %w", errSyntax)
	}
	return item(r.b.String(), r.seen), "", nil
}

// itemReader is readItem's place in the text and what it has read.
type itemReader struct {
	s            string
	i            int
	b            strings.Builder
	quoted, seen bool
}

// step reads the byte at i: an escape, a quote, or a byte of the item.
func (r *itemReader) step() error {
	c := r.s[r.i]
	switch {
	case c == '\\':
		if r.i+1 == len(r.s) {
			return fmt.Errorf("a backslash ends the value: %w", errSyntax)
		}
		r.i++
		_ = r.b.WriteByte(r.s[r.i])
	case c == '"' && r.quoted && r.i+1 < len(r.s) && r.s[r.i+1] == '"':
		_ = r.b.WriteByte('"')
		r.i++
	case c == '"':
		r.quoted = !r.quoted
	default:
		_ = r.b.WriteByte(c)
	}
	r.seen = true
	return nil
}

func item(s string, seen bool) *string {
	if !seen {
		return nil
	}
	return &s
}

// ParseMultirange reads a multirange: braces around ranges separated by
// commas.
func ParseMultirange(s string) ([]Range, error) {
	s = strings.TrimSpace(s)
	if len(s) < 2 || s[0] != '{' || s[len(s)-1] != '}' {
		return nil, fmt.Errorf("a multirange is enclosed in braces: %w", errSyntax)
	}
	body := strings.TrimSpace(s[1 : len(s)-1])
	out := []Range{}
	for body != "" {
		r, rest, err := readRange(body)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
		body = strings.TrimLeft(rest, space)
		if body == "" {
			break
		}
		if !strings.HasPrefix(body, comma) {
			return nil, fmt.Errorf("a multirange's ranges are separated by commas: %w", errSyntax)
		}
		body = body[1:]
	}
	return out, nil
}

// ParseComposite reads a row: parentheses around fields separated by commas,
// an empty field being null.
func ParseComposite(s string) ([]*string, error) {
	if len(s) < 2 || s[0] != '(' || s[len(s)-1] != ')' {
		return nil, fmt.Errorf("a row is enclosed in parentheses: %w", errSyntax)
	}
	body := s[1 : len(s)-1]
	var out []*string
	for {
		f, rest, err := readItem(body, comma)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
		if rest == "" {
			return out, nil
		}
		body = rest[1:]
	}
}

// ParseHstore reads an hstore: "key"=>"value" pairs separated by commas, a
// value written NULL being null.
func ParseHstore(s string) (map[string]*string, error) {
	out := map[string]*string{}
	rest := strings.TrimSpace(s)
	for rest != "" {
		k, after, err := readItem(rest, "=")
		if err != nil {
			return nil, err
		}
		if k == nil || !strings.HasPrefix(after, "=>") {
			return nil, fmt.Errorf("an hstore pair is key=>value: %w", errSyntax)
		}
		after = strings.TrimLeft(after[2:], space)
		unquoted := !strings.HasPrefix(after, `"`)
		v, tail, err := readItem(after, comma)
		if err != nil {
			return nil, err
		}
		if v != nil && unquoted && strings.EqualFold(strings.TrimSpace(*v), "null") {
			v = nil
		}
		out[strings.TrimSpace(*k)] = v
		rest = strings.TrimLeft(strings.TrimPrefix(tail, comma), space)
	}
	return out, nil
}

// ParseVector reads a pgvector vector: brackets around numbers separated by
// commas.
func ParseVector(s string) ([]float64, error) {
	s = strings.TrimSpace(s)
	if len(s) < 2 || s[0] != '[' || s[len(s)-1] != ']' {
		return nil, fmt.Errorf("a vector is enclosed in brackets: %w", errSyntax)
	}
	out := []float64{}
	body := strings.TrimSpace(s[1 : len(s)-1])
	if body == "" {
		return out, nil
	}
	for part := range strings.SplitSeq(body, comma) {
		f, err := strconv.ParseFloat(strings.TrimSpace(part), float64Bits)
		if err != nil {
			return nil, fmt.Errorf("value %q is not a number: %w", part, errSyntax)
		}
		out = append(out, f)
	}
	return out, nil
}

// float64Bits is the precision a vector's numbers are read at.
const float64Bits = 64

// Quote writes s as a double-quoted item, every quote and backslash in it
// escaped, which every one of these forms reads as s.
func Quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// FormatRange writes r in the form ParseRange reads.
func FormatRange(r Range) string {
	if r.Empty {
		return "empty"
	}
	var b strings.Builder
	_ = b.WriteByte("(["[boolIndex(r.LowerInc)])
	if r.Lower != nil {
		_, _ = b.WriteString(Quote(*r.Lower))
	}
	_, _ = b.WriteString(comma)
	if r.Upper != nil {
		_, _ = b.WriteString(Quote(*r.Upper))
	}
	_ = b.WriteByte(")]"[boolIndex(r.UpperInc)])
	return b.String()
}

func boolIndex(b bool) int {
	if b {
		return 1
	}
	return 0
}

// FormatMultirange writes ranges in the form ParseMultirange reads.
func FormatMultirange(ranges []Range) string {
	parts := make([]string, len(ranges))
	for i, r := range ranges {
		parts[i] = FormatRange(r)
	}
	return "{" + strings.Join(parts, comma) + "}"
}

// FormatComposite writes fields in the form ParseComposite reads, a nil field
// as null.
func FormatComposite(fields []*string) string {
	parts := make([]string, len(fields))
	for i, f := range fields {
		if f != nil {
			parts[i] = Quote(*f)
		}
	}
	return "(" + strings.Join(parts, comma) + ")"
}

// FormatHstore writes pairs in the form ParseHstore reads, a nil value as
// NULL. keys gives the order.
func FormatHstore(keys []string, pairs map[string]*string) string {
	parts := make([]string, len(keys))
	for i, k := range keys {
		v := "NULL"
		if p := pairs[k]; p != nil {
			v = Quote(*p)
		}
		parts[i] = Quote(k) + "=>" + v
	}
	return strings.Join(parts, ", ")
}
