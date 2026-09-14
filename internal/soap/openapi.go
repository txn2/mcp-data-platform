package soap

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

// ExtensionKey is the OpenAPI extension the importer writes onto every
// operation it renders, and the key the gateway reads to recognize one.
//
// It carries the four facts a SOAP call needs that a rendered REST operation
// has nowhere to put: which envelope version to write, which action header the
// binding requires, which address the operation is actually sent to, and which
// element the body carries in each direction.
const ExtensionKey = "x-soap"

// openapiVersion is the document version the importer emits.
//
// 3.0.3 rather than 3.1 because the rendered schemas use `nullable`, which 3.1
// replaced with a type union. The catalog's loader reads both; emitting the
// version whose vocabulary the schemas are written in keeps the document
// honest rather than merely accepted.
const openapiVersion = "3.0.3"

// Extension is the x-soap value on one rendered operation.
type Extension struct {
	// Version is Version11 or Version12.
	Version string `json:"version"`
	// Action is the binding's soapAction. It is always written, including
	// when empty: an empty action is sent as an empty header, which is not
	// the same as sending no header at all.
	Action string `json:"action"`
	// Path is the address every operation of the service is sent to. The
	// document's path key is synthetic (see Render), so this is the only
	// place the wire path exists.
	Path string `json:"path"`
	// Input is the element the request body carries.
	Input ExtensionElement `json:"input"`
	// Output is the element a successful response carries. Its Name is
	// empty for a one-way operation.
	Output *ExtensionElement `json:"output,omitempty"`
}

// ExtensionElement names one body element.
type ExtensionElement struct {
	Element   string `json:"element"`
	Namespace string `json:"namespace,omitempty"`
	// Qualified reports whether the element's descendants carry the
	// namespace, which is the schema's elementFormDefault.
	Qualified bool `json:"qualified,omitempty"`
}

// Render converts a parsed Service into the OpenAPI document the catalog
// stores and the gateway serves.
//
// Every operation of a SOAP service is a POST to one address, which OpenAPI
// cannot express: a path item holds at most one operation per method. The
// document therefore keys each operation under the address followed by the
// operation name, which is unique by construction because a portType's
// operation names are. That key is what api_discover lists as the operation's
// path and what a path-shaped persona rule matches; the address the request is
// actually sent to travels in x-soap.path, and the gateway substitutes it when
// it builds the URL. The alternative — one path item carrying one operation
// for the whole service — would reduce a hundred-operation ERP surface to a
// single undiscoverable endpoint, which is the state this import exists to
// end.
func Render(svc *Service) (string, error) {
	if svc == nil || len(svc.Operations) == 0 {
		return "", errors.New("soap: service has no operations to render")
	}
	doc := document{
		OpenAPI: openapiVersion,
		Info: info{
			Title:       title(svc),
			Description: svc.Documentation,
			Version:     "1.0.0",
		},
		Paths: map[string]pathItem{},
	}
	mediaType := svc.MediaType()
	for i := range svc.Operations {
		op := &svc.Operations[i]
		doc.Paths[documentPath(svc.Path, op.Name)] = pathItem{
			Post: renderOperation(svc, op, mediaType),
		}
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", fmt.Errorf("soap: rendering the imported document failed: %w", err)
	}
	return string(out), nil
}

// documentPath builds the synthetic, unique path key for one operation.
func documentPath(servicePath, operation string) string {
	return strings.TrimSuffix(servicePath, "/") + "/" + operation
}

// title is the rendered document's info.title, which is what api_discover
// shows as the spec's name.
func title(svc *Service) string {
	if svc.Name != "" {
		return svc.Name
	}
	return "SOAP service"
}

// renderOperation renders one WSDL operation as an OpenAPI operation.
func renderOperation(svc *Service, op *Operation, mediaType string) *operation {
	out := &operation{
		OperationID: op.Name,
		Summary:     summary(op),
		Description: op.Documentation,
		Responses:   map[string]response{},
		Extension: &Extension{
			Version: svc.SOAPVersion,
			Action:  op.SOAPAction,
			Path:    svc.Path,
			Input:   ExtensionElement{Element: op.Input.Name, Namespace: op.Input.Namespace, Qualified: op.Input.Qualified},
		},
	}
	if op.Input.Name != "" {
		out.RequestBody = &requestBody{
			Required: true,
			Content:  map[string]mediaTypeObject{mediaType: {Schema: bodySchema(op.Input)}},
		}
	}
	if op.Output.Name == "" {
		out.Responses["204"] = response{Description: "The operation returns no body."}
		return out
	}
	out.Extension.Output = &ExtensionElement{Element: op.Output.Name, Namespace: op.Output.Namespace, Qualified: op.Output.Qualified}
	out.Responses["200"] = response{
		Description: "The " + op.Output.Name + " element, as the body of a SOAP envelope.",
		Content:     map[string]mediaTypeObject{mediaType: {Schema: bodySchema(op.Output)}},
	}
	return out
}

// summary is the one-line label api_discover lists the operation under.
func summary(op *Operation) string {
	if op.Documentation != "" {
		return firstSentence(op.Documentation)
	}
	return "SOAP operation " + op.Name
}

// firstSentence trims a documentation block to its first sentence, bounded, so
// a summary stays a summary when the WSDL's documentation is a page of prose.
func firstSentence(doc string) string {
	const maxSummary = 200
	if i := strings.Index(doc, ". "); i > 0 && i < maxSummary {
		return doc[:i+1]
	}
	if len(doc) > maxSummary {
		return strings.TrimSpace(doc[:maxSummary]) + "..."
	}
	return doc
}

// bodySchema renders a body element's content as the operation's schema.
//
// The schema describes the element's CONTENT, not an object wrapping the
// element: the caller sends the fields of GetWeather, and the envelope writer
// supplies the GetWeather element and the envelope around it. An element with
// no declared content still gets an object, so a caller sending {} is sending
// a valid empty body rather than an untyped one.
func bodySchema(el Element) *Schema {
	if el.Schema == nil {
		return &Schema{Type: "object"}
	}
	return el.Schema
}

// The document shapes the importer marshals. They are declared here rather
// than reused from kin-openapi so that what the importer emits is data whose
// every field is chosen, and so that the result is validated by the same
// ParseSpec an operator-supplied document goes through rather than by a
// round-trip of the library's own structures.
type document struct {
	OpenAPI string              `json:"openapi"`
	Info    info                `json:"info"`
	Paths   map[string]pathItem `json:"paths"`
}

type info struct {
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Version     string `json:"version"`
}

type pathItem struct {
	Post *operation `json:"post,omitempty"`
}

type operation struct {
	OperationID string              `json:"operationId"`
	Summary     string              `json:"summary,omitempty"`
	Description string              `json:"description,omitempty"`
	RequestBody *requestBody        `json:"requestBody,omitempty"`
	Responses   map[string]response `json:"responses"`
	Extension   *Extension          `json:"x-soap,omitempty"`
}

type requestBody struct {
	Required bool                       `json:"required,omitempty"`
	Content  map[string]mediaTypeObject `json:"content"`
}

type response struct {
	Description string                     `json:"description"`
	Content     map[string]mediaTypeObject `json:"content,omitempty"`
}

type mediaTypeObject struct {
	Schema *Schema `json:"schema,omitempty"`
}

// Import parses a WSDL document and renders it in one step, which is what
// every caller outside this package wants.
func Import(doc string) (*Service, string, error) {
	svc, err := Parse(doc)
	if err != nil {
		return nil, "", err
	}
	rendered, err := Render(svc)
	if err != nil {
		return nil, "", err
	}
	return svc, rendered, nil
}

// ExtensionFrom reads the x-soap extension off a loaded OpenAPI operation,
// reporting whether the operation is a SOAP one at all.
//
// It is how every caller outside this package recognizes a SOAP operation: the
// gateway holds a parsed document and no memory of where the spec came from,
// and the presence of this extension is what says an envelope has to be built
// rather than the body sent as-is.
func ExtensionFrom(op *openapi3.Operation) (*Extension, bool) {
	if op == nil {
		return nil, false
	}
	raw, ok := op.Extensions[ExtensionKey]
	if !ok {
		return nil, false
	}
	var ext Extension
	if err := decodeExtension(raw, &ext); err != nil {
		return nil, false
	}
	if ext.Input.Element == "" {
		return nil, false
	}
	return &ext, true
}
