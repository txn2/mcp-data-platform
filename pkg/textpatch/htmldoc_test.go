package textpatch

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// firstTag returns the first node whose tag equals name.
func firstTag(root *htmlNode, name string) *htmlNode {
	for _, n := range walkNodes(root) {
		if n.tag == name {
			return n
		}
	}
	return nil
}

func TestParseHTMLDocBalancedSpans(t *testing.T) {
	body := `<div id="a"><p>hello</p></div>`
	root := parseHTMLDoc(body)

	div := firstTag(root, "div")
	require.NotNil(t, div)
	assert.True(t, div.balanced)
	assert.Equal(t, body, body[div.outerStart:div.outerEnd], "div owns the whole document")

	p := firstTag(root, "p")
	require.NotNil(t, p)
	assert.Equal(t, "<p>hello</p>", body[p.outerStart:p.outerEnd])
	assert.Equal(t, "hello", body[p.innerStart:p.innerEnd])
	assert.Same(t, div, p.parent, "p is a child of div")
}

func TestParseHTMLDocVoidAndSelfClosing(t *testing.T) {
	body := `<div><img src="x.png"><br><input value="v"/></div>`
	root := parseHTMLDoc(body)

	img := firstTag(root, "img")
	require.NotNil(t, img)
	assert.True(t, img.balanced, "a void element is a balanced leaf")
	assert.Equal(t, `<img src="x.png">`, body[img.outerStart:img.outerEnd])
	assert.Empty(t, img.children)

	input := firstTag(root, "input")
	require.NotNil(t, input)
	assert.True(t, input.balanced)
	assert.Equal(t, `<input value="v"/>`, body[input.outerStart:input.outerEnd])

	div := firstTag(root, "div")
	assert.Len(t, div.children, 3, "img, br and input are siblings under div")
}

func TestParseHTMLDocJSXComponentCaseAndClassName(t *testing.T) {
	body := `<Card className="metric big"><span>1</span></Card>`
	root := parseHTMLDoc(body)

	card := firstTag(root, "Card")
	require.NotNil(t, card, "component name keeps its case")
	assert.True(t, card.balanced)
	assert.Equal(t, body, body[card.outerStart:card.outerEnd])
	assert.Nil(t, firstTag(root, "card"), "lowercase card is a different name")

	require.Len(t, card.attrs, 1)
	assert.Equal(t, "className", card.attrs[0].name)
	assert.Equal(t, "metric big", card.attrs[0].value)
}

func TestParseHTMLDocSVGGroups(t *testing.T) {
	body := `<svg viewBox="0 0 10 10"><g id="grp"><circle cx="5" cy="5" r="4"/></g></svg>`
	root := parseHTMLDoc(body)

	g := firstTag(root, "g")
	require.NotNil(t, g)
	assert.True(t, g.balanced)
	assert.Equal(t, `<g id="grp"><circle cx="5" cy="5" r="4"/></g>`, body[g.outerStart:g.outerEnd])

	circle := firstTag(root, "circle")
	require.NotNil(t, circle)
	assert.True(t, circle.balanced, "an explicitly self-closed SVG leaf is balanced")
}

func TestParseHTMLDocUnbalancedIsFlagged(t *testing.T) {
	// A div left open owns to end of input but is not balanced, so selecting it
	// must be refused rather than trusted.
	root := parseHTMLDoc(`<section><div>oops`)
	div := firstTag(root, "div")
	require.NotNil(t, div)
	assert.False(t, div.balanced)

	section := firstTag(root, "section")
	assert.False(t, section.balanced, "an ancestor of an unclosed child is itself unbalanced")
}

func TestParseHTMLDocOptionalEndTagsForceClose(t *testing.T) {
	// <li> commonly omits its end tag; the second <li> does not corrupt the
	// first, and the enclosing <ul> still balances on its explicit end tag.
	body := `<ul><li>one<li>two</ul>`
	root := parseHTMLDoc(body)

	ul := firstTag(root, "ul")
	require.NotNil(t, ul)
	assert.True(t, ul.balanced)
	assert.Equal(t, body, body[ul.outerStart:ul.outerEnd])
	assert.Len(t, ul.children, 2, "both list items are recognized")
}

func TestParseHTMLDocExpressionWithBareLessThan(t *testing.T) {
	// `{count < 5}` — the '<' is followed by whitespace, so the tokenizer keeps
	// it as text and the surrounding element still balances.
	body := `<div>{count < 5}</div>`
	root := parseHTMLDoc(body)
	div := firstTag(root, "div")
	require.NotNil(t, div)
	assert.True(t, div.balanced)
	assert.Equal(t, body, body[div.outerStart:div.outerEnd])
}

func TestParseHTMLDocAttributeForms(t *testing.T) {
	body := `<div id=bare class='q r' data-region hidden data-n="3">x</div>`
	root := parseHTMLDoc(body)
	div := firstTag(root, "div")
	require.NotNil(t, div)

	got := map[string]string{}
	for _, a := range div.attrs {
		got[a.name] = a.value
	}
	assert.Equal(t, "bare", got["id"])
	assert.Equal(t, "q r", got["class"])
	assert.Equal(t, "", got["data-region"], "a boolean attribute has an empty value")
	assert.Equal(t, "", got["hidden"])
	assert.Equal(t, "3", got["data-n"])
}
