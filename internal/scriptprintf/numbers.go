package scriptprintf

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode"

	"go.starlark.net/starlark"
)

// intBase is how an integer verb writes its digits: the base, whether they
// are upper case, and the prefix '#' adds.
type intBase struct {
	base   int
	upper  bool
	prefix string
}

// intBases maps each integer verb to how it writes.
var intBases = map[byte]intBase{
	'd': {base: 10}, 'i': {base: 10}, 'u': {base: 10}, 'n': {base: 10},
	'x': {base: 16, prefix: "0x"}, 'X': {base: 16, upper: true, prefix: "0X"},
	'o': {base: 8, prefix: "0o"}, 'b': {base: 2, prefix: "0b"},
}

// Digit grouping: thousands in decimal, four digits in every other base, as
// Python's "_" separator groups hex, octal and binary.
const (
	decimalBase  = 10
	decimalGroup = 3
	binaryGroup  = 4
)

// Float conversion settings: Python's default precision, the bit size every
// float is, and the magnitudes outside which repr writes an exponent.
const (
	defaultPrecision = 6
	floatBits        = 64
	reprExpAbove     = 1e16
	reprExpBelow     = 1e-4
)

// intParts renders an integer under sp. minDigits is printf's precision
// (the least number of digits), which a format spec does not have.
func intParts(x *big.Int, sp spec, minDigits int) parts {
	b := intBases[sp.verb]
	digits := new(big.Int).Abs(x).Text(b.base)
	if b.upper {
		digits = strings.ToUpper(digits)
	}
	if len(digits) < minDigits {
		digits = strings.Repeat("0", minDigits-len(digits)) + digits
	}
	every := decimalGroup
	if b.base != decimalBase {
		every = binaryGroup
	}
	p := parts{sign: signOf(x.Sign() < 0, sp.sign), body: grouped(digits, sp.group, every)}
	if sp.alt {
		p.prefix = b.prefix
	}
	return p
}

// charOf is the character %c and {:c} write for v: a code point, or a
// one-character string.
func charOf(v starlark.Value) (string, error) {
	switch v := v.(type) {
	case starlark.Int:
		r, err := starlark.AsInt32(v)
		if err != nil || r < 0 || r > unicode.MaxRune {
			return "", fmt.Errorf("conversion c requires a valid Unicode code point, got %s", v)
		}
		return string(rune(r)), nil
	case starlark.String:
		if len([]rune(string(v))) != 1 {
			return "", fmt.Errorf("conversion c requires a single-character string, got %s", v)
		}
		return string(v), nil
	default:
		return "", fmt.Errorf("conversion c requires an int or a single-character string, not %s", v.Type())
	}
}

// floatParts renders f under sp, whose verb is one of e E f F g G %.
// A spec with no verb (str.format's default for a float with a precision) is
// 'g' that keeps one digit past the point.
func floatParts(f float64, sp spec) parts {
	p := parts{sign: signOf(math.Signbit(f) && !math.IsNaN(f), sp.sign)}
	abs := math.Abs(f)
	upper := sp.verb == 'E' || sp.verb == 'F' || sp.verb == 'G'
	if math.IsInf(f, 0) || math.IsNaN(f) {
		p.body = "inf"
		if math.IsNaN(f) {
			p.body = "nan"
		}
		if upper {
			p.body = strings.ToUpper(p.body)
		}
		return p
	}
	prec := sp.prec
	if prec < 0 {
		prec = defaultPrecision
	}
	p.body = floatDigits(abs, sp, prec)
	if upper {
		p.body = strings.ToUpper(p.body)
	}
	return groupFloat(p, sp.group)
}

// floatDigits writes abs under sp's verb at prec.
func floatDigits(abs float64, sp spec, prec int) string {
	switch sp.verb {
	case 'f', 'F':
		return alternate(strconv.FormatFloat(abs, 'f', prec, floatBits), sp.alt && prec == 0)
	case 'e', 'E':
		return fmt.Sprintf(flagged("e", sp.alt), prec, abs)
	case '%':
		return alternate(strconv.FormatFloat(abs*100, 'f', prec, floatBits), sp.alt && prec == 0) + "%"
	case 0:
		s := fmt.Sprintf(flagged("g", sp.alt), prec, abs)
		if !strings.ContainsAny(s, ".e") {
			s += ".0"
		}
		return s
	default: // g, G
		return fmt.Sprintf(flagged("g", sp.alt), prec, abs)
	}
}

// flagged is the fmt verb for a float conversion at a given precision, with
// '#' when the alternate form keeps the point and trailing zeros.
func flagged(verb string, alt bool) string {
	if alt {
		return "%#.*" + verb
	}
	return "%.*" + verb
}

// alternate keeps the decimal point '#' asks for on a fixed-point number
// written with no fraction.
func alternate(s string, keepPoint bool) string {
	if keepPoint {
		return s + "."
	}
	return s
}

// groupFloat groups the integer digits of a rendered float.
func groupFloat(p parts, sep byte) parts {
	if sep == 0 {
		return p
	}
	end := strings.IndexAny(p.body, ".e%")
	if end < 0 {
		end = len(p.body)
	}
	p.body = grouped(p.body[:end], sep, decimalGroup) + p.body[end:]
	return p
}

// reprDigits is a non-negative float as Python's repr writes it: the
// shortest digits that read back as the same float, fixed-point between
// 1e-4 and 1e16 with at least one digit after the point, and exponent form
// outside that range.
func reprDigits(abs float64) string {
	switch {
	case math.IsInf(abs, 0):
		return "inf"
	case math.IsNaN(abs):
		return "nan"
	case abs != 0 && (abs >= reprExpAbove || abs < reprExpBelow):
		return strconv.FormatFloat(abs, 'e', -1, floatBits)
	}
	s := strconv.FormatFloat(abs, 'f', -1, floatBits)
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s
}
