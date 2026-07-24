package textpatch

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// matchTags compiles sel, runs it over body, and returns the matched tags in
// document order, for compact assertions.
func matchTags(t *testing.T, body, sel string) []string {
	t.Helper()
	cs, err := parseSelector(sel, -1)
	require.NoError(t, err, "selector %q", sel)
	matches := selectorMatches(parseHTMLDoc(body), cs)
	tags := make([]string, 0, len(matches))
	for _, n := range matches {
		tags = append(tags, n.tag)
	}
	return tags
}

func TestSelectorTypeIdClassAttr(t *testing.T) {
	body := `<div id="root"><section class="card">a</section><section class="card wide">b</section><p data-region="notes">c</p></div>`

	assert.Equal(t, []string{"div"}, matchTags(t, body, "#root"))
	assert.Equal(t, []string{"section", "section"}, matchTags(t, body, ".card"))
	assert.Equal(t, []string{"section"}, matchTags(t, body, ".wide"))
	assert.Equal(t, []string{"section"}, matchTags(t, body, "section.wide"))
	assert.Equal(t, []string{"p"}, matchTags(t, body, "[data-region]"))
	assert.Equal(t, []string{"p"}, matchTags(t, body, `[data-region="notes"]`))
	assert.Empty(t, matchTags(t, body, `[data-region="missing"]`))
}

func TestSelectorCombinators(t *testing.T) {
	body := `<main><ul><li>a</li></ul><li>loose</li></main>`

	// Descendant: any li under main.
	assert.Equal(t, []string{"li", "li"}, matchTags(t, body, "main li"))
	// Child: only the li whose immediate parent is ul.
	assert.Equal(t, []string{"li"}, matchTags(t, body, "ul > li"))
	// Child of main excludes the one nested in ul.
	assert.Equal(t, []string{"li"}, matchTags(t, body, "main > li"))
}

func TestSelectorJSXClassNameAndComponentCase(t *testing.T) {
	body := `<Card className="metric"><Card className="metric inner">x</Card></Card><card className="metric">y</card>`

	// .metric reaches className, matching all three.
	assert.Equal(t, []string{"Card", "Card", "card"}, matchTags(t, body, ".metric"))
	// Component type is case-sensitive: Card matches the two, not <card>.
	assert.Equal(t, []string{"Card", "Card"}, matchTags(t, body, "Card"))
	assert.Equal(t, []string{"card"}, matchTags(t, body, "card"))
	// Compound: a Card with the inner class.
	assert.Equal(t, []string{"Card"}, matchTags(t, body, "Card.inner"))
}

func TestSelectorHTMLTagCaseInsensitive(t *testing.T) {
	body := `<DIV><Widget>x</Widget></DIV>`
	// A known HTML element matches case-insensitively however it is spelled.
	assert.Equal(t, []string{"DIV"}, matchTags(t, body, "div"))
	assert.Equal(t, []string{"DIV"}, matchTags(t, body, "DIV"))
	// Widget is not a known element, so it is a case-sensitive component.
	assert.Equal(t, []string{"Widget"}, matchTags(t, body, "Widget"))
	assert.Empty(t, matchTags(t, body, "widget"))
}

func TestParseSelectorErrors(t *testing.T) {
	for _, sel := range []string{"", ".", "#", "[unclosed", "[]", ">"} {
		_, err := parseSelector(sel, 3)
		var pe *Error
		require.ErrorAs(t, err, &pe, "selector %q should fail", sel)
		assert.Equal(t, CodeBadSelector, pe.Code)
		assert.Equal(t, 3, pe.EditIndex)
	}
}

func TestSelectorAttrValueWithSpaces(t *testing.T) {
	body := `<div data-label="a b c">x</div>`
	assert.Equal(t, []string{"div"}, matchTags(t, body, `[data-label="a b c"]`))
}
