package xmltree

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrBadPath reports a path outside the supported subset. It is a sentinel
// because a bad path is a caller mistake to be surfaced, never an empty
// result: a path language that answers "no matches" to a construct it does not
// implement teaches the caller that the data is missing.
var ErrBadPath = errors.New("unsupported path")

// PathSyntax is the whole of the supported language, in the words an error
// message uses. It is a constant so the refusal and the documentation cannot
// drift apart.
const PathSyntax = `supported paths are child steps (a/b/c), a descendant step (//c), ` +
	`the wildcard *, an attribute predicate [@name='value'] and a positional predicate [n]; ` +
	`names match on local name, so an upstream's namespace prefix does not matter`

// The two step separators, spelled once: a child step and a descendant step.
const (
	childSep      = "/"
	descendantSep = "//"
	// wildcard is the name test that matches any element.
	wildcard = "*"
)

// badPath composes the one shape every path refusal takes: the path as
// written, what is wrong with it, and the language it is being judged against.
// Composed in one place so a caller reading two different refusals is reading
// the same sentence with a different middle.
func badPath(src, detail string) error {
	return fmt.Errorf("in path %q: %w: %s; %s", src, ErrBadPath, detail, PathSyntax)
}

// axis is how a step reaches its candidates from a context node.
type axis int

const (
	// axisChild takes the context node's own children.
	axisChild axis = iota
	// axisDescendant takes every descendant of the context node at any
	// depth, in document order.
	axisDescendant
)

// predicate filters a step's candidates. Exactly one form is set: an attribute
// test, or a position.
type predicate struct {
	attr  string
	value string
	// index is the 1-based position when the predicate is positional, and
	// zero otherwise.
	index int
}

// step is one component of a compiled path.
type step struct {
	axis  axis
	name  string
	preds []predicate
}

// Path is a compiled path. Compiling is separated from matching so a caller
// that evaluates the same path against many documents pays for the parse once,
// and so a refusal happens where the path was written.
type Path struct {
	steps []step
}

// Compile parses a path, refusing anything outside the subset.
//
// A path is relative to the node it is evaluated against, so it does not begin
// with a single "/": a leading "//" is the descendant step and is accepted, but
// a lone leading slash would have to mean the document root, which the caller
// did not pass in.
func Compile(path string) (Path, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return Path{}, badPath(path, "the path is empty")
	}
	if strings.HasPrefix(trimmed, childSep) && !strings.HasPrefix(trimmed, descendantSep) {
		return Path{}, badPath(path, fmt.Sprintf(
			"a path is relative to the node it is evaluated against, so it cannot begin with a single %q — "+
				"write %q for a child step or %q for a descendant step",
			childSep, strings.TrimPrefix(trimmed, childSep), childSep+trimmed))
	}
	steps, err := compileSteps(trimmed, path)
	if err != nil {
		return Path{}, err
	}
	return Path{steps: steps}, nil
}

// compileSteps splits a path into steps, reading "//" as the descendant axis
// and "/" as the child axis.
func compileSteps(trimmed, src string) ([]step, error) {
	var steps []step
	rest := trimmed
	next := axisChild
	if strings.HasPrefix(rest, descendantSep) {
		next, rest = axisDescendant, rest[len(descendantSep):]
	}
	for {
		text, remainder, sep := cutStep(rest)
		s, err := compileStep(text, next, src)
		if err != nil {
			return nil, err
		}
		steps = append(steps, s)
		if sep == "" {
			return steps, nil
		}
		next, rest = axisChild, remainder
		if sep == descendantSep {
			next = axisDescendant
		}
	}
}

// cutStep splits off the leading step, returning it, what follows, and the
// separator that divided them ("" at the end of the path). It scans rather
// than splitting because a predicate may hold a slash inside its quoted value.
func cutStep(s string) (text, rest, sep string) {
	var quote byte
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '/':
			if strings.HasPrefix(s[i:], descendantSep) {
				return s[:i], s[i+len(descendantSep):], descendantSep
			}
			return s[:i], s[i+len(childSep):], childSep
		}
	}
	return s, "", ""
}

// compileStep parses one step: a name test followed by zero or more
// predicates.
func compileStep(text string, ax axis, src string) (step, error) {
	name, predsText, err := cutNameTest(text, src)
	if err != nil {
		return step{}, err
	}
	preds, err := compilePredicates(predsText, src)
	if err != nil {
		return step{}, err
	}
	return step{axis: ax, name: name, preds: preds}, nil
}

// cutNameTest splits a step into its name test and the predicate text that
// follows it.
func cutNameTest(text, src string) (name, preds string, err error) {
	i := strings.IndexByte(text, '[')
	name, preds = text, ""
	if i >= 0 {
		name, preds = text[:i], text[i:]
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", "", badPath(src, "a step has no name")
	}
	if name != wildcard && !isName(name) {
		return "", "", badPath(src, fmt.Sprintf("%q is not a name or the wildcard %q", name, wildcard))
	}
	return name, preds, nil
}

// isName reports whether s is an element name this package will match on:
// local names only, so a prefixed name is refused with the reason rather than
// silently never matching.
func isName(s string) bool {
	if s == "" {
		return false
	}
	if !nameStart(s[0]) {
		return false
	}
	for i := 1; i < len(s); i++ {
		if !nameStart(s[i]) && !nameFollow(s[i]) {
			return false
		}
	}
	return true
}

// nameStart reports the characters a name may open with.
func nameStart(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_'
}

// nameFollow reports the characters a name may carry after its first.
func nameFollow(c byte) bool {
	return c >= '0' && c <= '9' || c == '-' || c == '.'
}

// compilePredicates parses the bracketed predicates trailing a step.
func compilePredicates(text, src string) ([]predicate, error) {
	var preds []predicate
	for text != "" {
		if text[0] != '[' {
			return nil, badPath(src, fmt.Sprintf("%q is not a predicate", text))
		}
		end := strings.IndexByte(text, ']')
		if end < 0 {
			return nil, badPath(src, `a predicate is missing its "]"`)
		}
		p, err := compilePredicate(strings.TrimSpace(text[1:end]), src)
		if err != nil {
			return nil, err
		}
		preds = append(preds, p)
		text = text[end+1:]
	}
	return preds, nil
}

// compilePredicate parses one predicate body: an attribute test or a position.
func compilePredicate(body, src string) (predicate, error) {
	if strings.HasPrefix(body, "@") {
		return compileAttrPredicate(body, src)
	}
	n, err := strconv.Atoi(body)
	if err != nil || n < 1 {
		return predicate{}, badPath(src,
			fmt.Sprintf("%q is neither an attribute test nor a 1-based position", body))
	}
	return predicate{index: n}, nil
}

// compileAttrPredicate parses [@name='value'].
func compileAttrPredicate(body, src string) (predicate, error) {
	eq := strings.IndexByte(body, '=')
	if eq < 0 {
		return predicate{}, badPath(src, "an attribute predicate tests a value, as [@name='value']")
	}
	attr := strings.TrimSpace(body[1:eq])
	if !isName(attr) {
		return predicate{}, badPath(src, fmt.Sprintf("%q is not an attribute name", attr))
	}
	value := strings.TrimSpace(body[eq+1:])
	if len(value) < 2 || value[0] != value[len(value)-1] || (value[0] != '\'' && value[0] != '"') {
		return predicate{}, badPath(src, "the value in an attribute predicate is quoted, as [@name='value']")
	}
	return predicate{attr: attr, value: value[1 : len(value)-1]}, nil
}
