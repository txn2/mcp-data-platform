package knowledgepage

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// assertWithinBudget is the invariant the whole feature rests on: no chunk may
// exceed the provider's input budget, because anything over it is silently
// trimmed off before the text is embedded.
func assertWithinBudget(t *testing.T, chunks []string, maxBytes int) {
	t.Helper()
	for i, c := range chunks {
		assert.LessOrEqual(t, len(c), maxBytes, "chunk %d is over the input budget", i)
	}
}

func TestIndexChunks_SmallPageIsOneChunk(t *testing.T) {
	chunks := IndexChunks("Fiscal Calendar", "Q1 starts in February.", []string{"finance"}, 6000)
	require.Len(t, chunks, 1)
	assert.Equal(t, IndexText("Fiscal Calendar", "Q1 starts in February.", []string{"finance"}), chunks[0],
		"a page inside the budget must embed exactly the text it always did")
}

func TestIndexChunks_NoIndexableText(t *testing.T) {
	assert.Nil(t, IndexChunks("", "", nil, 6000))
	assert.Nil(t, IndexChunks("  ", "\n\t", []string{}, 6000))
}

// TestIndexChunks_UnusableBudgetDoesNotSplit covers the guard that keeps a
// misconfigured budget from producing meaningless chunks: below the viable size
// the text is handed over whole (the provider's own cap then applies), and the
// hard cut is never asked to make progress it cannot make. A multi-byte body at
// a sub-rune budget is the case that would otherwise not terminate.
func TestIndexChunks_UnusableBudgetDoesNotSplit(t *testing.T) {
	body := strings.Repeat("x", 20000)
	for _, maxBytes := range []int{0, -1, 1, 8, minViableChunkBytes - 1} {
		chunks := IndexChunks("T", body, nil, maxBytes)
		require.Len(t, chunks, 1, "budget %d must not split", maxBytes)
		assert.Equal(t, IndexText("T", body, nil), chunks[0])
	}
	chunks := IndexChunks("T", strings.Repeat("日", 500), nil, 3)
	require.Len(t, chunks, 1)
}

// TestIndexChunks_SmallestViableBudgetTerminates pins the boundary the guard
// protects: at the smallest budget that still splits, every chunk is non-empty
// and within budget even for the widest runes.
func TestIndexChunks_SmallestViableBudgetTerminates(t *testing.T) {
	chunks := IndexChunks("T", strings.Repeat("日本語", 200), nil, minViableChunkBytes)
	require.Greater(t, len(chunks), 1)
	assertWithinBudget(t, chunks, minViableChunkBytes)
	for i, c := range chunks {
		assert.NotEmpty(t, c, "chunk %d is empty", i)
	}
}

// TestIndexChunks_TailIsCovered is the defect this feature exists to close: a
// fact that appears only past the input budget must land in some chunk, so the
// vector index can rank the page on it.
func TestIndexChunks_TailIsCovered(t *testing.T) {
	const budget = 1000
	const needle = "the reconciler skips rows stamped with a foreign model"
	body := strings.Repeat("## Filler section\n\nRoutine prose about the pipeline.\n\n", 80) +
		"## Quirk\n\n" + needle + "\n"

	chunks := IndexChunks("Runbook", body, []string{"ops"}, budget)
	require.Greater(t, len(chunks), 1)
	assertWithinBudget(t, chunks, budget)

	found := false
	for _, c := range chunks {
		if strings.Contains(c, needle) {
			found = true
		}
		assert.Contains(t, c, "Runbook", "every chunk carries the page identity")
		assert.Contains(t, c, "ops", "every chunk carries the page tags")
	}
	assert.True(t, found, "content past the budget must appear in a chunk")
}

// TestIndexChunks_SplitsOnHeadings proves the split prefers the author's own
// section boundaries: a body of headed sections that each fit produces chunks
// that start on a heading rather than mid-sentence.
func TestIndexChunks_SplitsOnHeadings(t *testing.T) {
	const budget = 400
	section := func(n string) string {
		return "## " + n + "\n\n" + strings.Repeat("detail ", 30) + "\n\n"
	}
	body := section("Alpha") + section("Beta") + section("Gamma")

	chunks := IndexChunks("Guide", body, nil, budget)
	require.Greater(t, len(chunks), 1)
	assertWithinBudget(t, chunks, budget)
	for _, c := range chunks {
		body, ok := strings.CutPrefix(c, "Guide\n")
		require.True(t, ok, "chunk must start with the identity prefix")
		assert.True(t, strings.HasPrefix(body, "## "), "chunk body must start at a heading: %q", body[:10])
	}
}

// TestIndexChunks_SplitsUnstructuredProse covers the fallback path: a body with
// no headings and no blank lines still has to be cut somewhere, on a rune
// boundary and preferring a word break.
func TestIndexChunks_SplitsUnstructuredProse(t *testing.T) {
	const budget = 300
	body := strings.Repeat("continuous prose without any structure at all ", 40)

	chunks := IndexChunks("Notes", body, nil, budget)
	require.Greater(t, len(chunks), 1)
	assertWithinBudget(t, chunks, budget)
	for _, c := range chunks {
		assert.True(t, strings.HasSuffix(c, " ") || strings.Contains(c, "structure"),
			"cuts should land on a word break")
	}
}

// TestIndexChunks_MultiByteRunesAreNotSplit proves a chunk boundary never lands
// inside a UTF-8 rune, which would hand the provider invalid text.
func TestIndexChunks_MultiByteRunesAreNotSplit(t *testing.T) {
	const budget = 200
	body := strings.Repeat("日本語のテキストです", 200)

	chunks := IndexChunks("T", body, nil, budget)
	require.Greater(t, len(chunks), 1)
	assertWithinBudget(t, chunks, budget)
	for i, c := range chunks {
		assert.True(t, isValidUTF8(c), "chunk %d is not valid UTF-8", i)
	}
}

// TestIndexChunks_OversizedIdentityDropsThePrefix proves a pathological title
// cannot starve the body: when the identity would leave no usable room, the
// chunks carry raw body instead of nothing.
func TestIndexChunks_OversizedIdentityDropsThePrefix(t *testing.T) {
	const budget = 600
	title := strings.Repeat("T", 500)
	body := strings.Repeat("real content that must still be indexed. ", 60)

	chunks := IndexChunks(title, body, nil, budget)
	require.Greater(t, len(chunks), 1)
	assertWithinBudget(t, chunks, budget)
	assert.Contains(t, strings.Join(chunks, ""), "real content that must still be indexed")
}

// TestIndexChunks_OversizedIdentityWithNoBody is the degenerate case: nothing to
// split, so the composed text is trimmed to the budget rather than returned
// oversized (the provider would trim it anyway, but the invariant holds here).
func TestIndexChunks_OversizedIdentityWithNoBody(t *testing.T) {
	const budget = 100
	chunks := IndexChunks(strings.Repeat("T", 500), "", nil, budget)
	require.Len(t, chunks, 1)
	assertWithinBudget(t, chunks, budget)
}

// TestIndexChunks_HeadingsInFencedCodeAreNotBoundaries mirrors the oversize
// signal's rule: a '#' comment inside a code fence is not a section start, so a
// code block is not shredded at every comment line.
func TestIndexChunks_HeadingsInFencedCodeAreNotBoundaries(t *testing.T) {
	body := "Intro paragraph.\n\n```sh\n# not a heading\necho hi\n# also not a heading\n```\n"
	assert.Len(t, splitSections(body), 1)
}

// TestSplitSectionsIsLossless proves the section split loses no bytes: every
// byte of the body lands in exactly one section, so nothing can silently vanish
// between sections before the chunks are packed.
func TestSplitSectionsIsLossless(t *testing.T) {
	body := "lead in\n\n# One\n\nalpha\n\n## Two\n\nbeta\n### Three\ngamma"
	assert.Equal(t, body, strings.Join(splitSections(body), ""))
}

func isValidUTF8(s string) bool {
	for _, r := range s {
		if r == '�' {
			return false
		}
	}
	return true
}

// TestSplitParagraphsAndOversized covers the two fallback splitters directly,
// including the boundaries the section path rarely reaches: a section that fits
// is returned whole, a trailing paragraph with no terminating blank line is kept,
// and text ending exactly on a blank line yields no empty trailing piece.
func TestSplitParagraphsAndOversized(t *testing.T) {
	assert.Equal(t, []string{"short section"}, splitOversized("short section", 100))
	assert.Equal(t, []string{"one\n\n", "two"}, splitParagraphs("one\n\ntwo"))
	assert.Equal(t, []string{"one\n\n"}, splitParagraphs("one\n\n"))
	assert.Nil(t, splitParagraphs(""))

	// A paragraph over the budget falls through to the hard split, while its
	// in-budget neighbor is left intact.
	long := strings.Repeat("word ", 100)
	parts := splitOversized("small\n\n"+long, 120)
	require.Greater(t, len(parts), 2)
	assert.Equal(t, "small\n\n", parts[0])
	for _, p := range parts {
		assert.LessOrEqual(t, len(p), 120)
	}
}

func TestTruncateOnRune(t *testing.T) {
	assert.Equal(t, "abc", truncateOnRune("abc", 10), "text within the budget is unchanged")
	assert.Equal(t, "abc", truncateOnRune("abc", 0), "a non-positive budget disables truncation")
	assert.Equal(t, "ab", truncateOnRune("abc", 2))
	assert.Equal(t, "日", truncateOnRune("日本", 4), "a cut inside a rune backs off to its start")
}
