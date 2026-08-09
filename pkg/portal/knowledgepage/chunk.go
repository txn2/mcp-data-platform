package knowledgepage

import (
	"strings"
	"unicode/utf8"
)

// identityBudgetShare is the largest share of a chunk the per-chunk identity
// (title + tags) may consume before it is dropped and the raw body is chunked
// instead. Expressed as a divisor of the budget — the identity may take at most
// half — so the rule scales with whatever input budget the provider reports and a
// pathological title can never starve the content it is meant to identify.
const identityBudgetShare = 2

// minViableChunkBytes is the smallest input budget worth splitting to. Below it,
// splitting stops being meaningful (a chunk would hold a few words) and the hard
// cut can no longer be guaranteed to make progress: after the identity share is
// reserved, the remaining budget must still exceed the widest UTF-8 rune, or a
// rune-boundary cut could produce an empty piece and the split would not
// terminate. A provider reporting a budget this small is misconfigured; the text
// is handed over whole and its own cap applies, which is the same outcome the
// platform had before pages were chunked at all.
const minViableChunkBytes = 64

// IndexChunks splits a page's indexed text into the embeddable units its vector
// index is built from: one chunk per call to the embedding provider, each within
// maxBytes so NO part of the page is ever trimmed away before it is embedded
// (#1242). A page whose composed text already fits yields exactly one chunk, so
// the common case is unchanged from the single-vector design.
//
// Every chunk is composed through IndexText with the same title and tags, so each
// one carries the page's identity and lives in the same text space as a query
// embedded over IndexText. Only the body is split. The split prefers markdown
// section boundaries (an ATX heading starts a new unit), falls back to paragraph
// boundaries inside an oversized section, and finally to a hard cut on a UTF-8
// rune boundary, so a chunk boundary lands on a topic edge wherever the author's
// structure offers one.
//
// A maxBytes below minViableChunkBytes (including a non-positive one) disables
// splitting: one chunk with the whole text, because there is no budget worth
// respecting. A page with no indexable text at all yields no chunks, which the
// index consumer treats as a converged unit with no vectors.
func IndexChunks(title, body string, tags []string, maxBytes int) []string {
	full := IndexText(title, body, tags)
	if strings.TrimSpace(full) == "" {
		return nil
	}
	if maxBytes < minViableChunkBytes || len(full) <= maxBytes {
		return []string{full}
	}

	// Budget for the body slice each chunk carries: the composed text is the
	// identity (title + tags) plus one separator plus the slice, so subtracting
	// the identity's own composition bounds every chunk at maxBytes.
	compose := func(part string) string { return IndexText(title, part, tags) }
	budget := maxBytes - len(IndexText(title, "", tags)) - 1
	if budget < maxBytes/identityBudgetShare {
		compose = func(part string) string { return part }
		budget = maxBytes
	}

	parts := splitBody(body, budget)
	if len(parts) == 0 {
		// The body is empty (or whitespace) yet the composed text is over
		// budget: the identity alone is oversized, so there is nothing to
		// split and the provider's own cap is the only bound left.
		return []string{truncateOnRune(full, maxBytes)}
	}
	chunks := make([]string, 0, len(parts))
	for _, part := range parts {
		chunks = append(chunks, compose(part))
	}
	return chunks
}

// splitBody cuts body into pieces of at most budget bytes each, preferring
// section boundaries, then paragraph boundaries, then a hard rune-boundary cut,
// and packs consecutive pieces back together while they fit. Whitespace-only
// pieces are dropped (they carry no signal and would waste a provider call).
func splitBody(body string, budget int) []string {
	sections := splitSections(body)
	units := make([]string, 0, len(sections))
	for _, section := range sections {
		units = append(units, splitOversized(section, budget)...)
	}
	return packUnits(units, budget)
}

// splitSections cuts body before each ATX markdown heading, so a section and its
// prose stay in one unit wherever the page is structured. Every byte of body
// lands in exactly one section (concatenating them reproduces body), which is
// what keeps the split lossless. Headings inside fenced code blocks are ignored,
// the same rule countMarkdownHeadings applies.
func splitSections(body string) []string {
	var (
		sections []string
		start    int
		offset   int
		inFence  bool
	)
	for line := range strings.SplitSeq(body, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~"):
			inFence = !inFence
		case !inFence && isATXHeading(trimmed) && offset > start:
			sections = append(sections, body[start:offset])
			start = offset
		}
		offset += len(line) + 1 // +1 for the "\n" SplitSeq consumed
	}
	if start < len(body) {
		sections = append(sections, body[start:])
	}
	return sections
}

// splitOversized returns s unchanged when it fits the budget, else cuts it at
// paragraph boundaries and, for a paragraph that still does not fit, at a hard
// rune boundary. Every returned piece is at most budget bytes.
func splitOversized(s string, budget int) []string {
	if len(s) <= budget {
		return []string{s}
	}
	var out []string
	for _, para := range splitParagraphs(s) {
		if len(para) <= budget {
			out = append(out, para)
			continue
		}
		out = append(out, hardSplit(para, budget)...)
	}
	return out
}

// splitParagraphs cuts s after each blank line, keeping the separator with the
// paragraph that precedes it so the pieces concatenate back to s.
func splitParagraphs(s string) []string {
	const sep = "\n\n"
	var out []string
	for {
		i := strings.Index(s, sep)
		if i < 0 {
			if s != "" {
				out = append(out, s)
			}
			return out
		}
		cut := i + len(sep)
		out = append(out, s[:cut])
		s = s[cut:]
	}
}

// hardSplit cuts s into budget-sized pieces, backing each cut off to the last
// newline or space within the budget (so a word is not severed) and then to a
// UTF-8 rune boundary (so a multi-byte rune is never split).
func hardSplit(s string, budget int) []string {
	var out []string
	for len(s) > budget {
		cut := truncateOnRune(s, budget)
		if i := strings.LastIndexAny(cut, " \n"); i > 0 {
			cut = cut[:i+1]
		}
		out = append(out, cut)
		s = s[len(cut):]
	}
	if s != "" {
		out = append(out, s)
	}
	return out
}

// packUnits concatenates consecutive units while the result stays within budget,
// so a page of many small sections does not pay one provider call per heading.
// Whitespace-only units are dropped.
func packUnits(units []string, budget int) []string {
	var (
		out     []string
		current strings.Builder
	)
	flush := func() {
		if strings.TrimSpace(current.String()) != "" {
			out = append(out, current.String())
		}
		current.Reset()
	}
	for _, u := range units {
		if strings.TrimSpace(u) == "" {
			continue
		}
		if current.Len() > 0 && current.Len()+len(u) > budget {
			flush()
		}
		current.WriteString(u)
	}
	flush()
	return out
}

// truncateOnRune returns the longest prefix of s within maxBytes that ends on a
// UTF-8 rune boundary.
func truncateOnRune(s string, maxBytes int) string {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
