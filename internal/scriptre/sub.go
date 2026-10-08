package scriptre

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"go.starlark.net/starlark"
)

// sub is re.sub(pattern, repl, s, count=0): s with each match replaced, at
// most count of them when count is positive. repl is a template in Python's
// syntax (\1, \g<1>, \g<name>, \n) or a function called with each match that
// returns the replacement.
func (p *Pattern) sub(thread *starlark.Thread, name string, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var (
		repl  starlark.Value
		s     string
		count int
	)
	if err := starlark.UnpackArgs(name, args, kwargs, "repl", &repl, "s", &s, "count?", &count); err != nil {
		return nil, err //nolint:wrapcheck // call wraps it with the binding
	}
	charge(thread, len(s))
	replace, err := p.replacer(thread, repl)
	if err != nil {
		return nil, err
	}
	limit := -1
	if count > 0 {
		limit = count
	}
	var out strings.Builder
	last := 0
	for _, idx := range p.re.FindAllStringSubmatchIndex(s, limit) {
		_, _ = out.WriteString(s[last:idx[0]])
		text, err := replace(s, idx)
		if err != nil {
			return nil, err
		}
		_, _ = out.WriteString(text)
		last = idx[1]
	}
	_, _ = out.WriteString(s[last:])
	return starlark.String(out.String()), nil
}

// replacer returns what produces one match's replacement: the template
// expanded, or the function's result.
func (p *Pattern) replacer(thread *starlark.Thread, repl starlark.Value) (func(string, []int) (string, error), error) {
	if fn, ok := repl.(starlark.Callable); ok {
		return p.calling(thread, fn), nil
	}
	template, ok := starlark.AsString(repl)
	if !ok {
		return nil, fmt.Errorf("repl must be a string or a function, not %s", repl.Type())
	}
	parts, err := parseTemplate(template, p.re.NumSubexp(), p.re.SubexpIndex)
	if err != nil {
		return nil, err
	}
	return func(s string, idx []int) (string, error) { return expand(parts, s, idx), nil }, nil
}

// calling is a replacer that calls fn with each match.
func (p *Pattern) calling(thread *starlark.Thread, fn starlark.Callable) func(string, []int) (string, error) {
	return func(s string, idx []int) (string, error) {
		v, err := starlark.Call(thread, fn, starlark.Tuple{newMatch(p.re, s, idx)}, nil)
		if err != nil {
			return "", err //nolint:wrapcheck // the function's own error, already positioned
		}
		text, ok := starlark.AsString(v)
		if !ok {
			return "", fmt.Errorf("repl function returned %s, not a string", v.Type())
		}
		return text, nil
	}
}

// expand writes a parsed template for one match.
func expand(parts []templatePart, s string, idx []int) string {
	var b strings.Builder
	for _, part := range parts {
		switch {
		case part.group < 0:
			_, _ = b.WriteString(part.text)
		case idx[2*part.group] >= 0:
			_, _ = b.WriteString(s[idx[2*part.group]:idx[2*part.group+1]])
		}
	}
	return b.String()
}

// templatePart is a literal run of a replacement template (group -1) or a
// reference to a group.
type templatePart struct {
	text  string
	group int
}

// escapeRead is one escape after a backslash: a group reference (group >= 0)
// or a character escape (group -1, its text), and how many bytes after the
// backslash it took.
type escapeRead struct {
	group int
	width int
	text  string
}

// templateEscapes are the character escapes a template may use.
var templateEscapes = map[byte]string{'n': "\n", 't': "\t", 'r': "\r", 'f': "\f", 'v': "\v", 'a': "\a", 'b': "\b", '\\': "\\"}

// errEndEscape refuses a template ending in a lone backslash.
var errEndEscape = errors.New("bad escape (end of template)")

// parseTemplate reads a Python replacement template once, before any match
// is replaced, so a bad escape or a group the pattern does not have is
// refused even when nothing matches.
func parseTemplate(template string, groups int, named func(string) int) ([]templatePart, error) {
	var (
		parts   []templatePart
		literal strings.Builder
	)
	flush := func() {
		if literal.Len() > 0 {
			parts = append(parts, templatePart{text: literal.String(), group: -1})
			literal.Reset()
		}
	}
	for i := 0; i < len(template); i++ {
		if template[i] != '\\' {
			_ = literal.WriteByte(template[i])
			continue
		}
		e, err := templateEscape(template[i+1:], groups, named)
		if err != nil {
			return nil, err
		}
		i += e.width
		if e.group < 0 {
			_, _ = literal.WriteString(e.text)
			continue
		}
		flush()
		parts = append(parts, templatePart{group: e.group})
	}
	flush()
	return parts, nil
}

// templateEscape reads the escape after a backslash.
func templateEscape(rest string, groups int, named func(string) int) (escapeRead, error) {
	if rest == "" {
		return escapeRead{}, errEndEscape
	}
	switch c := rest[0]; {
	case c >= '0' && c <= '9':
		n := 1
		if len(rest) > 1 && rest[1] >= '0' && rest[1] <= '9' {
			n = 2
		}
		g, _ := strconv.Atoi(rest[:n])
		return checkGroup(g, n, groups)
	case c == 'g':
		return namedGroup(rest, groups, named)
	default:
		if esc, ok := templateEscapes[c]; ok {
			return escapeRead{group: -1, width: 1, text: esc}, nil
		}
		return escapeRead{}, fmt.Errorf(`bad escape \%c in repl; write \\ for a backslash`, c)
	}
}

// groupRefRE is \g<...> at the start of what follows the backslash.
var groupRefRE = regexp.MustCompile(`^g<([^>]*)>`)

// namedGroup reads \g<n> or \g<name>.
func namedGroup(rest string, groups int, named func(string) int) (escapeRead, error) {
	m := groupRefRE.FindStringSubmatch(rest)
	if m == nil {
		return escapeRead{}, errors.New(`bad group reference in repl; write \g<1> or \g<name>`)
	}
	ref, width := m[1], len(m[0])
	if g, convErr := strconv.Atoi(ref); convErr == nil {
		return checkGroup(g, width, groups)
	}
	g := named(ref)
	if g < 0 {
		return escapeRead{}, fmt.Errorf("unknown group name %q in repl", ref)
	}
	return escapeRead{group: g, width: width}, nil
}

// checkGroup refuses a reference to a group the pattern does not have.
func checkGroup(g, width, groups int) (escapeRead, error) {
	if g > groups {
		return escapeRead{}, fmt.Errorf("invalid group reference %d in repl; the pattern has %d group(s)", g, groups)
	}
	return escapeRead{group: g, width: width}, nil
}
