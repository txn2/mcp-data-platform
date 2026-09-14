package xmltree_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/xmltree"
)

// soapEnvelope is the worked example the module exists for: a namespaced
// envelope whose prefixes (soap, m) are the sender's choice, carrying a body a
// caller wants to read by local name.
const soapEnvelope = `<?xml version="1.0"?>
<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">
  <soap:Header><m:Trace xmlns:m="urn:acme:trace" level="2">on</m:Trace></soap:Header>
  <soap:Body>
    <GetRatesResponse xmlns="urn:acme:rates">
      <Rate currency="EUR">0.92</Rate>
      <Rate currency="GBP">0.79</Rate>
      <Rate currency="JPY">147.10</Rate>
    </GetRatesResponse>
  </soap:Body>
</soap:Envelope>`

func TestDecode_ElementFields(t *testing.T) {
	root, err := xmltree.Decode(soapEnvelope, xmltree.DefaultLimits)
	require.NoError(t, err)

	assert.Equal(t, "Envelope", root.Tag, "the prefix is resolved away; the local name is what a caller matches on")
	assert.Equal(t, "http://schemas.xmlsoap.org/soap/envelope/", root.NS)
	assert.Empty(t, root.Text, "an element holding only whitespace between children has no text")
	require.Len(t, root.Children, 2)
	assert.Equal(t, []string{"Header", "Body"}, []string{root.Children[0].Tag, root.Children[1].Tag},
		"children are in document order")

	trace := root.Children[0].Children[0]
	assert.Equal(t, "urn:acme:trace", trace.NS)
	assert.Equal(t, map[string]string{"level": "2"}, trace.Attrs,
		"a namespace declaration is not an attribute a caller reads")
	assert.Equal(t, "on", trace.Text)
}

func TestDecode_TextIsTheElementsOwn(t *testing.T) {
	root, err := xmltree.Decode(`<a> outer <b>inner</b> tail </a>`, xmltree.DefaultLimits)
	require.NoError(t, err)
	assert.Equal(t, "outer  tail", root.Text, "a descendant's character data belongs to the descendant")
	assert.Equal(t, "inner", root.Children[0].Text)
}

func TestDecode_RefusesDoctype(t *testing.T) {
	billion := `<!DOCTYPE lolz [<!ENTITY lol "lol"><!ENTITY lol2 "&lol;&lol;">]><lolz>&lol2;</lolz>`
	_, err := xmltree.Decode(billion, xmltree.DefaultLimits)
	require.ErrorIs(t, err, xmltree.ErrDoctype)
}

func TestDecode_RefusesOversizeDocument(t *testing.T) {
	lim := xmltree.DefaultLimits
	lim.MaxBytes = 32
	_, err := xmltree.Decode(`<a>`+strings.Repeat("x", 64)+`</a>`, lim)
	require.ErrorIs(t, err, xmltree.ErrTooLarge)
}

func TestDecode_RefusesDeepDocument(t *testing.T) {
	lim := xmltree.DefaultLimits
	lim.MaxDepth = 4
	deep := strings.Repeat("<a>", 10) + strings.Repeat("</a>", 10)
	_, err := xmltree.Decode(deep, lim)
	require.ErrorIs(t, err, xmltree.ErrTooDeep)
}

func TestDecode_RefusesWideDocument(t *testing.T) {
	lim := xmltree.DefaultLimits
	lim.MaxNodes = 5
	wide := "<a>" + strings.Repeat("<b/>", 20) + "</a>"
	_, err := xmltree.Decode(wide, lim)
	require.ErrorIs(t, err, xmltree.ErrTooManyNodes)
}

func TestDecode_Failures(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		want error
	}{
		{"empty document", "", xmltree.ErrNotElement},
		{"comment only", "<!-- nothing here -->", xmltree.ErrNotElement},
		{"mismatched end tag", "<a></b>", nil},
		{"unclosed element", "<a>", nil},
		{"unknown entity", "<a>&nbsp;</a>", nil},
		{"two roots", "<a/><b/>", nil},
		{"unsupported charset", `<?xml version="1.0" encoding="Shift_JIS"?><a/>`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := xmltree.Decode(tt.doc, xmltree.DefaultLimits)
			require.Error(t, err)
			if tt.want != nil {
				require.ErrorIs(t, err, tt.want)
			}
		})
	}
}

func TestDecode_ResolvesTheFivePredefinedEntities(t *testing.T) {
	root, err := xmltree.Decode(`<a t="&quot;q&quot;">&lt;b&gt; &amp; &apos;c&apos;</a>`, xmltree.DefaultLimits)
	require.NoError(t, err)
	assert.Equal(t, "<b> & 'c'", root.Text)
	assert.Equal(t, `"q"`, root.Attrs["t"])
}

func TestEncode_RoundTripsADecodedDocument(t *testing.T) {
	root, err := xmltree.Decode(soapEnvelope, xmltree.DefaultLimits)
	require.NoError(t, err)

	doc, err := xmltree.Encode(root, xmltree.DefaultLimits)
	require.NoError(t, err)

	again, err := xmltree.Decode(doc, xmltree.DefaultLimits)
	require.NoError(t, err)
	assert.Equal(t, root, again, "a decoded document survives a trip through the encoder unchanged")
}

func TestEncode_DeclaresANamespaceOnlyWhereItChanges(t *testing.T) {
	root, err := xmltree.Decode(`<a xmlns="urn:one"><b><c xmlns="urn:two"/></b></a>`, xmltree.DefaultLimits)
	require.NoError(t, err)

	doc, err := xmltree.Encode(root, xmltree.DefaultLimits)
	require.NoError(t, err)
	assert.Equal(t, `<a xmlns="urn:one"><b><c xmlns="urn:two"/></b></a>`, doc)
}

func TestEncode_EscapesAndOrdersAttributes(t *testing.T) {
	n := &xmltree.Node{
		Tag:   "note",
		Attrs: map[string]string{"z": "1", "a": `two "quoted" & <sharp>`},
		Text:  "5 < 6 & 7 > 6",
	}
	doc, err := xmltree.Encode(n, xmltree.DefaultLimits)
	require.NoError(t, err)
	assert.Equal(t, `<note a="two &quot;quoted&quot; &amp; &lt;sharp&gt;" z="1">5 &lt; 6 &amp; 7 &gt; 6</note>`, doc)

	back, err := xmltree.Decode(doc, xmltree.DefaultLimits)
	require.NoError(t, err)
	assert.Equal(t, n.Text, back.Text)
	assert.Equal(t, n.Attrs, back.Attrs)
}

func TestEncode_KeepsNewlinesReadable(t *testing.T) {
	doc, err := xmltree.Encode(&xmltree.Node{Tag: "a", Text: "one\ntwo"}, xmltree.DefaultLimits)
	require.NoError(t, err)
	assert.Equal(t, "<a>one\ntwo</a>", doc, "a multi-line body stays multi-line rather than becoming &#xA;")
}

func TestEncode_Refusals(t *testing.T) {
	tests := []struct {
		name string
		node *xmltree.Node
		lim  xmltree.Limits
		want error
	}{
		{"nil tree", nil, xmltree.DefaultLimits, xmltree.ErrBadTree},
		{"empty tag", &xmltree.Node{Tag: ""}, xmltree.DefaultLimits, xmltree.ErrBadTree},
		{"tag with a space", &xmltree.Node{Tag: "a b"}, xmltree.DefaultLimits, xmltree.ErrBadTree},
		{"nil child", &xmltree.Node{Tag: "a", Children: []*xmltree.Node{nil}}, xmltree.DefaultLimits, xmltree.ErrBadTree},
		{
			"unusable attribute name",
			&xmltree.Node{Tag: "a", Attrs: map[string]string{"bad name": "v"}},
			xmltree.DefaultLimits, xmltree.ErrBadTree,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := xmltree.Encode(tt.node, tt.lim)
			require.ErrorIs(t, err, tt.want)
		})
	}
}

func TestEncode_BoundsACyclicTree(t *testing.T) {
	// A tree assembled by a caller, unlike a decoded one, can reach
	// itself. The depth limit is what turns that into an error rather
	// than an unbounded walk.
	loop := &xmltree.Node{Tag: "a"}
	loop.Children = []*xmltree.Node{loop}
	_, err := xmltree.Encode(loop, xmltree.Limits{MaxBytes: 1 << 20, MaxDepth: 16, MaxNodes: 1000})
	require.ErrorIs(t, err, xmltree.ErrTooDeep)
}

func TestEncode_RefusesAnOversizeResult(t *testing.T) {
	n := &xmltree.Node{Tag: "a", Text: strings.Repeat("x", 128)}
	_, err := xmltree.Encode(n, xmltree.Limits{MaxBytes: 32, MaxDepth: 10, MaxNodes: 10})
	require.ErrorIs(t, err, xmltree.ErrTooLarge)
}

// TestDecode_ManyTextPiecesIsLinear pins the accumulation that used to append
// each piece of character data to the node's string. A document splits its
// character data at every entity reference, so the piece count is the
// document's to choose: 100k of them against a quadratic accumulator is tens
// of billions of byte copies, and this finishes in milliseconds.
func TestDecode_ManyTextPiecesIsLinear(t *testing.T) {
	const pieces = 100_000
	doc := "<a>" + strings.Repeat("x&amp;", pieces) + "</a>"

	root, err := xmltree.Decode(doc, xmltree.DefaultLimits)
	require.NoError(t, err)
	assert.Len(t, root.Text, pieces*2, "every piece is kept")
}

// TestEncode_RefusesBeforeAssemblingTheWholeDocument holds the byte cap against
// a tree inside the node and depth bounds whose text is far past it. Measuring
// only the finished string would mean assembling all of it first.
func TestEncode_RefusesBeforeAssemblingTheWholeDocument(t *testing.T) {
	root := &xmltree.Node{Tag: "a"}
	for range 64 {
		root.Children = append(root.Children, &xmltree.Node{Tag: "b", Text: strings.Repeat("x", 4096)})
	}
	_, err := xmltree.Encode(root, xmltree.Limits{MaxBytes: 8192, MaxDepth: 10, MaxNodes: 1000})
	require.ErrorIs(t, err, xmltree.ErrTooLarge)
}
