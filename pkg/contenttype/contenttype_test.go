package contenttype_test

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/contenttype"
)

// pngBytes is a minimal but structurally valid PNG: signature plus an IHDR
// chunk. http.DetectContentType keys off the 8-byte signature.
var pngBytes = append(
	[]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a},
	[]byte{0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R'}...,
)

var gifBytes = []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00,")

var pdfBytes = []byte("%PDF-1.7\n1 0 obj\n<< /Type /Catalog >>\nendobj\n")

var mp3Bytes = append([]byte{'I', 'D', '3', 0x04, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}, make([]byte, 32)...)

// mp4Bytes carries the ftyp box http.DetectContentType matches for video/mp4.
var mp4Bytes = append(
	[]byte{0x00, 0x00, 0x00, 0x18, 'f', 't', 'y', 'p', 'm', 'p', '4', '2', 0x00, 0x00, 0x00, 0x00, 'm', 'p', '4', '2', 'i', 's', 'o', 'm'},
	make([]byte, 16)...,
)

func TestNormalize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		declared string
		want     string
	}{
		{"empty", "", ""},
		{"whitespace only", "   ", ""},
		{"already canonical", "application/json", contenttype.JSON},
		{"alias text/json", "text/json", contenttype.JSON},
		{"alias with charset", "text/json; charset=utf-8", contenttype.JSON},
		{"uppercase", "APPLICATION/JSON", contenttype.JSON},
		{"text/xml to application/xml", "text/xml", contenttype.XML},
		{"yaml alias", "application/x-yaml", contenttype.YAML},
		{"jsonl alias", "application/jsonl", contenttype.NDJSON},
		{"problem+json", "application/problem+json", contenttype.JSON},
		{"jpg to jpeg", "image/jpg", "image/jpeg"},
		{"mp3 to mpeg", "audio/mp3", "audio/mpeg"},
		{"binary/octet-stream", "binary/octet-stream", contenttype.OctetStream},
		{"malformed parameter keeps base", "application/json; charset", contenttype.JSON},
		{"unregistered type passes through", "application/vnd.acme+json", "application/vnd.acme+json"},
		{"parameters stripped", "text/csv; charset=utf-8; header=present", contenttype.CSV},
		{"parameters with no base type", "; charset=utf-8", ""},
		{"not a media type", "not a media type at all", ""},
		{"header injection attempt", "text/plain\r\nX-Evil: 1", ""},
		{"missing subtype", "application", ""},
		{"empty subtype", "application/", ""},
		{"space inside the type", "application/js on", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, contenttype.Normalize(tt.declared))
		})
	}
}

func TestIsActive(t *testing.T) {
	t.Parallel()

	active := []string{
		"text/html", "TEXT/HTML; charset=utf-8", "text/jsx", "image/svg+xml",
		"application/javascript", "application/xhtml+xml",
	}
	for _, ct := range active {
		require.Truef(t, contenttype.IsActive(ct), "%q must be active", ct)
	}

	// XML is deliberately not active: Detect names it from content, and doing so
	// is safe because a viewer shows it as inert text. It is still a scriptable
	// document for serving purposes, which TestIsScriptableDocument covers.
	passive := []string{"application/json", "text/plain", "image/png", "application/xml", "text/markdown", ""}
	for _, ct := range passive {
		require.Falsef(t, contenttype.IsActive(ct), "%q must not be active", ct)
	}
}

// TestIsScriptableDocument covers the serving-side predicate. Every active type
// belongs to it, and so does the XML family, which a browser renders as a
// document that honors an <?xml-stylesheet?> processing instruction.
func TestIsScriptableDocument(t *testing.T) {
	t.Parallel()

	scriptable := []string{
		"text/html", "text/jsx", "image/svg+xml", "text/javascript", "application/javascript",
		"application/xhtml+xml", "APPLICATION/XHTML+XML; charset=utf-8",
		"application/xml", "text/xml", "application/x-xml",
		// An unregistered XML dialect has no entry and is covered by the
		// structured-suffix rule alone.
		"application/rss+xml", "application/atom+xml", "application/vnd.acme.thing+xml",
	}
	for _, ct := range scriptable {
		require.Truef(t, contenttype.IsScriptableDocument(ct), "%q must be a scriptable document", ct)
	}

	inert := []string{
		"", "text/plain", "text/markdown", "text/csv", "application/json", "application/vnd.api+json",
		"application/yaml", "application/pdf", "image/png", "audio/mpeg", "video/mp4",
		"application/octet-stream", "not a media type",
	}
	for _, ct := range inert {
		require.Falsef(t, contenttype.IsScriptableDocument(ct), "%q must not be a scriptable document", ct)
	}
}

func TestIsGeneric(t *testing.T) {
	t.Parallel()

	generic := []string{"", "application/octet-stream", "binary/octet-stream", "text/plain", "text/plain; charset=utf-8"}
	for _, ct := range generic {
		require.Truef(t, contenttype.IsGeneric(ct), "%q must be generic", ct)
	}

	specific := []string{"application/json", "image/png", "text/csv", "text/html"}
	for _, ct := range specific {
		require.Falsef(t, contenttype.IsGeneric(ct), "%q must not be generic", ct)
	}
}

func TestIsTextual(t *testing.T) {
	t.Parallel()

	textual := []string{
		"text/plain", "text/csv", "application/json", "application/x-ndjson", "application/xml",
		"application/yaml", "image/svg+xml", "application/sql",
		// TypeScript source is text that carries no text/ prefix; it is listed
		// because every reader of this predicate (the resource read path, the
		// search index consumer) must treat a .ts upload as readable text.
		"application/typescript",
		// Aliases must normalize before the family test, or a caller that declares
		// the alias gets the binary answer.
		"application/csv", "text/json", "application/javascript", "text/markdown; charset=utf-8",
	}
	for _, ct := range textual {
		require.Truef(t, contenttype.IsTextual(ct), "%q must be textual", ct)
	}

	binary := []string{"image/png", "audio/mpeg", "video/mp4", "application/pdf", "application/octet-stream", "application/zip"}
	for _, ct := range binary {
		require.Falsef(t, contenttype.IsTextual(ct), "%q must not be textual", ct)
	}
}

// TestDetectSpecificDeclarationWins covers the first rule of the contract: a
// declared type that is not generic is never second-guessed, even when the
// content plainly disagrees with it.
func TestDetectSpecificDeclarationWins(t *testing.T) {
	t.Parallel()

	require.Equal(t, contenttype.CSV, contenttype.Detect("text/csv", []byte(`{"a":1,"b":2}`)))
	require.Equal(t, contenttype.Markdown, contenttype.Detect("text/markdown", pngBytes))
	require.Equal(t, contenttype.HTML, contenttype.Detect("text/html", []byte("just words")))
	require.Equal(t, contenttype.JSON, contenttype.Detect("text/json; charset=utf-8", pdfBytes))
}

func TestDetectFamilies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		declared string
		content  []byte
		want     string
	}{
		// Structured text, all of which http.DetectContentType calls text/plain.
		{"json object", "", []byte(`{"name":"acme","rows":[1,2,3]}`), contenttype.JSON},
		{"json array", "", []byte(`[{"id":1},{"id":2}]`), contenttype.JSON},
		{"json empty array", "", []byte(`[]`), contenttype.JSON},
		{"json with leading whitespace", "", []byte("\n\n  {\"a\": 1}"), contenttype.JSON},
		{"json with utf8 bom", "", []byte("\uFEFF{\"a\": 1}"), contenttype.JSON},
		{"ndjson", "", []byte("{\"a\":1}\n{\"a\":2}\n{\"a\":3}\n"), contenttype.NDJSON},
		{"xml declaration", "", []byte(`<?xml version="1.0"?><catalog><item/></catalog>`), contenttype.XML},
		{"xml root element", "", []byte("<catalog>\n  <item id=\"1\"/>\n</catalog>\n"), contenttype.XML},
		{"yaml document marker", "", []byte("---\nname: acme\nrows:\n  - 1\n"), contenttype.YAML},
		{"yaml directive", "", []byte("%YAML 1.2\n---\nname: acme\n"), contenttype.YAML},
		{"csv", "", []byte("id,name,total\n1,acme,10\n2,globex,20\n3,initech,30\n"), contenttype.CSV},
		{"tsv", "", []byte("id\tname\ttotal\n1\tacme\t10\n2\tglobex\t20\n3\tinitech\t30\n"), contenttype.TSV},

		// Binary families, from http.DetectContentType.
		{"png", "", pngBytes, "image/png"},
		{"gif", "", gifBytes, "image/gif"},
		{"pdf", "", pdfBytes, contenttype.PDF},
		{"mp3", "", mp3Bytes, "audio/mpeg"},
		{"mp4", "", mp4Bytes, "video/mp4"},

		// Unstructured text and unknown binary.
		{"prose", "", []byte("The quarterly report is attached.\nRegards,\nOps\n"), contenttype.PlainText},
		{"unknown binary", "", []byte{0x00, 0x01, 0x02, 0x03, 0xff, 0xfe, 0x7f, 0x00}, contenttype.OctetStream},
		{"empty content no declaration", "", nil, contenttype.OctetStream},
		{"empty content generic declaration", "application/octet-stream", nil, contenttype.OctetStream},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, contenttype.Detect(tt.declared, tt.content))
		})
	}
}

// TestDetectMislabelMatrix is the acceptance case from issue #1007: content
// whose declared type is generic gets reclassified from the bytes, across every
// combination of generic declaration the write paths actually see.
func TestDetectMislabelMatrix(t *testing.T) {
	t.Parallel()

	jsonBody := []byte(`{"results":[{"id":1,"name":"acme"}],"total":1}`)
	csvBody := []byte("id,name\n1,acme\n2,globex\n3,initech\n")

	tests := []struct {
		name     string
		declared string
		content  []byte
		want     string
	}{
		{"json as text/plain", "text/plain", jsonBody, contenttype.JSON},
		{"json as text/plain with charset", "text/plain; charset=utf-8", jsonBody, contenttype.JSON},
		{"json as octet-stream", contenttype.OctetStream, jsonBody, contenttype.JSON},
		{"json with no declaration", "", jsonBody, contenttype.JSON},
		{"csv as octet-stream", contenttype.OctetStream, csvBody, contenttype.CSV},
		{"png as octet-stream", contenttype.OctetStream, pngBytes, "image/png"},
		{"pdf as text/plain", "text/plain", pdfBytes, contenttype.PDF},
		{"prose as octet-stream becomes text", contenttype.OctetStream, []byte("plain words here\n"), contenttype.PlainText},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, contenttype.Detect(tt.declared, tt.content))
		})
	}
}

// TestDetectNeverUpgradesToActiveType is the security rule of issue #1007:
// sniffing may reclassify content into passive families only. Content that
// sniffs as HTML, JSX or SVG must never come back as one of those types,
// because those render as executing markup.
func TestDetectNeverUpgradesToActiveType(t *testing.T) {
	t.Parallel()

	htmlBody := []byte("<!DOCTYPE html>\n<html><body><script>alert(1)</script></body></html>")
	svgBody := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)

	tests := []struct {
		name     string
		declared string
		content  []byte
		want     string
	}{
		{"html declared text/plain stays plain", "text/plain", htmlBody, contenttype.PlainText},
		{"html declared with charset stays plain", "text/plain; charset=utf-8", htmlBody, contenttype.PlainText},
		{"html declared octet-stream becomes plain", contenttype.OctetStream, htmlBody, contenttype.PlainText},
		{"html with no declaration becomes plain", "", htmlBody, contenttype.PlainText},
		{"svg declared text/plain stays plain", "text/plain", svgBody, contenttype.PlainText},
		{"svg with no declaration stays plain", "", svgBody, contenttype.PlainText},
		{"svg declared octet-stream becomes plain", contenttype.OctetStream, svgBody, contenttype.PlainText},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := contenttype.Detect(tt.declared, tt.content)
			require.Equal(t, tt.want, got)
			require.Falsef(t, contenttype.IsActive(got), "detection produced active type %q", got)
		})
	}
}

// TestDetectTruncatedPrefix proves detection works off a bounded window: a JSON
// document cut mid-token by the sniff limit is still classified as JSON.
func TestDetectTruncatedPrefix(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	buf.WriteString(`{"rows":[`)
	for i := range 5000 {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.WriteString(`{"id":`)
		buf.WriteString(strings.Repeat("9", 4))
		buf.WriteString(`,"name":"acme"}`)
	}
	buf.WriteString("]}")

	full := buf.Bytes()
	require.Greater(t, len(full), contenttype.StructuredSniffLen)

	require.Equal(t, contenttype.JSON, contenttype.DetectBytes("", full))
	require.Equal(t, contenttype.JSON, contenttype.Detect("", full[:contenttype.StructuredSniffLen]))
}

func TestDetectRejectsNonStructuredText(t *testing.T) {
	t.Parallel()

	// Each of these opens like a structured document but is not one, and must
	// fall back to plain text rather than reaching a structured viewer.
	tests := []struct {
		name    string
		content []byte
	}{
		{"lone brace", []byte("{")},
		{"broken json", []byte(`{"a": 1,,,}`)},
		{"prose starting with dash", []byte("- not yaml, just a bullet\nand more prose\n")},
		{"two-line csv is too short", []byte("a,b\n1,2\n")},
		{"ragged commas", []byte("one, two\nthree\nfour, five, six\n")},
		{"closing tag first", []byte("</item>\n<item>\n")},
		{"xml comment first", []byte("<!-- a comment -->\n<catalog/>\n")},
		{"processing instruction that is not xml", []byte("<?php echo 1; ?>\n")},
		{"element name starting with a digit", []byte("<1foo>bar</1foo>")},
		{"element name with an illegal character", []byte("<fo$o>x</fo$o>")},
		{"unterminated element", []byte("<catalog")},
		{"json lines with a non-json line", []byte("{\"a\":1}\nnot json at all\n{\"a\":2}\n")},
		{"json lines with a malformed line", []byte("{\"a\":1}\n{\"b\" 2}\n{\"c\":3}\n")},
		{"single line of json values then prose", []byte("{\"a\":1} and then prose\n")},
		{"truncated multibyte character", append([]byte("plain text tail "), 0xE2, 0x82)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := contenttype.Detect("", tt.content)
			require.Equal(t, contenttype.PlainText, got)
		})
	}
}

func TestDetectHTMLFragmentIsNotXML(t *testing.T) {
	t.Parallel()

	// A bare HTML fragment does not trip http.DetectContentType's HTML match,
	// so the XML root-element heuristic is what has to reject it.
	require.Equal(t, contenttype.PlainText, contenttype.Detect("", []byte("<div class=\"x\">hello</div>")))
	require.Equal(t, contenttype.PlainText, contenttype.Detect("", []byte("<span>hello</span>")))
}

func TestExtension(t *testing.T) {
	t.Parallel()

	tests := []struct {
		ct   string
		want string
	}{
		{contenttype.JSON, ".json"},
		{"text/json", ".json"},
		{contenttype.NDJSON, ".ndjson"},
		{contenttype.CSV, ".csv"},
		{contenttype.TSV, ".tsv"},
		{contenttype.XML, ".xml"},
		{"text/xml", ".xml"},
		{contenttype.YAML, ".yaml"},
		{contenttype.Markdown, ".md"},
		{contenttype.HTML, ".html"},
		{contenttype.JSX, ".html"},
		{contenttype.SVG, ".svg"},
		{contenttype.PDF, ".pdf"},
		{contenttype.PlainText, ".txt"},
		{"image/png", ".png"},
		{"image/jpg", ".jpg"},
		{"audio/mpeg", ".mp3"},
		{"video/mp4", ".mp4"},
		{"application/vnd.acme.thing+json", ".json"},
		{"application/atom+xml", ".xml"},
		{"application/vnd.acme+yaml", ".yaml"},
		{"text/x-unregistered", ".txt"},
		{contenttype.OctetStream, ".bin"},
		{"application/x-unknown-thing", ".bin"},
		{"", ".bin"},
	}

	for _, tt := range tests {
		t.Run(tt.ct, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, contenttype.Extension(tt.ct))
		})
	}
}

func TestDetectStream(t *testing.T) {
	t.Parallel()

	t.Run("classifies and replays the whole body", func(t *testing.T) {
		t.Parallel()
		body := `{"results":[{"id":1}],"total":1}`
		ct, replayed, err := contenttype.DetectStream("text/plain", strings.NewReader(body))
		require.NoError(t, err)
		require.Equal(t, contenttype.JSON, ct)

		got, readErr := io.ReadAll(replayed)
		require.NoError(t, readErr)
		require.Equal(t, body, string(got))
	})

	t.Run("replays a body longer than the sniff window", func(t *testing.T) {
		t.Parallel()
		body := `{"rows":[` + strings.Repeat(`{"id":1,"name":"acme"},`, 2000) + `{"id":2}]}`
		require.Greater(t, len(body), contenttype.StructuredSniffLen)

		ct, replayed, err := contenttype.DetectStream("", strings.NewReader(body))
		require.NoError(t, err)
		require.Equal(t, contenttype.JSON, ct)

		got, readErr := io.ReadAll(replayed)
		require.NoError(t, readErr)
		require.Equal(t, body, string(got))
	})

	t.Run("empty body", func(t *testing.T) {
		t.Parallel()
		ct, replayed, err := contenttype.DetectStream("", strings.NewReader(""))
		require.NoError(t, err)
		require.Equal(t, contenttype.OctetStream, ct)

		got, readErr := io.ReadAll(replayed)
		require.NoError(t, readErr)
		require.Empty(t, got)
	})

	t.Run("read error surfaces without losing consumed bytes", func(t *testing.T) {
		t.Parallel()
		want := errors.New("upstream reset")
		src := io.MultiReader(strings.NewReader("part"), errReader{want})

		ct, replayed, err := contenttype.DetectStream("text/csv", src)
		require.ErrorIs(t, err, want)
		require.Equal(t, contenttype.CSV, ct)

		got, _ := io.ReadAll(replayed)
		require.Equal(t, "part", string(got))
	})
}

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }
