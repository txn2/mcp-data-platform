package textpatch

import (
	"strings"
	"unicode/utf8"
)

// Apply runs an ordered list of edits against body and returns the patched
// document with a per-edit report and a unified diff of the changed hunks.
//
// Every edit resolves against an in-memory copy and the first failure aborts
// the whole call, so a partially applied patch is never written. Edits apply in
// order against the evolving body, which means a later edit may anchor on text
// an earlier edit introduced.
func Apply(body string, edits []Edit, opts Options) (Result, error) {
	if err := checkEditCount(edits, opts); err != nil {
		return Result{}, err
	}

	current := body
	reports := make([]EditResult, 0, len(edits))
	for i, e := range edits {
		next, report, err := applyOne(current, e, i, opts)
		if err != nil {
			return Result{}, err
		}
		current = next
		reports = append(reports, report)
	}

	if err := checkResultSize(current, opts); err != nil {
		return Result{}, err
	}

	return Result{
		Body:      current,
		Edits:     reports,
		Diff:      UnifiedDiff(body, current, opts.DiffContext),
		SizeBytes: len(current),
		Lines:     CountLines(current),
	}, nil
}

// checkEditCount rejects an empty or oversized edit list.
func checkEditCount(edits []Edit, opts Options) error {
	if len(edits) == 0 {
		return newError(CodeBadEdit, -1,
			"Supply at least one edit in \"edits\".", "no edits supplied")
	}
	limit := opts.MaxEdits
	if limit <= 0 {
		limit = DefaultMaxEdits
	}
	if len(edits) > limit {
		return newError(CodeTooLarge, -1,
			"Split the change into several patch calls.",
			"%d edits exceeds the %d-edit limit", len(edits), limit)
	}
	return nil
}

// checkResultSize rejects a patched body over the caller's size cap.
func checkResultSize(body string, opts Options) error {
	if opts.MaxResultBytes > 0 && len(body) > opts.MaxResultBytes {
		return newError(CodeTooLarge, -1,
			"Reduce the size of the inserted text.",
			"patched content is %d bytes, over the %d-byte maximum", len(body), opts.MaxResultBytes)
	}
	return nil
}

// applyOne dispatches a single edit and reports what it did.
func applyOne(body string, e Edit, index int, opts Options) (string, EditResult, error) {
	switch e.op() {
	case OpAppend:
		return body + e.Text, EditResult{Index: index, Op: OpAppend, Matches: 1, Line: CountLines(body) + 1}, nil
	case OpPrepend:
		return e.Text + body, EditResult{Index: index, Op: OpPrepend, Matches: 1, Line: 1}, nil
	case OpReplaceSection:
		return applyReplaceSection(body, e, index)
	case OpMoveSection:
		return applyMoveSection(body, e, index)
	case OpReplace, OpInsertBefore, OpInsertAfter:
		return applyAnchored(body, e, index, opts)
	default:
		return "", EditResult{}, newError(CodeBadEdit, index,
			"Use one of: replace, insert_before, insert_after, replace_section, move_section, append, prepend.",
			"unknown op %q", e.Op)
	}
}

// applyAnchored runs the operations that position text relative to an anchor.
func applyAnchored(body string, e Edit, index int, opts Options) (string, EditResult, error) {
	window, err := editWindow(body, e, index)
	if err != nil {
		return "", EditResult{}, err
	}
	ms, err := resolveAnchor(body, window, e, index, opts)
	if err != nil {
		return "", EditResult{}, err
	}

	return rewriteSpans(body, ms, e), EditResult{
		Index:      index,
		Op:         e.op(),
		Matches:    len(ms.spans),
		Normalized: ms.normalized,
		Line:       lineAt(body, ms.spans[0].start),
	}, nil
}

// rewriteSpans replaces every matched span in one forward pass.
//
// The spans are ascending and non-overlapping, so the body is copied once
// rather than once per span: an occurrence:"all" edit over a large document
// costs the length of the document, not the length times the match count.
func rewriteSpans(body string, ms matchSet, e Edit) string {
	var out strings.Builder
	out.Grow(len(body))
	cursor := 0
	for i, s := range ms.spans {
		out.WriteString(body[cursor:s.start])
		out.WriteString(anchoredText(ms, i, e, body[s.start:s.end]))
		cursor = s.end
	}
	out.WriteString(body[cursor:])
	return out.String()
}

// anchoredText renders what one matched span becomes under the edit's
// operation. matched is the original text of the span, kept in place by the
// insert operations.
func anchoredText(ms matchSet, i int, e Edit, matched string) string {
	switch e.op() {
	case OpInsertBefore:
		return e.Text + matched
	case OpInsertAfter:
		return matched + e.Text
	default:
		return ms.replacements[i]
	}
}

// editWindow returns the byte range an anchored edit may search: the named
// section when one scopes the edit, otherwise the whole body.
func editWindow(body string, e Edit, index int) (span, error) {
	if e.Section == "" {
		return span{start: 0, end: len(body)}, nil
	}
	sec, err := FindSection(body, e.Section, index)
	if err != nil {
		return span{}, err
	}
	return span{start: sec.start, end: sec.end}, nil
}

// locateWindow is editWindow against an outline the caller already built.
func locateWindow(secs []Section, body string, e Edit) (span, error) {
	if e.Section == "" {
		return span{start: 0, end: len(body)}, nil
	}
	sec, err := findIn(secs, e.Section, -1)
	if err != nil {
		return span{}, err
	}
	return span{start: sec.start, end: sec.end}, nil
}

// applyReplaceSection swaps a whole section (its heading through the line
// before the next heading of the same or higher level) for the edit's text.
func applyReplaceSection(body string, e Edit, index int) (string, EditResult, error) {
	sec, err := requireSection(body, e, index)
	if err != nil {
		return "", EditResult{}, err
	}
	return body[:sec.start] + e.Text + body[sec.end:], EditResult{
		Index:   index,
		Op:      OpReplaceSection,
		Matches: 1,
		Line:    sec.Line,
	}, nil
}

// applyMoveSection relocates a whole section before or after another heading,
// or to the start or end of the document.
func applyMoveSection(body string, e Edit, index int) (string, EditResult, error) {
	sec, err := requireSection(body, e, index)
	if err != nil {
		return "", EditResult{}, err
	}
	moved := body[sec.start:sec.end]
	remainder := body[:sec.start] + body[sec.end:]

	at, err := moveDestination(remainder, e, index)
	if err != nil {
		return "", EditResult{}, err
	}
	out := remainder[:at] + moved + remainder[at:]
	return out, EditResult{
		Index:   index,
		Op:      OpMoveSection,
		Matches: 1,
		Line:    lineAt(out, at),
	}, nil
}

// requireSection resolves the section an edit names, rejecting an edit that
// names none.
func requireSection(body string, e Edit, index int) (Section, error) {
	if e.Section == "" {
		return Section{}, newError(CodeBadEdit, index,
			"Supply \"section\" naming the heading to act on, for example \"## Methodology\".",
			"%s needs a \"section\"", e.op())
	}
	return FindSection(body, e.Section, index)
}

// moveDestination resolves where a moved section is reinserted in the body it
// was removed from: before or after another heading, or at the document's
// start or end.
func moveDestination(remainder string, e Edit, index int) (int, error) {
	switch {
	case e.Position == PositionStart:
		return 0, nil
	case e.Position == PositionEnd:
		return len(remainder), nil
	case e.Position != "":
		return 0, newError(CodeBadEdit, index,
			"\"position\" must be start or end; use \"before\" or \"after\" to move relative to a heading.",
			"invalid position %q", e.Position)
	case e.Before != "":
		target, err := FindSection(remainder, e.Before, index)
		if err != nil {
			return 0, err
		}
		return target.start, nil
	case e.After != "":
		target, err := FindSection(remainder, e.After, index)
		if err != nil {
			return 0, err
		}
		return target.end, nil
	default:
		return 0, newError(CodeBadEdit, index,
			"Supply \"before\" or \"after\" naming another heading, or \"position\" set to start or end.",
			"move_section needs a destination")
	}
}

// Match is one hit reported by Locate: enough to copy a verbatim anchor out of
// the context window without reading the document.
type Match struct {
	// Line is the 1-based line the match starts on.
	Line int `json:"line"`
	// Offset is the byte offset of the match in the document.
	Offset int `json:"offset"`
	// Section is the heading of the innermost section containing the match,
	// empty when the match sits above the first heading.
	Section string `json:"section,omitempty"`
	// Context is the surrounding text, wide enough to paste into a "find"
	// anchor when the match alone would be ambiguous.
	Context string `json:"context"`
}

// LocateResult reports every hit for a search, and how many there were in
// total, which is the number that tells an agent whether an anchor is safe.
type LocateResult struct {
	// Count is the number of matches found, up to the match cap. Truncated
	// reports whether the scan or the reported list stopped short.
	Count   int     `json:"count"`
	Matches []Match `json:"matches"`
	// Truncated is true when more hits exist than the reported list carries,
	// either because Limit cut the list or the match cap stopped the scan.
	Truncated bool `json:"truncated,omitempty"`
}

// LocateQuery selects what to search for and how much of each hit to report.
type LocateQuery struct {
	// Find is a literal anchor; Pattern is a regex. Exactly one is required.
	Find    string
	Pattern string
	// Section scopes the search to one section when set.
	Section string
	// ContextBytes is how much text to include around each hit; 0 uses
	// DefaultContextBytes.
	ContextBytes int
	// Limit caps the reported matches; 0 uses DefaultLocateLimit. Count
	// always reports the true total up to the match cap.
	Limit int
}

// Locate defaults.
const (
	// DefaultContextBytes is how much surrounding text a match reports.
	DefaultContextBytes = 160
	// DefaultLocateLimit caps reported matches.
	DefaultLocateLimit = 20
)

// Locate finds every occurrence of a literal or regex anchor and reports where
// each one sits, what section encloses it, and a context window wide enough to
// copy verbatim into a patch anchor.
//
// The count is the point: an agent that locates first never hits
// PATCH_AMBIGUOUS, and an agent that wanted to change every occurrence learns
// how many there are before deciding.
func Locate(body string, q LocateQuery, opts Options) (LocateResult, error) {
	e := Edit{Find: q.Find, Pattern: q.Pattern, Section: q.Section}
	if err := validateAnchor(e, -1); err != nil {
		return LocateResult{}, err
	}
	// The outline answers both the section scope and each match's enclosing
	// heading, so it is built once rather than once per question.
	secs := Outline(body)
	window, err := locateWindow(secs, body, e)
	if err != nil {
		return LocateResult{}, err
	}

	// Locate reports every hit rather than narrowing to one, so it scans
	// directly instead of going through the occurrence selector: an anchor
	// with more hits than the cap is the case an agent most needs answered,
	// not refused.
	matches, _, err := anchorMatches(body[window.start:window.end], e, -1, opts)
	if err != nil {
		return LocateResult{}, err
	}
	capped := len(matches) > maxMatches(opts)
	if capped {
		matches = matches[:maxMatches(opts)]
	}
	spans := shiftSpans(matches, window.start)

	limit := q.Limit
	if limit <= 0 {
		limit = DefaultLocateLimit
	}
	result := LocateResult{
		Count:     len(spans),
		Matches:   []Match{},
		Truncated: capped || len(spans) > limit,
	}
	for i, s := range spans {
		if i >= limit {
			break
		}
		result.Matches = append(result.Matches, Match{
			Line:    lineAt(body, s.start),
			Offset:  s.start,
			Section: sectionAt(secs, s.start),
			Context: contextWindow(body, s, q.ContextBytes),
		})
	}
	return result, nil
}

// contextWindow returns the text around a match, expanded to rune boundaries
// so the window is always valid UTF-8.
func contextWindow(body string, s span, width int) string {
	if width <= 0 {
		width = DefaultContextBytes
	}
	pad := width / 2
	start, end := max(0, s.start-pad), min(len(body), s.end+pad)
	for start > 0 && !utf8.RuneStart(body[start]) {
		start--
	}
	for end < len(body) && !utf8.RuneStart(body[end]) {
		end++
	}
	return body[start:end]
}
