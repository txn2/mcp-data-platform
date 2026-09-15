package soap

import (
	"strings"
	"testing"
)

// extendedWSDL carries the schema constructs the weather fixture does not: a
// complex type extending another, a choice, an element referenced by ref, an
// inline attribute type, an annotated element, an untyped element, and a type
// that is never declared.
const extendedWSDL = `<?xml version="1.0" encoding="utf-8"?>
<definitions targetNamespace="http://example.org/erp"
  xmlns:tns="http://example.org/erp"
  xmlns:xsd="http://www.w3.org/2001/XMLSchema"
  xmlns:soap12="http://schemas.xmlsoap.org/wsdl/soap12/"
  xmlns="http://schemas.xmlsoap.org/wsdl/">
  <types>
    <xsd:schema targetNamespace="http://example.org/erp">
      <xsd:element name="Code" type="xsd:string"/>
      <xsd:element name="PostOrder">
        <xsd:complexType>
          <xsd:sequence>
            <xsd:element name="Order" type="tns:RushOrder"/>
            <xsd:element ref="tns:Code" minOccurs="0"/>
            <xsd:element name="Freeform"/>
            <xsd:element name="Unknown" type="tns:NeverDeclared" minOccurs="0"/>
          </xsd:sequence>
        </xsd:complexType>
      </xsd:element>
      <xsd:complexType name="BaseOrder">
        <xsd:sequence>
          <xsd:element name="Id" type="xsd:string"/>
        </xsd:sequence>
        <xsd:attribute name="Locale">
          <xsd:simpleType>
            <xsd:restriction base="xsd:string">
              <xsd:enumeration value="en"/>
              <xsd:enumeration value="fr"/>
            </xsd:restriction>
          </xsd:simpleType>
        </xsd:attribute>
      </xsd:complexType>
      <xsd:complexType name="RushOrder">
        <xsd:complexContent>
          <xsd:extension base="tns:BaseOrder">
            <xsd:sequence>
              <xsd:element name="Priority" type="xsd:int"/>
              <xsd:choice>
                <xsd:element name="ShipBy" type="xsd:date"/>
                <xsd:element name="ShipWith" type="xsd:string"/>
              </xsd:choice>
            </xsd:sequence>
            <xsd:attribute name="Carrier" type="xsd:string"/>
          </xsd:extension>
        </xsd:complexContent>
      </xsd:complexType>
      <xsd:element name="PostOrderResponse">
        <xsd:complexType>
          <xsd:sequence>
            <xsd:element name="Accepted" type="xsd:boolean">
              <xsd:annotation>
                <xsd:documentation>
                  Whether the order was taken.
                </xsd:documentation>
              </xsd:annotation>
            </xsd:element>
          </xsd:sequence>
        </xsd:complexType>
      </xsd:element>
    </xsd:schema>
  </types>
  <message name="In"><part name="parameters" element="tns:PostOrder"/></message>
  <message name="Out"><part name="parameters" element="tns:PostOrderResponse"/></message>
  <portType name="ErpSoap">
    <operation name="PostOrder">
      <input message="tns:In"/>
      <output message="tns:Out"/>
    </operation>
  </portType>
  <binding name="ErpBinding" type="tns:ErpSoap">
    <soap12:binding transport="http://schemas.xmlsoap.org/soap/http"/>
    <operation name="PostOrder">
      <soap12:operation soapAction="urn:erp:PostOrder"/>
      <input><soap12:body use="literal"/></input>
      <output><soap12:body use="literal"/></output>
    </operation>
  </binding>
  <service name="ErpService">
    <port name="ErpSoap" binding="tns:ErpBinding">
      <soap12:address location="https://erp.example.org"/>
    </port>
  </service>
</definitions>`

// orderSchema is the Order property of the extended fixture's request.
func orderSchema(t *testing.T) *Schema {
	t.Helper()
	svc := mustParse(t, extendedWSDL)
	order := svc.Operations[0].Input.Schema.Properties["Order"]
	if order == nil {
		t.Fatal("request has no Order property")
	}
	return order
}

// A derived type carries what it inherits: a caller filling in a RushOrder has
// to send the base type's Id too, and a schema that omitted it would describe
// a body the upstream rejects.
func TestComplexContentExtensionInheritsTheBaseTypesProperties(t *testing.T) {
	order := orderSchema(t)

	if order.Properties["Id"] == nil {
		t.Errorf("Order = %v, want the base type's Id", keysOf(order.Properties))
	}
	if order.Properties["Priority"] == nil {
		t.Errorf("Order = %v, want the extension's Priority", keysOf(order.Properties))
	}
	if !contains(order.Required, "Id") {
		t.Errorf("required = %v, want the inherited Id", order.Required)
	}
	if !contains(order.Required, "Priority") {
		t.Errorf("required = %v, want Priority", order.Required)
	}
}

// Exactly one branch of a choice is present, so requiring any of them would
// refuse every valid body.
func TestChoiceContributesOptionalProperties(t *testing.T) {
	order := orderSchema(t)

	for _, name := range []string{"ShipBy", "ShipWith"} {
		if order.Properties[name] == nil {
			t.Errorf("Order = %v, want the choice branch %q", keysOf(order.Properties), name)
		}
		if contains(order.Required, name) {
			t.Errorf("required = %v, want the choice branch %q absent", order.Required, name)
		}
	}
}

func TestAttributesComeFromBothTheBaseTypeAndTheExtension(t *testing.T) {
	order := orderSchema(t)

	locale := order.Properties["Locale"]
	if locale == nil || !locale.Attribute {
		t.Fatalf("Locale = %+v, want an inherited attribute", locale)
	}
	// The attribute's type is declared inline rather than by reference.
	if len(locale.Enum) != 2 {
		t.Errorf("Locale enum = %v, want two values", locale.Enum)
	}
	carrier := order.Properties["Carrier"]
	if carrier == nil || !carrier.Attribute || carrier.Type != "string" {
		t.Errorf("Carrier = %+v, want a string attribute", carrier)
	}
	// use is absent on both, so neither is required.
	if contains(order.Required, "Carrier") {
		t.Errorf("required = %v, want Carrier absent", order.Required)
	}
}

// A particle may name its element by ref instead of declaring one inline; the
// name lives on the reference and the property has to pick it up.
func TestParticleNamedByRefKeepsTheReferencedName(t *testing.T) {
	svc := mustParse(t, extendedWSDL)
	in := svc.Operations[0].Input.Schema

	if in.Properties["Code"] == nil {
		t.Errorf("request = %v, want the ref-named Code", keysOf(in.Properties))
	}
	if contains(in.Required, "Code") {
		t.Errorf("required = %v, want Code absent (minOccurs=0)", in.Required)
	}
}

// An element with neither a type nor an inline definition is xs:anyType. No
// type at all is what says "any content", which is different from an object.
func TestUntypedElementRendersWithNoType(t *testing.T) {
	svc := mustParse(t, extendedWSDL)
	free := svc.Operations[0].Input.Schema.Properties["Freeform"]

	if free == nil {
		t.Fatal("request has no Freeform property")
	}
	if free.Type != "" {
		t.Errorf("Freeform type = %q, want none", free.Type)
	}
}

// A type the document never declares is a gap the caller has to see. The
// schema says so rather than silently rendering an empty object.
func TestUndeclaredTypeSaysSoInTheDescription(t *testing.T) {
	svc := mustParse(t, extendedWSDL)
	unknown := svc.Operations[0].Input.Schema.Properties["Unknown"]

	if unknown == nil {
		t.Fatal("request has no Unknown property")
	}
	if !strings.Contains(unknown.Description, "NeverDeclared") {
		t.Errorf("description = %q, want it to name the missing type", unknown.Description)
	}
}

func TestAnnotationBecomesThePropertyDescription(t *testing.T) {
	svc := mustParse(t, extendedWSDL)
	accepted := svc.Operations[0].Output.Schema.Properties["Accepted"]

	if accepted == nil {
		t.Fatal("response has no Accepted property")
	}
	if accepted.Description != "Whether the order was taken." {
		t.Errorf("description = %q", accepted.Description)
	}
	if accepted.Type != "boolean" {
		t.Errorf("type = %q, want boolean", accepted.Type)
	}
}

// An address with no path is still an address; the document needs a path, and
// "/" is the one it means.
func TestAddressWithNoPathBecomesRoot(t *testing.T) {
	svc := mustParse(t, extendedWSDL)

	if svc.Path != "/" {
		t.Errorf("path = %q, want /", svc.Path)
	}
	_, rendered := mustImport(t, extendedWSDL)
	if !strings.Contains(rendered, `"/PostOrder"`) {
		t.Errorf("rendered path keys do not include /PostOrder:\n%s", rendered)
	}
}

func TestAddressThatIsNotAURLIsRefused(t *testing.T) {
	doc := swap(extendedWSDL, `location="https://erp.example.org"`, `location="://nonsense"`)
	_, err := Parse(doc)
	if err == nil {
		t.Fatal("Parse accepted an address that is not a URL")
	}
	if !strings.Contains(err.Error(), "is not a URL") {
		t.Errorf("error = %q", err)
	}
}

// A schema may nest deeper than the importer follows. The branch where it
// stops has to say so, because a caller reading api_discover otherwise cannot
// tell a truncated shape from a complete one.
func TestSchemaDeeperThanTheBoundSaysWhereItStopped(t *testing.T) {
	svc := mustParse(t, recursiveWSDL)
	node := svc.Operations[0].Input.Schema

	var depth int
	for node != nil && node.Properties["Child"] != nil {
		node = node.Properties["Child"]
		depth++
		if depth > maxSchemaDepth+4 {
			t.Fatalf("descent did not stop after %d levels", depth)
		}
	}
	if node == nil || !strings.Contains(node.Description, "nests deeper") {
		t.Fatalf("descent stopped at %+v after %d levels, want a note saying so", node, depth)
	}
}

// recursiveWSDL declares a type that contains itself, which is valid XSD and
// describes a tree rather than an infinite document.
const recursiveWSDL = `<?xml version="1.0"?>
<definitions targetNamespace="http://example.org/tree"
  xmlns:tns="http://example.org/tree"
  xmlns:xsd="http://www.w3.org/2001/XMLSchema"
  xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/"
  xmlns="http://schemas.xmlsoap.org/wsdl/">
  <types>
    <xsd:schema targetNamespace="http://example.org/tree">
      <xsd:element name="Walk" type="tns:Node"/>
      <xsd:complexType name="Node">
        <xsd:sequence>
          <xsd:element name="Child" type="tns:Node" minOccurs="0"/>
        </xsd:sequence>
      </xsd:complexType>
    </xsd:schema>
  </types>
  <message name="In"><part name="parameters" element="tns:Walk"/></message>
  <portType name="TreeSoap">
    <operation name="Walk"><input message="tns:In"/></operation>
  </portType>
  <binding name="TreeBinding" type="tns:TreeSoap">
    <soap:binding transport="http://schemas.xmlsoap.org/soap/http"/>
    <operation name="Walk">
      <soap:operation soapAction="urn:tree:Walk"/>
      <input><soap:body use="literal"/></input>
    </operation>
  </binding>
  <service name="TreeService">
    <port name="TreeSoap" binding="tns:TreeBinding">
      <soap:address location="http://tree.example.org/t"/>
    </port>
  </service>
</definitions>`

// A summary is a summary: a WSDL whose documentation is a page of prose must
// not push all of it into the operation list.
func TestLongDocumentationIsTrimmedForTheSummary(t *testing.T) {
	long := strings.Repeat("word ", 80)
	doc := swap(weatherWSDL,
		`<wsdl:documentation>Returns the forecast. Accuracy is not guaranteed.</wsdl:documentation>`,
		`<wsdl:documentation>`+long+`</wsdl:documentation>`)
	svc := mustParse(t, doc)

	got := summary(&svc.Operations[0])
	if len(got) > 210 {
		t.Errorf("summary is %d chars, want it trimmed", len(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("summary = %q, want a trimmed marker", got)
	}
	// The full text is still available as the description.
	if len(svc.Operations[0].Documentation) < 300 {
		t.Error("the full documentation was trimmed rather than only the summary")
	}
}

func TestRenderRefusesAServiceWithNoOperations(t *testing.T) {
	if _, err := Render(nil); err == nil {
		t.Error("Render accepted a nil service")
	}
	if _, err := Render(&Service{Name: "x"}); err == nil {
		t.Error("Render accepted a service with no operations")
	}
}

func TestRenderTitlesAnUnnamedServiceGenerically(t *testing.T) {
	svc := &Service{
		Path:        "/svc",
		SOAPVersion: Version11,
		Operations:  []Operation{{Name: "Op", Input: Element{Name: "Op"}}},
	}
	rendered, err := Render(svc)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(rendered, `"title": "SOAP service"`) {
		t.Errorf("rendered document has no fallback title:\n%s", rendered)
	}
}

func TestImportReportsAParseFailureRatherThanRendering(t *testing.T) {
	if _, _, err := Import(`<nope/>`); err == nil {
		t.Error("Import accepted a document that is not a WSDL")
	}
}
