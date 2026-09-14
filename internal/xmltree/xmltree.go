// Package xmltree is the platform's one XML reader: a bounded decoder from a
// document to a tree of elements, a small path language over that tree, and an
// encoder back to a document.
//
// It exists as its own package because two callers need the same answer and
// must not fork it (#1735): a managed script reads an XML or SOAP response
// through the predeclared `xml` module, and api_invoke_endpoint decodes an
// XML-typed response body into the same tree. Neither Starlark nor HTTP
// appears here, which is what lets the tree, the limits and the path subset be
// tested against hand-written documents with no engine and no upstream.
//
// Everything in the package is a pure function of its input: no I/O, no clock,
// no entity resolution, no network. A document is read exactly as it was
// handed over.
package xmltree

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Node is one element of a decoded document.
//
// Text is the element's own character data, concatenated and trimmed; the
// character data of descendants belongs to those descendants. Children are the
// child elements in document order, which is what keeps repeated tags and
// their sequence readable — a map keyed by tag would lose both. The position
// of text relative to children in mixed content is not recorded, so Encode of
// a decoded prose document emits the element's text before its children rather
// than interleaved. Data documents, which is what the platform's callers read,
// round-trip exactly.
type Node struct {
	// Tag is the element's local name, with any namespace prefix resolved
	// away. Matching on it is what lets a caller write "Envelope/Body"
	// without knowing whether the upstream spells the prefix soap, soapenv
	// or S.
	Tag string
	// NS is the namespace URI the element was in, empty when none.
	NS string
	// Attrs are the element's attributes keyed by local name. An attribute
	// in a namespace keeps only its local name, for the same reason Tag
	// does.
	Attrs map[string]string
	// Text is the element's own character data, concatenated in document
	// order and trimmed of surrounding whitespace.
	Text string
	// Children are the child elements in document order.
	Children []*Node
}

// Limits bound what a decode will accept. Every field is required: a caller
// that wants the platform's own bounds asks for DefaultLimits rather than
// leaving a field zero, so "no limit" can never be reached by forgetting one.
type Limits struct {
	// MaxBytes caps the document as handed to Decode.
	MaxBytes int
	// MaxDepth caps element nesting. A document nested past it is refused
	// rather than recursed into.
	MaxDepth int
	// MaxNodes caps the element count. It is the bound that stops a wide
	// document, which MaxDepth does not.
	MaxNodes int
}

// DefaultLimits are the bounds both callers use unless a deployment's own
// budget is smaller. They are sized to hold the responses the gateway already
// buffers inline while refusing a document that could only be an attack: a
// SOAP envelope 200 levels deep or with 200k elements is not a payload anyone
// meant to send.
var DefaultLimits = Limits{
	MaxBytes: 8 << 20,
	MaxDepth: 200,
	MaxNodes: 200_000,
}

// Errors a caller can act on. They are sentinels because the script module
// turns each into its own message and the gateway decides per-error whether to
// fall back to the raw body.
var (
	// ErrTooLarge reports a document past Limits.MaxBytes.
	ErrTooLarge = errors.New("document exceeds the XML size limit")
	// ErrTooDeep reports nesting past Limits.MaxDepth.
	ErrTooDeep = errors.New("document exceeds the XML nesting limit")
	// ErrTooManyNodes reports an element count past Limits.MaxNodes.
	ErrTooManyNodes = errors.New("document exceeds the XML element limit")
	// ErrDoctype reports a document type declaration. See Decode.
	ErrDoctype = errors.New("document type declarations are not accepted")
	// ErrNotElement reports a document with no root element.
	ErrNotElement = errors.New("document has no root element")
)

// Decode reads a document into its root element.
//
// A document type declaration is refused outright. Go's decoder resolves no
// external entity, but an internal DTD can still declare entities that expand
// into each other, and a caller with a legitimate reason to send a DOCTYPE to
// this platform has not turned up. Refusing it is one rule with no parser
// state behind it, which is the kind of rule that stays correct.
//
// Decoding is strict: an unknown entity, a mismatched end tag or malformed
// markup is an error rather than a best guess, so a caller never acts on a
// tree the document did not describe.
func Decode(doc string, lim Limits) (*Node, error) {
	if len(doc) > lim.MaxBytes {
		return nil, fmt.Errorf("document is %d bytes: %w of %d bytes",
			len(doc), ErrTooLarge, lim.MaxBytes)
	}
	dec := xml.NewDecoder(strings.NewReader(doc))
	dec.Strict = true
	// An unknown character set is refused rather than read as bytes: the
	// decoder has no converter, and guessing would hand back mojibake that
	// looks like data.
	dec.CharsetReader = func(charset string, _ io.Reader) (io.Reader, error) {
		return nil, fmt.Errorf("unsupported XML character encoding %q; re-encode the document as UTF-8", charset)
	}
	return decodeTokens(dec, lim)
}

// decodeTokens walks the token stream into a tree, maintaining the open-element
// stack itself rather than recursing, so MaxDepth bounds a slice rather than
// the goroutine stack.
func decodeTokens(dec *xml.Decoder, lim Limits) (*Node, error) {
	b := builder{lim: lim}
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parsing XML: %w", err)
		}
		if err := b.token(tok); err != nil {
			return nil, err
		}
	}
	if b.root == nil {
		return nil, ErrNotElement
	}
	return b.root, nil
}

// builder is the tree under construction and the bounds it is held to.
//
// texts runs parallel to stack: an element's character data arrives in as many
// pieces as the document has entity references and markup boundaries inside
// it, and appending each piece to the node's string would copy everything
// collected so far every time -- quadratic in the number of pieces, which a
// document controls. It is accumulated here and moved onto the node once, when
// the element closes. The builders are held by pointer because growing this
// slice would otherwise copy a non-zero strings.Builder by value, which
// panics.
type builder struct {
	lim   Limits
	stack []*Node
	texts []*strings.Builder
	root  *Node
	nodes int
}

// token folds one token into the tree. A token kind this package has nothing
// to record -- a comment, a processing instruction, the XML declaration -- is
// passed over.
func (b *builder) token(tok xml.Token) error {
	switch t := tok.(type) {
	case xml.Directive:
		if isDoctype(t) {
			return ErrDoctype
		}
	case xml.StartElement:
		return b.start(t)
	case xml.EndElement:
		return b.end()
	case xml.CharData:
		if len(b.stack) > 0 {
			_, _ = b.texts[len(b.texts)-1].Write(t)
		}
	}
	return nil
}

// start opens an element, refusing one past either bound and one that would be
// a second root.
func (b *builder) start(t xml.StartElement) error {
	if len(b.stack) >= b.lim.MaxDepth {
		return fmt.Errorf("depth %d: %w of %d", len(b.stack)+1, ErrTooDeep, b.lim.MaxDepth)
	}
	b.nodes++
	if b.nodes > b.lim.MaxNodes {
		return fmt.Errorf("element %d: %w of %d", b.nodes, ErrTooManyNodes, b.lim.MaxNodes)
	}
	el := newElement(t)
	switch {
	case len(b.stack) > 0:
		parent := b.stack[len(b.stack)-1]
		parent.Children = append(parent.Children, el)
	case b.root != nil:
		return errors.New("parsing XML: document has more than one root element")
	default:
		b.root = el
	}
	b.stack = append(b.stack, el)
	b.texts = append(b.texts, &strings.Builder{})
	return nil
}

// end closes the innermost open element, trimming the text it collected.
func (b *builder) end() error {
	if len(b.stack) == 0 {
		return errors.New("parsing XML: end tag with no open element")
	}
	top := b.stack[len(b.stack)-1]
	top.Text = strings.TrimSpace(b.texts[len(b.texts)-1].String())
	b.stack = b.stack[:len(b.stack)-1]
	b.texts = b.texts[:len(b.texts)-1]
	return nil
}

// newElement builds a node from a start tag, keeping local names only.
//
// Namespace declarations (xmlns and xmlns:prefix) are dropped from Attrs: the
// decoder has already resolved every name through them, so keeping them would
// only offer a caller a second, prefix-dependent way to ask a question the
// resolved NS already answers.
func newElement(t xml.StartElement) *Node {
	el := &Node{Tag: t.Name.Local, NS: t.Name.Space}
	for _, a := range t.Attr {
		if a.Name.Local == "xmlns" || a.Name.Space == "xmlns" {
			continue
		}
		if el.Attrs == nil {
			el.Attrs = map[string]string{}
		}
		el.Attrs[a.Name.Local] = a.Value
	}
	return el
}

// isDoctype reports whether a directive is a document type declaration.
// Comments and processing instructions arrive as their own token kinds, so a
// directive is either a DOCTYPE or a construct that may only appear inside
// one.
func isDoctype(d xml.Directive) bool {
	return strings.HasPrefix(strings.TrimSpace(string(d)), "DOCTYPE")
}
