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
//   - A specific declared type wins unconditionally. Detection only runs when
//     the declaration is empty or generic (see IsGeneric).
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
var activeTypes = map[string]bool{
	HTML:       true,
	JSX:        true,
	SVG:        true,
	JavaScript: true,
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
	case JSON, NDJSON, XML, YAML, SVG, JavaScript, "application/sql":
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
	norm := Normalize(declared)
	if norm != "" && !IsGeneric(norm) {
		return norm
	}
	if len(prefix) == 0 {
		return fallback(norm)
	}

	sniffed := Normalize(http.DetectContentType(prefix))
	switch {
	case IsActive(sniffed):
		// Refuse the upgrade: keep the declaration, or plain text when the
		// writer declared nothing. The content is textual either way.
		return textFallback(norm)
	case sniffed != PlainText && !IsGeneric(sniffed):
		// A recognized binary family (image, audio, video, PDF, archive) or a
		// passive text family the sniffer names outright, such as XML.
		return sniffed
	}

	// http.DetectContentType reports JSON, NDJSON, XML, YAML, CSV and TSV all
	// as text/plain, so the structured heuristics run over the wider prefix.
	if structured := detectStructuredText(prefix); structured != "" {
		return structured
	}
	if sniffed == PlainText {
		// Textual but unstructured. Upgrading application/octet-stream to
		// text/plain is passive and lets the viewer show it as text.
		return PlainText
	}
	return fallback(norm)
}

// DetectBytes is Detect over a complete payload, truncating to the sniff window.
func DetectBytes(declared string, data []byte) string {
	if len(data) > StructuredSniffLen {
		data = data[:StructuredSniffLen]
	}
	return Detect(declared, data)
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
