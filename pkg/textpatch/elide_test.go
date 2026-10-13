package textpatch

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestElideDiffShowsTheWordThatChanged is #2115's criterion: one word changed
// inside a 15 KB single-line constant is a diff of a few hundred characters
// that shows the change.
func TestElideDiffShowsTheWordThatChanged(t *testing.T) {
	js := strings.Repeat("var a = document.querySelector('#form'); ", 360)
	oldBody := "PAGE_JS = \"" + js + "waitFor('ready');" + js + "\"\nprint(1)\n"
	newBody := "PAGE_JS = \"" + js + "waitFor('built');" + js + "\"\nprint(1)\n"
	require.Greater(t, len(oldBody), 15000)

	diff := UnifiedDiff(oldBody, newBody, 0)
	require.Greater(t, len(diff), 30000, "the plain diff prints the line twice")

	short := ElideDiff(diff, 0)
	assert.Less(t, len(short), 800)
	assert.Contains(t, short, "waitFor('ready')")
	assert.Contains(t, short, "waitFor('built')")
	assert.True(t, strings.HasPrefix(short, "@@ -1,2 +1,2 @@\n"))
	assert.Contains(t, short, " print(1)", "context lines are left alone")
	assert.Contains(t, short, "characters]", "a shortened line says how long it is")
}

func TestElideDiffLeavesShortLinesAndHeaders(t *testing.T) {
	diff := UnifiedDiffLabeled("a\nb\n", "a\nc\n", "v1", "v2", 0)
	assert.Equal(t, diff, ElideDiff(diff, 0))
	assert.Equal(t, "", ElideDiff("", 0))
}

func TestElideDiffShortensLinesWithoutAPartner(t *testing.T) {
	long := strings.Repeat("x", 1000)
	diff := UnifiedDiff("keep\n", "keep\n"+long+"\n", 0)
	short := ElideDiff(diff, 0)
	assert.Contains(t, short, "+"+strings.Repeat("x", elideSpan)+"… [1000 characters]")

	diff = UnifiedDiff("keep\n"+long+"\n"+long+"y\n", "keep\nz\n", 0)
	short = ElideDiff(diff, 0)
	assert.Equal(t, 2, strings.Count(short, "characters]"), "both removed lines are cut; the short line they became is not")
	assert.Contains(t, short, "\n+z")
	assert.Less(t, len(short), 1200)
}

func TestElidePair(t *testing.T) {
	a, b := ElidePair("short", "shorter", 0)
	assert.Equal(t, "short", a)
	assert.Equal(t, "shorter", b)

	head := strings.Repeat("h", 500)
	tail := strings.Repeat("t", 500)
	a, b = ElidePair(head+"old"+tail, head+"new"+tail, 100)
	assert.True(t, strings.HasPrefix(a, "…"))
	assert.Contains(t, a, "old")
	assert.Contains(t, b, "new")
	assert.Contains(t, a, "… [1003 characters]")

	rewritten := strings.Repeat("a", 1000)
	other := strings.Repeat("b", 1000)
	a, _ = ElidePair(rewritten, other, 100)
	assert.Equal(t, strings.Repeat("a", elideSpan)+"… [1000 characters]", a, "a line rewritten end to end shows its start")

	a, b = ElidePair("é"+strings.Repeat("ü", 600), "é"+strings.Repeat("ü", 599)+"x", 100)
	assert.True(t, strings.HasSuffix(a, "[601 characters]"), "runes, not bytes")
	assert.Contains(t, b, "x")
}

// TestALongContextLineIsShortened: an edit beside a long constant does not
// print the constant whole as context.
func TestALongContextLineIsShortened(t *testing.T) {
	long := strings.Repeat("x", 15_000)
	diff := "--- a\n+++ b\n@@ -1,2 +1,2 @@\n PAGE = \"" + long + "\"\n-n = 1\n+n = 2\n"
	got := ElideDiff(diff, 0)
	assert.Less(t, len(got), 600)
	assert.Contains(t, got, " PAGE = \"xxx")
	assert.Contains(t, got, "[15009 characters]")
	assert.Contains(t, got, "-n = 1\n+n = 2")
}
