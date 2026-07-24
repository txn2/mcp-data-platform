package textpatch

import (
	"fmt"
	"strings"
)

// Landmark is an addressable element an agent would target on an HTML, JSX or
// SVG document: one carrying an id or a data-* region marker. It reports a
// copyable selector, so outline answers "where can I patch" for a dashboard that
// has no headings at all.
type Landmark struct {
	// Tag is the element's tag name as written.
	Tag string `json:"tag"`
	// Selector is a selector that resolves to this element, ready to paste into
	// a patch.
	Selector string `json:"selector"`
	// ID is the element's id attribute when it has one.
	ID string `json:"id,omitempty"`
	// Line is the 1-based line of the element's start tag.
	Line int `json:"line"`
	// SizeBytes is the byte size of the element's whole outer span.
	SizeBytes int `json:"size_bytes"`
}

// htmlLandmarks returns the addressable landmarks of an HTML document in
// document order, never nil. Only balanced elements are reported, so every
// landmark's selector resolves to a span a patch can act on.
func htmlLandmarks(body string) []Landmark {
	root := parseHTMLDoc(body)
	out := []Landmark{}
	for _, n := range walkNodes(root) {
		if !n.balanced {
			continue
		}
		sel, ok := landmarkSelector(n)
		if !ok {
			continue
		}
		id, _ := n.attrValue("id")
		out = append(out, Landmark{
			Tag:       n.tag,
			Selector:  sel,
			ID:        id,
			Line:      lineAt(body, n.outerStart),
			SizeBytes: n.outerEnd - n.outerStart,
		})
	}
	return out
}

// landmarkSelector builds a copyable selector for an element and reports whether
// the element is a landmark at all. An id yields "#id"; otherwise the first
// data-* attribute yields "tag[data-x=\"v\"]" (or "tag[data-x]" when the marker
// is valueless). An element with neither is not a landmark.
func landmarkSelector(n *htmlNode) (string, bool) {
	if id, ok := n.attrValue("id"); ok && id != "" {
		return "#" + id, true
	}
	for _, a := range n.attrs {
		if !strings.HasPrefix(strings.ToLower(a.name), "data-") {
			continue
		}
		if a.value == "" {
			return fmt.Sprintf("%s[%s]", n.tag, a.name), true
		}
		return fmt.Sprintf("%s[%s=%q]", n.tag, a.name, a.value), true
	}
	return "", false
}
