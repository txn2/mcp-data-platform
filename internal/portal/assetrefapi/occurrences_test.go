package assetrefapi

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/assetrefs"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

func TestScannableRejectsNonTextAndOversize(t *testing.T) {
	tests := []struct {
		name  string
		asset portaldomain.Asset
		want  bool
	}{
		{"html", portaldomain.Asset{ContentType: "text/html", SizeBytes: 100}, true},
		{"markdown", portaldomain.Asset{ContentType: "text/markdown", SizeBytes: 1}, true},
		{"svg", portaldomain.Asset{ContentType: "image/svg+xml", SizeBytes: 100}, true},
		{"png", portaldomain.Asset{ContentType: "image/png", SizeBytes: 100}, false},
		{"empty", portaldomain.Asset{ContentType: "text/html", SizeBytes: 0}, false},
		{"oversize", portaldomain.Asset{ContentType: "text/html", SizeBytes: maxScanBytes + 1}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, scannable(&tc.asset))
		})
	}
}

// Only the declared URI is looked for. A different mcp:// URI in the same
// document belongs to no reference and is not reported against one.
func TestScanOccurrencesFindsOnlyDeclaredURIs(t *testing.T) {
	content := []byte("<img src=\"" + logoURI + "\">\n<img src=\"" + chartURI + "\">\n")
	found := scanOccurrences(content, []assetrefs.Ref{logoRef()})

	require.Len(t, found, 1)
	require.Len(t, found[refKey(logoRef())], 1)
	assert.Equal(t, 1, found[refKey(logoRef())][0].Line)
}

// Every line naming the URI is reported, so a warning can name all of them.
func TestScanOccurrencesReportsEveryLine(t *testing.T) {
	content := []byte("a\n<img src=\"" + logoURI + "\">\nb\n<a href=\"" + logoURI + "\">\n")
	found := scanOccurrences(content, []assetrefs.Ref{logoRef()})

	require.Len(t, found[refKey(logoRef())], 2)
	assert.Equal(t, 2, found[refKey(logoRef())][0].Line)
	assert.Equal(t, 4, found[refKey(logoRef())][1].Line)
}

// Past the cap the list stops and says so, so a warning built from it never
// reads as the whole of them.
func TestScanOccurrencesCapsAndMarksTruncation(t *testing.T) {
	content := []byte(strings.Repeat("<img src=\""+logoURI+"\">\n", maxOccurrencesPerRef+3))
	found := scanOccurrences(content, []assetrefs.Ref{logoRef()})

	hits := found[refKey(logoRef())]
	require.Len(t, hits, maxOccurrencesPerRef)
	assert.True(t, hits[len(hits)-1].Truncated)
}

// Content that never names the URI yields nothing, which is what tells the
// panel a removal breaks no markup.
func TestScanOccurrencesAbsentURI(t *testing.T) {
	assert.Nil(t, scanOccurrences([]byte("<p>no pictures here</p>"),
		[]assetrefs.Ref{logoRef()}))
}

func TestScanOccurrencesEmptyContent(t *testing.T) {
	assert.Nil(t, scanOccurrences(nil, []assetrefs.Ref{logoRef()}))
}

// A reference with no URI matches nothing rather than everything.
func TestScanOccurrencesIgnoresEmptyURI(t *testing.T) {
	ref := logoRef()
	ref.URI = ""
	assert.Nil(t, scanOccurrences([]byte("anything at all"),
		[]assetrefs.Ref{ref}))
}

// A markup line indented four levels reads as a sentence, not as leading space.
func TestSnippetCollapsesWhitespace(t *testing.T) {
	line := "        <img\tsrc=\"" + logoURI + "\">"
	got := snippet(line, strings.Index(line, logoURI), len(logoURI))
	assert.Equal(t, "<img src=\""+logoURI+"\">", got)
}

// A URI at the end of a very long line still appears in the fragment: the
// window is centered on the match rather than taken from the start of the line.
func TestSnippetWindowsAroundTheMatch(t *testing.T) {
	line := strings.Repeat("x", 400) + " " + logoURI + " " + strings.Repeat("y", 400)
	got := snippet(line, strings.Index(line, logoURI), len(logoURI))

	assert.Contains(t, got, logoURI)
	assert.True(t, strings.HasPrefix(got, "..."), "the cut left side is marked")
	assert.True(t, strings.HasSuffix(got, "..."), "the cut right side is marked")
	assert.LessOrEqual(t, len(got), snippetLimit+len("......"))
}

// A match at the very start of a long line is not marked as cut on the left.
func TestSnippetAtLineStart(t *testing.T) {
	line := logoURI + strings.Repeat("y", 400)
	got := snippet(line, 0, len(logoURI))

	assert.True(t, strings.HasPrefix(got, logoURI))
	assert.True(t, strings.HasSuffix(got, "..."))
}

// A URI longer than the whole snippet budget still yields the URI, rather than
// a negative window.
func TestSnippetURIWiderThanTheLimit(t *testing.T) {
	long := "mcp://global/" + strings.Repeat("a", snippetLimit)
	line := "<img src=\"" + long + "\">"
	got := snippet(line, strings.Index(line, long), len(long))
	assert.Contains(t, got, long)
}

func TestCapReachedNamesTheNumber(t *testing.T) {
	assert.Contains(t, capReached(), "20")
	assert.Equal(t, 20, assetrefs.MaxRefs,
		"the refusal above is only useful while it names the real cap")
}
