package textpatch

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// applyOK runs edits and fails the test if the patch is refused.
func applyOK(t *testing.T, body string, edits ...Edit) Result {
	t.Helper()
	res, err := Apply(body, edits, Options{})
	require.NoError(t, err)
	return res
}

// applyErr runs edits expecting refusal and returns the patch error.
func applyErr(t *testing.T, body string, edits ...Edit) *Error {
	t.Helper()
	res, err := Apply(body, edits, Options{})
	require.Error(t, err)
	assert.Empty(t, res.Body, "a refused patch produces no body")
	var pe *Error
	require.ErrorAs(t, err, &pe)
	return pe
}

func TestApplyReplaceLiteral(t *testing.T) {
	res := applyOK(t, report, Edit{
		Find:    "Revenue grew 12% year over year",
		Replace: "Revenue grew 14% year over year",
	})
	assert.Contains(t, res.Body, "grew 14%")
	assert.NotContains(t, res.Body, "grew 12%")
	assert.Equal(t, len(res.Body), res.SizeBytes)

	require.Len(t, res.Edits, 1)
	assert.Equal(t, OpReplace, res.Edits[0].Op)
	assert.Equal(t, 1, res.Edits[0].Matches)
	assert.Equal(t, 7, res.Edits[0].Line)
	assert.False(t, res.Edits[0].Normalized)

	// Only the one sentence moved: every other line survives verbatim.
	assert.Equal(t, strings.Count(report, "\n"), strings.Count(res.Body, "\n"))
	assert.Contains(t, res.Diff, "-Revenue grew 12% year over year.")
	assert.Contains(t, res.Diff, "+Revenue grew 14% year over year.")
}

func TestApplyReplaceIsCaseSensitiveAndLiteral(t *testing.T) {
	// A literal anchor is never treated as a regex, so metacharacters in the
	// replacement land verbatim.
	res := applyOK(t, "cost is $5 (approx)\n", Edit{Find: "$5 (approx)", Replace: "$7 (approx)"})
	assert.Equal(t, "cost is $7 (approx)\n", res.Body)
}

func TestApplyEmptyReplaceDeletes(t *testing.T) {
	res := applyOK(t, "keep this. drop this. keep that.\n", Edit{Find: " drop this."})
	assert.Equal(t, "keep this. keep that.\n", res.Body)
}

func TestApplyInsertBeforeAndAfterKeepTheAnchor(t *testing.T) {
	res := applyOK(t, report, Edit{
		Op:   OpInsertAfter,
		Find: "## Findings",
		Text: "\n\nAll figures are quarter-end.",
	})
	assert.Contains(t, res.Body, "## Findings\n\nAll figures are quarter-end.")

	res = applyOK(t, "line\n", Edit{Op: OpInsertBefore, Find: "line", Text: "first "})
	assert.Equal(t, "first line\n", res.Body)
}

func TestApplyAppendAndPrepend(t *testing.T) {
	res := applyOK(t, "body\n",
		Edit{Op: OpPrepend, Text: "top\n"},
		Edit{Op: OpAppend, Text: "bottom\n"},
	)
	assert.Equal(t, "top\nbody\nbottom\n", res.Body)
	assert.Equal(t, 1, res.Edits[0].Line)
	assert.Equal(t, 3, res.Edits[1].Line, "append lands after the lines that exist when it runs")
}

func TestApplyReplaceSection(t *testing.T) {
	res := applyOK(t, report, Edit{
		Op:      OpReplaceSection,
		Section: "## Methodology",
		Text:    "## Methodology\n\nRestated: we sampled 250 accounts.\n\n",
	})
	assert.Contains(t, res.Body, "Restated: we sampled 250 accounts.")
	assert.NotContains(t, res.Body, "We sampled 100 accounts.")
	assert.Contains(t, res.Body, "## Appendix A", "the following section is untouched")
	assert.Contains(t, res.Body, "Revenue grew 12%", "the preceding section is untouched")
	assert.Equal(t, 13, res.Edits[0].Line)
}

func TestApplyReplaceSectionCoversNestedSubsections(t *testing.T) {
	res := applyOK(t, report, Edit{
		Op:      OpReplaceSection,
		Section: "## Findings",
		Text:    "## Findings\n\nrewritten\n\n",
	})
	assert.NotContains(t, res.Body, "### Regional detail")
	assert.Contains(t, res.Body, "## Methodology")
}

func TestApplyMoveSection(t *testing.T) {
	res := applyOK(t, report, Edit{
		Op:      OpMoveSection,
		Section: "## Appendix A",
		After:   "## Appendix B",
	})
	assert.Less(t, strings.Index(res.Body, "## Appendix B"), strings.Index(res.Body, "## Appendix A"))
	assert.Contains(t, res.Body, "Alpha.")
	assert.Contains(t, res.Body, "Beta.")

	before := applyOK(t, report, Edit{
		Op:      OpMoveSection,
		Section: "## Methodology",
		Before:  "## Findings",
	})
	assert.Less(t, strings.Index(before.Body, "## Methodology"), strings.Index(before.Body, "## Findings"))

	start := applyOK(t, report, Edit{Op: OpMoveSection, Section: "## Appendix B", Position: PositionStart})
	assert.True(t, strings.HasPrefix(start.Body, "## Appendix B"))

	end := applyOK(t, report, Edit{Op: OpMoveSection, Section: "## Findings", Position: PositionEnd})
	assert.True(t, strings.HasSuffix(end.Body, "See the table above.\n\n"))
}

func TestApplyMoveSectionRequiresADestination(t *testing.T) {
	pe := applyErr(t, report, Edit{Op: OpMoveSection, Section: "## Appendix A"})
	assert.Equal(t, CodeBadEdit, pe.Code)
	assert.Contains(t, pe.Message, "destination")

	pe = applyErr(t, report, Edit{Op: OpMoveSection, Section: "## Appendix A", Position: "middle"})
	assert.Equal(t, CodeBadEdit, pe.Code)
}

func TestApplySectionScopesAnAmbiguousAnchor(t *testing.T) {
	body := "## One\n\nsee the table above.\n\n## Two\n\nsee the table above.\n"

	pe := applyErr(t, body, Edit{Find: "see the table above.", Replace: "see Table 1."})
	assert.Equal(t, CodeAmbiguous, pe.Code)
	assert.Contains(t, pe.Message, "matches 2 spans")

	res := applyOK(t, body, Edit{Section: "## Two", Find: "see the table above.", Replace: "see Table 1."})
	assert.Equal(t, "## One\n\nsee the table above.\n\n## Two\n\nsee Table 1.\n", res.Body)
}

func TestApplyOccurrenceSelectors(t *testing.T) {
	body := "a x b x c x d\n"

	res := applyOK(t, body, Edit{Find: "x", Replace: "1", Occurrence: OccurrenceFirst})
	assert.Equal(t, "a 1 b x c x d\n", res.Body)

	res = applyOK(t, body, Edit{Find: "x", Replace: "9", Occurrence: OccurrenceLast})
	assert.Equal(t, "a x b x c 9 d\n", res.Body)

	res = applyOK(t, body, Edit{Find: "x", Replace: "2", Occurrence: "2"})
	assert.Equal(t, "a x b 2 c x d\n", res.Body)

	res = applyOK(t, body, Edit{Find: "x", Replace: "*", Occurrence: OccurrenceAll})
	assert.Equal(t, "a * b * c * d\n", res.Body)
	assert.Equal(t, 3, res.Edits[0].Matches, "occurrence:all reports how many spans it changed")
}

func TestApplyOccurrenceOutOfRangeAndInvalid(t *testing.T) {
	pe := applyErr(t, "a x b x\n", Edit{Find: "x", Replace: "y", Occurrence: "5"})
	assert.Equal(t, CodeNoMatch, pe.Code)
	assert.Contains(t, pe.Message, "only 2 span")

	pe = applyErr(t, "a x\n", Edit{Find: "x", Replace: "y", Occurrence: "second"})
	assert.Equal(t, CodeBadEdit, pe.Code)
}

func TestApplyNoMatchIsRefused(t *testing.T) {
	pe := applyErr(t, report, Edit{Find: "text that is not there", Replace: "x"})
	assert.Equal(t, CodeNoMatch, pe.Code)
	assert.Contains(t, pe.Hint, "locate")
	assert.Equal(t, 0, pe.EditIndex)
}

func TestApplyIsAllOrNothing(t *testing.T) {
	pe := applyErr(t, report,
		Edit{Find: "Revenue grew 12%", Replace: "Revenue grew 14%"},
		Edit{Find: "not present anywhere", Replace: "x"},
	)
	assert.Equal(t, CodeNoMatch, pe.Code)
	assert.Equal(t, 1, pe.EditIndex, "the failing edit is named by index")
}

func TestApplyEditsSeeEarlierEdits(t *testing.T) {
	res := applyOK(t, "alpha\n",
		Edit{Find: "alpha", Replace: "beta"},
		Edit{Find: "beta", Replace: "gamma"},
	)
	assert.Equal(t, "gamma\n", res.Body)
}

func TestApplyNormalizedRetry(t *testing.T) {
	body := "line one   \r\nline two\r\n"

	res := applyOK(t, body, Edit{Find: "line one\nline two", Replace: "merged"})
	assert.True(t, res.Edits[0].Normalized, "the retry is reported so the caller knows the anchor was not exact")
	assert.Equal(t, "merged\r\n", res.Body)

	// An exact anchor never reports normalization.
	exact := applyOK(t, "plain text\n", Edit{Find: "plain", Replace: "simple"})
	assert.False(t, exact.Edits[0].Normalized)
}

func TestApplyNormalizedRetryLeavesUnmatchedTextIntact(t *testing.T) {
	body := "keep\ntrailing spaces here   \nkeep too\n"
	res := applyOK(t, body, Edit{Find: "trailing spaces here", Replace: "clean"})
	assert.Equal(t, "keep\nclean   \nkeep too\n", res.Body,
		"trailing whitespace the normalizer ignored stays outside the replaced span")
}

func TestApplyUnicodeAnchors(t *testing.T) {
	body := "# Résumé\n\nLe café coûte 5 €.\n\nEmoji: 🚀 launch\n"

	res := applyOK(t, body, Edit{Find: "coûte 5 €", Replace: "coûte 6 €"})
	assert.Contains(t, res.Body, "coûte 6 €")

	res = applyOK(t, body, Edit{Op: OpReplaceSection, Section: "Résumé", Text: "# Résumé\n\nshort\n"})
	assert.Equal(t, "# Résumé\n\nshort\n", res.Body)

	res = applyOK(t, body, Edit{Find: "🚀", Replace: "🛰"})
	assert.Contains(t, res.Body, "Emoji: 🛰 launch")
}

func TestApplyRegexWithCaptureExpansion(t *testing.T) {
	body := "Q1 FY24 and Q3 FY24 were revised.\n"
	res := applyOK(t, body, Edit{
		Pattern:    `Q[1-4] FY24`,
		Replace:    "$0 (restated)",
		Occurrence: OccurrenceAll,
	})
	assert.Equal(t, "Q1 FY24 (restated) and Q3 FY24 (restated) were revised.\n", res.Body)
	assert.Equal(t, 2, res.Edits[0].Matches)

	named := applyOK(t, "owner: alice\n", Edit{Pattern: `owner: (\w+)`, Replace: "owner: ${1}@example.com"})
	assert.Equal(t, "owner: alice@example.com\n", named.Body)
}

func TestApplyRegexAmbiguityStillRequiresOccurrence(t *testing.T) {
	pe := applyErr(t, "a1 a2\n", Edit{Pattern: `a\d`, Replace: "b"})
	assert.Equal(t, CodeAmbiguous, pe.Code)
}

func TestApplyBadPattern(t *testing.T) {
	pe := applyErr(t, "text\n", Edit{Pattern: `(unclosed`, Replace: "x"})
	assert.Equal(t, CodeBadPattern, pe.Code)
	assert.Contains(t, pe.Message, "does not compile")

	long := strings.Repeat("a", DefaultMaxPatternLen+1)
	res, err := Apply("text\n", []Edit{{Pattern: long, Replace: "x"}}, Options{})
	require.Error(t, err)
	assert.Empty(t, res.Body)
	var pe2 *Error
	require.ErrorAs(t, err, &pe2)
	assert.Equal(t, CodeBadPattern, pe2.Code)
}

func TestApplyAnchorValidation(t *testing.T) {
	pe := applyErr(t, "text\n", Edit{Replace: "x"})
	assert.Equal(t, CodeBadEdit, pe.Code)
	assert.Contains(t, pe.Message, "needs an anchor")

	pe = applyErr(t, "text\n", Edit{Find: "text", Pattern: "text", Replace: "x"})
	assert.Equal(t, CodeBadEdit, pe.Code)
	assert.Contains(t, pe.Message, "both")

	pe = applyErr(t, "text\n", Edit{Op: "rewrite_everything", Text: "x"})
	assert.Equal(t, CodeBadEdit, pe.Code)
	assert.Contains(t, pe.Message, "unknown op")

	pe = applyErr(t, report, Edit{Op: OpReplaceSection, Text: "x"})
	assert.Equal(t, CodeBadEdit, pe.Code)
	assert.Contains(t, pe.Message, "section")
}

func TestApplyLimits(t *testing.T) {
	_, err := Apply(report, nil, Options{})
	var pe *Error
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, CodeBadEdit, pe.Code)

	many := make([]Edit, 3)
	for i := range many {
		many[i] = Edit{Op: OpAppend, Text: "x"}
	}
	_, err = Apply("body", many, Options{MaxEdits: 2})
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, CodeTooLarge, pe.Code)

	_, err = Apply("body", []Edit{{Op: OpAppend, Text: strings.Repeat("x", 100)}}, Options{MaxResultBytes: 50})
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, CodeTooLarge, pe.Code)
	assert.Contains(t, pe.Message, "over the 50-byte maximum")

	body := strings.Repeat("x\n", 10)
	_, err = Apply(body, []Edit{{Find: "x", Replace: "y", Occurrence: OccurrenceAll}}, Options{MaxMatches: 3})
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, CodeTooLarge, pe.Code)
}

func TestApplyManyOccurrencesRewritesTheBodyOnce(t *testing.T) {
	// occurrence:"all" over a large document rewrites in one forward pass, so
	// the work is the size of the document rather than the document times the
	// match count. The assertion is on the result; the allocation count is
	// what the single pass buys.
	var sb strings.Builder
	for i := range 800 {
		fmt.Fprintf(&sb, "row %d: status PENDING and padding to add weight\n", i)
	}
	body := sb.String()

	res, err := Apply(body, []Edit{{Find: "PENDING", Replace: "SETTLED", Occurrence: OccurrenceAll}}, Options{})
	require.NoError(t, err)
	assert.Equal(t, 800, res.Edits[0].Matches)
	assert.Equal(t, strings.ReplaceAll(body, "PENDING", "SETTLED"), res.Body)
	assert.NotContains(t, res.Body, "PENDING")
}

func TestApplyOverlappingAnchorsUseNonOverlappingMatches(t *testing.T) {
	res := applyOK(t, "aaaa\n", Edit{Find: "aa", Replace: "b", Occurrence: OccurrenceAll})
	assert.Equal(t, "bb\n", res.Body)
	assert.Equal(t, 2, res.Edits[0].Matches)
}

func TestStaleBaseAndNotTextErrors(t *testing.T) {
	stale := StaleBaseError(3, 7)
	assert.Equal(t, CodeStaleBase, stale.Code)
	assert.Contains(t, stale.Error(), "does not match the current version 7")
	assert.NotContains(t, stale.Error(), "edit ")

	notText := NotTextError("application/pdf")
	assert.Equal(t, CodeNotText, notText.Code)
	assert.Contains(t, notText.Error(), "application/pdf")
}

func TestLocateReportsCountLinesSectionsAndContext(t *testing.T) {
	body := "# Doc\n\n## One\n\nsee the table above.\n\n## Two\n\nsee the table above.\n\n## Three\n\nsee the table above.\n"

	res, err := Locate(body, LocateQuery{Find: "see the table above."}, Options{})
	require.NoError(t, err)
	assert.Equal(t, 3, res.Count)
	require.Len(t, res.Matches, 3)
	assert.Equal(t, 5, res.Matches[0].Line)
	assert.Equal(t, "## One", res.Matches[0].Section)
	assert.Equal(t, "## Three", res.Matches[2].Section)
	assert.Contains(t, res.Matches[1].Context, "see the table above.")
	assert.Equal(t, strings.Index(body, "see the table above."), res.Matches[0].Offset)

	// An occurrence chosen from a locate count lands on the first try.
	patched := applyOK(t, body, Edit{Find: "see the table above.", Replace: "see Table 1.", Occurrence: "2"})
	assert.Equal(t, 1, strings.Count(patched.Body, "see Table 1."))
}

func TestLocateScopedAndLimited(t *testing.T) {
	body := "## One\n\nx marks it\n\n## Two\n\nx marks it\n"

	res, err := Locate(body, LocateQuery{Find: "x marks it", Section: "## Two"}, Options{})
	require.NoError(t, err)
	assert.Equal(t, 1, res.Count)
	assert.Equal(t, 7, res.Matches[0].Line)

	res, err = Locate(body, LocateQuery{Find: "x marks it", Limit: 1}, Options{})
	require.NoError(t, err)
	assert.Equal(t, 2, res.Count, "the count is the true total even when the list is truncated")
	assert.Len(t, res.Matches, 1)
	assert.True(t, res.Truncated)
}

func TestLocateRegexAndErrors(t *testing.T) {
	res, err := Locate("v1.2 and v3.4\n", LocateQuery{Pattern: `v\d\.\d`}, Options{})
	require.NoError(t, err)
	assert.Equal(t, 2, res.Count)

	_, err = Locate("text\n", LocateQuery{}, Options{})
	var pe *Error
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, CodeBadEdit, pe.Code)

	_, err = Locate("text\n", LocateQuery{Find: "x", Section: "Nowhere"}, Options{})
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, CodeSectionNotFound, pe.Code)

	_, err = Locate("text\n", LocateQuery{Pattern: "("}, Options{})
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, CodeBadPattern, pe.Code)
}

func TestLocateReportsACappedScanRatherThanFailing(t *testing.T) {
	// A very common anchor is exactly the case an agent needs answered, so
	// locate caps and flags the scan instead of refusing it the way a patch
	// with occurrence:"all" would.
	body := strings.Repeat("x\n", 20)
	res, err := Locate(body, LocateQuery{Find: "x", Limit: 5}, Options{MaxMatches: 8})
	require.NoError(t, err)
	assert.Equal(t, 8, res.Count)
	assert.Len(t, res.Matches, 5)
	assert.True(t, res.Truncated)

	_, err = Apply(body, []Edit{{Find: "x", Replace: "y", Occurrence: OccurrenceAll}}, Options{MaxMatches: 8})
	var pe *Error
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, CodeTooLarge, pe.Code, "an edit over the cap is still refused")
}

func TestLocateMissesReportZero(t *testing.T) {
	res, err := Locate(report, LocateQuery{Find: "absent phrase"}, Options{})
	require.NoError(t, err)
	assert.Equal(t, 0, res.Count)
	assert.Empty(t, res.Matches)
}

func TestContextWindowStaysOnRuneBoundaries(t *testing.T) {
	body := strings.Repeat("é", 200) + "TARGET" + strings.Repeat("ü", 200)
	res, err := Locate(body, LocateQuery{Find: "TARGET", ContextBytes: 40}, Options{})
	require.NoError(t, err)
	require.Len(t, res.Matches, 1)
	assert.True(t, utf8.ValidString(res.Matches[0].Context))
	assert.Contains(t, res.Matches[0].Context, "TARGET")
}

func TestNormalizeTextIndexMap(t *testing.T) {
	src := "a \r\nb\t\t\nc"
	norm, idx := normalizeText(src)
	assert.Equal(t, "a\nb\nc", norm)
	require.Len(t, idx, len(norm)+1)
	assert.Equal(t, len(src), int(idx[len(norm)]))
	for i := range norm {
		assert.Equal(t, norm[i], src[idx[i]], "normalized byte %d must point at its original", i)
	}
}

func TestApplyLargeDocumentEditIsProportionalToTheEdit(t *testing.T) {
	// The acceptance criterion: one sentence changed in a large report using
	// arguments far smaller than the document, with nothing else altered.
	var sb strings.Builder
	sb.WriteString("# Big Report\n\n")
	for i := range 600 {
		fmt.Fprintf(&sb, "## Section %d\n\nFiller paragraph number %d with enough text to add weight.\n\n", i, i)
	}
	sb.WriteString("## Methodology\n\nrevenue grew 12% year over year, measured across all accounts.\n")
	body := sb.String()
	require.Greater(t, len(body), 40_000)

	edit := Edit{
		Find:    "revenue grew 12% year over year",
		Replace: "revenue grew 14% year over year",
	}
	res, err := Apply(body, []Edit{edit}, Options{})
	require.NoError(t, err)

	assert.Less(t, len(edit.Find)+len(edit.Replace), 1024, "the edit payload is under 1 KB")
	assert.Less(t, len(res.Diff), 1024, "the response diff is under 1 KB")
	assert.Equal(t, len(body)+len("14%")-len("12%"), len(res.Body))
	assert.Equal(t, strings.Replace(body, edit.Find, edit.Replace, 1), res.Body)
}
