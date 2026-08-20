// Package textpatch applies anchored edits to a text document and reports what
// changed, without knowing what kind of document it is. Prompts, assets, and
// (later) knowledge pages all hand it a body and get a body back, so one edit
// grammar serves every content kind.
//
// The grammar is "find this exact text, put that text in its place": an anchor
// must resolve to exactly one span unless the caller opts into an occurrence,
// and an anchor that matches nothing or matches ambiguously aborts the whole
// call rather than applying a guess. Line numbers are read output only and are
// never accepted as an edit anchor, because a line number is stale the moment
// an earlier edit lands.
package textpatch

import "fmt"

// Edit operations. An edit with no Op is a Replace.
const (
	// OpReplace swaps the span matched by Find (literal) or Pattern (regex)
	// for Replace. An empty Replace deletes the matched span.
	OpReplace = "replace"
	// OpInsertBefore places Text immediately before the matched anchor,
	// leaving the anchor in place.
	OpInsertBefore = "insert_before"
	// OpInsertAfter places Text immediately after the matched anchor,
	// leaving the anchor in place.
	OpInsertAfter = "insert_after"
	// OpReplaceSection replaces a whole markdown section (its heading
	// through the next heading of the same or higher level) with Text.
	OpReplaceSection = "replace_section"
	// OpReplaceContent replaces the interior of a selector-addressed element
	// with Text, leaving the element's own tags exactly as written. It is the
	// data-island operation: a region's markup stays put while what it holds is
	// swapped.
	OpReplaceContent = "replace_content"
	// OpMoveSection relocates a whole section before or after another
	// heading, or to the start or end of the document.
	OpMoveSection = "move_section"
	// OpAppend adds Text at the end of the body; no anchor needed.
	OpAppend = "append"
	// OpPrepend adds Text at the start of the body; no anchor needed.
	OpPrepend = "prepend"
)

// Occurrence selectors accepted by Edit.Occurrence in addition to a 1-based
// decimal index. Absent means "the anchor must be unique".
const (
	OccurrenceFirst = "first"
	OccurrenceLast  = "last"
	OccurrenceAll   = "all"
)

// Move destinations accepted by Edit.Position for OpMoveSection.
const (
	PositionStart = "start"
	PositionEnd   = "end"
)

// Error codes. Every failure an agent can correct carries one of these, so the
// caller can branch without parsing prose.
const (
	CodeNoMatch         = "PATCH_NO_MATCH"
	CodeAmbiguous       = "PATCH_AMBIGUOUS"
	CodeStaleBase       = "PATCH_STALE_BASE"
	CodeNotText         = "PATCH_NOT_TEXT"
	CodeTooLarge        = "PATCH_TOO_LARGE"
	CodeSectionNotFound = "PATCH_SECTION_NOT_FOUND"
	CodeBadPattern      = "PATCH_BAD_PATTERN"
	CodeBadEdit         = "PATCH_BAD_EDIT"
	// CodeBadSelector reports a CSS selector that does not parse.
	CodeBadSelector = "PATCH_BAD_SELECTOR"
	// CodeNoStructure reports a document whose content type has no addressable
	// structure, so section- and selector-scoped verbs do not apply to it.
	CodeNoStructure = "PATCH_NO_STRUCTURE"
	// CodeUnresolvedMarkup reports markup that could not be resolved into a
	// reliable element tree, so no element span was trusted.
	CodeUnresolvedMarkup = "PATCH_UNRESOLVED_MARKUP"
)

// Limits bounding a single Apply call. Zero means the package default.
const (
	// DefaultMaxEdits caps how many edits one call may carry.
	DefaultMaxEdits = 100
	// DefaultMaxPatternLen caps the length of a regex source string. RE2
	// match time is linear, so this bounds compilation cost rather than
	// guarding against catastrophic backtracking.
	DefaultMaxPatternLen = 512
	// DefaultMaxMatches caps how many spans a single occurrence:"all" edit
	// may touch.
	DefaultMaxMatches = 1000
)

// Edit is one anchored change. Field use depends on Op:
//
//	replace          Find or Pattern -> Replace (empty Replace deletes)
//	insert_before    Find or Pattern, Text placed before the anchor
//	insert_after     Find or Pattern, Text placed after the anchor
//	replace_section  Section -> Text
//	replace_content  Selector -> Text (the element's interior; its tags stay)
//	move_section     Section, plus Before, After, or Position
//	append/prepend   Text only
//
// Section additionally scopes the anchor search on replace, insert_before, and
// insert_after, which is how a phrase repeated across a document becomes
// unambiguous without quoting a long anchor. Selector does the same on an HTML,
// JSX or SVG document, naming an element by CSS selector; it is the precise form
// (a balanced element span) where section is the convenient one (a heading).
type Edit struct {
	Op         string `json:"op,omitempty"`
	Find       string `json:"find,omitempty"`
	Pattern    string `json:"pattern,omitempty"`
	Replace    string `json:"replace,omitempty"`
	Text       string `json:"text,omitempty"`
	Section    string `json:"section,omitempty"`
	Selector   string `json:"selector,omitempty"`
	Occurrence string `json:"occurrence,omitempty"`
	Before     string `json:"before,omitempty"`
	After      string `json:"after,omitempty"`
	Position   string `json:"position,omitempty"`
}

// region returns the section/selector/occurrence an edit names as a region.
func (e Edit) region() regionRequest {
	return regionRequest{section: e.Section, selector: e.Selector, occurrence: e.Occurrence}
}

// op returns the effective operation, defaulting an absent Op to replace.
func (e Edit) op() string {
	if e.Op == "" {
		return OpReplace
	}
	return e.Op
}

// Options bound one Apply call. A zero Options uses the package defaults and
// imposes no result-size limit.
type Options struct {
	// Syntax is the region grammar for section- and selector-scoped edits. The
	// zero value is markdown, so an Options that sets no syntax keeps the
	// markdown behavior #1033 shipped.
	Syntax Syntax
	// MaxEdits caps the number of edits; 0 uses DefaultMaxEdits.
	MaxEdits int
	// MaxPatternLen caps regex source length; 0 uses DefaultMaxPatternLen.
	MaxPatternLen int
	// MaxMatches caps spans touched by one occurrence:"all" edit; 0 uses
	// DefaultMaxMatches.
	MaxMatches int
	// MaxResultBytes rejects a result body larger than this; 0 means no cap.
	MaxResultBytes int
	// DiffContext is the number of unchanged context lines around each diff
	// hunk; 0 uses defaultDiffContext.
	DiffContext int
}

// EditResult reports the outcome of one applied edit. It deliberately carries
// no content: the point of patching is that the body never crosses the wire.
type EditResult struct {
	// Index is the edit's position in the request list.
	Index int `json:"index"`
	// Op is the effective operation that ran.
	Op string `json:"op"`
	// Matches is how many spans the edit changed (1 unless occurrence:"all").
	Matches int `json:"matches"`
	// Normalized is true when the exact anchor failed and the edit resolved
	// only after CRLF and trailing-whitespace normalization.
	Normalized bool `json:"normalized,omitempty"`
	// Line is the 1-based line in the pre-edit body where the change landed.
	Line int `json:"line"`
}

// Result is the outcome of an Apply call.
type Result struct {
	// Body is the patched document.
	Body string `json:"-"`
	// Edits reports each edit in request order.
	Edits []EditResult `json:"edits"`
	// Diff is a unified diff of the changed hunks only.
	Diff string `json:"diff"`
	// SizeBytes is the length of the patched body.
	SizeBytes int `json:"size_bytes"`
	// Lines is the line count of the patched body.
	Lines int `json:"lines"`
}

// Error is a corrective, self-describing patch failure. Code is stable and
// machine-readable; Hint tells the agent how to recover.
type Error struct {
	Code string
	// EditIndex is the 0-based index of the failing edit, or -1 when the
	// failure is not attributable to one edit.
	EditIndex int
	Message   string
	Hint      string
}

// Error implements error, naming the failing edit when there is one.
func (e *Error) Error() string {
	if e.EditIndex >= 0 {
		return fmt.Sprintf("edit %d: %s", e.EditIndex, e.Message)
	}
	return e.Message
}

// StaleBaseError refuses a patch whose base_version no longer matches the
// stored content, so a concurrent edit is never silently overwritten.
func StaleBaseError(base, current int) *Error {
	return newError(CodeStaleBase, -1,
		"Re-read the content (action=get_content or stats) and re-anchor the patch against the current version.",
		"base_version %d does not match the current version %d", base, current)
}

// NotTextError refuses a verb that only makes sense on text against binary
// content, rather than corrupting it or dumping it as garbage.
func NotTextError(contentType string) *Error {
	return newError(CodeNotText, -1,
		"These verbs are text-only. Replace the whole object with a full content update instead.",
		"content type %q is not textual", contentType)
}

// newError builds a patch error attributed to one edit.
func newError(code string, index int, hint, format string, args ...any) *Error {
	return &Error{
		Code:      code,
		EditIndex: index,
		Message:   fmt.Sprintf(format, args...),
		Hint:      hint,
	}
}
