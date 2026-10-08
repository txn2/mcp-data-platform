package scriptre

import (
	"fmt"
	"regexp"
	"slices"
	"sort"

	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

// Pattern is a compiled pattern, as re.compile returns it. Its methods are
// the module's functions without the pattern argument.
type Pattern struct {
	pattern string
	flags   int
	re      *regexp.Regexp
}

// patternMethods are the methods a Pattern has, which are also the module
// functions that take a pattern first.
var patternMethods = []string{"findall", "finditer", "fullmatch", "match", "search", "split", "sub"}

// String is Python's repr of a compiled pattern.
func (p *Pattern) String() string {
	if p.flags == 0 {
		return fmt.Sprintf("re.compile(%s)", starlark.String(p.pattern))
	}
	return fmt.Sprintf("re.compile(%s, %d)", starlark.String(p.pattern), p.flags)
}

// Type is the name a script reads from type().
func (*Pattern) Type() string { return "re.Pattern" }

// Freeze has nothing mutable to freeze.
func (*Pattern) Freeze() {}

// Truth is always true.
func (*Pattern) Truth() starlark.Bool { return starlark.True }

// Hash is the pattern text's, so a compiled pattern can key a dict.
func (p *Pattern) Hash() (uint32, error) { return starlark.String(p.pattern).Hash() } //nolint:wrapcheck // a string's hash cannot fail

// CompareSameType makes two patterns equal when their text and flags are, as
// Python's are, so a compiled pattern can key a dict.
func (p *Pattern) CompareSameType(op syntax.Token, y starlark.Value, _ int) (bool, error) {
	q, ok := y.(*Pattern)
	same := ok && p.pattern == q.pattern && p.flags == q.flags
	switch op {
	case syntax.EQL:
		return same, nil
	case syntax.NEQ:
		return !same, nil
	default:
		return false, fmt.Errorf("compiled patterns support only == and !=, not %s", op)
	}
}

// AttrNames lists the methods and the three fields.
func (*Pattern) AttrNames() []string {
	names := append([]string{"flags", "groups", "pattern"}, patternMethods...)
	sort.Strings(names)
	return names
}

// Attr returns a field, or the method bound to this pattern.
func (p *Pattern) Attr(name string) (starlark.Value, error) {
	switch name {
	case "pattern":
		return starlark.String(p.pattern), nil
	case "flags":
		return starlark.MakeInt(p.flags), nil
	case "groups":
		return starlark.MakeInt(p.re.NumSubexp()), nil
	}
	if slices.Contains(patternMethods, name) {
		return starlark.NewBuiltin("re.Pattern."+name, func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
			return p.call(thread, name, b.Name(), args, kwargs)
		}), nil
	}
	return nil, nil //nolint:nilnil // Starlark's contract for "no such attribute"
}

// call runs one method over the remaining arguments.
func (p *Pattern) call(thread *starlark.Thread, method, name string, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var (
		out starlark.Value
		err error
	)
	switch method {
	case "sub":
		out, err = p.sub(thread, name, args, kwargs)
	case "split":
		out, err = p.split(thread, name, args, kwargs)
	default:
		out, err = p.scan(thread, method, name, args, kwargs)
	}
	if err != nil {
		return nil, fmt.Errorf("in %s: %w", name, err)
	}
	return out, nil
}

// scan is search, match, fullmatch, findall and finditer: the methods that
// take the string alone.
func (p *Pattern) scan(thread *starlark.Thread, method, name string, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var s string
	if err := starlark.UnpackArgs(name, args, kwargs, "s", &s); err != nil {
		return nil, err //nolint:wrapcheck // call wraps it with the binding
	}
	charge(thread, len(s))
	switch method {
	case "findall":
		return p.findall(s), nil
	case "finditer":
		return p.finditer(s), nil
	}
	re, err := p.anchored(thread, method)
	if err != nil {
		return nil, err
	}
	return newMatch(p.re, s, re.FindStringSubmatchIndex(s)), nil
}

// anchored is the program search, match or fullmatch runs: the pattern
// itself, or the pattern held to the start (and end) of the string. Go's
// regexp has no anchored entry point, so the anchor is part of the program,
// cached with the rest of the run's patterns.
func (p *Pattern) anchored(thread *starlark.Thread, method string) (*regexp.Regexp, error) {
	switch method {
	case "match":
		return compileSource(thread, `\A(?:`+p.re.String()+`)`, p.pattern)
	case "fullmatch":
		return compileSource(thread, `\A(?:`+p.re.String()+`)\z`, p.pattern)
	default:
		return p.re, nil
	}
}

// findall is Python's: the whole matches when the pattern has no group, the
// first group's text when it has one, and a tuple of every group's when it
// has more. A group that took no part is "".
func (p *Pattern) findall(s string) starlark.Value {
	all := p.re.FindAllStringSubmatchIndex(s, -1)
	out := make([]starlark.Value, 0, len(all))
	groups := p.re.NumSubexp()
	for _, idx := range all {
		switch groups {
		case 0:
			out = append(out, starlark.String(s[idx[0]:idx[1]]))
		case 1:
			out = append(out, groupText(s, idx, 1, starlark.String("")))
		default:
			tuple := make(starlark.Tuple, groups)
			for g := 1; g <= groups; g++ {
				tuple[g-1] = groupText(s, idx, g, starlark.String(""))
			}
			out = append(out, tuple)
		}
	}
	return starlark.NewList(out)
}

// finditer is every match, in order, as a list: Starlark has no lazy
// iterator a builtin could hand back.
func (p *Pattern) finditer(s string) starlark.Value {
	all := p.re.FindAllStringSubmatchIndex(s, -1)
	out := make([]starlark.Value, 0, len(all))
	for _, idx := range all {
		out = append(out, newMatch(p.re, s, idx))
	}
	return starlark.NewList(out)
}

// split is Python's: the text between matches, each followed by every
// group's text (None for a group that took no part), at most maxsplit
// splits when it is positive.
func (p *Pattern) split(thread *starlark.Thread, name string, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var s string
	maxsplit := 0
	if err := starlark.UnpackArgs(name, args, kwargs, "s", &s, "maxsplit?", &maxsplit); err != nil {
		return nil, err //nolint:wrapcheck // call wraps it with the binding
	}
	charge(thread, len(s))
	limit := -1
	if maxsplit > 0 {
		limit = maxsplit
	}
	var out []starlark.Value
	last := 0
	for _, idx := range p.re.FindAllStringSubmatchIndex(s, limit) {
		out = append(out, starlark.String(s[last:idx[0]]))
		for g := 1; g <= p.re.NumSubexp(); g++ {
			out = append(out, groupText(s, idx, g, starlark.None))
		}
		last = idx[1]
	}
	out = append(out, starlark.String(s[last:]))
	return starlark.NewList(out), nil
}

// groupText is group g of a match, or absent when the group took no part.
func groupText(s string, idx []int, g int, absent starlark.Value) starlark.Value {
	if idx[2*g] < 0 {
		return absent
	}
	return starlark.String(s[idx[2*g]:idx[2*g+1]])
}
