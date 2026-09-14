package xmltree_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/xmltree"
)

// catalog exercises every construct the path subset supports: repeated
// siblings, an attribute worth selecting on, nesting for the descendant step,
// and a second branch so a wildcard has something to distinguish.
const catalog = `<catalog xmlns="urn:acme:catalog">
  <books>
    <book id="b1" status="live"><title>First</title><price currency="USD">10</price></book>
    <book id="b2" status="draft"><title>Second</title><price currency="EUR">20</price></book>
    <book id="b3" status="live"><title>Third</title><price currency="USD">30</price></book>
  </books>
  <notes><note>keep</note></notes>
</catalog>`

func decodeCatalog(t *testing.T) *xmltree.Node {
	t.Helper()
	root, err := xmltree.Decode(catalog, xmltree.DefaultLimits)
	require.NoError(t, err)
	return root
}

func TestFindAll_Subset(t *testing.T) {
	root := decodeCatalog(t)
	tests := []struct {
		name string
		path string
		want []string
	}{
		{"child steps", "books/book/title", []string{"First", "Second", "Third"}},
		{"descendant step", "//title", []string{"First", "Second", "Third"}},
		{"descendant then child", "//book/price", []string{"10", "20", "30"}},
		{"wildcard step", "books/*/title", []string{"First", "Second", "Third"}},
		{"wildcard as the only step", "*", []string{"", ""}},
		{"attribute predicate", "//book[@status='live']/title", []string{"First", "Third"}},
		{"double-quoted value", `//book[@status="draft"]/title`, []string{"Second"}},
		{"positional predicate", "books/book[2]/title", []string{"Second"}},
		{"two predicates", "//book[@status='live'][2]/title", []string{"Third"}},
		{"predicate on a wildcard", "books/*[@id='b1']/title", []string{"First"}},
		{"no match", "books/book/isbn", nil},
		{"position past the end", "books/book[9]", nil},
		{"attribute value that does not occur", "//book[@status='gone']", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := xmltree.FindAll(root, tt.path)
			require.NoError(t, err)
			var texts []string
			for _, n := range got {
				texts = append(texts, n.Text)
			}
			assert.Equal(t, tt.want, texts)
		})
	}
}

func TestFind_FirstMatchOrNil(t *testing.T) {
	root := decodeCatalog(t)

	first, err := xmltree.Find(root, "//book/title")
	require.NoError(t, err)
	require.NotNil(t, first)
	assert.Equal(t, "First", first.Text)

	missing, err := xmltree.Find(root, "//isbn")
	require.NoError(t, err, "a path that matches nothing is not an error")
	assert.Nil(t, missing)
}

func TestFind_MatchesOnLocalNameAcrossNamespaces(t *testing.T) {
	// The prefixes here are the sender's choice and differ from the ones
	// in the catalog fixture; a caller should not have to know either.
	doc := `<e:Envelope xmlns:e="urn:env"><e:Body><r:Rate xmlns:r="urn:rates">7</r:Rate></e:Body></e:Envelope>`
	root, err := xmltree.Decode(doc, xmltree.DefaultLimits)
	require.NoError(t, err)

	rate, err := xmltree.Find(root, "//Rate")
	require.NoError(t, err)
	require.NotNil(t, rate)
	assert.Equal(t, "7", rate.Text)
	assert.Equal(t, "urn:rates", rate.NS, "the namespace is still on the element for a caller that cares")
}

func TestFindAll_DescendantStepReturnsANodeOnce(t *testing.T) {
	// Nested context nodes are the one shape that can reach the same node
	// twice; the result is a set, as XPath's is.
	root, err := xmltree.Decode(`<a><a><b/></a></a>`, xmltree.DefaultLimits)
	require.NoError(t, err)
	got, err := xmltree.FindAll(root, "//a//b")
	require.NoError(t, err)
	assert.Len(t, got, 1)
}

func TestFindAll_RefusesPathsOutsideTheSubset(t *testing.T) {
	root := decodeCatalog(t)
	paths := []string{
		"",
		"   ",
		"/books",
		"books//",
		"books/book[",
		"books/book[0]",
		"books/book[-1]",
		"books/book[last()]",
		"books/book[@status]",
		"books/book[@status=live]",
		"books/book[@status='live'",
		"books/book[text()='x']",
		"books/book/@id",
		"soap:Envelope",
		"books/book[1]extra",
		"books/./book",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			got, err := xmltree.FindAll(root, path)
			require.ErrorIs(t, err, xmltree.ErrBadPath,
				"an unsupported path has to be refused, never answered with no matches")
			assert.Nil(t, got)
			assert.Contains(t, err.Error(), xmltree.PathSyntax,
				"a refusal states the language it is refusing against")
		})
	}
}

func TestSelect_NilNode(t *testing.T) {
	p, err := xmltree.Compile("a")
	require.NoError(t, err)
	assert.Nil(t, p.Select(nil))
}

func TestCompile_AcceptsASlashInsideAPredicateValue(t *testing.T) {
	root, err := xmltree.Decode(`<a><b href="/v1/items">x</b><b href="other">y</b></a>`, xmltree.DefaultLimits)
	require.NoError(t, err)
	got, err := xmltree.FindAll(root, `b[@href='/v1/items']`)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "x", got[0].Text)
}
