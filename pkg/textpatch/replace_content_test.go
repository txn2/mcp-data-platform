package textpatch

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// islandPage is the data-island shape #1389 targets: a document whose one
// marked region holds the data its visualizations render from.
const islandPage = `<div class="dash">
  <h1>Revenue</h1>
  <script type="application/json" id="data">{"old": true}</script>
  <div id="chart"></div>
</div>`

func TestReplaceContentSwapsOnlyTheInterior(t *testing.T) {
	// Acceptance: the element's interior is replaced; its own tags and every
	// other byte of the document stay exactly as written.
	res := applyHTML(t, islandPage, Edit{
		Op: OpReplaceContent, Selector: "#data", Text: `{"regions": [1, 2]}`,
	})
	assert.Contains(t, res.Body, `<script type="application/json" id="data">{"regions": [1, 2]}</script>`)
	assert.NotContains(t, res.Body, `"old"`)
	assert.Contains(t, res.Body, "<h1>Revenue</h1>")
	assert.Contains(t, res.Body, `<div id="chart"></div>`)
	assert.Equal(t, 1, res.Edits[0].Matches)
	assert.Equal(t, OpReplaceContent, res.Edits[0].Op)
}

func TestReplaceContentEmptiesAnElement(t *testing.T) {
	// An empty Text is a deliberate emptying, not a refusal: the element stays,
	// holding nothing.
	res := applyHTML(t, islandPage, Edit{Op: OpReplaceContent, Selector: "#data"})
	assert.Contains(t, res.Body, `<script type="application/json" id="data"></script>`)
}

func TestReplaceContentOnJSXComponent(t *testing.T) {
	// A JSX component resolves case-sensitively like every selector edit, and
	// only its interior moves.
	res := applyHTML(t, jsxDashboard, Edit{
		Op: OpReplaceContent, Selector: `[data-region="churn"]`, Text: "<Value>0.9%</Value>",
	})
	assert.Contains(t, res.Body, `<Card className="metric" data-region="churn"><Value>0.9%</Value></Card>`)
	assert.NotContains(t, res.Body, "2.1%")
}

func TestReplaceContentRequiresSelector(t *testing.T) {
	// A section names a heading region with no enclosing tag pair, so the op
	// refuses it and points at the operation that does apply.
	pe := applyHTMLErr(t, islandPage, Edit{Op: OpReplaceContent, Section: "Revenue", Text: "x"})
	assert.Equal(t, CodeBadEdit, pe.Code)
	assert.Contains(t, pe.Hint, "selector")

	pe = applyHTMLErr(t, islandPage, Edit{Op: OpReplaceContent, Text: "x"})
	assert.Equal(t, CodeBadEdit, pe.Code)
}

func TestReplaceContentRefusesVoidAndSelfClosing(t *testing.T) {
	// A void or self-closing element has no interior: text spliced at its
	// offsets would land OUTSIDE the element, so the op refuses rather than
	// silently appending.
	for name, body := range map[string]string{
		"void":         `<p><img id="data" src="x.png"></p>`,
		"self-closing": `<Dash><Chart id="data"/></Dash>`,
	} {
		t.Run(name, func(t *testing.T) {
			pe := applyHTMLErr(t, body, Edit{Op: OpReplaceContent, Selector: "#data", Text: "x"})
			assert.Equal(t, CodeBadEdit, pe.Code)
			assert.Contains(t, pe.Message, "no interior")
		})
	}
}

func TestReplaceContentRefusalsShareSelectorRules(t *testing.T) {
	// The op resolves through the same region machinery as every selector edit:
	// no match, ambiguity, and a markdown document answer identically.
	pe := applyHTMLErr(t, islandPage, Edit{Op: OpReplaceContent, Selector: "#missing", Text: "x"})
	assert.Equal(t, CodeSectionNotFound, pe.Code)

	pe = applyHTMLErr(t, jsxDashboard, Edit{Op: OpReplaceContent, Selector: ".metric", Text: "x"})
	assert.Equal(t, CodeAmbiguous, pe.Code)

	_, err := Apply("# Doc\n\nbody\n", []Edit{{Op: OpReplaceContent, Selector: "#data", Text: "x"}},
		Options{Syntax: SyntaxMarkdown})
	assert.Error(t, err)
}
