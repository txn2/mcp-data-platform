// Package scriptprintf is the string formatting a managed script's `%`
// operator and str.format method run (#2048).
//
// Starlark's own `%` takes bare verbs only and its str.format takes no format
// spec, so "%02X" % b, "%.2f" % rate and "{:>10}".format(name) fail at run
// time with "unknown conversion". Every Python-shaped language an author
// knows accepts them, and the failure surfaces on the first run that reaches
// the line, often a branch no draft took. This package implements the
// printf subset (flags - 0 + space #, width, precision, * for either) and
// Python's format-spec mini-language, and Rewrite routes a script's `%` and
// .format through them before the script is compiled. A left operand that is
// not a string, and a .format on anything but a string, go to Starlark's own.
//
// The package knows nothing about runs or the platform; CheckPercent and
// CheckFormat let the authoring gates refuse a literal format string that
// would still fail, at save rather than mid-run.
package scriptprintf

import (
	"strings"
	"unicode/utf8"
)

// spec is one parsed directive: a printf conversion or a format spec, in the
// shape both render through.
type spec struct {
	fill  rune
	align byte // '<', '>', '^', '=' or 0 for the value's default
	sign  byte // '+', ' ', '-' or 0
	alt   bool // '#'
	zero  bool // '0'
	width int  // -1 for none
	prec  int  // -1 for none
	group byte // ',', '_' or 0
	verb  byte // the conversion or presentation type; 0 for none
}

// newSpec is a spec with nothing set.
func newSpec() spec { return spec{fill: ' ', width: -1, prec: -1} }

// parts is a rendered value before padding: the sign, the base prefix
// (0x, 0o, 0b), and the rest. Zero padding goes between the prefix and the
// rest, which is why they are kept apart.
type parts struct {
	sign, prefix, body string
}

// pad lays p out in sp's width, by sp's alignment. right says which default
// alignment applies: right (every printf conversion, and a number in a
// format spec) or left (anything else in a format spec).
func pad(p parts, sp spec, right bool) string {
	whole := p.sign + p.prefix + p.body
	n := utf8.RuneCountInString(whole)
	if sp.width <= n {
		return whole
	}
	gap := sp.width - n
	fill := string(sp.fill)
	align := sp.align
	if align == 0 {
		align = '<'
		if right {
			align = '>'
		}
	}
	switch align {
	case '<':
		return whole + strings.Repeat(fill, gap)
	case '^':
		left := gap / 2
		return strings.Repeat(fill, left) + whole + strings.Repeat(fill, gap-left)
	case '=':
		return p.sign + p.prefix + strings.Repeat(fill, gap) + p.body
	default:
		return strings.Repeat(fill, gap) + whole
	}
}

// signOf is the sign a number is written with.
func signOf(negative bool, flag byte) string {
	switch {
	case negative:
		return "-"
	case flag == '+':
		return "+"
	case flag == ' ':
		return " "
	default:
		return ""
	}
}

// grouped inserts sep between every `every` digits of digits, counting from
// the right.
func grouped(digits string, sep byte, every int) string {
	if sep == 0 || len(digits) <= every {
		return digits
	}
	var b strings.Builder
	lead := len(digits) % every
	if lead == 0 {
		lead = every
	}
	_, _ = b.WriteString(digits[:lead])
	for i := lead; i < len(digits); i += every {
		_ = b.WriteByte(sep)
		_, _ = b.WriteString(digits[i : i+every])
	}
	return b.String()
}

// truncated is s cut to prec runes, or s when prec is unset.
func truncated(s string, prec int) string {
	if prec < 0 || utf8.RuneCountInString(s) <= prec {
		return s
	}
	runes := []rune(s)
	return string(runes[:prec])
}
