package apigateway

import (
	"fmt"
	"maps"
	"net/http"
	"strconv"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/txn2/mcp-data-platform/internal/soap"
)

// The headers a SOAP request carries beyond the ordinary ones. SOAP 1.1 sends
// the action as its own header; 1.2 moved it into a Content-Type parameter,
// and sending the 1.1 header to a 1.2 endpoint is a common way to be refused.
const (
	headerSOAPAction = "SOAPAction"
	headerAccept     = "Accept"
	charsetUTF8      = "; charset=utf-8"
)

// soapOperation is a catalog operation the gateway has to speak SOAP to.
//
// Everything it needs travels in the rendered document: the extension says
// which envelope to write, which action to announce and which address the
// operation is really sent to, and the request schema says where each field of
// the caller's object goes. The gateway keeps no memory of the WSDL those came
// from, which is what lets a spec be shared, refreshed or replaced without the
// invoke path knowing.
type soapOperation struct {
	ext    *soap.Extension
	schema *openapi3.Schema
}

// resolveSOAPOperation returns the SOAP operation a request addresses, nil
// when the operation is an ordinary one.
//
// Only POST is considered because every SOAP operation is one; the check saves
// walking the path index for the GET that a REST-shaped connection is mostly
// made of.
func resolveSOAPOperation(specs map[string]*specState, method, path string) *soapOperation {
	if len(specs) == 0 || method != http.MethodPost {
		return nil
	}
	path = stripQueryAndFragment(path)
	for _, st := range specs {
		if st == nil || st.doc == nil {
			continue
		}
		item := findMostSpecificPathMatch(st, path)
		if item == nil {
			continue
		}
		op := operationForMethod(item, method)
		ext, ok := soap.ExtensionFrom(op)
		if !ok {
			continue
		}
		return &soapOperation{ext: ext, schema: soapRequestSchema(op)}
	}
	return nil
}

// soapRequestSchema returns the schema of the operation's request body, nil
// when it declares none.
func soapRequestSchema(op *openapi3.Operation) *openapi3.Schema {
	if op == nil || op.RequestBody == nil || op.RequestBody.Value == nil {
		return nil
	}
	for _, mt := range op.RequestBody.Value.Content {
		if mt != nil && mt.Schema != nil {
			return mt.Schema.Value
		}
	}
	return nil
}

// wirePath is the path a request is actually sent to.
//
// Every operation of a SOAP service is a POST to one address, which one
// OpenAPI path item cannot hold, so the rendered document keys each operation
// under the address followed by the operation name and carries the address
// itself in the extension. The document key is what discovery lists and what a
// path-shaped persona rule matches; this is what reaches the upstream.
func wirePath(sop *soapOperation, path string) string {
	if sop == nil || sop.ext.Path == "" {
		return path
	}
	return sop.ext.Path
}

// encodeFor builds the request body and the headers for one call, which for a
// SOAP operation means assembling an envelope the caller never has to see.
//
// A string body still goes out verbatim: a caller holding an envelope of their
// own — one this importer's schema cannot express, one copied from a vendor's
// documentation — keeps the way through it has always had, and only an object
// body is assembled here.
func encodeFor(sop *soapOperation, method string, in InvokeInput, declared []string) (encodedBody, map[string]string, error) {
	if sop == nil {
		enc, err := encodeBody(method, in.Body, declared, in.Headers)
		return enc, in.Headers, err
	}
	enc, err := sop.encodeBody(in.Body)
	if err != nil {
		return encodedBody{}, nil, err
	}
	return enc, sop.headers(in.Headers), nil
}

// encodeBody renders the caller's body as the envelope the upstream reads.
func (s *soapOperation) encodeBody(body any) (encodedBody, error) {
	contentType := s.contentType()
	if raw, isString := body.(string); isString {
		return encodedBody{data: []byte(raw), contentType: contentType}, nil
	}
	if body == nil {
		body = map[string]any{}
	}
	envelope, err := soap.Encode(s.ext, s.schema, body)
	if err != nil {
		return encodedBody{}, fmt.Errorf("apigateway: building the SOAP request: %w", err)
	}
	return encodedBody{data: []byte(envelope), contentType: contentType}, nil
}

// contentType is the media type the operation's envelope is sent as. SOAP 1.2
// carries the action as a parameter of it rather than as its own header.
func (s *soapOperation) contentType() string {
	if s.ext.Version == soap.Version12 {
		ct := soap.MediaType12 + charsetUTF8
		if s.ext.Action != "" {
			ct += "; action=" + strconv.Quote(s.ext.Action)
		}
		return ct
	}
	return soap.MediaType11 + charsetUTF8
}

// headers adds what a SOAP request announces about itself, leaving anything
// the caller set in place.
//
// The caller's map is copied rather than written to: it belongs to the
// caller's InvokeInput, and an api_export that retries or a walk that pages
// would otherwise accumulate headers from the call before it.
//
// SOAPAction is written for 1.1 even when the action is empty, quoted as the
// specification requires. An empty action and no action at all are different
// to several stacks, and the operation's binding is what says which this is.
func (s *soapOperation) headers(caller map[string]string) map[string]string {
	out := make(map[string]string, len(caller)+2)
	maps.Copy(out, caller)
	if s.ext.Version != soap.Version12 && !hasHeader(out, headerSOAPAction) {
		out[headerSOAPAction] = strconv.Quote(s.ext.Action)
	}
	// Without this the gateway asks for JSON, which a SOAP endpoint may
	// answer with a 406 rather than the envelope it was going to send.
	if !hasHeader(out, headerAccept) {
		out[headerAccept] = mediaTypeFor(s.ext.Version) + ", text/xml;q=0.9, */*;q=0.5"
	}
	return out
}

// mediaTypeFor is the envelope media type of a SOAP version.
func mediaTypeFor(version string) string {
	if version == soap.Version12 {
		return soap.MediaType12
	}
	return soap.MediaType11
}

// hasHeader reports whether a caller already set a header, whatever casing
// they spelled it in.
func hasHeader(headers map[string]string, name string) bool {
	for k := range headers {
		if http.CanonicalHeaderKey(k) == http.CanonicalHeaderKey(name) {
			return true
		}
	}
	return false
}
