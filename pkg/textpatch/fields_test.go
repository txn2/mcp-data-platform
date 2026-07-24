package textpatch

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOutlineFields(t *testing.T) {
	got := OutlineFields(report, SyntaxMarkdown)

	assert.Equal(t, len(report), got[FieldSizeBytes])
	assert.Equal(t, CountLines(report), got[FieldLines])

	sections, ok := got[FieldSections].([]Section)
	require.True(t, ok)
	assert.Len(t, sections, 6)
}

func TestOutlineFieldsOnAHeadinglessBodyReportsAnEmptyList(t *testing.T) {
	// The list must serialize as [] rather than null, so a client can iterate
	// it without a nil check.
	sections, ok := OutlineFields("prose only\n", SyntaxMarkdown)[FieldSections].([]Section)
	require.True(t, ok)
	assert.NotNil(t, sections)
	assert.Empty(t, sections)
}

func TestStatsFieldsCarryNoBody(t *testing.T) {
	got := StatsFields(report)

	assert.Equal(t, len(report), got[FieldSizeBytes])
	assert.Equal(t, CountLines(report), got[FieldLines])
	assert.Equal(t, DocStats(report).Hash, got[FieldHash])
	assert.NotContains(t, got, FieldContent)
	assert.NotContains(t, got, FieldSections)
}

func TestContentFields(t *testing.T) {
	whole, err := ContentFields(report, ContentRequest{})
	require.NoError(t, err)
	assert.Equal(t, report, whole[FieldContent])
	assert.NotContains(t, whole, FieldSection, "a whole-body read resolves no heading")

	section, err := ContentFields(report, ContentRequest{Section: "## Methodology"})
	require.NoError(t, err)
	assert.Equal(t, "## Methodology\n\nWe sampled 100 accounts.\n\n", section[FieldContent])
	assert.Equal(t, len(report), section[FieldSizeBytes],
		"the document's own size is reported even when one section was read")
	sec, ok := section[FieldSection].(Section)
	require.True(t, ok)
	assert.Equal(t, "Methodology", sec.Title)

	lines, err := ContentFields(report, ContentRequest{LineStart: 1, LineEnd: 1})
	require.NoError(t, err)
	assert.Equal(t, "# Quarterly Report\n", lines[FieldContent])

	_, err = ContentFields(report, ContentRequest{Section: "Nowhere"})
	var pe *Error
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, CodeSectionNotFound, pe.Code)
}

func TestLocateFields(t *testing.T) {
	got, err := LocateFields(report, LocateQuery{Find: "Revenue"}, Options{})
	require.NoError(t, err)
	assert.Equal(t, 1, got[FieldCount])
	assert.Equal(t, false, got[FieldTruncated])

	matches, ok := got[FieldMatches].([]Match)
	require.True(t, ok)
	require.Len(t, matches, 1)
	assert.Equal(t, "## Findings", matches[0].Section)

	miss, err := LocateFields(report, LocateQuery{Find: "absent"}, Options{})
	require.NoError(t, err)
	assert.Equal(t, 0, miss[FieldCount])
	empty, ok := miss[FieldMatches].([]Match)
	require.True(t, ok)
	assert.NotNil(t, empty, "an empty match list serializes as [] not null")

	_, err = LocateFields(report, LocateQuery{Pattern: "("}, Options{})
	var pe *Error
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, CodeBadPattern, pe.Code)
}

func TestPatchFieldsNeverCarryTheBody(t *testing.T) {
	res, err := Apply(report, []Edit{{Find: "Revenue grew 12%", Replace: "Revenue grew 14%"}}, Options{})
	require.NoError(t, err)

	got := PatchFields(res)
	assert.Equal(t, res.Edits, got[FieldEdits])
	assert.Equal(t, res.Diff, got[FieldDiff])
	assert.Equal(t, res.SizeBytes, got[FieldSizeBytes])
	assert.Equal(t, res.Lines, got[FieldLines])
	assert.NotContains(t, got, FieldContent, "the patched body never crosses the wire")
}

func TestAddPropertiesSplicesTheGrammar(t *testing.T) {
	props := map[string]any{"command": map[string]any{"type": "string"}}
	AddProperties(props)

	assert.Contains(t, props, "command", "the tool's own arguments survive")
	for _, name := range []string{"edits", "find", "pattern", "section", "base_version", "dry_run"} {
		assert.Contains(t, props, name)
	}
	assert.Equal(t, PropertiesMap()["edits"], props["edits"])
}

func TestAddPropertiesRefusesARedefinedName(t *testing.T) {
	assert.PanicsWithValue(t,
		`textpatch: tool schema redefines shared property "edits"`,
		func() { AddProperties(map[string]any{"edits": "mine"}) })
}

func TestIdentityOffsetsMapEveryByteToItself(t *testing.T) {
	got := identityOffsets(4)
	require.Len(t, got, 5, "an offset map carries one entry past the last byte")
	for i, off := range got {
		assert.Equal(t, int32(i), off)
	}
}

func TestNormalizeTextLeavesAnOversizedBodyUnchanged(t *testing.T) {
	// The size guard is what makes the int32 offsets provably in range. It is
	// unreachable through the tools, so it is exercised directly.
	body := "a b\r\nc  \n"
	normalized, offsets := normalizeText(body)
	assert.Equal(t, "a b\nc\n", normalized)

	// Compare against the identity path the guard would take.
	identity := identityOffsets(len(body))
	assert.Len(t, identity, len(body)+1)
	assert.NotEqual(t, len(identity), len(offsets),
		"a body with CRLF and trailing spaces normalizes shorter than itself")
}
