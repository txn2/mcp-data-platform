package apigateway

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"mime/multipart"
	"net/textproto"
	"slices"
	"sort"
	"strings"
)

// multipartFormData is the media type this encoder answers for. An
// operation whose catalog entry declares it (or a caller who pins it in
// headers) gets a body assembled by mime/multipart rather than
// json.Marshal (issue #1296).
const multipartFormData = "multipart/form-data"

// octetStream is the part Content-Type used for a file part that does
// not name one. RFC 7578 section 4.4 makes it the default for a part
// carrying a filename.
const octetStream = "application/octet-stream"

// Field names recognized inside a body value that describes one part
// rather than a plain field. A map carrying any of them is a part
// descriptor, and a descriptor may carry nothing else: a misspelled
// attribute alongside a recognized one ("file_name" with "content") is
// refused by name rather than dropped from the part that goes out.
const (
	partKeyFilename      = "filename"
	partKeyContentType   = "content_type"
	partKeyContent       = "content"
	partKeyContentBase64 = "content_base64"
)

// quoteEscaper escapes the two characters that are not representable
// raw inside a quoted-string Content-Disposition parameter. It mirrors
// what mime/multipart's own CreateFormFile does; the parts here are
// built through CreatePart instead because a part may also need an
// explicit Content-Type.
//
//nolint:gochecknoglobals // compiled once; used on every part header
var quoteEscaper = strings.NewReplacer("\\", "\\\\", `"`, "\\\"")

// isMultipartFormData reports whether contentType names multipart/form-data,
// ignoring any parameters (a boundary) and casing. An unparseable value
// falls back to comparing the text before the first ";" so a caller's
// malformed header still routes to the multipart encoder rather than
// being silently JSON-encoded under a multipart header.
func isMultipartFormData(contentType string) bool {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mt = strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
	}
	return mt == multipartFormData
}

// encodeMultipartBody assembles a multipart/form-data body from an
// object body and returns it with the Content-Type carrying the
// gateway-generated boundary.
//
// The body must be a JSON object; each key is one form field:
//
//   - a scalar becomes a plain text field,
//   - an array becomes one part per element under the same field name,
//   - an object carrying any of filename / content / content_base64 /
//     content_type becomes a part with those attributes: content is sent
//     as UTF-8 text, content_base64 is decoded first, and a part naming a
//     filename defaults to application/octet-stream. Such an object may
//     carry no other key, so a typo is refused rather than dropped,
//   - any other object is JSON-encoded into a text field, which is how
//     upstreams that take a JSON metadata field alongside a file part
//     expect it.
//
// The returned encoding is authoritative: the boundary is generated
// here and a caller-supplied Content-Type header must not survive,
// because a boundary that does not match these bytes yields a body the
// upstream parses as zero parts (issue #1296).
func encodeMultipartBody(body any) (encodedBody, error) {
	fields, ok := body.(map[string]any)
	if !ok {
		return encodedBody{}, fmt.Errorf(
			"apigateway: this operation takes multipart/form-data, so body must be an object of form fields, not %T. "+
				"A file part is {\"filename\": \"data.csv\", \"content\": \"...\"} (or \"content_base64\" for binary); "+
				"every other value is sent as a text field. The gateway generates the multipart boundary, so do not "+
				"assemble the body or set Content-Type yourself", body)
	}
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, name := range names {
		if err := writeMultipartField(w, name, fields[name]); err != nil {
			return encodedBody{}, err
		}
	}
	if err := w.Close(); err != nil {
		return encodedBody{}, fmt.Errorf("apigateway: finishing multipart body: %w", err)
	}
	return encodedBody{data: buf.Bytes(), contentType: w.FormDataContentType(), authoritative: true}, nil
}

// writeMultipartField writes the part(s) one body key produces. See
// encodeMultipartBody for the value-shape rules.
func writeMultipartField(w *multipart.Writer, name string, value any) error {
	if err := rejectHeaderBreak(name, "field name", name); err != nil {
		return err
	}
	switch v := value.(type) {
	case nil:
		return nil
	case []any:
		return writeMultipartElements(w, name, v)
	case map[string]any:
		return writeMultipartObject(w, name, v)
	default:
		return writeMultipartText(w, name, scalarToString(v))
	}
}

// writeMultipartElements sends one part per array element under the
// same field name, which is how form encoding represents a repeated
// field (and how an upstream taking several files under one name
// expects them).
func writeMultipartElements(w *multipart.Writer, name string, items []any) error {
	for _, item := range items {
		if err := writeMultipartField(w, name, item); err != nil {
			return err
		}
	}
	return nil
}

// writeMultipartObject routes an object value: a part descriptor to the
// part writer, anything else to a JSON-encoded text field.
func writeMultipartObject(w *multipart.Writer, name string, obj map[string]any) error {
	if isPartDescriptor(obj) {
		return writeMultipartPart(w, name, obj)
	}
	encoded, err := json.Marshal(obj)
	if err != nil {
		return fmt.Errorf("apigateway: encoding multipart field %q as JSON: %w", name, err)
	}
	return writeMultipartText(w, name, string(encoded))
}

// writeMultipartText writes a plain text field part.
func writeMultipartText(w *multipart.Writer, name, value string) error {
	if err := w.WriteField(name, value); err != nil {
		return fmt.Errorf("apigateway: writing multipart field %q: %w", name, err)
	}
	return nil
}

// partKeys is the closed set of attributes a part descriptor may carry.
//
//nolint:gochecknoglobals // intentionally a package-level constant set
var partKeys = []string{partKeyFilename, partKeyContentType, partKeyContent, partKeyContentBase64}

// isPartDescriptor reports whether an object value describes one part
// (a file or an explicitly typed field) rather than a nested structure
// to JSON-encode. Presence of any recognized key is enough; validation
// of the combination happens in writeMultipartPart, so a half-formed
// descriptor produces a message naming what is missing.
func isPartDescriptor(obj map[string]any) bool {
	for _, key := range partKeys {
		if _, ok := obj[key]; ok {
			return true
		}
	}
	return false
}

// rejectUnknownPartKeys refuses a descriptor carrying an attribute the
// encoder does not recognize. Ignoring one would silently drop data the
// caller meant to send — a descriptor is not a passthrough object, so
// every key has to be one the part can carry.
func rejectUnknownPartKeys(spec map[string]any, field string) error {
	unknown := make([]string, 0, len(spec))
	for key := range spec {
		if !slices.Contains(partKeys, key) {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return fmt.Errorf("apigateway: multipart field %q: unknown part attribute(s) %s; a part carries only %s",
		field, strings.Join(unknown, ", "), strings.Join(partKeys, ", "))
}

// writeMultipartPart writes one part from a descriptor object.
func writeMultipartPart(w *multipart.Writer, name string, spec map[string]any) error {
	if err := rejectUnknownPartKeys(spec, name); err != nil {
		return err
	}
	filename, err := partString(spec, name, partKeyFilename)
	if err != nil {
		return err
	}
	contentType, err := partString(spec, name, partKeyContentType)
	if err != nil {
		return err
	}
	data, err := partContent(spec, name)
	if err != nil {
		return err
	}
	if contentType == "" && filename != "" {
		contentType = octetStream
	}
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", partDisposition(name, filename))
	if contentType != "" {
		header.Set(headerContentType, contentType)
	}
	pw, err := w.CreatePart(header)
	if err != nil {
		return fmt.Errorf("apigateway: creating multipart part %q: %w", name, err)
	}
	if _, err := pw.Write(data); err != nil {
		return fmt.Errorf("apigateway: writing multipart part %q: %w", name, err)
	}
	return nil
}

// partDisposition builds the Content-Disposition header for one part.
// Both the field name and the filename are quoted and escaped;
// rejectHeaderBreak has already refused a value that could break out of
// the header.
func partDisposition(name, filename string) string {
	d := `form-data; name="` + quoteEscaper.Replace(name) + `"`
	if filename != "" {
		d += `; filename="` + quoteEscaper.Replace(filename) + `"`
	}
	return d
}

// partString reads an optional string attribute off a part descriptor.
// A non-string value is an error rather than a coerced string: the
// caller meant something the gateway would otherwise silently reshape.
// The value is refused outright when it could break the part header.
func partString(spec map[string]any, field, key string) (string, error) {
	raw, ok := spec[key]
	if !ok || raw == nil {
		return "", nil
	}
	s, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("apigateway: multipart field %q: %s must be a string, got %T", field, key, raw)
	}
	if err := rejectHeaderBreak(field, key, s); err != nil {
		return "", err
	}
	return s, nil
}

// partContent returns the bytes a part descriptor carries. Exactly one
// of content (UTF-8 text) or content_base64 (decoded) must be present.
func partContent(spec map[string]any, field string) ([]byte, error) {
	raw, hasRaw := spec[partKeyContent]
	encoded, hasEncoded := spec[partKeyContentBase64]
	switch {
	case hasRaw && hasEncoded:
		return nil, fmt.Errorf("apigateway: multipart field %q sets both %s and %s; supply exactly one",
			field, partKeyContent, partKeyContentBase64)
	case hasRaw:
		s, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("apigateway: multipart field %q: %s must be a string, got %T",
				field, partKeyContent, raw)
		}
		return []byte(s), nil
	case hasEncoded:
		return decodePartBase64(encoded, field)
	default:
		return nil, fmt.Errorf("apigateway: multipart field %q describes a part but carries no bytes; set %s (text) or %s (binary)",
			field, partKeyContent, partKeyContentBase64)
	}
}

// decodePartBase64 decodes a content_base64 attribute, accepting both
// the padded and unpadded standard alphabets because models emit
// either.
func decodePartBase64(value any, field string) ([]byte, error) {
	s, ok := value.(string)
	if !ok {
		return nil, fmt.Errorf("apigateway: multipart field %q: %s must be a string, got %T",
			field, partKeyContentBase64, value)
	}
	if decoded, err := base64.StdEncoding.DecodeString(s); err == nil {
		return decoded, nil
	}
	decoded, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(s, "="))
	if err != nil {
		return nil, fmt.Errorf("apigateway: multipart field %q: %s is not valid base64: %w",
			field, partKeyContentBase64, err)
	}
	return decoded, nil
}

// rejectHeaderBreak refuses a value carrying CR or LF. Field names,
// filenames, and part content types are written into part headers
// verbatim, so a line break would let tool arguments append headers of
// their own to the part.
func rejectHeaderBreak(field, kind, value string) error {
	if strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("apigateway: multipart field %q: %s must not contain a line break", field, kind)
	}
	return nil
}
