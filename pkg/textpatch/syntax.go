package textpatch

import (
	"strconv"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/contenttype"
)

// Syntax is how a document's regions are named. It is derived from the content
// type, not guessed from the bytes, so the same body addressed as markdown and
// as HTML answers to different region grammars deliberately.
type Syntax int

const (
	// SyntaxMarkdown names regions by ATX heading. It is the zero value, so a
	// caller that sets no syntax keeps the markdown behavior #1033 shipped.
	SyntaxMarkdown Syntax = iota
	// SyntaxHTML names regions by HTML heading or by CSS selector, over an
	// element tree with balanced spans. It serves HTML, JSX, SVG and XML.
	SyntaxHTML
	// SyntaxNone has no addressable structure; only anchored edits apply.
	SyntaxNone
)

// SyntaxForContentType selects the region grammar for a media type, routing
// through pkg/contenttype so the platform's single detection seam decides. HTML,
// JSX, SVG and XML get the element grammar; markdown and plain text keep ATX
// headings; every other textual type has no structure.
func SyntaxForContentType(ct string) Syntax {
	switch contenttype.Normalize(ct) {
	case contenttype.HTML, contenttype.JSX, contenttype.SVG, contenttype.XML:
		return SyntaxHTML
	case contenttype.Markdown, contenttype.PlainText:
		return SyntaxMarkdown
	default:
		return SyntaxNone
	}
}

// regionRequest names a region to resolve: a heading section or a CSS selector,
// with an optional occurrence to disambiguate multiple selector matches.
type regionRequest struct {
	section    string
	selector   string
	occurrence string
}

// hasRegion reports whether the request names a region at all.
func (r regionRequest) hasRegion() bool {
	return r.section != "" || r.selector != ""
}

// resolveRegion resolves the region a request names into a single Section span,
// dispatching on syntax. A selector is refused on any non-HTML document, and a
// section is refused on a structureless one, each with a message naming the
// grammar that document actually supports.
func resolveRegion(body string, syntax Syntax, req regionRequest, index int) (Section, error) {
	switch {
	case req.section != "" && req.selector != "":
		return Section{}, newError(CodeBadEdit, index,
			"Name a region with either \"section\" or \"selector\", not both.",
			"both \"section\" and \"selector\" were supplied")
	case req.selector != "":
		return resolveSelectorRegion(body, syntax, req, index)
	case req.section != "":
		return resolveSectionRegion(body, syntax, req.section, index)
	default:
		return Section{}, newError(CodeBadEdit, index,
			"Name the region to act on with \"section\" or \"selector\".",
			"no region named")
	}
}

// resolveSectionRegion resolves a heading section against the document's syntax.
func resolveSectionRegion(body string, syntax Syntax, name string, index int) (Section, error) {
	if syntax == SyntaxNone {
		return Section{}, noStructureError(index)
	}
	return findIn(docHeadings(body, syntax), name, index)
}

// resolveSelectorRegion resolves a CSS selector, which only HTML syntax allows.
func resolveSelectorRegion(body string, syntax Syntax, req regionRequest, index int) (Section, error) {
	switch syntax {
	case SyntaxMarkdown:
		return Section{}, newError(CodeBadEdit, index,
			"A markdown document has no elements; name the region with \"section\" (a heading) instead.",
			"\"selector\" does not apply to a markdown document")
	case SyntaxNone:
		return Section{}, noStructureError(index)
	}

	cs, err := parseSelector(req.selector, index)
	if err != nil {
		return Section{}, err
	}
	node, err := pickSelectorNode(selectorMatches(parseHTMLDoc(body), cs), req, index)
	if err != nil {
		return Section{}, err
	}
	if !node.balanced {
		return Section{}, newError(CodeUnresolvedMarkup, index,
			"The element's tags do not balance in the source, so its bounds cannot be trusted. Fix the markup, or target a well-formed ancestor.",
			"selector %q resolved to an element whose markup could not be reliably bounded", req.selector)
	}
	return sectionFromNode(body, req.selector, node), nil
}

// pickSelectorNode applies the occurrence rule to a selector's matches, the same
// explicit opt-in a repeated text anchor uses. A region is a single element, so
// occurrence:"all" is refused in favor of an index.
func pickSelectorNode(matches []*htmlNode, req regionRequest, index int) (*htmlNode, error) {
	if len(matches) == 0 {
		return nil, newError(CodeSectionNotFound, index,
			"Call action=outline for the document's landmarks, or action=locate to find the text, then select an element that exists.",
			"selector %q matched no element", req.selector)
	}
	switch req.occurrence {
	case "":
		if len(matches) > 1 {
			return nil, newError(CodeAmbiguous, index,
				"Set \"occurrence\" to first, last, or a 1-based index, or narrow the selector so it matches one element.",
				"selector %q matches %d elements; expected exactly 1", req.selector, len(matches))
		}
		return matches[0], nil
	case OccurrenceFirst:
		return matches[0], nil
	case OccurrenceLast:
		return matches[len(matches)-1], nil
	case OccurrenceAll:
		return nil, newError(CodeBadEdit, index,
			"A region is one element; pass a 1-based index instead of \"all\".",
			"occurrence \"all\" does not name a single region")
	default:
		return pickNthNode(matches, req, index)
	}
}

// pickNthNode resolves a 1-based numeric occurrence against selector matches.
func pickNthNode(matches []*htmlNode, req regionRequest, index int) (*htmlNode, error) {
	n, err := strconv.Atoi(req.occurrence)
	if err != nil || n < 1 {
		return nil, newError(CodeBadEdit, index,
			"\"occurrence\" must be first, last, or a 1-based integer.",
			"invalid occurrence %q", req.occurrence)
	}
	if n > len(matches) {
		return nil, newError(CodeSectionNotFound, index,
			"Call action=outline to count the matching elements before choosing an occurrence.",
			"occurrence %d requested but selector %q matches only %d element(s)", n, req.selector, len(matches))
	}
	return matches[n-1], nil
}

// sectionFromNode renders a selector-addressed element as a Section the applier
// consumes, its span the element's balanced outer bounds.
func sectionFromNode(body, selector string, node *htmlNode) Section {
	return Section{
		Heading:   selector,
		Title:     node.tag,
		Line:      lineAt(body, node.outerStart),
		SizeBytes: node.outerEnd - node.outerStart,
		start:     node.outerStart,
		end:       node.outerEnd,
	}
}

// docHeadings returns the heading sections used to resolve a section name and to
// answer outline, in document order. Markdown scans ATX headings; HTML derives
// them from <h1>..<h6> elements, spanned heading-to-next like markdown.
func docHeadings(body string, syntax Syntax) []Section {
	if syntax == SyntaxHTML {
		return htmlHeadings(body)
	}
	secs := scanHeadings(body)
	closeAndPath(secs, len(body))
	return secs
}

// htmlHeadings builds the heading tree of an HTML document from its <h1>..<h6>
// elements. Each section starts at its heading element and, via closeAndPath,
// runs to the next heading of the same or higher level, mirroring markdown.
func htmlHeadings(body string) []Section {
	root := parseHTMLDoc(body)
	var secs []Section
	for _, n := range walkNodes(root) {
		level := headingLevel(n.tag)
		if level == 0 {
			continue
		}
		title := collapseText(stripTags(body[n.innerStart:n.innerEnd]))
		secs = append(secs, Section{
			Heading: title,
			Title:   title,
			Level:   level,
			Line:    lineAt(body, n.outerStart),
			start:   n.outerStart,
		})
	}
	closeAndPath(secs, len(body))
	return secs
}

// headingLevel returns 1..6 for an <h1>..<h6> tag, or 0 for anything else.
func headingLevel(tag string) int {
	if len(tag) == 2 && (tag[0] == 'h' || tag[0] == 'H') && tag[1] >= '1' && tag[1] <= '6' {
		return int(tag[1] - '0')
	}
	return 0
}

// stripTags removes tag markup from a fragment, leaving its text.
func stripTags(s string) string {
	var b strings.Builder
	depth := 0
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '<':
			depth++
		case s[i] == '>' && depth > 0:
			depth--
		case depth == 0:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// collapseText trims a text fragment and collapses interior whitespace runs to
// single spaces, so a heading title is one clean line.
func collapseText(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// noStructureError refuses a section- or selector-scoped verb on a document
// whose content type has no addressable structure, naming the anchored ops.
func noStructureError(index int) *Error {
	return newError(CodeNoStructure, index,
		"This content type has no sections or elements to address. Use anchored edits (find/replace, insert_before, insert_after) instead.",
		"the document has no addressable structure")
}
