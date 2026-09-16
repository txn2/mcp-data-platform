package apigateway

import (
	"encoding/json"
	"errors"
	"mime"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/txn2/mcp-data-platform/internal/soap"
	"github.com/txn2/mcp-data-platform/internal/xmltree"
)

// Decode modes a caller may ask for on api_invoke_endpoint. They are the
// values of the `decode` input and the values the tool schema enumerates.
const (
	// DecodeAuto is the default: JSON when the response says JSON, a
	// parsed XML tree when the catalog declares an XML response for the
	// operation, and the raw text otherwise.
	DecodeAuto = "auto"
	// DecodeJSON parses the body as JSON whatever it claims to be.
	DecodeJSON = "json"
	// DecodeXML parses the body as XML whatever it claims to be.
	DecodeXML = "xml"
	// DecodeText returns the body as a string, parsing nothing.
	DecodeText = "text"
)

// A mode outside this set never reaches the toolkit: `decode` is declared as an
// enum on the tool's input schema (schemas.go) and the SDK refuses an unknown
// value against it, naming the four. There is deliberately no second check
// here, and no REST shim path around it -- gatewayhttp's invokeRequest carries
// a fixed subset of the input that does not include decode.

// responseDecoder turns a response body into the value the tool returns.
//
// It carries the two facts the decision needs beyond the body itself: what the
// caller asked for, and what the catalog says the operation answers with. The
// response Content-Type arrives per call because only the response has it.
type responseDecoder struct {
	// mode is the caller's `decode` input, empty meaning auto.
	mode string
	// declaredXML records that the resolved operation declares an XML
	// media type on a success response. It is what lets auto mode decode a
	// SOAP or WebDAV answer without the caller passing anything, while a
	// catalog-less connection keeps returning the raw text it always has.
	declaredXML bool
	// soapOperation records that the operation is a SOAP one. It does two
	// things: it makes auto mode read an XML response without the catalog
	// having declared one, because a SOAP operation answers XML by
	// definition, and it makes the decoded tree worth checking for a
	// soap:Fault. Both are confined to auto and the XML branch, so a caller
	// who pinned decode=text gets the text they asked for.
	soapOperation bool
}

// The media types this file names more than once: the XML type an operation
// declares, and the OpenAPI responses key for the catch-all response.
const (
	applicationXML  = "application/xml"
	defaultResponse = "default"
	// statusCodeDigits is the length of an OpenAPI responses key that
	// names a status ("200") or a range ("2XX").
	statusCodeDigits = 3
)

// newResponseDecoder resolves what this call's response will be read as.
//
// The catalog is consulted only for a caller that did not pin the mode: a
// declared media type cannot change what an explicit decode asked for, and
// resolving it is a path match against every spec of the connection on a call
// whose answer is already known.
func newResponseDecoder(in InvokeInput, specs map[string]*specState) responseDecoder {
	d := responseDecoder{
		mode:          in.Decode,
		soapOperation: resolveSOAPOperation(specs, in.Method, in.Path) != nil,
	}
	if in.Decode == "" || in.Decode == DecodeAuto {
		d.declaredXML = resolveDeclaresXMLResponse(specs, in.Method, in.Path)
	}
	return d
}

// decoded is the outcome of one decode: the body value, whether it came back
// as JSON (which is the only shape the pagination probe understands), and the
// note to put on the output when a decode the caller asked for did not work.
type decoded struct {
	body any
	json bool
	note string
	// fault is a soap:Fault's own message, empty when the response is not
	// one. It becomes InvokeOutput.Error, which is what the audit record
	// and the caller read the failure by.
	fault string
}

// decode parses a response body according to the mode, the response
// Content-Type and the catalog.
//
// A parse failure never loses the body: the raw text is returned and the
// reason is carried as a note, because a caller that asked for XML and got an
// HTML error page is better served by seeing the page than by an empty tree.
func (d responseDecoder) decode(contentType string, body []byte) decoded {
	if len(body) == 0 {
		return decoded{}
	}
	switch d.effectiveMode(contentType) {
	case DecodeJSON:
		return decodeJSONBody(body, d.mode == DecodeJSON)
	case DecodeXML:
		out, root := decodeXMLBody(body)
		if d.soapOperation {
			if fault, isFault := soap.FaultFromTree(root); isFault {
				out.fault = fault.Message()
			}
		}
		return out
	default:
		return decoded{body: string(body)}
	}
}

// effectiveMode resolves auto against the response and the catalog. An
// explicit mode is returned unchanged, so a caller can always overrule both.
//
// Auto reads JSON exactly where it always did — the response says JSON — and
// reads XML only where the catalog declared it, never on the Content-Type
// alone. A connection with no catalog, and every WebDAV route the gateway
// already serves, therefore returns the same string body it returned before
// this existed; opting in is a catalog entry or the `decode` input.
func (d responseDecoder) effectiveMode(contentType string) string {
	if d.mode != "" && d.mode != DecodeAuto {
		return d.mode
	}
	mediaType := parseMediaType(contentType)
	if strings.Contains(mediaType, "json") {
		return DecodeJSON
	}
	if (d.declaredXML || d.soapOperation) && (mediaType == "" || isXMLMediaType(mediaType)) {
		return DecodeXML
	}
	return DecodeText
}

// decodeJSONBody parses JSON, falling back to the raw text. asked says the
// caller pinned decode=json, which is what decides whether the fallback says
// so: a caller who asked for a JSON reading and is holding a string is owed
// the reason, the way a forced XML read is (#1763). Auto's fallback stays
// silent and unchanged -- a JSON-typed response that is not JSON has been
// returned as text since the gateway shipped, and a note on every one of them
// would be noise on a path nobody asked to parse.
func decodeJSONBody(body []byte, asked bool) decoded {
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		out := decoded{body: string(body)}
		if asked {
			out.note = jsonDecodeNote(err)
		}
		return out
	}
	return decoded{body: v, json: true}
}

// jsonDecodeNote is the hint on a response the caller asked to read as JSON
// and that is not JSON. It says where the body went, because the caller asked
// for a value and is holding a string.
func jsonDecodeNote(err error) string {
	return "Could not read the response as JSON: " + err.Error() +
		". The body is returned as text instead; decode=xml reads an XML document, and decode=text asks for the string."
}

// decodeXMLBody parses XML into the same tree a managed script's xml.decode
// produces, so one document reads the same way through either surface.
//
// The parsed root is returned beside the rendered value so a caller that has
// more to ask of the document — whether it is a soap:Fault — reads the tree
// that was already built rather than parsing the body a second time.
func decodeXMLBody(body []byte) (decoded, *xmltree.Node) {
	root, err := xmltree.Decode(string(body), xmltree.DefaultLimits)
	if err != nil {
		return decoded{body: string(body), note: xmlDecodeNote(err)}, nil
	}
	return decoded{body: xmlValue(root)}, root
}

// xmlDecodeNote is the hint on a response that could not be read as XML. It
// says where the body went, because the caller asked for a tree and is holding
// a string.
func xmlDecodeNote(err error) string {
	action := "The body is returned as text instead."
	if errors.Is(err, xmltree.ErrTooLarge) || errors.Is(err, xmltree.ErrTooDeep) || errors.Is(err, xmltree.ErrTooManyNodes) {
		action = "The body is returned as text instead; use api_export to stream a document this large."
	}
	return "Could not read the response as XML: " + err.Error() + ". " + action
}

// xmlValue renders a decoded element as the nested maps the tool returns.
// The five fields are the ones a managed script sees on an element, so the
// same document has one shape whether it was read in a script or through a
// tool call.
func xmlValue(n *xmltree.Node) map[string]any {
	children := make([]any, 0, len(n.Children))
	for _, c := range n.Children {
		children = append(children, xmlValue(c))
	}
	attrs := make(map[string]any, len(n.Attrs))
	for k, v := range n.Attrs {
		attrs[k] = v
	}
	return map[string]any{
		"tag":      n.Tag,
		"ns":       n.NS,
		"attrs":    attrs,
		"text":     n.Text,
		"children": children,
	}
}

// parseMediaType returns the lowercase media type of a Content-Type header,
// dropping its parameters. An unparseable header yields the empty string,
// which every caller reads as "the response said nothing usable".
func parseMediaType(contentType string) string {
	if contentType == "" {
		return ""
	}
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return ""
	}
	return strings.ToLower(mt)
}

// isXMLMediaType reports whether a media type is an XML one, covering the
// +xml structured suffix that SOAP 1.2 (application/soap+xml) and most
// modern XML vocabularies use.
func isXMLMediaType(mediaType string) bool {
	return mediaType == "text/xml" || mediaType == applicationXML || strings.HasSuffix(mediaType, "+xml")
}

// resolveDeclaresXMLResponse reports whether the catalog operation this
// request resolves to declares an XML media type on a success response.
//
// It is the response-side twin of resolveDeclaredContentTypes and matches the
// operation the same way, through the same path index and the same WebDAV
// route table, so the media type the decoder acts on and the operation_id the
// metrics label always come from one operation.
func resolveDeclaresXMLResponse(specs map[string]*specState, method, path string) bool {
	if len(specs) == 0 {
		return false
	}
	path = stripQueryAndFragment(path)
	upperMethod := strings.ToUpper(method)
	for _, st := range specs {
		for _, ct := range successResponseContentTypes(st, upperMethod, path) {
			if isXMLMediaType(strings.ToLower(ct)) {
				return true
			}
		}
	}
	return false
}

// successResponseContentTypes returns the media types one spec declares for
// the operation's success responses.
func successResponseContentTypes(st *specState, method, path string) []string {
	if st == nil || st.doc == nil || st.doc.Paths == nil {
		return nil
	}
	item := findMostSpecificPathMatch(st, path)
	if item == nil {
		return nil
	}
	op := operationForMethod(item, method)
	if op == nil || op.Responses == nil {
		return nil
	}
	return collectSuccessContentTypes(op.Responses)
}

// collectSuccessContentTypes gathers the media types of every 2xx response,
// falling back to `default` when the document declares no success status.
func collectSuccessContentTypes(responses *openapi3.Responses) []string {
	var (
		success  []string
		fallback []string
	)
	for status, ref := range responses.Map() {
		if ref == nil || ref.Value == nil {
			continue
		}
		types := sortedContentTypes(ref.Value.Content)
		switch {
		case isSuccessStatus(status):
			success = append(success, types...)
		case status == defaultResponse:
			fallback = append(fallback, types...)
		}
	}
	if len(success) == 0 {
		success = fallback
	}
	sort.Strings(success)
	return success
}

// isSuccessStatus reports whether an OpenAPI responses key is a 2xx one,
// accepting both the concrete form ("200") and the range form ("2XX").
func isSuccessStatus(status string) bool {
	return len(status) == statusCodeDigits && status[0] == '2'
}
