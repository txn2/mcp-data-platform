package textchunk

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	assert.Equal(t, "abc", TruncateOnRune("abc", 10), "text within the budget is unchanged")
	assert.Equal(t, "abc", TruncateOnRune("abc", 0), "a non-positive budget disables truncation")
	assert.Equal(t, "ab", TruncateOnRune("abc", 2))
	assert.Equal(t, "日", TruncateOnRune("日本", 4), "a cut inside a rune backs off to its start")
}

func TestSplitText_EveryPieceFitsAndNothingIsLost(t *testing.T) {
	text := "alpha beta\n\n" + strings.Repeat("gamma ", 60) + "\n\ndelta"
	lines := func(s string) []string { return []string{s} }
	parts := SplitText(text, 100, lines)
	require.Greater(t, len(parts), 1)
	for _, p := range parts {
		assert.LessOrEqual(t, len(p), 100)
	}
	assert.Equal(t, text, strings.Join(parts, ""), "the pieces concatenate back to the text")
	assert.Nil(t, SplitText("  \n\n ", 100, lines), "whitespace carries nothing to embed")
}
