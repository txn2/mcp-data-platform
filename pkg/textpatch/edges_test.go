package textpatch

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNoMatchErrorNamesThePatternAndSection(t *testing.T) {
	pe := applyErr(t, report, Edit{Pattern: `zzz\d+`, Replace: "x"})
	assert.Equal(t, CodeNoMatch, pe.Code)
	assert.Contains(t, pe.Message, "pattern")
	assert.Contains(t, pe.Message, "zzz")

	pe = applyErr(t, report, Edit{Section: "## Methodology", Find: "Revenue grew", Replace: "x"})
	assert.Equal(t, CodeNoMatch, pe.Code)
	assert.Contains(t, pe.Message, `within section "## Methodology"`)
}

func TestNoMatchErrorTruncatesALongAnchor(t *testing.T) {
	long := strings.Repeat("q", anchorEchoLimit+50)
	pe := applyErr(t, report, Edit{Find: long, Replace: "x"})
	assert.Contains(t, pe.Message, "...")
	assert.Less(t, len(pe.Message), len(long)+120)
}

func TestTruncate(t *testing.T) {
	assert.Equal(t, "abc", truncate("abc", 5))
	assert.Equal(t, "abc", truncate("abc", 3))
	assert.Equal(t, "ab...", truncate("abcd", 2))
}

func TestLineAtClampsPastTheEnd(t *testing.T) {
	body := "one\ntwo\n"
	assert.Equal(t, 1, lineAt(body, 0))
	assert.Equal(t, 2, lineAt(body, 4))
	assert.Equal(t, 3, lineAt(body, 999), "an offset past the end clamps to the last line")
}

func TestContextWindowAtDocumentEdges(t *testing.T) {
	res, err := Locate("TARGET tail", LocateQuery{Find: "TARGET", ContextBytes: 200}, Options{})
	require.NoError(t, err)
	require.Len(t, res.Matches, 1)
	assert.Equal(t, "TARGET tail", res.Matches[0].Context, "the window clamps to the document")

	res, err = Locate("head TARGET", LocateQuery{Find: "TARGET"}, Options{})
	require.NoError(t, err)
	assert.Equal(t, "head TARGET", res.Matches[0].Context)
}

func TestContextWindowExpandsOffMidRuneBoundaries(t *testing.T) {
	// Three-byte runes on both sides with an odd half-width guarantee both
	// window edges land inside a rune and have to be pushed outward.
	body := strings.Repeat("→", 50) + "TARGET" + strings.Repeat("←", 50)
	res, err := Locate(body, LocateQuery{Find: "TARGET", ContextBytes: 41}, Options{})
	require.NoError(t, err)
	require.Len(t, res.Matches, 1)

	ctxWindow := res.Matches[0].Context
	assert.True(t, utf8.ValidString(ctxWindow))
	assert.Contains(t, ctxWindow, "TARGET")
	assert.True(t, strings.HasPrefix(ctxWindow, "→"))
	assert.True(t, strings.HasSuffix(ctxWindow, "←"))
}

func TestSectionTextReportsAMissingSection(t *testing.T) {
	_, _, err := SectionText(report, "Nowhere")
	var pe *Error
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, CodeSectionNotFound, pe.Code)

	_, _, err = Content(report, ContentRequest{Section: "Nowhere"})
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, CodeSectionNotFound, pe.Code)
}

func TestLiteralMatchesIgnoresAnAnchorThatNormalizesAway(t *testing.T) {
	// A whitespace-only anchor normalizes to nothing; the retry must not spin
	// on an empty needle, and the call is refused.
	pe := applyErr(t, "body\n", Edit{Find: "   ", Replace: "x"})
	assert.Equal(t, CodeNoMatch, pe.Code)
}

func TestOutlineOfAHeadinglessDocument(t *testing.T) {
	assert.Empty(t, Outline("just prose\nwith no headings\n"))
	assert.Empty(t, Outline(""))

	res, err := Locate("just prose\n", LocateQuery{Find: "prose"}, Options{})
	require.NoError(t, err)
	assert.Empty(t, res.Matches[0].Section, "a match above the first heading has no enclosing section")
}
