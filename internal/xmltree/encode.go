package xmltree

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ErrBadTree reports a tree that cannot be written as XML.
var ErrBadTree = errors.New("tree cannot be encoded as XML")

// doc accumulates a rendering. strings.Builder never fails, so its error
// returns are discarded once here rather than at every write.
type doc struct{ b strings.Builder }

func (d *doc) write(s string) { _, _ = d.b.WriteString(s) }

// escaped writes text with the five XML metacharacters replaced.
//
// encoding/xml's EscapeText is not used because it also rewrites newline,
// carriage return and tab as character references, which is correct inside an
// attribute and turns a multi-line text body into something no human reads.
// Quotes are escaped so the same routine serves both positions.
func (d *doc) escaped(s string) {
	for _, r := range s {
		switch r {
		case '<':
			d.write("&lt;")
		case '>':
			d.write("&gt;")
		case '&':
			d.write("&amp;")
		case '\'':
			d.write("&apos;")
		case '"':
			d.write("&quot;")
		default:
			_, _ = d.b.WriteRune(r)
		}
	}
}

// Encode writes a tree as an XML document.
//
// Each element is written with its own text before its children, which is the
// shape Decode produces for a data document. An element in a namespace carries
// xmlns only where that namespace differs from the one it inherits, so a SOAP
// envelope reads the way its sender wrote it rather than repeating the
// declaration on every node.
//
// The same depth and element limits apply as on the way in: a tree assembled
// in a script is caller-supplied data like any other, and a list that holds
// itself would otherwise be an unbounded walk.
func Encode(n *Node, lim Limits) (string, error) {
	if n == nil {
		return "", fmt.Errorf("nothing to encode: %w", ErrBadTree)
	}
	w := &encoder{lim: lim}
	if err := w.element(n, "", 0); err != nil {
		return "", err
	}
	if w.out.b.Len() > lim.MaxBytes {
		return "", fmt.Errorf("encoded document is %d bytes: %w of %d bytes",
			w.out.b.Len(), ErrTooLarge, lim.MaxBytes)
	}
	return w.out.b.String(), nil
}

// encoder carries the one rendering and the budget it is held to, so the
// recursion passes only what changes per element.
type encoder struct {
	out   doc
	lim   Limits
	nodes int
}

// element renders one element and its subtree. inheritedNS is the default
// namespace in force at this point, so a redundant xmlns is left off.
func (e *encoder) element(n *Node, inheritedNS string, depth int) error {
	if err := e.admit(n, depth); err != nil {
		return err
	}
	e.open(n, inheritedNS)
	if err := e.attrs(n); err != nil {
		return err
	}
	if n.Text == "" && len(n.Children) == 0 {
		e.out.write("/>")
		return nil
	}
	e.out.write(">")
	e.out.escaped(n.Text)
	for _, c := range n.Children {
		if err := e.element(c, n.NS, depth+1); err != nil {
			return err
		}
	}
	e.out.write("</" + n.Tag + ">")
	return nil
}

// admit is every reason an element is refused before anything is written for
// it: the bounds it is past, and a name that cannot be spelled.
func (e *encoder) admit(n *Node, depth int) error {
	if n == nil {
		return fmt.Errorf("a child is missing: %w", ErrBadTree)
	}
	if depth >= e.lim.MaxDepth {
		return fmt.Errorf("depth %d: %w of %d", depth+1, ErrTooDeep, e.lim.MaxDepth)
	}
	e.nodes++
	if e.nodes > e.lim.MaxNodes {
		return fmt.Errorf("element %d: %w of %d", e.nodes, ErrTooManyNodes, e.lim.MaxNodes)
	}
	if !isName(n.Tag) {
		return fmt.Errorf("element name %q is not usable: %w", n.Tag, ErrBadTree)
	}
	// Checked per element rather than only at the end: a tree inside the
	// node and depth bounds can still hold more text than the budget, and
	// the point of a byte cap is not to have assembled the whole thing
	// first.
	if e.out.b.Len() > e.lim.MaxBytes {
		return fmt.Errorf("encoded document is past %d bytes: %w of %d bytes",
			e.out.b.Len(), ErrTooLarge, e.lim.MaxBytes)
	}
	return nil
}

// open writes the start tag and the namespace declaration, if this element
// needs one.
func (e *encoder) open(n *Node, inheritedNS string) {
	e.out.write("<" + n.Tag)
	if n.NS != inheritedNS {
		e.out.write(` xmlns="`)
		e.out.escaped(n.NS)
		e.out.write(`"`)
	}
}

// attrs renders an element's attributes in name order. The order is sorted
// rather than arbitrary because a map has none and an encoder whose output
// changes between runs cannot be compared, cached or asserted on.
func (e *encoder) attrs(n *Node) error {
	names := make([]string, 0, len(n.Attrs))
	for k := range n.Attrs {
		if !isName(k) {
			return fmt.Errorf("attribute name %q on <%s> is not usable: %w", k, n.Tag, ErrBadTree)
		}
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		e.out.write(" " + k + `="`)
		e.out.escaped(n.Attrs[k])
		e.out.write(`"`)
	}
	return nil
}
