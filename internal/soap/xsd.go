package soap

import (
	"fmt"
	"maps"
	"strconv"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/xmltree"
)

// maxSchemaDepth bounds how far a type definition is followed before the
// importer stops descending.
//
// XSD types may reference each other in a cycle, and a schema that does so is
// valid: an element whose type contains itself describes a tree, not an
// infinite document. The importer renders a JSON Schema, which has no
// reference of its own here, so it must stop somewhere. It stops at a depth
// past any real request body and says so in the description it leaves behind,
// rather than emitting a shape whose truncation the caller cannot see.
const maxSchemaDepth = 12

// The XSD names the importer reads in more than one place. Naming them keeps
// one spelling of each, which is what stops a typo in one branch from silently
// rendering an empty shape in that branch alone.
const (
	attrName       = "name"
	elemSimpleType = "simpleType"
)

// Schema is the subset of JSON Schema the importer emits. It is rendered into
// the OpenAPI document as-is, so the field names are the JSON Schema ones.
//
// It is a type of its own rather than kin-openapi's: the importer's output is
// validated by the same ParseSpec every other spec goes through, and building
// the document as data keeps that validation an honest check rather than a
// round-trip of the library's own structures.
type Schema struct {
	Type        string             `json:"type,omitempty"`
	Format      string             `json:"format,omitempty"`
	Description string             `json:"description,omitempty"`
	Properties  map[string]*Schema `json:"properties,omitempty"`
	Required    []string           `json:"required,omitempty"`
	Items       *Schema            `json:"items,omitempty"`
	Enum        []string           `json:"enum,omitempty"`
	Nullable    bool               `json:"nullable,omitempty"`
	// Order is the sequence the upstream requires this object's child
	// elements in. An xsd:sequence is ordered and a JSON object is not, so
	// without it the encoder would emit the properties in whatever order a
	// map yields and the upstream would reject a body whose every value was
	// correct. Attributes are absent from it: their position is the start
	// tag, not the sequence.
	Order []string `json:"x-soap-order,omitempty"`
	// Attribute marks a property the envelope writer emits as an XML
	// attribute rather than a child element. It is an extension because
	// JSON Schema has no notion of the distinction and the encoder needs
	// it: a property written in the wrong position is silently ignored by
	// the upstream.
	Attribute bool `json:"x-soap-attribute,omitempty"`
}

// schemaSet is every inline XSD in the document, indexed by the names a
// reference resolves against.
type schemaSet struct {
	// elements are top-level xsd:element declarations by name.
	elements map[string]*xmltree.Node
	// types are top-level xsd:complexType and xsd:simpleType by name.
	types map[string]*xmltree.Node
	// targetNS is the namespace a body element is written in. It is the
	// schema's own targetNamespace where it declares one, and the
	// document's otherwise.
	targetNS map[string]string
	// qualified records each element's schema elementFormDefault. A body
	// element is global and therefore always qualified; its DESCENDANTS are
	// qualified only where the schema says so, and an upstream rejects a
	// body that guesses wrong in either direction.
	qualified map[string]bool
	// defaultNS is the WSDL's targetNamespace, used for an element whose
	// schema declares none.
	defaultNS string
}

// newSchemaSet indexes every schema under the document's wsdl:types elements.
func newSchemaSet(types []*xmltree.Node, defaultNS string) *schemaSet {
	s := &schemaSet{
		elements:  map[string]*xmltree.Node{},
		types:     map[string]*xmltree.Node{},
		targetNS:  map[string]string{},
		qualified: map[string]bool{},
		defaultNS: defaultNS,
	}
	for _, t := range types {
		for _, schema := range children(t, nsXSD, "schema") {
			s.add(schema)
		}
	}
	return s
}

// add indexes one xsd:schema's top-level declarations.
func (s *schemaSet) add(schema *xmltree.Node) {
	ns := schema.Attrs["targetNamespace"]
	if ns == "" {
		ns = s.defaultNS
	}
	// Unqualified is the XSD default, so only the explicit value qualifies.
	qualified := schema.Attrs["elementFormDefault"] == "qualified"
	for _, el := range children(schema, nsXSD, "element") {
		name := el.Attrs[attrName]
		if name == "" {
			continue
		}
		if _, seen := s.elements[name]; !seen {
			s.elements[name] = el
			s.targetNS[name] = ns
			s.qualified[name] = qualified
		}
	}
	for _, kind := range []string{"complexType", elemSimpleType} {
		for _, t := range children(schema, nsXSD, kind) {
			name := t.Attrs[attrName]
			if name == "" {
				continue
			}
			if _, seen := s.types[name]; !seen {
				s.types[name] = t
			}
		}
	}
}

// element resolves a top-level element declaration into the body element an
// operation carries.
func (s *schemaSet) element(name string) (Element, error) {
	node := s.elements[name]
	if node == nil {
		return Element{}, fmt.Errorf("soap: message part names element %q, which no inline schema declares; a WSDL whose types are in an imported schema is not imported", name)
	}
	return Element{
		Name:      name,
		Namespace: s.targetNS[name],
		Qualified: s.qualified[name],
		Schema:    s.elementSchema(node, 0),
	}, nil
}

// elementSchema renders one element declaration's content.
//
// An element is either typed by reference or carries its definition inline,
// and both forms are common in the same document. A depth past the bound
// yields a schema that says so rather than nothing, so a caller reading
// api_discover can tell a truncated branch from an empty one.
func (s *schemaSet) elementSchema(el *xmltree.Node, depth int) *Schema {
	if depth > maxSchemaDepth {
		return &Schema{Description: "The schema nests deeper than the importer follows; send this branch as a string of XML."}
	}
	if ref := el.Attrs["type"]; ref != "" {
		return s.typeSchema(localPart(ref), depth)
	}
	if inline := child(el, nsXSD, "complexType"); inline != nil {
		return s.complexType(inline, depth)
	}
	if inline := child(el, nsXSD, elemSimpleType); inline != nil {
		return s.simpleType(inline, depth)
	}
	// An element with neither a type nor an inline definition is xs:anyType:
	// any content at all. Emitting no type says exactly that.
	return &Schema{}
}

// typeSchema renders a named type, resolving a builtin before the document's
// own definitions so a schema that redefines a builtin name cannot change what
// xs:string means.
func (s *schemaSet) typeSchema(name string, depth int) *Schema {
	if b, ok := builtin(name); ok {
		return b
	}
	node := s.types[name]
	if node == nil {
		return &Schema{Description: "Type " + name + " is not declared by an inline schema; send this branch as a string of XML."}
	}
	if node.Tag == elemSimpleType {
		return s.simpleType(node, depth)
	}
	return s.complexType(node, depth+1)
}

// complexType renders a complex type as an object.
//
// The content model is read from whichever compositor the type uses: sequence,
// all and choice all contribute their element children as properties. choice
// contributes them as optional, because exactly one of them is present and a
// required list would refuse every valid body.
func (s *schemaSet) complexType(t *xmltree.Node, depth int) *Schema {
	out := &Schema{Type: "object", Properties: map[string]*Schema{}}
	if ext := complexContentExtension(t); ext != nil {
		s.mergeBase(out, ext, depth)
		s.addParticles(out, ext, depth, false)
		s.addAttributes(out, ext, depth)
		return out
	}
	s.addParticles(out, t, depth, false)
	s.addAttributes(out, t, depth)
	return out
}

// complexContentExtension returns the xsd:extension inside a type's
// complexContent, nil when the type does not extend another.
func complexContentExtension(t *xmltree.Node) *xmltree.Node {
	content := child(t, nsXSD, "complexContent")
	if content == nil {
		return nil
	}
	return child(content, nsXSD, "extension")
}

// mergeBase folds an extension's base type into the schema being built, so a
// derived type carries the properties it inherits.
func (s *schemaSet) mergeBase(out *Schema, ext *xmltree.Node, depth int) {
	base := localPart(ext.Attrs["base"])
	if base == "" || depth > maxSchemaDepth {
		return
	}
	inherited := s.typeSchema(base, depth+1)
	if inherited == nil {
		return
	}
	maps.Copy(out.Properties, inherited.Properties)
	out.Required = append(out.Required, inherited.Required...)
	// The base type's elements precede the extension's on the wire.
	out.Order = append(out.Order, inherited.Order...)
}

// addParticles adds every element child of a type's compositors as a property.
func (s *schemaSet) addParticles(out *Schema, t *xmltree.Node, depth int, optional bool) {
	for _, compositor := range []string{"sequence", "all", "choice"} {
		for _, group := range children(t, nsXSD, compositor) {
			s.addGroup(out, group, depth, optional || compositor == "choice")
		}
	}
}

// addGroup adds one compositor's element children, descending through nested
// compositors so a sequence inside a choice contributes its elements too.
func (s *schemaSet) addGroup(out *Schema, group *xmltree.Node, depth int, optional bool) {
	for _, el := range children(group, nsXSD, "element") {
		s.addElementProperty(out, el, depth, optional)
	}
	for _, nested := range []string{"sequence", "all", "choice"} {
		for _, inner := range children(group, nsXSD, nested) {
			s.addGroup(out, inner, depth, optional || nested == "choice")
		}
	}
}

// addElementProperty adds one element particle as a property, wrapping it in
// an array when it may repeat and recording it as required when it must appear.
func (s *schemaSet) addElementProperty(out *Schema, el *xmltree.Node, depth int, optional bool) {
	name := el.Attrs[attrName]
	if name == "" {
		// A particle that references a global element by ref carries the
		// name there rather than locally.
		name = localPart(el.Attrs["ref"])
	}
	if name == "" {
		return
	}
	prop := s.elementSchema(el, depth+1)
	if el.Attrs["nillable"] == "true" {
		prop.Nullable = true
	}
	if doc := annotation(el); doc != "" {
		prop.Description = doc
	}
	if repeats(el) {
		prop = &Schema{Type: "array", Items: prop}
	}
	out.Properties[name] = prop
	out.Order = append(out.Order, name)
	if !optional && required(el) {
		out.Required = append(out.Required, name)
	}
}

// addAttributes adds a type's attribute declarations as properties marked for
// the envelope writer.
func (s *schemaSet) addAttributes(out *Schema, t *xmltree.Node, depth int) {
	for _, a := range children(t, nsXSD, "attribute") {
		name := a.Attrs[attrName]
		if name == "" {
			name = localPart(a.Attrs["ref"])
		}
		if name == "" {
			continue
		}
		prop := s.attributeSchema(a, depth)
		prop.Attribute = true
		out.Properties[name] = prop
		if a.Attrs["use"] == "required" {
			out.Required = append(out.Required, name)
		}
	}
}

// attributeSchema renders an attribute's type. An attribute is always simple
// content, so a missing type is a string rather than an open shape.
func (s *schemaSet) attributeSchema(a *xmltree.Node, depth int) *Schema {
	if ref := a.Attrs["type"]; ref != "" {
		return s.typeSchema(localPart(ref), depth+1)
	}
	if inline := child(a, nsXSD, elemSimpleType); inline != nil {
		return s.simpleType(inline, depth+1)
	}
	return &Schema{Type: "string"}
}

// simpleType renders a simple type: its base, plus the enumeration when the
// restriction declares one.
func (s *schemaSet) simpleType(t *xmltree.Node, depth int) *Schema {
	restriction := child(t, nsXSD, "restriction")
	if restriction == nil {
		// A union or a list of another type is rendered as a string,
		// which is what its lexical form is on the wire.
		return &Schema{Type: "string"}
	}
	out := s.typeSchema(localPart(restriction.Attrs["base"]), depth+1)
	if out == nil {
		out = &Schema{Type: "string"}
	}
	for _, e := range children(restriction, nsXSD, "enumeration") {
		if v := e.Attrs["value"]; v != "" {
			out.Enum = append(out.Enum, v)
		}
	}
	return out
}

// repeats reports whether an element particle may appear more than once.
func repeats(el *xmltree.Node) bool {
	occurs := el.Attrs["maxOccurs"]
	if occurs == "" {
		return false
	}
	if occurs == "unbounded" {
		return true
	}
	n, err := strconv.Atoi(occurs)
	return err == nil && n > 1
}

// required reports whether an element particle must appear. minOccurs defaults
// to 1, so an element that says nothing is required.
func required(el *xmltree.Node) bool {
	occurs := el.Attrs["minOccurs"]
	if occurs == "" {
		return true
	}
	n, err := strconv.Atoi(occurs)
	return err == nil && n > 0
}

// annotation returns an element's xsd:documentation text, collapsed onto one
// line so it can be a property description.
func annotation(el *xmltree.Node) string {
	ann := child(el, nsXSD, "annotation")
	if ann == nil {
		return ""
	}
	doc := child(ann, nsXSD, "documentation")
	if doc == nil {
		return ""
	}
	return strings.Join(strings.Fields(doc.Text), " ")
}

// builtinSchemas maps the XSD builtin types the importer recognizes onto their
// JSON Schema equivalent. A type absent from the table is one the document
// defines itself.
//
// The date and time types keep their XSD lexical form as a string with a
// format, because that is what the upstream parses; converting them to another
// representation would put the importer in the business of rewriting values.
var builtinSchemas = map[string]Schema{
	"string":             {Type: "string"},
	"normalizedString":   {Type: "string"},
	"token":              {Type: "string"},
	"NMTOKEN":            {Type: "string"},
	"Name":               {Type: "string"},
	"NCName":             {Type: "string"},
	"ID":                 {Type: "string"},
	"IDREF":              {Type: "string"},
	"language":           {Type: "string"},
	"QName":              {Type: "string"},
	"anyURI":             {Type: "string"},
	"anySimpleType":      {Type: "string"},
	"base64Binary":       {Type: "string", Format: "byte"},
	"hexBinary":          {Type: "string"},
	"boolean":            {Type: "boolean"},
	"decimal":            {Type: "number"},
	"double":             {Type: "number", Format: "double"},
	"float":              {Type: "number", Format: "float"},
	"integer":            {Type: "integer"},
	"int":                {Type: "integer", Format: "int32"},
	"long":               {Type: "integer", Format: "int64"},
	"short":              {Type: "integer"},
	"byte":               {Type: "integer"},
	"unsignedInt":        {Type: "integer", Format: "int64"},
	"unsignedLong":       {Type: "integer", Format: "int64"},
	"unsignedShort":      {Type: "integer"},
	"unsignedByte":       {Type: "integer"},
	"nonNegativeInteger": {Type: "integer"},
	"positiveInteger":    {Type: "integer"},
	"nonPositiveInteger": {Type: "integer"},
	"negativeInteger":    {Type: "integer"},
	"date":               {Type: "string", Format: "date"},
	"dateTime":           {Type: "string", Format: "date-time"},
	"time":               {Type: "string"},
	"duration":           {Type: "string"},
	"gYear":              {Type: "string"},
	"gYearMonth":         {Type: "string"},
	"gMonthDay":          {Type: "string"},
	"anyType":            {},
}

// builtin returns the schema for an XSD builtin type name.
func builtin(name string) (*Schema, bool) {
	s, ok := builtinSchemas[name]
	if !ok {
		return nil, false
	}
	out := s
	return &out, true
}
