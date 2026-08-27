// Package contenttype detects and normalizes the media type of stored content.
//
// Every platform write path that accepts content from an outside source (an
// upstream API response, a multipart upload, a tool argument) reaches this
// package. A declared type that is specific is always honored; a declared type
// that is absent or generic is replaced with a type sniffed from the content
// itself, so a JSON payload an upstream API labeled text/plain still reaches
// the viewer as application/json.
//
// # Detection contract
//
//   - A specific declared type wins. Detection only runs when the declaration
//     is empty or generic (see IsGeneric).
//   - The one exception is a caller that has the filename and uses DetectFile:
//     a declaration both the extension and the content contradict loses to
//     what those two agree on. A .csv uploaded from a machine that declared
//     application/vnd.ms-excel is stored as a CSV.
//   - Binary families come from http.DetectContentType, which recognizes
//     images, audio, video, PDF and archives from their magic bytes.
//   - Structured text (JSON, NDJSON, XML, YAML, CSV, TSV) is layered on top,
//     because http.DetectContentType reports every one of them as text/plain.
//   - Detection reads a bounded prefix, never the whole payload, so streaming
//     writers do not have to buffer.
//
// # Active-type rule
//
// Detection never promotes content to an active type (see IsActive). Active
// types execute script when a viewer renders them, so they render only when an
// author declared them deliberately. Content that sniffs as HTML but was
// declared text/plain stays text/plain; content that sniffs as HTML with no
// declaration at all becomes text/plain. This keeps a mislabeled upload from
// turning itself into script-bearing content.
//
// # Scriptable-document rule
//
// IsActive answers what detection may produce. It does not answer whether
// stored bytes may render inline on the platform's origin, which is a wider
// question: XML is safe to name from content and unsafe to render, because a
// browser navigating to it builds a document that honors an <?xml-stylesheet?>
// processing instruction. IsScriptableDocument answers that one, over a
// superset of the active types.
package contenttype

import (
	"mime"
	"net/http"
	"regexp"
	"strings"
)

// Canonical media types. Detection and normalization collapse every alias of a
// family onto exactly one of these values, so downstream consumers (renderer
// registry, extension mapping, viewer selection) match on one string per family.
const (
	// JSON is the canonical type for JSON documents.
	JSON = "application/json"
	// NDJSON is the canonical type for newline-delimited JSON.
	NDJSON = "application/x-ndjson"
	// CSV is the canonical type for comma-separated values.
	CSV = "text/csv"
	// TSV is the canonical type for tab-separated values.
	TSV = "text/tab-separated-values"
	// XML is the canonical type for XML documents.
	XML = "application/xml"
	// YAML is the canonical type for YAML documents.
	YAML = "application/yaml"
	// Markdown is the canonical type for Markdown documents.
	Markdown = "text/markdown"
	// PlainText is the canonical type for unstructured text.
	PlainText = "text/plain"
	// HTML is the canonical type for HTML documents. Active.
	HTML = "text/html"
	// JSX is the canonical type for React/JSX components. Active.
	JSX = "text/jsx"
	// SVG is the canonical type for SVG images. Active.
	SVG = "image/svg+xml"
	// XHTML is the canonical type for XHTML documents. Active: a browser
	// renders XHTML natively and runs the script inside it.
	XHTML = "application/xhtml+xml"
	// JavaScript is the canonical type for standalone JavaScript source.
	JavaScript = "text/javascript"
	// PDF is the canonical type for PDF documents.
	PDF = "application/pdf"
	// OctetStream is the type for content of unknown or unrecognized shape.
	OctetStream = "application/octet-stream"
)

// BinarySniffLen is the prefix length http.DetectContentType examines. Reading
// more than this for the binary sniff cannot change its answer.
const BinarySniffLen = 512

// StructuredSniffLen is the prefix length the structured-text heuristics
// examine. It is large enough to hold several rows of a CSV or the opening
// tokens of a JSON document while staying cheap for a streaming writer to hold
// in memory.
const StructuredSniffLen = 8192

// aliases maps every non-canonical spelling of a family onto its canonical
// type. Keys are already lowercased and parameter-free.
var aliases = map[string]string{
	"text/json":                    JSON,
	"application/x-json":           JSON,
	"application/ld+json":          JSON,
	"application/ndjson":           NDJSON,
	"text/x-ndjson":                NDJSON,
	"application/jsonl":            NDJSON,
	"text/xml":                     XML,
	"application/x-xml":            XML,
	"text/yaml":                    YAML,
	"text/x-yaml":                  YAML,
	"application/x-yaml":           YAML,
	"application/csv":              CSV,
	"text/comma-separated-values":  CSV,
	"text/tsv":                     TSV,
	"text/x-tsv":                   TSV,
	"text/x-markdown":              Markdown,
	"application/markdown":         Markdown,
	"application/javascript":       JavaScript,
	"application/x-javascript":     JavaScript,
	"text/x-jsx":                   JSX,
	"application/jsx":              JSX,
	"text/babel":                   JSX,
	"application/svg+xml":          SVG,
	"binary/octet-stream":          OctetStream,
	"application/binary":           OctetStream,
	"application/unknown":          OctetStream,
	"audio/mp3":                    "audio/mpeg",
	"audio/x-wav":                  "audio/wav",
	"audio/wave":                   "audio/wav",
	"audio/x-flac":                 "audio/flac",
	"audio/x-m4a":                  "audio/mp4",
	"image/jpg":                    "image/jpeg",
	"image/x-png":                  "image/png",
	"video/x-m4v":                  "video/mp4",
	"application/x-yaml-stream":    YAML,
	"application/vnd.api+json":     JSON,
	"application/problem+json":     JSON,
	"application/x-sql":            "application/sql",
	"text/x-sql":                   "application/sql",
	"text/x-python-script":         "text/x-python",
	"application/x-python-code":    "text/x-python",
	"application/x-zip-compressed": "application/zip",
}

// activeTypes are the media types whose renderers execute author-supplied
// script or markup. Detection may never produce one of these from content
// alone; only an explicit declaration can.
//
// This set governs detection, not serving. Adding a type here stops a
// mislabeled upload from turning itself into script-bearing content; it does
// not decide whether stored bytes may render inline on the platform's origin.
// That question is IsScriptableDocument, whose set is a superset of this one,
// because a family can be safe to name from content and still unsafe to render
// (see scriptableDocumentTypes). Adding a family to only one of the two sets is
// almost always a mistake.
var activeTypes = map[string]bool{
	HTML:       true,
	JSX:        true,
	SVG:        true,
	JavaScript: true,
	XHTML:      true,
}

// scriptableDocumentTypes are the media types a browser turns into a live
// markup document, with a render tree the author of the bytes controls. It is
// the active types plus the XML family: XML is safe to name from content and a
// viewer shows it as inert text, but a browser navigating to application/xml
// builds a document and honors an <?xml-stylesheet?> processing instruction.
//
// This set is security-relevant. It is what keeps stored, author-controlled
// bytes from rendering as a document on the platform's own origin, where script
// would inherit the viewer's session. A renderable family added to the platform
// belongs here unless a browser is known to render it inert.
//
// The set is a floor, not the whole defense: blobserve serves every response
// under a sandbox CSP, so a family missed here is contained rather than
// exploitable. IsScriptableDocument additionally treats any `+xml` structured
// suffix as a member, so an unregistered XML dialect is covered without an
// entry.
var scriptableDocumentTypes = newScriptableDocumentSet()

// newScriptableDocumentSet derives the scriptable-document set from
// activeTypes, so the two cannot drift: every active type is by definition a
// scriptable document.
func newScriptableDocumentSet() map[string]bool {
	set := map[string]bool{XML: true}
	for ct := range activeTypes {
		set[ct] = true
	}
	return set
}

// genericTypes are declarations that carry no information about the shape of
// the content, and so do not suppress detection.
var genericTypes = map[string]bool{
	"":          true,
	OctetStream: true,
	PlainText:   true,
}

// mediaTypeRe matches a well-formed `type/subtype` against the RFC 2045 token
// grammar. Normalize applies it to whatever it recovers from a declaration, so
// a value that is not a media type never reaches a caller — a Content-Type
// header written from an unvalidated string is a header-injection vector, and a
// stored type that is not a media type has no family to render as.
var mediaTypeRe = regexp.MustCompile(`^[a-z0-9][a-z0-9!#$&^_.+-]*/[a-z0-9][a-z0-9!#$&^_.+-]*$`)

// Normalize reduces a declared media type to its canonical, parameter-free,
// lowercase form. It returns the empty string when the input is empty or is not
// a well-formed media type.
func Normalize(declared string) string {
	trimmed := strings.TrimSpace(declared)
	if trimmed == "" {
		return ""
	}
	base, _, err := mime.ParseMediaType(trimmed)
	if err != nil {
		// A bare type with a malformed parameter still carries a usable base;
		// fall back to everything before the first ';'. The grammar check
		// below is what keeps this from admitting arbitrary text.
		base = strings.Split(trimmed, ";")[0]
	}
	base = strings.ToLower(strings.TrimSpace(base))
	if !mediaTypeRe.MatchString(base) {
		return ""
	}
	if canonical, ok := aliases[base]; ok {
		return canonical
	}
	return base
}

// IsActive reports whether a media type renders as executable markup or script.
// Detection never upgrades content into one of these types.
func IsActive(ct string) bool {
	return activeTypes[Normalize(ct)]
}

// IsScriptableDocument reports whether a browser navigating to a response of
// this type builds a document whose render tree the author of the bytes
// controls. Stored content of such a type must never be served for inline
// rendering on the platform's origin.
//
// Any `+xml` structured suffix qualifies, so an XML dialect that has no entry
// in scriptableDocumentTypes is still covered.
func IsScriptableDocument(ct string) bool {
	norm := Normalize(ct)
	return scriptableDocumentTypes[norm] || strings.HasSuffix(norm, "+xml")
}

// IsImage reports whether a media type names an image family. It is the accept
// decision for a slot that holds a picture and nothing else -- a brand logo, a
// thumbnail -- taken over the type this package resolved rather than over a
// Content-Type header a caller read for itself.
//
// Every image family qualifies, including SVG: a caller that must distinguish
// vector from raster compares against SVG directly, because the two are
// inlined differently (markup for one, a data: URI for the other) even though
// both are images.
func IsImage(ct string) bool {
	return strings.HasPrefix(Normalize(ct), "image/")
}

// IsGeneric reports whether a declared media type is uninformative enough that
// the content itself should be consulted.
func IsGeneric(ct string) bool {
	return genericTypes[Normalize(ct)]
}

// IsTextual reports whether a canonical media type holds human-readable text,
// and so can be loaded into a text editor or embedded in a page as a string.
func IsTextual(ct string) bool {
	norm := Normalize(ct)
	if strings.HasPrefix(norm, "text/") {
		return true
	}
	switch norm {
	case JSON, NDJSON, XML, YAML, SVG, JavaScript, "application/sql", "application/typescript":
		return true
	default:
		return false
	}
}

// Detect returns the canonical media type for content whose first bytes are
// prefix and whose writer declared declared.
//
// A specific declaration is returned unchanged (normalized). A generic or
// absent declaration is replaced by the type sniffed from prefix, subject to
// the active-type rule: a sniff that lands on HTML, JSX, SVG or JavaScript is
// discarded in favor of the declaration (or text/plain when there was none).
//
// prefix should hold at least BinarySniffLen bytes for binary families and up
// to StructuredSniffLen bytes for the structured-text heuristics; a shorter
// prefix simply yields a less confident answer.
func Detect(declared string, prefix []byte) string {
	return DetectFile(declared, "", prefix)
}

// DetectFile is Detect for content that arrived under a filename.
//
// A specific declaration still wins, with one exception: when the filename's
// extension and the content itself both name a different family, that family
// wins. Neither signal is enough on its own -- a name is not evidence about
// bytes, and content may not upgrade itself past a declaration -- but a
// declaration contradicted by both is wrong about what it labels.
//
// This is what makes a .csv usable when the uploading machine declared
// application/vnd.ms-excel, which is what Windows sends for .csv when Excel is
// installed. It does not promote a mislabeled binary on the strength of its
// name: a .csv holding a PNG sniffs as image/png, disagrees with the name, and
// keeps its declaration.
//
// A filename of "" makes this identical to Detect.
func DetectFile(declared, filename string, prefix []byte) string {
	norm := Normalize(declared)
	if norm != "" && !IsGeneric(norm) {
		return declaredOrNamed(norm, filename, prefix)
	}
	if len(prefix) == 0 {
		return fallback(norm)
	}

	sniffed := Normalize(http.DetectContentType(prefix))
	if IsActive(sniffed) {
		// Refuse the upgrade: keep the declaration, or plain text when the
		// writer declared nothing. The content is textual either way.
		return textFallback(norm)
	}
	if content := specificContentType(sniffed, prefix); content != "" {
		return content
	}
	if sniffed == PlainText {
		// Textual but unstructured. Upgrading application/octet-stream to
		// text/plain is passive and lets the viewer show it as text.
		return PlainText
	}
	return fallback(norm)
}

// declaredOrNamed resolves a specific declaration against the filename and the
// content, returning the type the last two agree on when it contradicts the
// declaration, and the declaration otherwise.
func declaredOrNamed(norm, filename string, prefix []byte) string {
	named := TypeForFilename(filename)
	if named == "" || named == norm || len(prefix) == 0 {
		return norm
	}
	// A declaration the extension table would spell the same way is not
	// contradicting the name: application/rss+xml on a .xml, or a vendor
	// `+json` type on a .json, is a narrower answer than the table holds
	// rather than a wrong one, and it survives.
	if Extension(norm) == Extension(named) {
		return norm
	}
	if named != sniffType(prefix) {
		return norm
	}
	return named
}

// sniffType names the family the bytes themselves identify, or the empty
// string when they identify none -- unstructured text and unrecognized binary
// both land there, as does content that sniffs active, which detection may
// never name from content alone.
func sniffType(prefix []byte) string {
	sniffed := Normalize(http.DetectContentType(prefix))
	if IsActive(sniffed) {
		return ""
	}
	return specificContentType(sniffed, prefix)
}

// specificContentType returns the family the content identifies, given what
// the binary sniffer already made of it, or "" when it identifies none.
func specificContentType(sniffed string, prefix []byte) string {
	if sniffed != PlainText && !IsGeneric(sniffed) {
		// A recognized binary family (image, audio, video, PDF, archive) or a
		// passive text family the sniffer names outright, such as XML.
		return sniffed
	}
	// http.DetectContentType reports JSON, NDJSON, XML, YAML, CSV and TSV all
	// as text/plain, so the structured heuristics run over the wider prefix.
	return detectStructuredText(prefix)
}

// DetectBytes is Detect over a complete payload, truncating to the sniff window.
func DetectBytes(declared string, data []byte) string {
	return DetectFileBytes(declared, "", data)
}

// DetectFileBytes is DetectFile over a complete payload, truncating to the
// sniff window.
func DetectFileBytes(declared, filename string, data []byte) string {
	if len(data) > StructuredSniffLen {
		data = data[:StructuredSniffLen]
	}
	return DetectFile(declared, filename, data)
}

// fallback picks the type to report when detection produced nothing usable.
func fallback(norm string) string {
	if norm != "" {
		return norm
	}
	return OctetStream
}

// textFallback picks the type to report when detection produced an active type
// and had to be discarded. The content is known to be textual.
func textFallback(norm string) string {
	if norm != "" && norm != OctetStream {
		return norm
	}
	return PlainText
}
