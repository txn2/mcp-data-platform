package textpatch

import (
	"strings"

	"golang.org/x/net/html"
)

// htmlNode is one element in a byte-offset-preserving element tree, the model
// that makes selector-addressed edits balanced by construction. Unlike the
// standard html.Parse tree it records where every element's tag pair sits in the
// original bytes, so a region can be cut out and put back without re-rendering
// anything the caller did not touch.
//
// The four offsets bracket the element:
//
//	outerStart  the '<' of the start tag
//	innerStart  just past the start tag's '>'
//	innerEnd    the '<' of the end tag (== outerEnd for void/self-closing)
//	outerEnd    just past the end tag's '>' (or the self-closing '>')
//
// balanced is true only when an explicit matching end tag (or a void/self-close)
// closed the element, so its span is trustworthy. An element left open at end of
// input, or force-closed because an ancestor's end tag arrived first, is not
// balanced and selecting it is refused rather than guessed at.
type htmlNode struct {
	tag        string
	attrs      []htmlAttr
	outerStart int
	innerStart int
	innerEnd   int
	outerEnd   int
	balanced   bool
	parent     *htmlNode
	children   []*htmlNode
}

// htmlAttr is one attribute as written, with its name and value case preserved
// so a JSX component's className and a case-sensitive attribute survive.
type htmlAttr struct {
	name  string
	value string
}

// voidElements are HTML elements that never carry an end tag; the tokenizer
// reports them as a start tag alone, so the tree treats them as balanced leaves.
var voidElements = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true,
	"hr": true, "img": true, "input": true, "link": true, "meta": true,
	"param": true, "source": true, "track": true, "wbr": true,
}

// parseHTMLDoc builds the element tree of body. It never fails at parse time:
// an element whose end tag is missing or crosses another element's boundary is
// recorded as unbalanced, and the refusal happens only if such an element is the
// one a selector resolves to. The returned node is a synthetic root whose
// children are the document's top-level elements.
func parseHTMLDoc(body string) *htmlNode {
	root := &htmlNode{outerEnd: len(body), innerEnd: len(body)}
	stack := []*htmlNode{root}
	z := html.NewTokenizer(strings.NewReader(body))
	offset := 0

	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		// Raw() aliases the tokenizer's buffer, valid only until the next
		// Next(); string(raw) copies so the retained node spans stay sound.
		raw := string(z.Raw())
		next := offset + len(raw)
		switch tt {
		case html.StartTagToken:
			stack = openElement(stack, raw, offset, next)
		case html.SelfClosingTagToken:
			appendLeaf(stack[len(stack)-1], rawSelfClosing(raw, offset, next))
		case html.EndTagToken:
			stack = closeElement(stack, parseTagName(raw), offset, next)
		}
		offset = next
	}
	closeOpen(stack, offset)
	return root
}

// openElement attaches a start tag to the current parent. A void element is a
// balanced leaf; anything else is pushed as the new open parent. Before it
// attaches, any open peer whose end tag this start implies (an omitted </li>
// before the next <li>, and the like) is closed as a valid balanced element.
func openElement(stack []*htmlNode, raw string, start, end int) []*htmlNode {
	name := parseTagName(raw)
	stack = closeImpliedPeers(stack, name, start)
	parent := stack[len(stack)-1]
	node := &htmlNode{
		tag:        name,
		attrs:      parseAttrs(raw),
		outerStart: start,
		innerStart: end,
		innerEnd:   end,
		outerEnd:   end,
		parent:     parent,
	}
	parent.children = append(parent.children, node)
	if voidElements[strings.ToLower(name)] {
		node.balanced = true
		return stack
	}
	return append(stack, node)
}

// closeImpliedPeers force-closes open elements whose end tag the opening of name
// implies, per the HTML5 optional-end-tag rules (a new <li> ends an open <li>, a
// new <tr> ends an open <td> then <tr>, and so on). Such a close is valid HTML,
// so the peer is marked balanced and ends where the new element begins. The scan
// stops at the first open element name does not close, so a non-peer parent is
// never crossed.
func closeImpliedPeers(stack []*htmlNode, name string, at int) []*htmlNode {
	for len(stack) > 1 && impliesEndOf(name, stack[len(stack)-1].tag) {
		top := stack[len(stack)-1]
		top.innerEnd = at
		top.outerEnd = at
		top.balanced = true
		stack = stack[:len(stack)-1]
	}
	return stack
}

// HTML tag names whose balance behavior the tree tracks explicitly, named once
// so the peer tables below repeat no string literal.
const (
	elLi       = "li"
	elDd       = "dd"
	elDt       = "dt"
	elP        = "p"
	elOption   = "option"
	elOptgroup = "optgroup"
	elTd       = "td"
	elTh       = "th"
	elTr       = "tr"
	elThead    = "thead"
	elTbody    = "tbody"
	elTfoot    = "tfoot"
	elRp       = "rp"
	elRt       = "rt"
)

// tableCells, tableRows and tableSections build up the table peer sets, each tag
// named once, so a start tag closes the right open ancestors.
var (
	defItems      = newStringSet(elDd, elDt)
	ruby          = newStringSet(elRp, elRt)
	tableCells    = newStringSet(elTd, elTh)
	tableRows     = unionSet(tableCells, elTr)
	tableSections = unionSet(tableRows, elThead, elTbody, elTfoot)
)

// impliedEndTags maps an opening tag to the set of open peer tags whose end tag
// it implies, per the HTML5 optional-end-tag rules (a new <li> ends an open
// <li>, a new <tr> ends an open <td> then <tr>, and so on).
var impliedEndTags = map[string]map[string]bool{
	elLi:       newStringSet(elLi),
	elDd:       defItems,
	elDt:       defItems,
	elP:        newStringSet(elP),
	elOption:   newStringSet(elOption),
	elOptgroup: newStringSet(elOption, elOptgroup),
	elTd:       tableCells,
	elTh:       tableCells,
	elTr:       tableRows,
	elThead:    tableSections,
	elTbody:    tableSections,
	elTfoot:    tableSections,
	elRp:       ruby,
	elRt:       ruby,
}

// impliesEndOf reports whether opening the element named open implies the end
// tag of an open element named top.
func impliesEndOf(open, top string) bool {
	if peers, ok := impliedEndTags[strings.ToLower(open)]; ok {
		return peers[strings.ToLower(top)]
	}
	return false
}

// unionSet returns a new set combining base with extra members.
func unionSet(base map[string]bool, extra ...string) map[string]bool {
	out := make(map[string]bool, len(base)+len(extra))
	for k := range base {
		out[k] = true
	}
	for _, e := range extra {
		out[e] = true
	}
	return out
}

// rawSelfClosing builds the balanced leaf a `<tag/>` token produces.
func rawSelfClosing(raw string, start, end int) *htmlNode {
	return &htmlNode{
		tag:        parseTagName(raw),
		attrs:      parseAttrs(raw),
		outerStart: start,
		innerStart: end,
		innerEnd:   end,
		outerEnd:   end,
		balanced:   true,
	}
}

// walkNodes returns every element node under root in document order, the
// synthetic root itself excluded.
func walkNodes(root *htmlNode) []*htmlNode {
	var out []*htmlNode
	var rec func(n *htmlNode)
	rec = func(n *htmlNode) {
		for _, c := range n.children {
			out = append(out, c)
			rec(c)
		}
	}
	rec(root)
	return out
}

// appendLeaf attaches an already-closed node to a parent.
func appendLeaf(parent, node *htmlNode) {
	node.parent = parent
	parent.children = append(parent.children, node)
}

// closeElement resolves an end tag against the open-element stack. It closes the
// nearest matching open element, force-closing (as unbalanced) any elements that
// were still open inside it — the case of omitted optional end tags. A stray end
// tag that matches no open element is ignored: it corrupts no span.
func closeElement(stack []*htmlNode, name string, endStart, endEnd int) []*htmlNode {
	for i := len(stack) - 1; i >= 1; i-- {
		if !tagNameEqual(stack[i].tag, name) {
			continue
		}
		// Everything above the match was left open inside it. An element whose
		// end tag may be omitted (an <li> before its </ul>, a <td> before its
		// </tr>) is validly closed here and stays balanced; anything else was
		// genuinely left open and is not.
		for j := len(stack) - 1; j > i; j-- {
			stack[j].innerEnd = endStart
			stack[j].outerEnd = endStart
			stack[j].balanced = hasOptionalEndTag(stack[j].tag)
		}
		stack[i].innerEnd = endStart
		stack[i].outerEnd = endEnd
		stack[i].balanced = true
		return stack[:i]
	}
	return stack
}

// closeOpen closes every element still open at end of input. Each owns up to the
// end of the document; an optional-end-tag element is validly closed there and
// stays balanced, while a genuinely unclosed element (an open <div>) is not, so
// selecting it is refused.
func closeOpen(stack []*htmlNode, end int) {
	for i := len(stack) - 1; i >= 1; i-- {
		stack[i].innerEnd = end
		stack[i].outerEnd = end
		stack[i].balanced = hasOptionalEndTag(stack[i].tag)
	}
}

// hasOptionalEndTag reports whether an element's end tag may be omitted in HTML,
// so an element closed by its parent's end tag or by end of input is still valid
// (and thus balanced) rather than malformed.
func hasOptionalEndTag(tag string) bool {
	return optionalEndTags[strings.ToLower(tag)]
}

// optionalEndTags is the set of elements whose end tag may be omitted, so one
// closed by its parent's end tag or by end of input is valid rather than
// malformed. It extends the implied-end-tag family with the container elements
// whose end tags are also optional.
var optionalEndTags = unionSet(tableSections,
	elLi, elDd, elDt, elP, elOption, elOptgroup,
	elRp, elRt, "caption", "colgroup", "html", "head", "body")

// tagNameEqual reports whether two tag names name the same element. A known HTML
// or SVG element matches case-insensitively, so <DIV> and <div> are the same; an
// unknown name is treated as a JSX component and matches only exactly, so <Card>
// and <card> are different elements. This keeps HTML case-insensitivity without
// collapsing a component onto a same-spelled lowercase tag.
func tagNameEqual(a, b string) bool {
	if a == b {
		return true
	}
	return isKnownElement(a) && strings.EqualFold(a, b)
}

// isKnownElement reports whether name (any case) is a standard HTML or SVG
// element, and so should be matched case-insensitively rather than as a
// case-sensitive component.
func isKnownElement(name string) bool {
	return knownElements[strings.ToLower(name)]
}

// knownElements is the set of standard HTML and SVG element names, used to tell
// a real element (case-insensitive) from a JSX component (case-sensitive).
var knownElements = newStringSet(
	// HTML.
	"a", "abbr", "address", "area", "article", "aside", "audio", "b", "base",
	"bdi", "bdo", "blockquote", "body", "br", "button", "canvas", "caption",
	"cite", "code", "col", "colgroup", "data", "datalist", "dd", "del",
	"details", "dfn", "dialog", "div", "dl", "dt", "em", "embed", "fieldset",
	"figcaption", "figure", "footer", "form", "h1", "h2", "h3", "h4", "h5", "h6",
	"head", "header", "hgroup", "hr", "html", "i", "iframe", "img", "input",
	"ins", "kbd", "label", "legend", "li", "link", "main", "map", "mark", "menu",
	"meta", "meter", "nav", "noscript", "object", "ol", "optgroup", "option",
	"output", "p", "param", "picture", "pre", "progress", "q", "rp", "rt",
	"ruby", "s", "samp", "script", "section", "select", "slot", "small",
	"source", "span", "strong", "style", "sub", "summary", "sup", "table",
	"tbody", "td", "template", "textarea", "tfoot", "th", "thead", "time",
	"title", "tr", "track", "u", "ul", "var", "video", "wbr",
	// SVG.
	"svg", "g", "path", "circle", "ellipse", "line", "polyline", "polygon",
	"rect", "text", "tspan", "defs", "use", "symbol", "marker", "clippath",
	"mask", "pattern", "lineargradient", "radialgradient", "stop", "filter",
	"foreignobject", "image", "textpath", "desc",
)

// newStringSet builds a set from the given values.
func newStringSet(vals ...string) map[string]bool {
	set := make(map[string]bool, len(vals))
	for _, v := range vals {
		set[v] = true
	}
	return set
}

// parseTagName reads the tag name out of a raw tag token ("<div ...>", "</div>",
// "<Card/>"), preserving case so JSX component names survive.
func parseTagName(raw string) string {
	s := strings.TrimPrefix(raw, "<")
	s = strings.TrimPrefix(s, "/")
	end := 0
	for end < len(s) && !isTagNameBoundary(s[end]) {
		end++
	}
	return s[:end]
}

// isHTMLSpace reports whether c is ASCII whitespace as HTML tokenizes it.
func isHTMLSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f'
}

// isTagNameBoundary reports whether c ends a tag name.
func isTagNameBoundary(c byte) bool {
	return isHTMLSpace(c) || c == '>' || c == '/'
}

// parseAttrs reads the attributes out of a raw start-tag token, preserving the
// case of both names and values. It is deliberately lenient: JSX spread
// (`{...props}`) and expression attributes it cannot make into a name/value pair
// are skipped rather than derailing the scan, because their presence never
// changes which element a selector addresses.
func parseAttrs(raw string) []htmlAttr {
	s := attrRegion(raw)
	var attrs []htmlAttr
	for i := 0; i < len(s); {
		i = skipSpace(s, i)
		if i >= len(s) {
			break
		}
		name, ni := readAttrName(s, i)
		if name == "" {
			i = skipToken(s, i)
			continue
		}
		value, vi := readAttrValue(s, ni)
		attrs = append(attrs, htmlAttr{name: name, value: value})
		i = vi
	}
	return attrs
}

// attrRegion returns the slice of a raw start tag between the tag name and its
// closing '>' (or '/>'), where the attributes live.
func attrRegion(raw string) string {
	s := strings.TrimPrefix(raw, "<")
	nameEnd := 0
	for nameEnd < len(s) && !isTagNameBoundary(s[nameEnd]) {
		nameEnd++
	}
	s = s[nameEnd:]
	s = strings.TrimSuffix(s, ">")
	s = strings.TrimSuffix(s, "/")
	return s
}

// skipSpace advances past ASCII whitespace.
func skipSpace(s string, i int) int {
	for i < len(s) && isHTMLSpace(s[i]) {
		i++
	}
	return i
}

// skipToken advances past one non-space run, used to step over a fragment the
// attribute scanner does not recognize (a spread or a bare expression).
func skipToken(s string, i int) int {
	for i < len(s) && !isHTMLSpace(s[i]) {
		i++
	}
	return i + 1
}

// readAttrName reads an attribute name starting at i, returning it and the index
// just past it. An empty name signals a fragment the caller should skip.
func readAttrName(s string, i int) (name string, next int) {
	start := i
	for i < len(s) && isAttrNameByte(s[i]) {
		i++
	}
	return s[start:i], i
}

// isAttrNameByte reports whether c may appear in an attribute name. It admits
// the letters, digits, hyphen and colon of HTML and SVG names, so data-* and
// namespaced attributes (xlink:href) parse.
func isAttrNameByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' ||
		c >= '0' && c <= '9' || c == '-' || c == '_' || c == ':' || c == '.'
}

// readAttrValue reads the value following an attribute name, given the index
// just past the name. A name with no '=' is a boolean attribute with an empty
// value. Quoted values keep their interior verbatim; an unquoted value runs to
// the next space.
func readAttrValue(s string, i int) (value string, next int) {
	i = skipSpace(s, i)
	if i >= len(s) || s[i] != '=' {
		return "", i
	}
	i = skipSpace(s, i+1)
	if i >= len(s) {
		return "", i
	}
	if q := s[i]; q == '"' || q == '\'' {
		return readQuotedValue(s, i, q)
	}
	return readUnquotedValue(s, i)
}

// readQuotedValue reads a quoted attribute value; the opening quote is at i.
func readQuotedValue(s string, i int, q byte) (value string, next int) {
	end := strings.IndexByte(s[i+1:], q)
	if end < 0 {
		return s[i+1:], len(s)
	}
	return s[i+1 : i+1+end], i + 1 + end + 1
}

// readUnquotedValue reads an unquoted attribute value running to the next space.
func readUnquotedValue(s string, i int) (value string, next int) {
	start := i
	for i < len(s) && !isHTMLSpace(s[i]) {
		i++
	}
	return s[start:i], i
}
