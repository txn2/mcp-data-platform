package textpatch

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
)

// maxHeadingLevel is the deepest ATX heading markdown recognizes.
const maxHeadingLevel = 6

// maxHeadingIndent is the deepest leading indentation an ATX heading may carry;
// four spaces begins an indented code block instead.
const maxHeadingIndent = 3

// sectionPathSep joins ancestor titles in a Section.Path.
const sectionPathSep = " > "

// newline is the line terminator the document scanners split and count on.
const newline = "\n"

// Section is one markdown heading and the span it owns: the heading line
// through the line before the next heading of the same or higher level.
type Section struct {
	// Heading is the full heading line as written ("## Methodology").
	Heading string `json:"heading"`
	// Title is the heading text with its hashes and spacing removed.
	Title string `json:"title"`
	// Path is the ancestor chain ("Report > Methodology"), which
	// disambiguates a title that repeats under different parents.
	Path string `json:"path"`
	// Level is 1 for "#" through 6 for "######".
	Level int `json:"level"`
	// Line is the 1-based line of the heading.
	Line int `json:"line"`
	// SizeBytes is the byte size of the whole section, heading included.
	SizeBytes int `json:"size_bytes"`

	// start and end are the section's byte span in the body, end exclusive.
	start int
	end   int
}

// Stats is the cheap metadata of a document: enough to decide whether to read
// it at all, without reading any of it.
type Stats struct {
	SizeBytes int    `json:"size_bytes"`
	Lines     int    `json:"lines"`
	Hash      string `json:"hash"`
}

// DocStats reports the size, line count, and content hash of a body. The hash
// is sha256 hex, so a caller can tell whether a document changed without
// transferring it. Callers that need only the size or the line count should use
// len and CountLines, which do not hash.
func DocStats(body string) Stats {
	h := sha256.New()
	// Streaming the string avoids the whole-body []byte copy Sum256 forces.
	_, _ = io.WriteString(h, body)
	return Stats{
		SizeBytes: len(body),
		Lines:     CountLines(body),
		Hash:      hex.EncodeToString(h.Sum(nil)),
	}
}

// CountLines returns the number of lines in body. An empty body has zero
// lines; a body not ending in a newline still counts its final partial line.
func CountLines(body string) int {
	if body == "" {
		return 0
	}
	n := strings.Count(body, newline)
	if !strings.HasSuffix(body, newline) {
		n++
	}
	return n
}

// Outline returns the heading tree of body in document order, never nil.
// Headings inside fenced code blocks are not headings, so a shell comment in a
// fenced example never becomes a section.
func Outline(body string) []Section {
	secs := scanHeadings(body)
	closeAndPath(secs, len(body))
	return secs
}

// scanHeadings walks body line by line collecting ATX headings outside fenced
// code blocks, recording each one's start offset and line number.
func scanHeadings(body string) []Section {
	secs := []Section{}
	offset, line, fence := 0, 0, ""
	for _, raw := range strings.SplitAfter(body, newline) {
		text := strings.TrimSuffix(raw, newline)
		line++
		if next, isFence := nextFence(fence, text); isFence {
			fence = next
		} else if level, title, ok := parseHeading(text); ok && fence == "" {
			secs = append(secs, Section{
				Heading: strings.TrimRight(text, " \t\r"),
				Title:   title,
				Level:   level,
				Line:    line,
				start:   offset,
			})
		}
		offset += len(raw)
	}
	return secs
}

// nextFence reports the fenced-code state after a line. isFence is true when
// the line is a fence marker (and so is never a heading); the returned state
// opens a block, closes the block its own marker opened, or stays put when a
// different marker appears inside an open block.
func nextFence(fence, line string) (state string, isFence bool) {
	marker := fenceMarker(line)
	switch {
	case marker == "":
		return fence, false
	case fence == "":
		return marker, true
	case marker == fence:
		return "", true
	default:
		return fence, true
	}
}

// closeAndPath assigns every section its end offset, byte size, and ancestor
// path in one pass.
//
// The stack holds the currently open sections, whose levels strictly increase,
// so the heading that pops a section is exactly the next heading at or above
// its level: the one that closes its span. Computing both from the same stack
// keeps the span and the path from ever disagreeing.
func closeAndPath(secs []Section, bodyLen int) {
	var stack []int
	for i := range secs {
		for len(stack) > 0 && secs[stack[len(stack)-1]].Level >= secs[i].Level {
			closeAt(secs, stack[len(stack)-1], secs[i].start)
			stack = stack[:len(stack)-1]
		}
		secs[i].Path = joinPath(secs, stack, secs[i].Title)
		stack = append(stack, i)
	}
	// Sections still open at the end of the document own the rest of it.
	for _, open := range stack {
		closeAt(secs, open, bodyLen)
	}
}

// closeAt ends a section at an offset and records its size.
func closeAt(secs []Section, i, end int) {
	secs[i].end = end
	secs[i].SizeBytes = end - secs[i].start
}

// joinPath renders the ancestor chain of a heading from the open-section stack.
func joinPath(secs []Section, stack []int, title string) string {
	if len(stack) == 0 {
		return title
	}
	parts := make([]string, 0, len(stack)+1)
	for _, anc := range stack {
		parts = append(parts, secs[anc].Title)
	}
	return strings.Join(append(parts, title), sectionPathSep)
}

// fenceMarker returns the fence token ("```" or "~~~") when line opens or
// closes a fenced code block, or "" when it does not.
func fenceMarker(line string) string {
	trimmed := strings.TrimLeft(line, " ")
	switch {
	case strings.HasPrefix(trimmed, "```"):
		return "```"
	case strings.HasPrefix(trimmed, "~~~"):
		return "~~~"
	default:
		return ""
	}
}

// parseHeading reports whether line is an ATX heading, returning its level and
// title. Up to three leading spaces are allowed, as markdown does; a run of
// more than six hashes, or hashes with no following space, is not a heading.
func parseHeading(line string) (level int, title string, ok bool) {
	trimmed := strings.TrimRight(line, " \t\r")
	indent := len(trimmed) - len(strings.TrimLeft(trimmed, " "))
	if indent > maxHeadingIndent {
		return 0, "", false
	}
	trimmed = trimmed[indent:]
	for level < len(trimmed) && trimmed[level] == '#' {
		level++
	}
	if level == 0 || level > maxHeadingLevel {
		return 0, "", false
	}
	rest := trimmed[level:]
	if rest != "" && rest[0] != ' ' && rest[0] != '\t' {
		return 0, "", false
	}
	// A trailing closing sequence ("## Title ##") is decoration, not title.
	title = strings.TrimRight(strings.TrimSpace(rest), "#")
	return level, strings.TrimSpace(title), true
}

// FindSection resolves a caller-supplied section name against body's outline.
// The name may be the heading line ("## Methodology"), the bare title
// ("Methodology"), or an ancestor path ("Report > Methodology"); matching is
// case-insensitive. A name that matches nothing or more than one section is an
// error carrying the outline or the disambiguating advice.
func FindSection(body, name string, editIndex int) (Section, error) {
	return findIn(Outline(body), name, editIndex)
}

// findIn resolves a section name against an outline already computed, so a
// caller holding one does not rebuild it.
func findIn(secs []Section, name string, editIndex int) (Section, error) {
	matches := matchSections(secs, name)
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return Section{}, newError(CodeSectionNotFound, editIndex,
			"Call action=outline for the document's headings and use one verbatim.",
			"no section titled %q; the document has %d heading(s): %s",
			name, len(secs), strings.Join(headingList(secs), ", "))
	default:
		return Section{}, newError(CodeAmbiguous, editIndex,
			"Use the full heading path to disambiguate, for example \"Report > Methodology\".",
			"%d sections match %q: %s", len(matches), name,
			strings.Join(pathList(matches), ", "))
	}
}

// matchSections returns the sections a name selects, preferring an exact path
// match so a fully-qualified name is never ambiguous. The name's two normalized
// forms are computed once rather than per section.
func matchSections(secs []Section, name string) []Section {
	wantPath := normalizePath(strings.TrimSpace(name))
	wantTitle := headingTitle(strings.TrimSpace(name))

	var byPath, byTitle []Section
	for _, s := range secs {
		if strings.EqualFold(normalizePath(s.Path), wantPath) {
			byPath = append(byPath, s)
		}
		if strings.EqualFold(s.Title, wantTitle) {
			byTitle = append(byTitle, s)
		}
	}
	if len(byPath) > 0 {
		return byPath
	}
	return byTitle
}

// normalizePath collapses the spacing around a path separator so
// "Report>Methodology" and "Report > Methodology" compare equal.
func normalizePath(p string) string {
	if !strings.Contains(p, ">") {
		return strings.TrimSpace(p)
	}
	parts := strings.Split(p, ">")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return strings.Join(parts, ">")
}

// headingTitle reduces a caller-supplied heading ("## Methodology") to its
// bare title, leaving an already-bare title untouched.
func headingTitle(name string) string {
	if _, title, ok := parseHeading(name); ok {
		return title
	}
	return strings.TrimSpace(name)
}

// headingList renders headings for a not-found hint.
func headingList(secs []Section) []string {
	out := make([]string, 0, len(secs))
	for _, s := range secs {
		out = append(out, fmt.Sprintf("%q (line %d)", s.Heading, s.Line))
	}
	return out
}

// pathList renders section paths for an ambiguity hint.
func pathList(secs []Section) []string {
	out := make([]string, 0, len(secs))
	for _, s := range secs {
		out = append(out, fmt.Sprintf("%q (line %d)", s.Path, s.Line))
	}
	return out
}

// ContentRequest selects which span of a document to read: the whole body when
// nothing is set, one named section, or an inclusive 1-based line range.
type ContentRequest struct {
	Section   string
	LineStart int
	LineEnd   int
}

// Content returns the requested span of body. The returned Section is
// populated only for a section read, so a caller can report which heading it
// resolved to.
func Content(body string, req ContentRequest) (string, Section, error) {
	switch {
	case req.Section != "":
		return SectionText(body, req.Section)
	case req.LineStart > 0 || req.LineEnd > 0:
		start := req.LineStart
		if start <= 0 {
			start = 1
		}
		text, err := LineRange(body, start, req.LineEnd)
		return text, Section{}, err
	default:
		return body, Section{}, nil
	}
}

// SectionText returns the text of the named section, heading included.
func SectionText(body, name string) (string, Section, error) {
	sec, err := FindSection(body, name, -1)
	if err != nil {
		return "", Section{}, err
	}
	return body[sec.start:sec.end], sec, nil
}

// LineRange returns lines [start, end] of body, 1-based and inclusive. end may
// exceed the line count, in which case the rest of the body is returned.
//
// It resolves the two byte offsets directly rather than materializing the
// document's lines, so reading ten lines out of a large asset stays cheap.
func LineRange(body string, start, end int) (string, error) {
	total := CountLines(body)
	if err := checkLineRange(start, end, total); err != nil {
		return "", err
	}
	if end <= 0 || end > total {
		end = total
	}
	return body[offsetOfLine(body, start):offsetOfLine(body, end+1)], nil
}

// checkLineRange rejects a range that cannot address any of the document.
func checkLineRange(start, end, total int) error {
	switch {
	case start < 1:
		return newError(CodeBadEdit, -1,
			"Line numbers are 1-based; pass start >= 1.", "invalid line_range start %d", start)
	case end > 0 && end < start:
		return newError(CodeBadEdit, -1,
			"Pass end >= start, or omit end to read to the end of the document.",
			"invalid line_range %d-%d", start, end)
	case start > total:
		return newError(CodeBadEdit, -1,
			fmt.Sprintf("The document has %d line(s); pass a start within it.", total),
			"line_range start %d is past the end of the document", start)
	default:
		return nil
	}
}

// offsetOfLine returns the byte offset where a 1-based line begins, or the
// document length when the line is past the end.
func offsetOfLine(body string, line int) int {
	off := 0
	for n := 1; n < line; n++ {
		i := strings.IndexByte(body[off:], '\n')
		if i < 0 {
			return len(body)
		}
		off += i + 1
	}
	return off
}

// lineAt returns the 1-based line number of a byte offset in body.
func lineAt(body string, offset int) int {
	if offset > len(body) {
		offset = len(body)
	}
	return strings.Count(body[:offset], newline) + 1
}

// sectionAt returns the heading of the innermost section containing offset, or
// "" when the offset sits above the first heading.
func sectionAt(secs []Section, offset int) string {
	heading := ""
	for _, s := range secs {
		if offset >= s.start && offset < s.end {
			heading = s.Heading
		}
	}
	return heading
}
