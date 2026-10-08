package scriptprintf

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"go.starlark.net/starlark"
)

// formatTypes are the presentation types a format spec accepts.
const formatTypes = "s d n b o x X c e E f F g G %"

// field is one replacement field of a str.format template, or the literal
// text after the last one.
type field struct {
	literal string
	name    string // "" for the next automatic position
	conv    byte   // 's', 'r' or 0
	spec    spec
	end     bool
}

// Refusals of a template's braces.
var (
	errSingleBrace    = errors.New("single '}' in format")
	errUnmatchedBrace = errors.New("unmatched '{' in format")
)

// parseFormat splits a str.format template into its fields.
func parseFormat(format string) ([]field, error) {
	var (
		out     []field
		literal strings.Builder
	)
	for i := 0; i < len(format); i++ {
		c := format[i]
		if c != '{' && c != '}' {
			_ = literal.WriteByte(c)
			continue
		}
		if i+1 < len(format) && format[i+1] == c {
			_ = literal.WriteByte(c)
			i++
			continue
		}
		if c == '}' {
			return nil, errSingleBrace
		}
		end := strings.IndexByte(format[i:], '}')
		if end < 0 {
			return nil, errUnmatchedBrace
		}
		f, err := parseField(format[i+1 : i+end])
		if err != nil {
			return nil, err
		}
		f.literal = literal.String()
		literal.Reset()
		out = append(out, f)
		i += end
	}
	return append(out, field{literal: literal.String(), end: true}), nil
}

// parseField reads the inside of one {...}: name, !conversion and :spec.
func parseField(s string) (field, error) {
	f := field{spec: newSpec()}
	if strings.Contains(s, "{") {
		return f, fmt.Errorf("nested replacement fields are not supported: {%s}", s)
	}
	head, specText, hasSpec := strings.Cut(s, ":")
	name, conv, hasConv := strings.Cut(head, "!")
	f.name = name
	if strings.ContainsAny(name, ".[") {
		return f, fmt.Errorf("attribute and element syntax are not supported in replacement fields: {%s}", s)
	}
	if hasConv {
		if conv != "s" && conv != "r" {
			return f, fmt.Errorf("unknown conversion !%s; use !s or !r", conv)
		}
		f.conv = conv[0]
	}
	if hasSpec {
		sp, err := parseSpec(specText)
		if err != nil {
			return f, err
		}
		f.spec = sp
	}
	return f, nil
}

// parseSpec reads Python's format-spec mini-language:
// [[fill]align][sign][#][0][width][,|_][.precision][type].
func parseSpec(text string) (spec, error) {
	sp := newSpec()
	s := sp.readAlign(text)
	s = sp.readFlags(s)
	s, _, sp.width = number(s)
	if s != "" && (s[0] == ',' || s[0] == '_') {
		sp.group, s = s[0], s[1:]
	}
	s, err := sp.readPrecision(s, text)
	if err != nil {
		return sp, err
	}
	if len(s) > 1 || s == " " || (s != "" && !strings.Contains(formatTypes, s)) {
		return sp, fmt.Errorf("invalid format spec %q; a spec is [[fill]align][sign][#][0][width][,][.precision][type] with type one of %s", text, formatTypes)
	}
	if s != "" {
		sp.verb = s[0]
	}
	return sp, nil
}

// alignments are the align characters a spec may start with.
const alignments = "<>^="

// readAlign reads [[fill]align] and returns the rest.
func (sp *spec) readAlign(s string) string {
	if r, n := utf8.DecodeRuneInString(s); n > 0 && len(s) > n && strings.IndexByte(alignments, s[n]) >= 0 {
		sp.fill, sp.align = r, s[n]
		return s[n+1:]
	}
	if s != "" && strings.IndexByte(alignments, s[0]) >= 0 {
		sp.align = s[0]
		return s[1:]
	}
	return s
}

// readFlags reads [sign][#][0] and returns the rest.
func (sp *spec) readFlags(s string) string {
	if s != "" && strings.IndexByte("+- ", s[0]) >= 0 {
		sp.sign, s = s[0], s[1:]
	}
	if rest, ok := strings.CutPrefix(s, "#"); ok {
		sp.alt, s = true, rest
	}
	if rest, ok := strings.CutPrefix(s, "0"); ok {
		sp.zero, s = true, rest
	}
	return s
}

// readPrecision reads [.precision] and returns the rest.
func (sp *spec) readPrecision(s, text string) (string, error) {
	rest, ok := strings.CutPrefix(s, ".")
	if !ok {
		return s, nil
	}
	rest, star, prec := number(rest)
	if star || prec < 0 {
		return "", fmt.Errorf("format spec %q has a precision with no digits after the point", text)
	}
	sp.prec = prec
	return rest, nil
}

// CheckFormat reports why template would be refused by str.format, or nil.
// It reads the template alone: whether the arguments fit is the run's to
// find out.
func CheckFormat(template string) error {
	_, err := parseFormat(template)
	return err
}

// formatArgs is what a template's fields draw from.
type formatArgs struct {
	args   starlark.Tuple
	kwargs []starlark.Tuple
	auto   int
	manual bool
}

// lookup is the value a field names.
func (a *formatArgs) lookup(name string) (starlark.Value, error) {
	if name == "" {
		if a.manual {
			return nil, errors.New("cannot switch from manual field specification to automatic field numbering")
		}
		a.auto++
		return a.index(a.auto - 1)
	}
	if n, err := strconv.Atoi(name); err == nil {
		if a.auto > 0 {
			return nil, errors.New("cannot switch from automatic field numbering to manual field specification")
		}
		a.manual = true
		return a.index(n)
	}
	for _, kv := range a.kwargs {
		if key, _ := kv[0].(starlark.String); string(key) == name {
			return kv[1], nil
		}
	}
	return nil, fmt.Errorf("keyword %s not found", name)
}

// index is the n'th positional argument.
func (a *formatArgs) index(n int) (starlark.Value, error) {
	if n < 0 || n >= len(a.args) {
		return nil, errors.New("tuple index out of range")
	}
	return a.args[n], nil
}

// Format is s.format(*args, **kwargs) for a string s.
func Format(template string, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	fields, err := parseFormat(template)
	if err != nil {
		return nil, err
	}
	from := &formatArgs{args: args, kwargs: kwargs}
	var b strings.Builder
	for _, f := range fields {
		_, _ = b.WriteString(f.literal)
		if f.end {
			break
		}
		v, err := from.lookup(f.name)
		if err != nil {
			return nil, err
		}
		if f.conv != 0 {
			v = starlark.String(textOf(v, f.conv == 'r'))
		}
		s, err := formatValue(v, f.spec)
		if err != nil {
			return nil, err
		}
		_, _ = b.WriteString(s)
	}
	return starlark.String(b.String()), nil
}

// formatValue renders v under a format spec.
func formatValue(v starlark.Value, sp spec) (string, error) {
	if sp == newSpec() {
		return textOf(v, false), nil
	}
	numeric := isNumber(v)
	sp = zeroFilled(sp, numeric)
	switch {
	case sp.verb == 'c':
		c, err := charOf(v)
		return pad(parts{body: c}, sp, false), err
	case strings.IndexByte(intVerbs, sp.verb) >= 0:
		return formatInt(v, sp)
	case strings.IndexByte(floatVerbs, sp.verb) >= 0:
		return formatFloat(v, sp)
	case sp.verb == 0 && numeric:
		return formatNumberDefault(v, sp)
	default:
		return pad(parts{body: truncated(textOf(v, false), sp.prec)}, sp, false), nil
	}
}

// zeroFilled applies the 0 flag of a spec with no explicit alignment: fill
// with zeros, after the sign for a number.
func zeroFilled(sp spec, numeric bool) spec {
	if sp.zero && sp.align == 0 {
		sp.fill = '0'
		if numeric {
			sp.align = '='
		}
	}
	return sp
}

// The presentation types by what they format.
const (
	intVerbs   = "dnboxX"
	floatVerbs = "eEfFgG%"
)

// formatFloat renders a number under a float presentation type.
func formatFloat(v starlark.Value, sp spec) (string, error) {
	f, ok := starlark.AsFloat(v)
	if !ok {
		return "", fmt.Errorf("format code %q needs a number, not %s", sp.verb, v.Type())
	}
	return pad(floatParts(f, sp), sp, true), nil
}

// formatInt renders an int under an integer presentation type.
func formatInt(v starlark.Value, sp spec) (string, error) {
	i, ok := v.(starlark.Int)
	if !ok {
		return "", fmt.Errorf("format code %q needs an int, not %s", sp.verb, v.Type())
	}
	if sp.prec >= 0 {
		return "", errors.New("precision is not allowed in an integer format spec")
	}
	return pad(intParts(i.BigInt(), sp, 0), sp, true), nil
}

// formatNumberDefault renders a number with no presentation type: an int as
// 'd', a float as Python's repr writes it, or as 'g' keeping a point when a
// precision is given. A field with no spec at all is not rendered here: it is
// str(v), as Starlark's own format writes it.
func formatNumberDefault(v starlark.Value, sp spec) (string, error) {
	if i, ok := v.(starlark.Int); ok {
		sp.verb = 'd'
		return formatInt(i, sp)
	}
	fv, _ := v.(starlark.Float)
	f := float64(fv)
	if sp.prec >= 0 {
		return pad(floatParts(f, sp), sp, true), nil
	}
	p := parts{sign: signOf(math.Signbit(f) && !math.IsNaN(f), sp.sign), body: reprDigits(math.Abs(f))}
	return pad(groupFloat(p, sp.group), sp, true), nil
}

// isNumber reports whether v is an int or a float.
func isNumber(v starlark.Value) bool {
	switch v.(type) {
	case starlark.Int, starlark.Float:
		return true
	default:
		return false
	}
}
