package textpatch

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnifiedDiffIdenticalBodiesProduceNoDiff(t *testing.T) {
	assert.Empty(t, UnifiedDiff(report, report, 0))
	assert.Empty(t, UnifiedDiff("", "", 0))
	assert.Empty(t, UnifiedDiffLabeled(report, report, "v1", "v2", 0))
}

func TestUnifiedDiffSingleLineChange(t *testing.T) {
	old := "alpha\nbravo\ncharlie\ndelta\necho\n"
	updated := "alpha\nbravo\nCHARLIE\ndelta\necho\n"

	got := UnifiedDiff(old, updated, 1)
	assert.Equal(t, "@@ -2,3 +2,3 @@\n bravo\n-charlie\n+CHARLIE\n delta\n", got)
}

func TestUnifiedDiffReportsOnlyChangedHunks(t *testing.T) {
	var oldSB, newSB strings.Builder
	for i := range 200 {
		fmt.Fprintf(&oldSB, "line %d\n", i)
		if i == 100 {
			newSB.WriteString("line 100 changed\n")
			continue
		}
		fmt.Fprintf(&newSB, "line %d\n", i)
	}

	got := UnifiedDiff(oldSB.String(), newSB.String(), 0)
	assert.Equal(t, 1, strings.Count(got, "@@ -"), "one contiguous change is one hunk")
	assert.Contains(t, got, "-line 100\n")
	assert.Contains(t, got, "+line 100 changed\n")
	assert.NotContains(t, got, "line 50")
	assert.Less(t, len(got), 200, "the diff is proportional to the change, not the document")
}

func TestUnifiedDiffSeparatesDistantChangesIntoHunks(t *testing.T) {
	var oldSB, newSB strings.Builder
	for i := range 60 {
		fmt.Fprintf(&oldSB, "line %d\n", i)
		switch i {
		case 5, 50:
			fmt.Fprintf(&newSB, "line %d edited\n", i)
		default:
			fmt.Fprintf(&newSB, "line %d\n", i)
		}
	}

	got := UnifiedDiff(oldSB.String(), newSB.String(), 3)
	assert.Equal(t, 2, strings.Count(got, "@@ -"))
}

func TestUnifiedDiffInsertionAndDeletionOnly(t *testing.T) {
	added := UnifiedDiff("a\nb\n", "a\nb\nc\n", 1)
	assert.Contains(t, added, "+c")
	assert.NotContains(t, added, "\n-", "a pure insertion carries no deletion lines")

	removed := UnifiedDiff("a\nb\nc\n", "a\nc\n", 1)
	assert.Contains(t, removed, "-b")

	fromEmpty := UnifiedDiff("", "new\n", 1)
	assert.Equal(t, "@@ -1,0 +1,1 @@\n+new\n", fromEmpty)
}

func TestUnifiedDiffHunkHeaderCounts(t *testing.T) {
	got := UnifiedDiff("a\nb\nc\n", "a\nb1\nb2\nc\n", 1)
	require.True(t, strings.HasPrefix(got, "@@ -1,3 +1,4 @@\n"), "got %q", got)
}

func TestUnifiedDiffLabeledCarriesTheHeader(t *testing.T) {
	got := UnifiedDiffLabeled("a\n", "b\n", "v3", "v4", 1)
	assert.True(t, strings.HasPrefix(got, "--- v3\n+++ v4\n@@"), "got %q", got)
}

func TestDiffMiddleFallsBackForWhollyDifferentRegions(t *testing.T) {
	// A region whose LCS table would exceed the cell budget is rendered as a
	// delete-all/insert-all block rather than allocating without bound.
	const n = 2100 // (n+1)^2 exceeds maxDiffCells
	a := make([]string, 0, n)
	b := make([]string, 0, n)
	for i := range n {
		a = append(a, fmt.Sprintf("old %d", i))
		b = append(b, fmt.Sprintf("new %d", i))
	}
	require.Greater(t, (len(a)+1)*(len(b)+1), maxDiffCells)

	script := diffMiddle(a, b)
	require.Len(t, script, 2*n)
	assert.Equal(t, tagDelete, script[0].tag)
	assert.Equal(t, tagDelete, script[n-1].tag)
	assert.Equal(t, tagInsert, script[n].tag)
	assert.Equal(t, tagInsert, script[2*n-1].tag)
}

func TestUnifiedDiffContextSpansTheTrimmedHeadAndTail(t *testing.T) {
	// The unchanged head and tail are trimmed before diffing, so the context
	// lines around a hunk have to be drawn back out of them. A change in the
	// middle of a document must still show its neighbors.
	old := "a\nb\nc\nTARGET\nd\ne\nf\n"
	updated := "a\nb\nc\nCHANGED\nd\ne\nf\n"

	got := UnifiedDiff(old, updated, 2)
	assert.Equal(t, "@@ -2,5 +2,5 @@\n b\n c\n-TARGET\n+CHANGED\n d\n e\n", got)
}

func TestUnifiedDiffContextClampsAtDocumentEdges(t *testing.T) {
	// A change on the first line has no head to draw context from; the hunk
	// coordinates must reflect what was actually available.
	got := UnifiedDiff("first\nb\nc\n", "FIRST\nb\nc\n", 2)
	assert.Equal(t, "@@ -1,3 +1,3 @@\n-first\n+FIRST\n b\n c\n", got)

	// And a change on the last line has no tail.
	got = UnifiedDiff("a\nb\nlast\n", "a\nb\nLAST\n", 2)
	assert.Equal(t, "@@ -1,3 +1,3 @@\n a\n b\n-last\n+LAST\n", got)
}

func TestSplitLines(t *testing.T) {
	assert.Nil(t, splitLines(""))
	assert.Equal(t, []string{"a", "b"}, splitLines("a\nb\n"))
	assert.Equal(t, []string{"a", "b"}, splitLines("a\nb"))
	assert.Equal(t, []string{"a", ""}, splitLines("a\n\n"))
}
