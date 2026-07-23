package textpatch

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// report is a small multi-section document reused across tests.
const report = `# Quarterly Report

Intro paragraph.

## Findings

Revenue grew 12% year over year.

### Regional detail

See the table above.

## Methodology

We sampled 100 accounts.

## Appendix A

Alpha.

## Appendix B

Beta.
`

func TestOutlineLevelsLinesAndSizes(t *testing.T) {
	secs := Outline(report)
	require.Len(t, secs, 6)

	assert.Equal(t, "# Quarterly Report", secs[0].Heading)
	assert.Equal(t, 1, secs[0].Level)
	assert.Equal(t, 1, secs[0].Line)
	assert.Equal(t, len(report), secs[0].SizeBytes, "a level-1 heading owns the rest of the document")

	assert.Equal(t, "Findings", secs[1].Title)
	assert.Equal(t, 5, secs[1].Line)
	assert.Equal(t, "Quarterly Report > Findings", secs[1].Path)

	assert.Equal(t, 3, secs[2].Level)
	assert.Equal(t, "Quarterly Report > Findings > Regional detail", secs[2].Path)

	// Findings ends where Methodology begins, so its span covers its own
	// subsection but not the sibling that follows.
	findings := report[secs[1].start:secs[1].end]
	assert.Contains(t, findings, "Regional detail")
	assert.NotContains(t, findings, "Methodology")
}

func TestOutlineIgnoresHeadingsInsideCodeFences(t *testing.T) {
	body := "# Title\n\n```sh\n# not a heading\n```\n\n## Real\n\ntext\n"
	secs := Outline(body)
	require.Len(t, secs, 2)
	assert.Equal(t, "# Title", secs[0].Heading)
	assert.Equal(t, "## Real", secs[1].Heading)

	tilde := "# Title\n\n~~~\n### fenced\n~~~\n"
	require.Len(t, Outline(tilde), 1)
}

func TestParseHeadingRejectsNonHeadings(t *testing.T) {
	for _, line := range []string{
		"    # over-indented",
		"####### seven hashes",
		"#nospace",
		"plain text",
		"",
	} {
		_, _, ok := parseHeading(line)
		assert.False(t, ok, "%q must not parse as a heading", line)
	}

	level, title, ok := parseHeading("  ## Closed heading ##")
	require.True(t, ok)
	assert.Equal(t, 2, level)
	assert.Equal(t, "Closed heading", title)
}

func TestDocStats(t *testing.T) {
	st := DocStats(report)
	assert.Equal(t, len(report), st.SizeBytes)
	assert.Equal(t, strings.Count(report, "\n"), st.Lines)
	sum := sha256.Sum256([]byte(report))
	assert.Equal(t, hex.EncodeToString(sum[:]), st.Hash)

	assert.Equal(t, Stats{SizeBytes: 0, Lines: 0, Hash: DocStats("").Hash}, DocStats(""))
	assert.Equal(t, 1, DocStats("no trailing newline").Lines)
}

func TestFindSectionByHeadingTitleAndPath(t *testing.T) {
	for _, name := range []string{"## Methodology", "Methodology", "methodology", "Quarterly Report > Methodology"} {
		sec, err := FindSection(report, name, -1)
		require.NoError(t, err, name)
		assert.Equal(t, "## Methodology", sec.Heading)
	}
}

func TestFindSectionNotFoundCarriesOutline(t *testing.T) {
	_, err := FindSection(report, "Conclusions", 2)
	var pe *Error
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, CodeSectionNotFound, pe.Code)
	assert.Equal(t, 2, pe.EditIndex)
	assert.Contains(t, pe.Message, "## Methodology")
	assert.Contains(t, err.Error(), "edit 2")
}

func TestFindSectionAmbiguousResolvedByPath(t *testing.T) {
	body := "# A\n\n## Notes\n\nfirst\n\n# B\n\n## Notes\n\nsecond\n"
	_, err := FindSection(body, "Notes", -1)
	var pe *Error
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, CodeAmbiguous, pe.Code)

	sec, err := FindSection(body, "B > Notes", -1)
	require.NoError(t, err)
	assert.Equal(t, 9, sec.Line)
}

func TestSectionTextAndContent(t *testing.T) {
	text, sec, err := SectionText(report, "## Methodology")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(text, "## Methodology"))
	assert.Contains(t, text, "We sampled 100 accounts.")
	assert.NotContains(t, text, "Appendix A")
	assert.Equal(t, len(text), sec.SizeBytes)

	whole, _, err := Content(report, ContentRequest{})
	require.NoError(t, err)
	assert.Equal(t, report, whole)

	scoped, secMeta, err := Content(report, ContentRequest{Section: "Appendix B"})
	require.NoError(t, err)
	assert.Equal(t, "## Appendix B\n\nBeta.\n", scoped)
	assert.Equal(t, "Appendix B", secMeta.Title)

	lines, _, err := Content(report, ContentRequest{LineStart: 1, LineEnd: 2})
	require.NoError(t, err)
	assert.Equal(t, "# Quarterly Report\n\n", lines)
}

func TestLineRange(t *testing.T) {
	body := "one\ntwo\nthree\n"

	got, err := LineRange(body, 2, 3)
	require.NoError(t, err)
	assert.Equal(t, "two\nthree\n", got)

	got, err = LineRange(body, 2, 0)
	require.NoError(t, err)
	assert.Equal(t, "two\nthree\n", got, "an omitted end reads to the end of the document")

	got, err = LineRange(body, 2, 99)
	require.NoError(t, err)
	assert.Equal(t, "two\nthree\n", got, "an end past the document is clamped")

	for _, tc := range []struct{ start, end int }{{0, 1}, {3, 2}, {9, 9}} {
		_, err := LineRange(body, tc.start, tc.end)
		var pe *Error
		require.ErrorAs(t, err, &pe, "start=%d end=%d", tc.start, tc.end)
		assert.Equal(t, CodeBadEdit, pe.Code)
	}
}

func TestContentLineStartDefaultsToOne(t *testing.T) {
	got, _, err := Content("a\nb\nc\n", ContentRequest{LineEnd: 2})
	require.NoError(t, err)
	assert.Equal(t, "a\nb\n", got)
}
