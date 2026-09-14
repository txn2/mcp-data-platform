package soap

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/txn2/mcp-data-platform/internal/xmltree"
)

// The envelope namespaces. SOAP 1.2 changed both this and the media type, and
// an upstream of either version refuses the other's.
const (
	envelopeNS11 = "http://schemas.xmlsoap.org/soap/envelope/"
	envelopeNS12 = "http://www.w3.org/2003/05/soap-envelope"
)

// The schema extensions the importer writes and the encoder reads. They are
// the two facts a JSON Schema cannot carry and an XML document cannot do
// without: where a value goes, and in what order.
const (
	attributeExtension = "x-soap-attribute"
	orderExtension     = "x-soap-order"
)

// Number formatting bases, named because the linter reads a bare 10 or 64 as a
// magic number.
const (
	base10      = 10
	float64Bits = 64
)

// ErrNotAnObject is returned when a caller sends a body the encoder cannot
// place into an envelope. A caller holding XML already has a way through — a
// string body is sent verbatim — so the refusal names it.
var ErrNotAnObject = errors.New("soap: a SOAP operation's body must be an object of the operation's fields, or a string containing the whole envelope")

// Encode builds the request document for one SOAP operation from a body the
// caller sent as an object.
//
// The caller supplies the operation's fields and nothing else: the envelope,
// the body wrapper, the operation's own element and its namespace are all
// known from the extension the import wrote, and assembling them is the point
// of the exercise. schema is the operation's request schema, which says which
// fields are attributes rather than child elements and what order the children
// go in; a nil schema still encodes, with every field an element in sorted
// order, because a connection may carry an operation whose schema the WSDL
// left open.
func Encode(ext *Extension, schema *openapi3.Schema, body any) (string, error) {
	if ext == nil || ext.Input.Element == "" {
		return "", errors.New("soap: the operation declares no request element")
	}
	fields, ok := objectOf(body)
	if !ok {
		return "", ErrNotAnObject
	}
	payload := &xmltree.Node{Tag: ext.Input.Element, NS: ext.Input.Namespace}
	enc := encoder{ns: childNamespace(ext.Input)}
	if err := enc.fill(payload, schema, fields, 1); err != nil {
		return "", err
	}
	envelopeNS := envelopeNS11
	if ext.Version == Version12 {
		envelopeNS = envelopeNS12
	}
	root := &xmltree.Node{
		Tag: "Envelope",
		NS:  envelopeNS,
		Children: []*xmltree.Node{{
			Tag:      "Body",
			NS:       envelopeNS,
			Children: []*xmltree.Node{payload},
		}},
	}
	out, err := xmltree.Encode(root, xmltree.DefaultLimits)
	if err != nil {
		return "", fmt.Errorf("soap: building the envelope failed: %w", err)
	}
	return out, nil
}

// childNamespace is the namespace an element's children are written in. An
// unqualified schema writes them with no namespace at all, which is the XSD
// default and what most .NET and Java services publish.
func childNamespace(el ExtensionElement) string {
	if el.Qualified {
		return el.Namespace
	}
	return ""
}

// encoder carries the one fact every step of the walk shares: the namespace a
// child element is written in, which the schema's elementFormDefault decides
// once for the whole body.
type encoder struct {
	ns string
}

// fill writes one object's fields onto a node as attributes and children.
//
// Depth is bounded by the same limit the shared encoder enforces, so a body
// that nests without end is refused while it is being built rather than after
// a tree too large to encode has been allocated.
func (e encoder) fill(node *xmltree.Node, schema *openapi3.Schema, fields map[string]any, depth int) error {
	if depth > xmltree.DefaultLimits.MaxDepth {
		return fmt.Errorf("soap: the body nests deeper than %d levels", xmltree.DefaultLimits.MaxDepth)
	}
	for _, name := range fieldOrder(schema, fields) {
		value := fields[name]
		prop := propertySchema(schema, name)
		if isAttribute(prop) {
			if node.Attrs == nil {
				node.Attrs = map[string]string{}
			}
			node.Attrs[name] = scalar(value)
			continue
		}
		if err := e.appendChildren(node, prop, name, value, depth); err != nil {
			return err
		}
	}
	return nil
}

// appendChildren writes one field as one element, or as a run of sibling
// elements when the value is a list. A repeated element is a list of siblings
// in XML, not one element containing a list, so a slice expands here rather
// than nesting.
func (e encoder) appendChildren(node *xmltree.Node, prop *openapi3.Schema, name string, value any, depth int) error {
	items, repeated := value.([]any)
	if !repeated {
		child, err := e.element(prop, name, value, depth)
		if err != nil {
			return err
		}
		node.Children = append(node.Children, child)
		return nil
	}
	for _, item := range items {
		child, err := e.element(itemSchema(prop), name, item, depth)
		if err != nil {
			return err
		}
		node.Children = append(node.Children, child)
	}
	return nil
}

// element builds one child element from one value.
func (e encoder) element(prop *openapi3.Schema, name string, value any, depth int) (*xmltree.Node, error) {
	child := &xmltree.Node{Tag: name, NS: e.ns}
	nested, isObject := objectOf(value)
	if !isObject {
		child.Text = scalar(value)
		return child, nil
	}
	if err := e.fill(child, prop, nested, depth+1); err != nil {
		return nil, err
	}
	return child, nil
}

// fieldOrder is the order the fields are written in: the schema's declared
// sequence first, for the fields the caller actually sent, then anything else
// the caller sent in a stable order.
//
// A field the schema does not declare is written rather than refused, which is
// the gateway's standing position on request bodies: it does not validate
// them, and an upstream's own error is a better answer than a guess about
// whether a schema is complete.
func fieldOrder(schema *openapi3.Schema, fields map[string]any) []string {
	out := make([]string, 0, len(fields))
	seen := make(map[string]bool, len(fields))
	for _, name := range declaredOrder(schema) {
		if _, sent := fields[name]; sent && !seen[name] {
			out = append(out, name)
			seen[name] = true
		}
	}
	rest := make([]string, 0, len(fields))
	for name := range fields {
		if !seen[name] {
			rest = append(rest, name)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}

// declaredOrder reads the sequence the importer recorded on an object schema.
func declaredOrder(schema *openapi3.Schema) []string {
	if schema == nil {
		return nil
	}
	raw, ok := schema.Extensions[orderExtension]
	if !ok {
		return nil
	}
	var order []string
	if decodeExtension(raw, &order) != nil {
		return nil
	}
	return order
}

// propertySchema returns one property's schema, nil when the schema does not
// declare it.
func propertySchema(schema *openapi3.Schema, name string) *openapi3.Schema {
	if schema == nil || schema.Properties == nil {
		return nil
	}
	ref := schema.Properties[name]
	if ref == nil {
		return nil
	}
	return ref.Value
}

// itemSchema returns an array schema's item schema, or the schema itself when
// it is not an array. A caller may send a list for a field the schema declares
// singly; the elements are still written, each against the best shape there is.
func itemSchema(prop *openapi3.Schema) *openapi3.Schema {
	if prop == nil || prop.Items == nil {
		return prop
	}
	return prop.Items.Value
}

// isAttribute reports whether a property is written as an XML attribute.
func isAttribute(prop *openapi3.Schema) bool {
	if prop == nil {
		return false
	}
	raw, ok := prop.Extensions[attributeExtension]
	if !ok {
		return false
	}
	var flag bool
	if decodeExtension(raw, &flag) != nil {
		return false
	}
	return flag
}

// decodeExtension reads an OpenAPI extension value into out.
//
// The loader hands an extension back either as the decoded Go value or as the
// raw JSON it was read from, depending on how the document reached it, so both
// are accepted rather than one being assumed.
func decodeExtension(raw, out any) error {
	if raw == nil {
		return errors.New("soap: extension is absent")
	}
	if bytes, isRaw := raw.(json.RawMessage); isRaw {
		if err := json.Unmarshal(bytes, out); err != nil {
			return fmt.Errorf("soap: reading extension: %w", err)
		}
		return nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return fmt.Errorf("soap: reading extension: %w", err)
	}
	if err := json.Unmarshal(encoded, out); err != nil {
		return fmt.Errorf("soap: reading extension: %w", err)
	}
	return nil
}

// objectOf reports whether a value is a JSON object, returning its fields.
func objectOf(value any) (map[string]any, bool) {
	fields, ok := value.(map[string]any)
	return fields, ok
}

// scalar renders a leaf value as the text an XML document carries.
//
// A number arrives from JSON as a float64 whatever it was written as, so an
// integral value is written without a decimal point: an upstream whose schema
// says xsd:int rejects "3" spelled "3.0".
func scalar(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case bool:
		return strconv.FormatBool(v)
	case float64:
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), base10)
		}
		return strconv.FormatFloat(v, 'f', -1, float64Bits)
	case json.Number:
		return v.String()
	default:
		return fmt.Sprint(v)
	}
}
