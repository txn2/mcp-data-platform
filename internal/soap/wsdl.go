// Package soap is what the API gateway knows about SOAP, in one place: the
// WSDL import that turns a service description into the operation model the
// gateway already serves, and the envelope and fault handling one of those
// operations needs on the wire.
//
// A WSDL describes the same three things an OpenAPI document does — a set of
// named operations, the shape each one takes, and the address they are sent to
// — in a different vocabulary. Parse reads the document into that shared
// shape; OpenAPI renders it as a document the gateway's existing loader,
// discovery, persona route rules and metrics read without modification. The
// SOAP-specific facts a caller cannot derive from the rendered operation (the
// envelope version, the action header, the body element and its namespace)
// travel with it as the x-soap extension, which is what lets the invoke path
// build an envelope from a plain object.
//
// The subset is document/literal, SOAP 1.1 and 1.2, which is the overwhelming
// majority of what is still deployed. RPC and encoded bindings are refused by
// name rather than imported wrongly: their body element is assembled from the
// operation and part names rather than declared by a schema, so an importer
// that treated them as document/literal would emit a request shape that does
// not exist.
//
// References inside the document are resolved by the local part of their
// QName. The decoder resolves every element name through the document's
// namespace declarations and then drops the declarations
// (internal/xmltree), so a prefix in an attribute VALUE — which is what
// `message`, `type` and `element` carry — has nothing left to resolve
// against. Within one WSDL that costs nothing, because every such reference
// names a definition in the same document; a reference that does not resolve
// locally is an error naming what was missing, never a silent mismatch.
package soap

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/xmltree"
)

// The namespaces the importer matches on. Elements are matched by namespace
// and local name rather than by prefix, so a document is read the same way
// whichever prefixes its author chose.
const (
	nsWSDL   = "http://schemas.xmlsoap.org/wsdl/"
	nsSOAP11 = "http://schemas.xmlsoap.org/wsdl/soap/"
	nsSOAP12 = "http://schemas.xmlsoap.org/wsdl/soap12/"
	nsXSD    = "http://www.w3.org/2001/XMLSchema"

	// elemOperation is the element name both the portType and the binding
	// spell an operation with.
	elemOperation = "operation"
)

// SOAP versions the importer emits, as they appear in x-soap.version and in
// the media type each one requires.
const (
	Version11 = "1.1"
	Version12 = "1.2"

	// MediaType11 is the Content-Type a SOAP 1.1 request carries. SOAP
	// 1.2 replaced it with its own type, and an upstream of either version
	// rejects the other's.
	MediaType11 = "text/xml"
	// MediaType12 is the SOAP 1.2 Content-Type.
	MediaType12 = "application/soap+xml"
)

// ErrNotWSDL is returned when the document's root is not a WSDL 1.1
// definitions element. It is separate from a parse failure so the admin
// handler can tell an operator who pasted an OpenAPI document into a wsdl
// spec entry apart from one who pasted a broken WSDL.
var ErrNotWSDL = errors.New("soap: document root is not a WSDL 1.1 <definitions> element")

// Service is one imported WSDL: the service the document describes and the
// operations it exposes, already resolved down to what an HTTP caller needs.
type Service struct {
	// Name is the wsdl:service name, used as the rendered document's title.
	Name string
	// Documentation is the service's wsdl:documentation, empty when absent.
	Documentation string
	// TargetNamespace is the document's targetNamespace. It is the default
	// namespace of the body elements and is what an envelope is built with.
	TargetNamespace string
	// Address is the soap:address location of the port that was imported,
	// as written. Only its path reaches the rendered document; the host is
	// dropped because a catalog spec is shared across connections and each
	// connection supplies its own base_url.
	Address string
	// Path is Address's path, always starting with "/".
	Path string
	// SOAPVersion is Version11 or Version12, taken from the binding.
	SOAPVersion string
	// Operations are the port type's operations in document order.
	Operations []Operation
}

// Operation is one WSDL operation resolved into what a call needs: the name it
// is addressed by, the action header the binding requires, and the element the
// body carries in each direction.
type Operation struct {
	// Name is the wsdl:operation name. It becomes the operationId, which is
	// what api_discover lists and what a persona route rule is written
	// against.
	Name string
	// Documentation is the operation's wsdl:documentation, empty when absent.
	Documentation string
	// SOAPAction is the binding's soapAction. An empty action is legal and
	// is sent as an empty header value, which is not the same as omitting
	// the header: several stacks dispatch on its presence.
	SOAPAction string
	// Input is the element the request body carries.
	Input Element
	// Output is the element a successful response body carries. Its Name is
	// empty for a one-way operation, which declares no output message.
	Output Element
}

// Element is one message's body element: the name and namespace it is written
// with, and the shape of its content as a JSON Schema the caller fills in.
type Element struct {
	// Name is the element's local name.
	Name string
	// Namespace is the element's namespace URI.
	Namespace string
	// Qualified reports whether the element's DESCENDANTS carry the
	// namespace too, which is the schema's elementFormDefault. The element
	// itself is global and always carries it.
	Qualified bool
	// Schema is the element's content as a JSON Schema object. It is nil
	// when the element declares no content.
	Schema *Schema
}

// MediaType returns the Content-Type a request to this service carries.
func (s *Service) MediaType() string {
	if s.SOAPVersion == Version12 {
		return MediaType12
	}
	return MediaType11
}

// Parse reads a WSDL 1.1 document into a Service.
//
// The document is parsed under the platform's shared XML bounds, so a WSDL
// that is too large, too deep, or carries a document type declaration is
// refused the same way every other XML the platform reads is.
func Parse(doc string) (*Service, error) {
	root, err := xmltree.Decode(doc, xmltree.DefaultLimits)
	if err != nil {
		return nil, fmt.Errorf("soap: %w", err)
	}
	if root.NS != nsWSDL || root.Tag != "definitions" {
		return nil, ErrNotWSDL
	}
	d := &definitions{
		root:      root,
		targetNS:  root.Attrs["targetNamespace"],
		messages:  index(children(root, nsWSDL, "message")),
		portTypes: index(children(root, nsWSDL, "portType")),
		bindings:  index(children(root, nsWSDL, "binding")),
	}
	d.schema = newSchemaSet(children(root, nsWSDL, "types"), d.targetNS)
	return d.service()
}

// definitions is the document's top-level index: the four child collections a
// resolution walks, keyed by the name a QName's local part matches.
type definitions struct {
	root      *xmltree.Node
	targetNS  string
	messages  map[string]*xmltree.Node
	portTypes map[string]*xmltree.Node
	bindings  map[string]*xmltree.Node
	schema    *schemaSet
}

// service resolves the one port the importer serves and builds the Service
// around it.
func (d *definitions) service() (*Service, error) {
	svcNode, port, err := d.soapPort()
	if err != nil {
		return nil, err
	}
	bindingName := localPart(port.Attrs["binding"])
	binding := d.bindings[bindingName]
	if binding == nil {
		return nil, fmt.Errorf("soap: port %q names binding %q, which the document does not define", port.Attrs["name"], bindingName)
	}
	version, err := bindingSOAPVersion(binding)
	if err != nil {
		return nil, err
	}
	address := soapAddress(port)
	path, err := addressPath(address)
	if err != nil {
		return nil, err
	}
	svc := &Service{
		Name:            svcNode.Attrs["name"],
		Documentation:   documentation(svcNode),
		TargetNamespace: d.targetNS,
		Address:         address,
		Path:            path,
		SOAPVersion:     version,
	}
	if svc.Operations, err = d.operations(binding); err != nil {
		return nil, err
	}
	return svc, nil
}

// soapPort finds the first service port bound over SOAP.
//
// A WSDL routinely declares the same port type over several bindings — SOAP
// 1.1, SOAP 1.2, and an HTTP GET/POST binding for the same operations. The
// importer takes the first SOAP port in document order, which is the one a
// generator emits first and the one a human reads as primary.
func (d *definitions) soapPort() (svc, port *xmltree.Node, err error) {
	services := children(d.root, nsWSDL, "service")
	if len(services) == 0 {
		return nil, nil, errors.New("soap: document declares no <service>")
	}
	for _, s := range services {
		for _, p := range children(s, nsWSDL, "port") {
			if soapAddressNode(p) != nil {
				return s, p, nil
			}
		}
	}
	return nil, nil, errors.New("soap: no service port has a <soap:address>; only SOAP 1.1 and SOAP 1.2 bindings are imported")
}

// operations resolves every operation the binding declares, in the order the
// port type declares them.
func (d *definitions) operations(binding *xmltree.Node) ([]Operation, error) {
	portTypeName := localPart(binding.Attrs["type"])
	portType := d.portTypes[portTypeName]
	if portType == nil {
		return nil, fmt.Errorf("soap: binding %q names portType %q, which the document does not define", binding.Attrs["name"], portTypeName)
	}
	bound := index(children(binding, nsWSDL, elemOperation))
	var out []Operation
	for _, op := range children(portType, nsWSDL, elemOperation) {
		name := op.Attrs["name"]
		bindingOp := bound[name]
		if bindingOp == nil {
			continue
		}
		if err := checkDocumentLiteral(name, bindingOp); err != nil {
			return nil, err
		}
		resolved, err := d.operation(name, op, bindingOp)
		if err != nil {
			return nil, err
		}
		out = append(out, resolved)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("soap: portType %q declares no operation the binding also binds", portTypeName)
	}
	return out, nil
}

// operation resolves one operation's action and its input and output elements.
func (d *definitions) operation(name string, op, bindingOp *xmltree.Node) (Operation, error) {
	resolved := Operation{
		Name:          name,
		Documentation: documentation(op),
		SOAPAction:    soapAction(bindingOp),
	}
	var err error
	if resolved.Input, err = d.bodyElement(name, op, "input"); err != nil {
		return Operation{}, err
	}
	if resolved.Output, err = d.bodyElement(name, op, "output"); err != nil {
		return Operation{}, err
	}
	return resolved, nil
}

// bodyElement resolves the element one direction of an operation carries.
//
// A direction the operation does not declare (the output of a one-way
// operation) yields the zero Element rather than an error. A declared
// direction whose message or part cannot be resolved is an error: the
// alternative is an operation whose body shape is silently empty.
func (d *definitions) bodyElement(opName string, op *xmltree.Node, direction string) (Element, error) {
	node := child(op, nsWSDL, direction)
	if node == nil {
		return Element{}, nil
	}
	msgName := localPart(node.Attrs["message"])
	msg := d.messages[msgName]
	if msg == nil {
		return Element{}, fmt.Errorf("soap: operation %q %s names message %q, which the document does not define", opName, direction, msgName)
	}
	parts := children(msg, nsWSDL, "part")
	if len(parts) == 0 {
		return Element{}, nil
	}
	ref := parts[0].Attrs["element"]
	if ref == "" {
		return Element{}, fmt.Errorf("soap: operation %q %s message %q has a part with no element; only document/literal bindings are imported", opName, direction, msgName)
	}
	return d.schema.element(localPart(ref))
}

// checkDocumentLiteral refuses a binding operation the importer would read
// wrongly.
//
// Style is document unless the binding or the operation says otherwise, and
// use is literal unless the body says otherwise, so both checks look for the
// value that is present rather than the value that is absent.
func checkDocumentLiteral(name string, bindingOp *xmltree.Node) error {
	if style := attr(soapChild(bindingOp, elemOperation), "style"); style == "rpc" {
		return fmt.Errorf("soap: operation %q uses the rpc style, which is not imported; only document/literal bindings are", name)
	}
	for _, direction := range []string{"input", "output"} {
		dir := child(bindingOp, nsWSDL, direction)
		if dir == nil {
			continue
		}
		if use := attr(soapChild(dir, "body"), "use"); use == "encoded" {
			return fmt.Errorf("soap: operation %q %s uses encoded, which is not imported; only document/literal bindings are", name, direction)
		}
	}
	return nil
}

// bindingSOAPVersion reports which SOAP version a binding speaks, from the
// namespace of its soap:binding element.
func bindingSOAPVersion(binding *xmltree.Node) (string, error) {
	switch {
	case child(binding, nsSOAP12, "binding") != nil:
		return Version12, nil
	case child(binding, nsSOAP11, "binding") != nil:
		return Version11, nil
	default:
		return "", fmt.Errorf("soap: binding %q has no <soap:binding>; only SOAP 1.1 and SOAP 1.2 bindings are imported", binding.Attrs["name"])
	}
}

// soapAction returns the binding operation's soapAction, which is declared on
// the soap:operation element in either SOAP namespace.
func soapAction(bindingOp *xmltree.Node) string {
	return attr(soapChild(bindingOp, elemOperation), "soapAction")
}

// soapAddressNode returns a port's soap:address in either SOAP namespace, nil
// when the port is not bound over SOAP.
func soapAddressNode(port *xmltree.Node) *xmltree.Node {
	if n := child(port, nsSOAP12, "address"); n != nil {
		return n
	}
	return child(port, nsSOAP11, "address")
}

// soapAddress returns a port's address location, empty when absent.
func soapAddress(port *xmltree.Node) string {
	return attr(soapAddressNode(port), "location")
}

// addressPath reduces a soap:address location to the path the gateway appends
// to a connection's base_url.
//
// The host is dropped deliberately: a catalog spec is shared across
// connections, and each connection names the deployment it talks to. A
// location with no path answers "/" rather than the empty string, which is
// what an OpenAPI path must be.
func addressPath(address string) (string, error) {
	if address == "" {
		return "", errors.New("soap: service port has no address location")
	}
	u, err := url.Parse(address)
	if err != nil {
		return "", fmt.Errorf("soap: service port address %q is not a URL: %w", address, err)
	}
	if u.Path == "" {
		return "/", nil
	}
	return u.Path, nil
}

// documentation returns an element's wsdl:documentation text, trimmed and
// collapsed onto one line so it can be a summary.
func documentation(n *xmltree.Node) string {
	doc := child(n, nsWSDL, "documentation")
	if doc == nil {
		return ""
	}
	return strings.Join(strings.Fields(doc.Text), " ")
}

// index keys nodes by their name attribute, keeping the first of a repeated
// name so resolution is deterministic on a document that declares one twice.
func index(nodes []*xmltree.Node) map[string]*xmltree.Node {
	out := make(map[string]*xmltree.Node, len(nodes))
	for _, n := range nodes {
		name := n.Attrs["name"]
		if name == "" {
			continue
		}
		if _, seen := out[name]; !seen {
			out[name] = n
		}
	}
	return out
}

// child returns n's first child in namespace ns with local name tag, nil when
// there is none. A nil receiver answers nil so a caller can chain a lookup
// through an element that may be absent.
func child(n *xmltree.Node, ns, tag string) *xmltree.Node {
	if n == nil {
		return nil
	}
	for _, c := range n.Children {
		if c.NS == ns && c.Tag == tag {
			return c
		}
	}
	return nil
}

// children returns every child of n in namespace ns with local name tag.
func children(n *xmltree.Node, ns, tag string) []*xmltree.Node {
	if n == nil {
		return nil
	}
	var out []*xmltree.Node
	for _, c := range n.Children {
		if c.NS == ns && c.Tag == tag {
			out = append(out, c)
		}
	}
	return out
}

// soapChild returns n's first child with local name tag in either SOAP
// namespace. The two versions declare the same elements, and every caller here
// wants whichever one the document used.
func soapChild(n *xmltree.Node, tag string) *xmltree.Node {
	if c := child(n, nsSOAP12, tag); c != nil {
		return c
	}
	return child(n, nsSOAP11, tag)
}

// attr returns n's attribute by local name, empty when the attribute or the
// element itself is absent. A nil-safe read is what lets a caller ask an
// optional element for an optional attribute in one expression.
func attr(n *xmltree.Node, name string) string {
	if n == nil {
		return ""
	}
	return n.Attrs[name]
}

// localPart returns the part of a QName after its prefix. See the package
// comment for why a prefix cannot be resolved to a namespace here.
func localPart(qname string) string {
	if _, after, found := strings.Cut(qname, ":"); found {
		return after
	}
	return qname
}
