package knowledgepage

import (
	"strings"

	"github.com/txn2/mcp-data-platform/internal/textchunk"
)

// identityBudgetShare is the largest share of a chunk the per-chunk identity
// (title + tags) may consume before it is dropped and the raw body is chunked
// instead. Expressed as a divisor of the budget — the identity may take at most
// half — so the rule scales with whatever input budget the provider reports and a
// pathological title can never starve the content it is meant to identify.
const identityBudgetShare = 2

// minViableChunkBytes is the smallest input budget worth splitting to; see
// textchunk.MinViableChunkBytes. After the identity share is reserved, the
// remaining budget still exceeds the widest UTF-8 rune.
const minViableChunkBytes = textchunk.MinViableChunkBytes

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
		return []string{textchunk.TruncateOnRune(full, maxBytes)}
	}
	chunks := make([]string, 0, len(parts))
	for _, part := range parts {
		chunks = append(chunks, compose(part))
	}
	return chunks
}

// splitBody cuts body into pieces of at most budget bytes each, preferring
// markdown section boundaries (textchunk.SplitText does the rest).
func splitBody(body string, budget int) []string {
	return textchunk.SplitText(body, budget, splitSections)
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
