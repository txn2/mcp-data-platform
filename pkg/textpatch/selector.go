package textpatch

import (
	"fmt"
	"slices"
	"strings"
)

// combinator is the relationship between two adjacent compounds in a selector.
type combinator int

const (
	// combDescendant is a whitespace combinator: the left compound matches some
	// ancestor of the right.
	combDescendant combinator = iota
	// combChild is a '>' combinator: the left compound matches the immediate
	// parent of the right.
	combChild
)

// attrCond is one attribute condition of a compound selector. When hasValue is
// false it tests only for the attribute's presence.
type attrCond struct {
	name     string
	hasValue bool
	value    string
}

// compound is a single element condition: an optional tag plus any number of
// id, class and attribute constraints, all of which must hold.
type compound struct {
	tag     string
	id      string
	classes []string
	attrs   []attrCond
}

// step is one compound in a compiled selector together with the combinator that
// links it to the preceding step. The combinator on the first step is unused.
type step struct {
	comb combinator
	sel  compound
}

// compiledSelector is a selector as a left-to-right sequence of steps.
type compiledSelector []step

// parseSelector compiles a CSS selector from the supported subset: type,
// #id, .class, [attr], [attr=value] compounds joined by descendant (space) or
// child (>) combinators. A selector it cannot parse is a corrective error.
func parseSelector(sel string, index int) (compiledSelector, error) {
	raws, err := splitSelectorSteps(sel, index)
	if err != nil {
		return nil, err
	}
	if len(raws) == 0 {
		return nil, badSelector(index, sel, "the selector is empty")
	}
	out := make(compiledSelector, 0, len(raws))
	for _, r := range raws {
		c, err := parseCompound(r.text, sel, index)
		if err != nil {
			return nil, err
		}
		out = append(out, step{comb: r.comb, sel: c})
	}
	return out, nil
}

// rawStep is one compound's text and its preceding combinator, before the
// compound itself is parsed.
type rawStep struct {
	comb combinator
	text string
}

// splitSelectorSteps breaks a selector into its compounds and the combinators
// between them, keeping bracketed attribute values (which may contain spaces or
// '>') intact.
func splitSelectorSteps(sel string, index int) ([]rawStep, error) {
	sc := &selectorScanner{comb: combDescendant}
	for i := 0; i < len(sel); i++ {
		sc.feed(sel[i])
	}
	if sc.depth != 0 {
		return nil, badSelector(index, sel, "unbalanced '[' in an attribute condition")
	}
	sc.flush()
	return sc.steps, nil
}

// selectorScanner accumulates compounds as it reads a selector byte by byte,
// tracking the pending combinator, whether whitespace has been seen since the
// current compound, and the bracket depth that suspends splitting.
type selectorScanner struct {
	steps    []rawStep
	cur      strings.Builder
	comb     combinator
	sawSpace bool
	depth    int
}

// feed folds one byte into the scanner.
func (sc *selectorScanner) feed(ch byte) {
	switch {
	case sc.depth == 0 && isHTMLSpace(ch):
		sc.sawSpace = sc.cur.Len() > 0
	case sc.depth == 0 && ch == '>':
		sc.flush()
		sc.comb = combChild
	default:
		if sc.sawSpace && sc.cur.Len() > 0 {
			sc.flush()
		}
		sc.trackBracket(ch)
		sc.cur.WriteByte(ch)
	}
}

// trackBracket adjusts the bracket depth so attribute conditions are not split.
func (sc *selectorScanner) trackBracket(ch byte) {
	if ch == '[' {
		sc.depth++
	} else if ch == ']' && sc.depth > 0 {
		sc.depth--
	}
}

// flush closes the pending compound, if any, with its preceding combinator.
func (sc *selectorScanner) flush() {
	if sc.cur.Len() == 0 {
		return
	}
	c := sc.comb
	if len(sc.steps) == 0 {
		c = combDescendant
	}
	sc.steps = append(sc.steps, rawStep{comb: c, text: sc.cur.String()})
	sc.cur.Reset()
	sc.comb, sc.sawSpace = combDescendant, false
}

// parseCompound parses one compound selector (no combinators) into its
// constraints. An empty compound, or one with a malformed piece, is an error.
func parseCompound(text, sel string, index int) (compound, error) {
	var c compound
	for i := 0; i < len(text); {
		next, err := parseCompoundPiece(text, i, sel, index, &c)
		if err != nil {
			return compound{}, err
		}
		i = next
	}
	if c.tag == "" && c.id == "" && len(c.classes) == 0 && len(c.attrs) == 0 {
		return compound{}, badSelector(index, sel, "a compound with no tag, id, class, or attribute")
	}
	return c, nil
}

// parseCompoundPiece parses the one piece of a compound starting at i — a class,
// id, attribute condition, or type — into c, returning the index just past it.
func parseCompoundPiece(text string, i int, sel string, index int, c *compound) (int, error) {
	switch text[i] {
	case '.':
		name, next := readIdentifier(text, i+1)
		if name == "" {
			return 0, badSelector(index, sel, "a '.' with no class name")
		}
		c.classes = append(c.classes, name)
		return next, nil
	case '#':
		name, next := readIdentifier(text, i+1)
		if name == "" {
			return 0, badSelector(index, sel, "a '#' with no id")
		}
		c.id = name
		return next, nil
	case '[':
		cond, next, err := readAttrCond(text, i, sel, index)
		if err != nil {
			return 0, err
		}
		c.attrs = append(c.attrs, cond)
		return next, nil
	default:
		name, next := readIdentifier(text, i)
		if name == "" || c.tag != "" {
			return 0, badSelector(index, sel, fmt.Sprintf("unexpected %q", text[i:]))
		}
		c.tag = name
		return next, nil
	}
}

// readIdentifier reads a tag/class/id token starting at i: letters, digits,
// hyphen, underscore and colon. It returns the token and the next index.
func readIdentifier(s string, i int) (name string, next int) {
	start := i
	for i < len(s) && isIdentByte(s[i]) {
		i++
	}
	return s[start:i], i
}

// isIdentByte reports whether c may appear in a selector identifier. Colon is
// admitted for namespaced SVG names (an unlikely target but harmless).
func isIdentByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' ||
		c >= '0' && c <= '9' || c == '-' || c == '_' || c == ':'
}

// readAttrCond parses a bracketed attribute condition starting at the '[' at i,
// returning the condition and the index just past the ']'.
func readAttrCond(s string, i int, sel string, index int) (attrCond, int, error) {
	end := strings.IndexByte(s[i:], ']')
	if end < 0 {
		return attrCond{}, 0, badSelector(index, sel, "an attribute condition with no ']'")
	}
	inner := s[i+1 : i+end]
	next := i + end + 1

	rawName, rawValue, hasValue := strings.Cut(inner, "=")
	name := strings.TrimSpace(rawName)
	if name == "" {
		return attrCond{}, 0, badSelector(index, sel, "an attribute condition with no name")
	}
	if !hasValue {
		return attrCond{name: name}, next, nil
	}
	return attrCond{name: name, hasValue: true, value: unquoteAttrValue(strings.TrimSpace(rawValue))}, next, nil
}

// unquoteAttrValue strips a single or double quote pair from an attribute value.
func unquoteAttrValue(v string) string {
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		return v[1 : len(v)-1]
	}
	return v
}

// badSelector builds a PATCH_BAD_SELECTOR error naming the parse problem.
func badSelector(index int, sel, reason string) *Error {
	return newError(CodeBadSelector, index,
		"Fix the selector; the supported forms are tag, #id, .class, [attr], and [attr=value], joined by spaces or '>'.",
		"selector %q does not parse: %s", sel, reason)
}

// selectorMatches returns every element in root that the selector selects, in
// document order.
func selectorMatches(root *htmlNode, sel compiledSelector) []*htmlNode {
	var out []*htmlNode
	last := len(sel) - 1
	for _, n := range walkNodes(root) {
		if matchChain(n, sel, last) {
			out = append(out, n)
		}
	}
	return out
}

// matchChain reports whether node satisfies step i of the selector and every
// preceding step holds against the appropriate ancestor.
func matchChain(node *htmlNode, sel compiledSelector, i int) bool {
	if !matchCompound(node, sel[i].sel) {
		return false
	}
	if i == 0 {
		return true
	}
	switch sel[i].comb {
	case combChild:
		return node.parent != nil && node.parent.tag != "" && matchChain(node.parent, sel, i-1)
	default: // combDescendant
		for a := node.parent; a != nil && a.tag != ""; a = a.parent {
			if matchChain(a, sel, i-1) {
				return true
			}
		}
		return false
	}
}

// matchCompound reports whether one element satisfies a compound's tag, id,
// class and attribute constraints.
func matchCompound(n *htmlNode, c compound) bool {
	if c.tag != "" && !tagNameEqual(n.tag, c.tag) {
		return false
	}
	if c.id != "" {
		if v, ok := n.attrValue("id"); !ok || v != c.id {
			return false
		}
	}
	if !hasAllClasses(n, c.classes) {
		return false
	}
	for _, cond := range c.attrs {
		if !matchAttrCond(n, cond) {
			return false
		}
	}
	return true
}

// hasAllClasses reports whether the element carries every requested class, in
// either a class or a JSX className attribute.
func hasAllClasses(n *htmlNode, want []string) bool {
	if len(want) == 0 {
		return true
	}
	have := n.classList()
	for _, w := range want {
		if !slices.Contains(have, w) {
			return false
		}
	}
	return true
}

// matchAttrCond reports whether the element satisfies one attribute condition.
func matchAttrCond(n *htmlNode, cond attrCond) bool {
	v, ok := n.attrValue(cond.name)
	if !ok {
		return false
	}
	return !cond.hasValue || v == cond.value
}

// attrValue returns the value of the named attribute, matching the name case-
// insensitively as HTML attribute names are. A JSX className is also returned
// under the name "class" so a .class selector reaches it.
func (n *htmlNode) attrValue(name string) (string, bool) {
	for _, a := range n.attrs {
		if strings.EqualFold(a.name, name) {
			return a.value, true
		}
		if name == "class" && a.name == "className" {
			return a.value, true
		}
	}
	return "", false
}

// classList returns the element's classes from its class or className attribute,
// split on whitespace. Class names are case-sensitive.
func (n *htmlNode) classList() []string {
	for _, a := range n.attrs {
		if strings.EqualFold(a.name, "class") || a.name == "className" {
			return strings.Fields(a.value)
		}
	}
	return nil
}
