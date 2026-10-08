package scriptre

import (
	"errors"
	"fmt"
	"regexp"

	"go.starlark.net/starlark"
)

// Match is one match, as search, match, fullmatch and finditer return it.
type Match struct {
	re  *regexp.Regexp
	s   string
	idx []int
}

// newMatch is the match idx describes, or None when there is none.
func newMatch(re *regexp.Regexp, s string, idx []int) starlark.Value {
	if idx == nil {
		return starlark.None
	}
	return &Match{re: re, s: s, idx: idx}
}

// matchMethods are a Match's methods.
var matchMethods = []string{"end", "group", "groupdict", "groups", "span", "start"}

// String is Python's repr of a match.
func (m *Match) String() string {
	return fmt.Sprintf("<re.Match object; span=(%d, %d), match=%s>", m.idx[0], m.idx[1], starlark.String(m.s[m.idx[0]:m.idx[1]]))
}

// Type is the name a script reads from type().
func (*Match) Type() string { return "re.Match" }

// Freeze has nothing mutable to freeze.
func (*Match) Freeze() {}

// Truth is always true: a failed match is None.
func (*Match) Truth() starlark.Bool { return starlark.True }

// Hash refuses, as Python's does not define one either.
func (*Match) Hash() (uint32, error) { return 0, errUnhashable }

// errUnhashable is a match's answer to hash().
var errUnhashable = errors.New("unhashable: re.Match")

// AttrNames lists the methods and the string field.
func (*Match) AttrNames() []string {
	return append(append([]string(nil), matchMethods...), "string")
}

// Attr returns the matched string or a method bound to this match.
func (m *Match) Attr(name string) (starlark.Value, error) {
	if name == "string" {
		return starlark.String(m.s), nil
	}
	fn, ok := map[string]func(string, starlark.Tuple, []starlark.Tuple) (starlark.Value, error){
		"group":     m.group,
		"groups":    m.groups,
		"groupdict": m.groupdict,
		"start":     m.offset(0),
		"end":       m.offset(1),
		"span":      m.span,
	}[name]
	if !ok {
		return nil, nil //nolint:nilnil // Starlark's contract for "no such attribute"
	}
	return starlark.NewBuiltin("re.Match."+name, func(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		v, err := fn(b.Name(), args, kwargs)
		if err != nil {
			return nil, fmt.Errorf("in %s: %w", b.Name(), err)
		}
		return v, nil
	}), nil
}

// index resolves a group argument, a number or a name, to its number.
func (m *Match) index(v starlark.Value) (int, error) {
	if name, ok := starlark.AsString(v); ok {
		g := m.re.SubexpIndex(name)
		if g < 0 {
			return 0, fmt.Errorf("no such group %q", name)
		}
		return g, nil
	}
	var g int
	if err := starlark.AsInt(v, &g); err != nil {
		return 0, fmt.Errorf("group must be an int or a name, not %s", v.Type())
	}
	if g < 0 || g > m.re.NumSubexp() {
		return 0, fmt.Errorf("no such group %d; the pattern has %d", g, m.re.NumSubexp())
	}
	return g, nil
}

// group is m.group(*g): the whole match with no argument, one group's text
// (None when it took no part) with one, and a tuple of them with more.
func (m *Match) group(_ string, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if len(kwargs) > 0 {
		return nil, errors.New("group takes no keyword arguments")
	}
	if len(args) == 0 {
		args = starlark.Tuple{starlark.MakeInt(0)}
	}
	out := make(starlark.Tuple, len(args))
	for i, a := range args {
		g, err := m.index(a)
		if err != nil {
			return nil, err
		}
		out[i] = groupText(m.s, m.idx, g, starlark.None)
	}
	if len(out) == 1 {
		return out[0], nil
	}
	return out, nil
}

// groups is m.groups(default=None): every group's text, default for a group
// that took no part.
func (m *Match) groups(name string, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var absent starlark.Value = starlark.None
	if err := starlark.UnpackArgs(name, args, kwargs, "default?", &absent); err != nil {
		return nil, err //nolint:wrapcheck // Attr wraps it with the binding
	}
	out := make(starlark.Tuple, m.re.NumSubexp())
	for g := range out {
		out[g] = groupText(m.s, m.idx, g+1, absent)
	}
	return out, nil
}

// groupdict is m.groupdict(default=None): every named group's text by name.
func (m *Match) groupdict(name string, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var absent starlark.Value = starlark.None
	if err := starlark.UnpackArgs(name, args, kwargs, "default?", &absent); err != nil {
		return nil, err //nolint:wrapcheck // Attr wraps it with the binding
	}
	out := starlark.NewDict(m.re.NumSubexp())
	for g, n := range m.re.SubexpNames() {
		if n != "" {
			_ = out.SetKey(starlark.String(n), groupText(m.s, m.idx, g, absent))
		}
	}
	return out, nil
}

// offset is m.start(g=0) (side 0) or m.end(g=0) (side 1): the byte offset,
// -1 for a group that took no part.
func (m *Match) offset(side int) func(string, starlark.Tuple, []starlark.Tuple) (starlark.Value, error) {
	return func(name string, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		g, err := m.groupArg(name, args, kwargs)
		if err != nil {
			return nil, err
		}
		return starlark.MakeInt(m.idx[2*g+side]), nil
	}
}

// span is m.span(g=0): (start, end).
func (m *Match) span(name string, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	g, err := m.groupArg(name, args, kwargs)
	if err != nil {
		return nil, err
	}
	return starlark.Tuple{starlark.MakeInt(m.idx[2*g]), starlark.MakeInt(m.idx[2*g+1])}, nil
}

// groupArg reads the optional group argument of start, end and span.
func (m *Match) groupArg(name string, args starlark.Tuple, kwargs []starlark.Tuple) (int, error) {
	var v starlark.Value = starlark.MakeInt(0)
	if err := starlark.UnpackArgs(name, args, kwargs, "group?", &v); err != nil {
		return 0, err //nolint:wrapcheck // Attr wraps it with the binding
	}
	return m.index(v)
}
