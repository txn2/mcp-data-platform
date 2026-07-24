package textpatch

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// applyHTML runs edits over an HTML-syntax document, failing on refusal.
func applyHTML(t *testing.T, body string, edits ...Edit) Result {
	t.Helper()
	res, err := Apply(body, edits, Options{Syntax: SyntaxHTML})
	require.NoError(t, err)
	return res
}

// applyHTMLErr runs edits over an HTML document expecting refusal.
func applyHTMLErr(t *testing.T, body string, edits ...Edit) *Error {
	t.Helper()
	res, err := Apply(body, edits, Options{Syntax: SyntaxHTML})
	require.Error(t, err)
	assert.Empty(t, res.Body, "a refused patch produces no body")
	var pe *Error
	require.ErrorAs(t, err, &pe)
	return pe
}

// jsxDashboard is a headingless JSX dashboard of three cards, the shape #1039
// targets: structural editing must reach it where a heading grammar cannot.
const jsxDashboard = `<Dashboard>
  <Card className="metric" data-region="revenue"><h3>Revenue</h3><Value>$1.2M</Value></Card>
  <Card className="metric" data-region="users"><h3>Users</h3><Value>18,204</Value></Card>
  <Card className="metric" data-region="churn"><h3>Churn</h3><Value>2.1%</Value></Card>
</Dashboard>`

func TestSelectorReplaceRewritesOnlyThatElement(t *testing.T) {
	// Acceptance: rewriting one card by selector changes only that element's
	// bytes and leaves every sibling untouched.
	res := applyHTML(t, jsxDashboard, Edit{
		Op:       OpReplaceSection,
		Selector: `[data-region="users"]`,
		Text:     `<Card className="metric" data-region="users"><h3>Active Users</h3><Value>19,001</Value></Card>`,
	})
	assert.Contains(t, res.Body, "Active Users")
	assert.Contains(t, res.Body, "19,001")
	// The other two cards are byte-for-byte unchanged.
	assert.Contains(t, res.Body, `<Card className="metric" data-region="revenue"><h3>Revenue</h3><Value>$1.2M</Value></Card>`)
	assert.Contains(t, res.Body, `<Card className="metric" data-region="churn"><h3>Churn</h3><Value>2.1%</Value></Card>`)
	assert.NotContains(t, res.Body, "18,204")
}

func TestSelectorAmbiguityRefusedThenOccurrenceSucceeds(t *testing.T) {
	// Acceptance: a selector matching three elements is refused with
	// PATCH_AMBIGUOUS naming the count; the same call with occurrence succeeds.
	pe := applyHTMLErr(t, jsxDashboard, Edit{
		Op: OpReplaceSection, Selector: ".metric", Text: "<Card>x</Card>",
	})
	assert.Equal(t, CodeAmbiguous, pe.Code)
	assert.Contains(t, pe.Message, "3 elements")

	res := applyHTML(t, jsxDashboard, Edit{
		Op: OpReplaceSection, Selector: ".metric", Occurrence: "2",
		Text: `<Card data-region="users">replaced</Card>`,
	})
	assert.Contains(t, res.Body, `<Card data-region="users">replaced</Card>`)
	assert.NotContains(t, res.Body, "18,204", "the second card is the one replaced")
	assert.Contains(t, res.Body, "$1.2M", "the first card is untouched")
}

func TestSelectorAnchoredEditScopesToElement(t *testing.T) {
	// An anchored replace scoped to one card changes only that card's copy even
	// though the same text could appear elsewhere.
	res := applyHTML(t, jsxDashboard, Edit{
		Op: OpReplace, Selector: `[data-region="churn"]`, Find: "Churn", Replace: "Attrition",
	})
	assert.Contains(t, res.Body, "Attrition")
	assert.Equal(t, 1, res.Edits[0].Matches)
}

func TestUnbalancedSelectorTargetRefused(t *testing.T) {
	// Acceptance: markup that cannot be resolved is refused with
	// PATCH_UNRESOLVED_MARKUP, having written nothing.
	body := `<section><div id="broken"><p>oops</section>`
	pe := applyHTMLErr(t, body, Edit{
		Op: OpReplaceSection, Selector: "#broken", Text: "<div>fixed</div>",
	})
	assert.Equal(t, CodeUnresolvedMarkup, pe.Code)
}

func TestSelectorMoveKeepsMarkupBalanced(t *testing.T) {
	// Acceptance: move_section on a selector-addressed element produces balanced
	// markup — the moved card reparses as a single balanced element.
	res := applyHTML(t, jsxDashboard, Edit{
		Op: OpMoveSection, Selector: `[data-region="churn"]`, Position: PositionStart,
	})
	root := parseHTMLDoc(res.Body)
	moved := firstTag(root, "Card")
	require.NotNil(t, moved)
	assert.True(t, moved.balanced)
	assert.True(t, strings.Index(res.Body, "Churn") < strings.Index(res.Body, "Revenue"),
		"the churn card moved ahead of the revenue card")
}

// htmlReport is a heading-structured HTML report, the other acceptance target.
const htmlReport = `<article>
<h1>Quarterly Report</h1>
<p>Intro.</p>
<h2>Findings</h2>
<p>Revenue grew 12%.</p>
<h2>Methodology</h2>
<p>We sampled 100 accounts.</p>
</article>`

func TestHTMLHeadingSectionBehavesLikeMarkdown(t *testing.T) {
	// Acceptance: restating one section of an HTML report by heading title
	// behaves as the markdown equivalent does — the section from its heading to
	// the next same-or-higher heading is replaced.
	res := applyHTML(t, htmlReport, Edit{
		Op: OpReplaceSection, Section: "Methodology",
		Text: "<h2>Methodology</h2>\n<p>We sampled 250 accounts.</p>\n",
	})
	assert.Contains(t, res.Body, "250 accounts")
	assert.NotContains(t, res.Body, "100 accounts")
	assert.Contains(t, res.Body, "Revenue grew 12%.", "the Findings section is untouched")
}

func TestHTMLOutlineHeadings(t *testing.T) {
	secs := Outline(htmlReport, SyntaxHTML)
	require.Len(t, secs, 3)
	assert.Equal(t, "Quarterly Report", secs[0].Title)
	assert.Equal(t, 1, secs[0].Level)
	assert.Equal(t, "Findings", secs[1].Title)
	assert.Equal(t, "Quarterly Report > Methodology", secs[2].Path)
}

func TestOutlineLandmarksForHeadinglessDashboard(t *testing.T) {
	// Acceptance: outline on a JSX dashboard with no heading tags returns a
	// non-empty landmark list whose selectors can be pasted back into a
	// successful patch.
	fields := OutlineFields(jsxDashboard, SyntaxHTML)
	landmarks, ok := fields[FieldLandmarks].([]Landmark)
	require.True(t, ok)
	require.NotEmpty(t, landmarks)

	// Every landmark selector resolves and patches.
	for _, lm := range landmarks {
		res, err := Apply(jsxDashboard, []Edit{{
			Op: OpReplace, Selector: lm.Selector, Find: "metric", Replace: "metric-v2", Occurrence: "first",
		}}, Options{Syntax: SyntaxHTML})
		require.NoError(t, err, "landmark selector %q must patch", lm.Selector)
		assert.Contains(t, res.Body, "metric-v2")
	}
}

func TestSelectorRefusedOnMarkdownNamesSection(t *testing.T) {
	// Acceptance: a selector against a markdown asset is refused with a message
	// naming the right alternative.
	_, err := Apply(report, []Edit{{Op: OpReplaceSection, Selector: ".card", Text: "x"}}, Options{Syntax: SyntaxMarkdown})
	var pe *Error
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, CodeBadEdit, pe.Code)
	assert.Contains(t, pe.Message, "selector")
	assert.Contains(t, pe.Hint, "section")
}

func TestSelectorRefusedOnStructurelessContent(t *testing.T) {
	// A selector or section against structureless content is refused with
	// PATCH_NO_STRUCTURE pointing at anchored edits.
	json := `{"a":1,"b":2}`
	pe := selectorErr(t, json, Edit{Op: OpReplaceSection, Selector: ".a", Text: "x"})
	assert.Equal(t, CodeNoStructure, pe.Code)
	assert.Contains(t, pe.Hint, "anchored")

	pe = selectorErr(t, json, Edit{Op: OpReplaceSection, Section: "whatever", Text: "x"})
	assert.Equal(t, CodeNoStructure, pe.Code)
}

// selectorErr runs one edit under SyntaxNone expecting refusal.
func selectorErr(t *testing.T, body string, e Edit) *Error {
	t.Helper()
	_, err := Apply(body, []Edit{e}, Options{Syntax: SyntaxNone})
	var pe *Error
	require.ErrorAs(t, err, &pe)
	return pe
}

func TestMarkdownUnaffectedBySyntaxAddition(t *testing.T) {
	// Acceptance: a markdown asset is unaffected — the #1033 behavior is intact.
	res := applyOK(t, report, Edit{Op: OpReplaceSection, Section: "Methodology", Text: "## Methodology\n\nUpdated.\n"})
	assert.Contains(t, res.Body, "Updated.")
	assert.NotContains(t, res.Body, "We sampled 100 accounts.")
}

func TestBadSelectorReported(t *testing.T) {
	pe := applyHTMLErr(t, jsxDashboard, Edit{Op: OpReplaceSection, Selector: "[unterminated", Text: "x"})
	assert.Equal(t, CodeBadSelector, pe.Code)
}

func TestGetContentBySelector(t *testing.T) {
	text, sec, err := Content(jsxDashboard, ContentRequest{
		Syntax: SyntaxHTML, Selector: `[data-region="revenue"]`,
	})
	require.NoError(t, err)
	assert.Equal(t, `<Card className="metric" data-region="revenue"><h3>Revenue</h3><Value>$1.2M</Value></Card>`, text)
	assert.Equal(t, "Card", sec.Title)
}

func TestLocateScopedBySelector(t *testing.T) {
	res, err := Locate(jsxDashboard, LocateQuery{
		Find: "metric", Selector: `[data-region="churn"]`,
	}, Options{Syntax: SyntaxHTML})
	require.NoError(t, err)
	assert.Equal(t, 1, res.Count, "the anchor is scoped to one card")
}
