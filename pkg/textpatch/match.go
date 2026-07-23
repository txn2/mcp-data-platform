package textpatch

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// span is a byte range in a body, end exclusive.
type span struct {
	start int
	end   int
}

// rawMatch is one anchor hit inside the searched subject: its span in the
// subject and, for a regex anchor, the submatch index slice used to expand
// capture references in the replacement.
type rawMatch struct {
	at  span
	sub []int
}

// matchSet is the resolved anchor of one edit: where in the body it changes and
// what each span becomes. Regex captures are already expanded, so the applier
// never needs the compiled pattern or the matched subject.
type matchSet struct {
	// spans are the body-relative ranges to change, in ascending order.
	spans []span
	// replacements holds the text each span becomes under a replace, parallel
	// to spans. The insert operations position their own text and ignore it.
	replacements []string
	// normalized is true when the exact anchor failed and the hit was found
	// only after CRLF and trailing-whitespace normalization.
	normalized bool
}

// resolveAnchor finds every span an edit's anchor selects inside window,
// retrying once under normalization when the exact match finds nothing.
//
// The normalized retry maps CRLF to LF and ignores trailing whitespace on each
// line. Nothing beyond that: a plausible-but-wrong edit applied silently to a
// user's document is worse than a rejection the agent can correct.
func resolveAnchor(body string, window span, e Edit, index int, opts Options) (matchSet, error) {
	if err := validateAnchor(e, index); err != nil {
		return matchSet{}, err
	}

	text := body[window.start:window.end]
	exact, re, err := matchWindow(text, e, index, opts)
	if err != nil {
		return matchSet{}, err
	}
	if len(exact) > 0 {
		return matchSet{
			spans:        shiftSpans(exact, window.start),
			replacements: expandAll(exact, re, text, e.Replace),
		}, nil
	}

	norm, idx := normalizeText(text)
	relaxed, re, err := matchWindow(norm, normalizedEdit(e), index, opts)
	if err != nil {
		return matchSet{}, err
	}
	if len(relaxed) == 0 {
		return matchSet{}, noMatchError(e, index)
	}
	return matchSet{
		spans:        mapSpans(relaxed, idx, window.start),
		replacements: expandAll(relaxed, re, norm, e.Replace),
		normalized:   true,
	}, nil
}

// expandAll renders the replacement for every matched span. A literal anchor
// replaces literally; a regex anchor expands $1-style capture references
// against the same engine and subject that produced the match.
func expandAll(matches []rawMatch, re *regexp.Regexp, subject, template string) []string {
	out := make([]string, len(matches))
	for i, m := range matches {
		if re == nil {
			out[i] = template
			continue
		}
		out[i] = string(re.ExpandString(nil, template, subject, m.sub))
	}
	return out
}

// validateAnchor rejects an anchored edit that names no anchor or names both.
func validateAnchor(e Edit, index int) error {
	if e.Find == "" && e.Pattern == "" {
		return newError(CodeBadEdit, index,
			"Supply either \"find\" (literal anchor text) or \"pattern\" (regex).",
			"%s needs an anchor", e.op())
	}
	if e.Find != "" && e.Pattern != "" {
		return newError(CodeBadEdit, index,
			"Supply exactly one of \"find\" or \"pattern\".",
			"%s carries both \"find\" and \"pattern\"", e.op())
	}
	return nil
}

// normalizedEdit returns the edit with its literal anchor normalized the same
// way the body was, so the retry compares like with like.
func normalizedEdit(e Edit) Edit {
	if e.Find != "" {
		e.Find, _ = normalizeText(e.Find)
	}
	return e
}

// shiftSpans rebases subject-relative hits onto the whole body.
func shiftSpans(matches []rawMatch, base int) []span {
	out := make([]span, len(matches))
	for i, m := range matches {
		out[i] = span{start: m.at.start + base, end: m.at.end + base}
	}
	return out
}

// mapSpans converts hits found in normalized text back to body offsets using
// the normalized-to-original index map, rebased by the window's start. An end
// offset is taken one past the last matched byte so trailing whitespace the
// normalizer dropped stays outside the replaced span.
func mapSpans(matches []rawMatch, idx []int32, base int) []span {
	out := make([]span, len(matches))
	for i, m := range matches {
		start := int(idx[m.at.start])
		end := start
		if m.at.end > m.at.start {
			end = int(idx[m.at.end-1]) + 1
		}
		out[i] = span{start: start + base, end: end + base}
	}
	return out
}

// matchWindow finds the hits an edit's anchor selects within text and applies
// the occurrence selector, returning the compiled pattern so the caller can
// expand capture references against the same engine.
func matchWindow(text string, e Edit, index int, opts Options) ([]rawMatch, *regexp.Regexp, error) {
	all, re, err := anchorMatches(text, e, index, opts)
	if err != nil {
		return nil, nil, err
	}
	if len(all) == 0 {
		return nil, re, nil
	}
	picked, err := selectOccurrence(all, e, index, opts)
	if err != nil {
		return nil, nil, err
	}
	return picked, re, nil
}

// anchorMatches finds every hit of an edit's anchor in text, scanning at most
// one past the match cap so the caller can tell a capped scan from a complete
// one. It applies no occurrence selection: Locate reports every hit, while an
// edit narrows the set afterwards.
func anchorMatches(text string, e Edit, index int, opts Options) ([]rawMatch, *regexp.Regexp, error) {
	if e.Pattern == "" {
		return literalMatches(text, e.Find, maxMatches(opts)+1), nil, nil
	}
	re, err := compilePattern(e.Pattern, index, opts)
	if err != nil {
		return nil, nil, err
	}
	var all []rawMatch
	for _, sub := range re.FindAllStringSubmatchIndex(text, maxMatches(opts)+1) {
		all = append(all, rawMatch{at: span{start: sub[0], end: sub[1]}, sub: sub})
	}
	return all, re, nil
}

// literalMatches returns up to limit non-overlapping occurrences of needle.
func literalMatches(text, needle string, limit int) []rawMatch {
	var out []rawMatch
	if needle == "" {
		return nil
	}
	for off := 0; off+len(needle) <= len(text) && len(out) < limit; {
		i := strings.Index(text[off:], needle)
		if i < 0 {
			break
		}
		start := off + i
		out = append(out, rawMatch{at: span{start: start, end: start + len(needle)}})
		off = start + len(needle)
	}
	return out
}

// compilePattern compiles a regex anchor under the pattern-length cap. Go's
// regexp is RE2, so match time is linear in the input and a pathological
// pattern cannot hang the server; the cap bounds compilation cost.
func compilePattern(pattern string, index int, opts Options) (*regexp.Regexp, error) {
	limit := opts.MaxPatternLen
	if limit <= 0 {
		limit = DefaultMaxPatternLen
	}
	if len(pattern) > limit {
		return nil, newError(CodeBadPattern, index,
			"Shorten the pattern, or anchor the edit with a literal \"find\" instead.",
			"pattern is %d bytes, over the %d-byte limit", len(pattern), limit)
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, newError(CodeBadPattern, index,
			"Fix the regular expression; it must be valid Go RE2 syntax.",
			"pattern does not compile: %v", err)
	}
	return re, nil
}

// maxMatches returns the effective cap on spans one edit may touch.
func maxMatches(opts Options) int {
	if opts.MaxMatches > 0 {
		return opts.MaxMatches
	}
	return DefaultMaxMatches
}

// selectOccurrence narrows the matched hits by the edit's occurrence selector.
// An absent selector requires the anchor to be unique, so ambiguity is never
// resolved silently.
func selectOccurrence(all []rawMatch, e Edit, index int, opts Options) ([]rawMatch, error) {
	switch e.Occurrence {
	case "":
		if len(all) > 1 {
			return nil, newError(CodeAmbiguous, index,
				"Lengthen the anchor so it is unique, scope it with \"section\", or set \"occurrence\" to first, last, all, or a 1-based index.",
				"anchor matches %d spans; expected exactly 1", len(all))
		}
		return all, nil
	case OccurrenceFirst:
		return all[:1], nil
	case OccurrenceLast:
		return all[len(all)-1:], nil
	case OccurrenceAll:
		if len(all) > maxMatches(opts) {
			return nil, newError(CodeTooLarge, index,
				"Narrow the anchor so it matches fewer spans.",
				"anchor matches more than the %d-span limit", maxMatches(opts))
		}
		return all, nil
	default:
		return selectNth(all, e.Occurrence, index)
	}
}

// selectNth resolves a 1-based numeric occurrence selector.
func selectNth(all []rawMatch, selector string, index int) ([]rawMatch, error) {
	n, err := strconv.Atoi(selector)
	if err != nil || n < 1 {
		return nil, newError(CodeBadEdit, index,
			"\"occurrence\" must be first, last, all, or a 1-based integer.",
			"invalid occurrence %q", selector)
	}
	if n > len(all) {
		return nil, newError(CodeNoMatch, index,
			"Call action=locate to count the matches before choosing an occurrence.",
			"occurrence %d requested but the anchor matches only %d span(s)", n, len(all))
	}
	return all[n-1 : n], nil
}

// noMatchError reports an anchor that matched nothing even after the
// normalized retry.
func noMatchError(e Edit, index int) *Error {
	anchor, kind := e.Find, "text"
	if e.Pattern != "" {
		anchor, kind = e.Pattern, "pattern"
	}
	scope := ""
	if e.Section != "" {
		scope = fmt.Sprintf(" within section %q", e.Section)
	}
	return newError(CodeNoMatch, index,
		"Call action=locate and copy the anchor verbatim from a match's context window. Whitespace and CRLF differences were already retried.",
		"anchor %s %q matched nothing%s", kind, truncate(anchor, anchorEchoLimit), scope)
}

// anchorEchoLimit caps how much of a failing anchor an error echoes back.
const anchorEchoLimit = 120

// truncate shortens a string for an error message.
func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "..."
}

// maxNormalizeBytes is the largest body the normalized retry maps, chosen so
// every offset fits an int32. Bodies reaching the tools are capped far below it.
const maxNormalizeBytes = math.MaxInt32 - 1

// identityOffsets returns the offset map of an unmodified body of length n.
func identityOffsets(n int) []int32 {
	out := make([]int32, n+1)
	for i := range out {
		out[i] = int32(i)
	}
	return out
}

// normalizeText maps CRLF to LF and drops trailing spaces and tabs from each
// line. It returns the normalized text and an index map whose i-th entry is
// the original offset of the i-th normalized byte, with a final entry holding
// the original length so an end offset always maps.
//
// Normalization only removes bytes, never inserts, which is what makes the
// index map a simple ascending list. The map is int32 because bodies are size-
// capped far below 2 GB, which halves what is already the retry path's largest
// allocation.
func normalizeText(s string) (normalized string, offsets []int32) {
	if len(s) > maxNormalizeBytes {
		// Unreachable through the tools, whose content ceilings are orders of
		// magnitude below this; the guard is what makes the int32 offsets
		// provably in range rather than merely expected to be.
		return s, identityOffsets(len(s))
	}
	n := normalizer{idx: make([]int32, 0, len(s)+1)}
	n.out.Grow(len(s))
	for i := range len(s) {
		n.step(s, int32(i)) // #nosec G115 -- len(s) <= maxNormalizeBytes, checked above
	}
	// Same bound: the final entry maps an end offset back to the original.
	return n.out.String(), append(n.idx, int32(len(s))) // #nosec G115 -- bounded above
}

// normalizer accumulates the normalized text and its offset map. Whitespace is
// held back rather than written, because whether it is trailing (dropped) or
// interior (kept) is only known once the next non-space byte arrives.
type normalizer struct {
	out     strings.Builder
	idx     []int32
	pending []int32
}

// step folds the byte at offset i into the normalized text.
func (n *normalizer) step(s string, i int32) {
	switch c := s[i]; {
	case c == '\r' && int(i)+1 < len(s) && s[i+1] == '\n':
		n.pending = n.pending[:0] // the CR of a CRLF disappears
	case c == '\n':
		n.pending = n.pending[:0] // whitespace before a newline was trailing
		n.write(s, i)
	case c == ' ' || c == '\t':
		n.pending = append(n.pending, i)
	default:
		for _, p := range n.pending {
			n.write(s, p)
		}
		n.pending = n.pending[:0]
		n.write(s, i)
	}
}

// write emits the byte at offset i and records where it came from.
func (n *normalizer) write(s string, i int32) {
	n.out.WriteByte(s[i])
	n.idx = append(n.idx, i)
}
