package knowledgepage

import (
	"fmt"
	"strings"
)

// Default oversized-page thresholds (#705). A page crossing either bound gets a
// non-blocking suggestion to split into focused, cross-linked sub-pages; the write
// always succeeds. The platform signals, the agent performs the semantic split.
const (
	// DefaultOversizeBytes is the body byte size at or above which the split
	// suggestion fires (~16 KiB of markdown, well under the 1 MiB hard cap but
	// large enough that a single page is becoming an index's worth of content).
	DefaultOversizeBytes = 16384
	// DefaultOversizeSections is the markdown-heading count at or above which the
	// split suggestion fires: many top-level sections is the structural tell that a
	// page is covering several topics that each want their own page.
	DefaultOversizeSections = 12
	// maxATXHeadingLevel is the deepest ATX heading markdown recognizes (######);
	// more than six leading '#' is not a heading.
	maxATXHeadingLevel = 6
)

// SplitSuggestion reports whether a page body is large enough to suggest splitting
// it into focused, cross-linked sub-pages, and the human-readable suggestion when
// so (#705, Part B). It is a pure signal: it never blocks a write. A non-positive
// threshold disables that arm. ok is true when either the byte size or the heading
// count crosses its (positive) threshold.
func SplitSuggestion(body string, byteThreshold, sectionThreshold int) (string, bool) {
	bytes := len(body)
	sections := countMarkdownHeadings(body)

	overBytes := byteThreshold > 0 && bytes >= byteThreshold
	overSections := sectionThreshold > 0 && sections >= sectionThreshold
	if !overBytes && !overSections {
		return "", false
	}

	var reason string
	switch {
	case overBytes && overSections:
		reason = fmt.Sprintf("this page is %d bytes across %d sections", bytes, sections)
	case overBytes:
		reason = fmt.Sprintf("this page is %d bytes", bytes)
	default:
		reason = fmt.Sprintf("this page has %d sections", sections)
	}
	return reason + "; consider splitting it into focused sub-pages and linking them from an index page" +
		" (cite each with an mcp:knowledge_page: reference) so each topic stays small and progressively revealed", true
}

// countMarkdownHeadings counts ATX markdown headings (lines beginning with one to
// six '#' followed by whitespace), skipping fenced code blocks so a shell comment
// or a '#' in a code sample is not mistaken for a section. Setext headings
// (underlined with === / ---) are not counted: the apply/save bodies are
// agent-authored ATX markdown, and counting '---' rules would over-count.
func countMarkdownHeadings(body string) int {
	count := 0
	inFence := false
	for line := range strings.SplitSeq(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if isATXHeading(trimmed) {
			count++
		}
	}
	return count
}

// isATXHeading reports whether a trimmed line is an ATX markdown heading: one to
// six leading '#' characters followed by a space (so "#tag" is not a heading).
func isATXHeading(trimmed string) bool {
	hashes := 0
	for _, r := range trimmed {
		if r != '#' {
			break
		}
		hashes++
	}
	if hashes == 0 || hashes > maxATXHeadingLevel {
		return false
	}
	rest := trimmed[hashes:]
	return strings.HasPrefix(rest, " ") || strings.HasPrefix(rest, "\t")
}
