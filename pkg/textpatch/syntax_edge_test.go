package textpatch

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSyntaxForContentType(t *testing.T) {
	cases := map[string]Syntax{
		"text/html":                SyntaxHTML,
		"text/jsx":                 SyntaxHTML,
		"image/svg+xml":            SyntaxHTML,
		"application/xml":          SyntaxHTML,
		"text/html; charset=utf-8": SyntaxHTML,
		"text/markdown":            SyntaxMarkdown,
		"text/plain":               SyntaxMarkdown,
		"application/json":         SyntaxNone,
		"text/csv":                 SyntaxNone,
		"application/sql":          SyntaxNone,
		"":                         SyntaxNone,
	}
	for ct, want := range cases {
		assert.Equal(t, want, SyntaxForContentType(ct), ct)
	}
}

func TestImpliesEndOfGroups(t *testing.T) {
	// Every optional-end-tag group closes its own peers, exercised through the
	// tree so the peers become balanced siblings.
	bodies := map[string]string{
		"dl":     `<dl><dt>a<dd>b<dt>c</dl>`,
		"ol":     `<ol><li>a<li>b</ol>`,
		"select": `<select><option>a<option>b</select>`,
		"optg":   `<select><optgroup><option>a<optgroup><option>b</select>`,
		"table":  `<table><tbody><tr><td>a<td>b<tr><td>c</tbody></table>`,
		"ruby":   `<ruby>x<rt>a<rt>b</ruby>`,
	}
	for name, body := range bodies {
		root := parseHTMLDoc(body)
		for _, n := range walkNodes(root) {
			assert.True(t, n.balanced, "%s: %s should balance", name, n.tag)
		}
	}
}

func TestParseAttrsSkipsSpreadAndExpression(t *testing.T) {
	// A JSX spread and an expression attribute do not derail the scan; the named
	// attribute is still parsed.
	root := parseHTMLDoc(`<Card {...props} id="k" onClick={handler}>x</Card>`)
	card := firstTag(root, "Card")
	require.NotNil(t, card)
	v, ok := card.attrValue("id")
	assert.True(t, ok)
	assert.Equal(t, "k", v)
}

func TestLandmarkSelectorForms(t *testing.T) {
	// data-* with a value, valueless data-*, and no landmark at all.
	root := parseHTMLDoc(`<div data-region="a"></div><div data-flag></div><span>plain</span>`)
	nodes := walkNodes(root)

	sel, ok := landmarkSelector(nodes[0])
	require.True(t, ok)
	assert.Equal(t, `div[data-region="a"]`, sel)

	sel, ok = landmarkSelector(nodes[1])
	require.True(t, ok)
	assert.Equal(t, "div[data-flag]", sel)

	_, ok = landmarkSelector(nodes[2])
	assert.False(t, ok, "an element with neither id nor data-* is not a landmark")
}

func TestOutlineNoneAndNil(t *testing.T) {
	assert.Empty(t, Outline("anything", SyntaxNone))
	assert.Empty(t, Outline("<p>no headings</p>", SyntaxHTML))
}

func TestUnquoteAttrValueUnquoted(t *testing.T) {
	assert.Equal(t, "bare", unquoteAttrValue("bare"))
	assert.Equal(t, "quoted", unquoteAttrValue(`"quoted"`))
	assert.Equal(t, "single", unquoteAttrValue(`'single'`))
}

func TestResolveRegionBothAndNeither(t *testing.T) {
	_, err := resolveRegion("<div id='a'></div>", SyntaxHTML,
		regionRequest{section: "h", selector: "#a"}, 0)
	var pe *Error
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, CodeBadEdit, pe.Code)
	assert.Contains(t, pe.Message, "both")

	_, err = resolveRegion("body", SyntaxHTML, regionRequest{}, 0)
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, CodeBadEdit, pe.Code)
}

func TestPickSelectorNodeOccurrences(t *testing.T) {
	first, _, err := getByOccurrence("first")
	require.NoError(t, err)
	assert.Contains(t, first, "1")

	last, _, err := getByOccurrence("last")
	require.NoError(t, err)
	assert.Contains(t, last, "3")

	// occurrence:"all" does not name a single region.
	_, _, err = getByOccurrence("all")
	var pe *Error
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, CodeBadEdit, pe.Code)

	// An out-of-range index and a malformed one.
	_, _, err = getByOccurrence("9")
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, CodeSectionNotFound, pe.Code)

	_, _, err = getByOccurrence("-1")
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, CodeBadEdit, pe.Code)
}

// getByOccurrence resolves the ".c" selector against a fixed three-div body
// under HTML syntax, varying only the occurrence.
func getByOccurrence(occurrence string) (string, Section, error) {
	const body = `<div class="c">1</div><div class="c">2</div><div class="c">3</div>`
	return Content(body, ContentRequest{Syntax: SyntaxHTML, Selector: ".c", Occurrence: occurrence})
}

func TestStripTagsAndCollapse(t *testing.T) {
	assert.Equal(t, "Find ings", stripTags("Find <b>ings</b>"))
	assert.Equal(t, "one two", collapseText("  one   two  "))
}

func TestHTMLHeadingWithNestedMarkup(t *testing.T) {
	secs := Outline(`<h2>Find<em>ings</em> <b>Q3</b></h2>`, SyntaxHTML)
	require.Len(t, secs, 1)
	assert.Equal(t, "Findings Q3", secs[0].Title)
}
