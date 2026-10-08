package scriptprintf

import (
	"errors"
	"fmt"
	"strings"

	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

// percentVerbs are the conversions `%` accepts, as the refusal of any other
// names them.
const percentVerbs = "s r d i u o x X e E f F g G c %"

// directive is one `%` conversion of a format string.
type directive struct {
	literal  string // text before the directive
	key      string // %(key)s; "" for a positional argument
	keyed    bool
	spec     spec
	starWide bool // width is *
	starPrec bool // precision is *
	end      bool // no directive: literal is the format's tail
}

// parsePercent splits format into its directives.
func parsePercent(format string) ([]directive, error) {
	var out []directive
	for {
		i := strings.IndexByte(format, '%')
		if i < 0 {
			return append(out, directive{literal: format, end: true}), nil
		}
		d, rest, err := parseDirective(format[i+1:])
		if err != nil {
			return nil, err
		}
		d.literal = format[:i]
		out = append(out, d)
		format = rest
	}
}

// Refusals of a format string, and of its arguments.
var (
	errIncompleteKey = errors.New("incomplete format key")
	errIncomplete    = errors.New("incomplete format")
	errTooFewArgs    = errors.New("not enough arguments for format string")
	errNoMapping     = errors.New("format requires a mapping")
	errTooManyArgs   = errors.New("not all arguments converted during string formatting")
)

// parseDirective reads one conversion after its '%'.
func parseDirective(s string) (directive, string, error) {
	d := directive{spec: newSpec()}
	if strings.HasPrefix(s, "(") {
		j := strings.IndexByte(s, ')')
		if j < 0 {
			return d, "", errIncompleteKey
		}
		d.key, d.keyed, s = s[1:j], true, s[j+1:]
	}
	s = d.flags(s)
	s, d.starWide, d.spec.width = number(s)
	if strings.HasPrefix(s, ".") {
		s, d.starPrec, d.spec.prec = number(s[1:])
		if d.spec.prec < 0 && !d.starPrec {
			d.spec.prec = 0
		}
	}
	s = strings.TrimLeft(s, "hlL")
	if s == "" {
		return d, "", errIncomplete
	}
	d.spec.verb = s[0]
	if !strings.Contains(percentVerbs, string(s[0])) || s[0] == ' ' {
		return d, "", fmt.Errorf("unsupported format character %q; %% takes the conversions %s with the flags - 0 + space #, a width and a .precision", s[0], percentVerbs)
	}
	return d, s[1:], nil
}

// flags reads the conversion flags.
func (d *directive) flags(s string) string {
	for s != "" {
		switch s[0] {
		case '-':
			d.spec.align = '<'
		case '0':
			d.spec.zero = true
		case '+':
			d.spec.sign = '+'
		case ' ':
			if d.spec.sign == 0 {
				d.spec.sign = ' '
			}
		case '#':
			d.spec.alt = true
		default:
			return s
		}
		s = s[1:]
	}
	return s
}

// number reads a width or precision: digits, or '*' for one taken from the
// arguments. -1 when there is neither.
func number(s string) (rest string, star bool, n int) {
	if strings.HasPrefix(s, "*") {
		return s[1:], true, -1
	}
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 {
		return s, false, -1
	}
	_, _ = fmt.Sscan(s[:i], &n)
	return s[i:], false, n
}

// CheckPercent reports why format would be refused as the left operand of
// `%`, or nil. It reads the format alone: whether the arguments fit is the
// run's to find out.
func CheckPercent(format string) error {
	_, err := parsePercent(format)
	return err
}

// percentArgs is the right operand of `%`, read in order.
type percentArgs struct {
	mapping starlark.Mapping
	values  starlark.Tuple
	next    int
}

// take is the next positional argument.
func (a *percentArgs) take() (starlark.Value, error) {
	if a.next >= len(a.values) {
		return nil, errTooFewArgs
	}
	v := a.values[a.next]
	a.next++
	return v, nil
}

// Percent is `x % y`: Python's printf-style formatting when x is a string,
// and Starlark's own operator otherwise.
func Percent(x, y starlark.Value) (starlark.Value, error) {
	format, ok := x.(starlark.String)
	if !ok {
		return starlark.Binary(syntax.PERCENT, x, y) //nolint:wrapcheck // Starlark's own message for its own operator
	}
	ds, err := parsePercent(string(format))
	if err != nil {
		return nil, err
	}
	args := &percentArgs{values: starlark.Tuple{y}}
	if t, ok := y.(starlark.Tuple); ok {
		args.values = t
	}
	if m, ok := y.(starlark.Mapping); ok {
		args.mapping = m
	}
	var b strings.Builder
	for _, d := range ds {
		_, _ = b.WriteString(d.literal)
		if d.end {
			break
		}
		if err := d.write(&b, args); err != nil {
			return nil, err
		}
	}
	if args.next < len(args.values) && args.mapping == nil {
		return nil, errTooManyArgs
	}
	return starlark.String(b.String()), nil
}

// write renders one directive into b, taking what it needs from args.
func (d directive) write(b *strings.Builder, args *percentArgs) error {
	if d.spec.verb == '%' {
		_ = b.WriteByte('%')
		return nil
	}
	sp := d.spec
	var err error
	if sp.width, err = d.star(d.starWide, sp.width, args); err != nil {
		return err
	}
	if sp.prec, err = d.star(d.starPrec, sp.prec, args); err != nil {
		return err
	}
	if d.starWide && sp.width < 0 { // a negative * width left-justifies, as in C
		sp.width, sp.align = -sp.width, '<'
	}
	v, err := d.value(args)
	if err != nil {
		return err
	}
	s, err := percentValue(v, sp)
	if err != nil {
		return err
	}
	_, _ = b.WriteString(s)
	return nil
}

// star is a width or precision, taken from the arguments when it was '*'.
func (directive) star(star bool, n int, args *percentArgs) (int, error) {
	if !star {
		return n, nil
	}
	v, err := args.take()
	if err != nil {
		return 0, err
	}
	var out int
	if err := starlark.AsInt(v, &out); err != nil {
		return 0, fmt.Errorf("a * width or precision must be an int, not %s", v.Type())
	}
	return out, nil
}

// value is the argument a directive formats.
func (d directive) value(args *percentArgs) (starlark.Value, error) {
	if !d.keyed {
		return args.take()
	}
	if args.mapping == nil {
		return nil, errNoMapping
	}
	v, found, err := args.mapping.Get(starlark.String(d.key))
	if err != nil {
		return nil, err //nolint:wrapcheck // the mapping's own lookup error
	}
	if !found {
		return nil, fmt.Errorf("key not found: %s", d.key)
	}
	return v, nil
}

// percentValue renders v under a printf conversion.
func percentValue(v starlark.Value, sp spec) (string, error) {
	zeroPad := func(numeric bool) spec {
		if sp.zero && numeric && sp.align != '<' {
			sp.fill, sp.align = '0', '='
		}
		return sp
	}
	switch sp.verb {
	case 's', 'r':
		return pad(parts{body: truncated(textOf(v, sp.verb == 'r'), sp.prec)}, sp, true), nil
	case 'c':
		c, err := charOf(v)
		return pad(parts{body: c}, sp, true), err
	case 'e', 'E', 'f', 'F', 'g', 'G':
		f, ok := starlark.AsFloat(v)
		if !ok {
			return "", fmt.Errorf("conversion %c requires a float, not %s", sp.verb, v.Type())
		}
		p := floatParts(f, sp)
		return pad(p, zeroPad(!strings.ContainsAny(p.body, "ni")), true), nil
	default: // d i u o x X
		i, err := starlark.NumberToInt(v)
		if err != nil {
			return "", fmt.Errorf("conversion %c requires an integer: %w", sp.verb, err)
		}
		return pad(intParts(i.BigInt(), sp, sp.prec), zeroPad(true), true), nil
	}
}

// textOf is str(v), or repr(v) when repr is asked for.
func textOf(v starlark.Value, repr bool) string {
	if s, ok := v.(starlark.String); ok && !repr {
		return string(s)
	}
	return v.String()
}
