package catalogapi

import (
	"strings"
	"testing"

	apicatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
)

// specFormatWSDL is a minimal document/literal SOAP 1.1 service.
const specFormatWSDL = `<?xml version="1.0"?>
<definitions targetNamespace="urn:acme:orders"
  xmlns:tns="urn:acme:orders"
  xmlns:xsd="http://www.w3.org/2001/XMLSchema"
  xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/"
  xmlns="http://schemas.xmlsoap.org/wsdl/">
  <types>
    <xsd:schema targetNamespace="urn:acme:orders">
      <xsd:element name="GetOrder">
        <xsd:complexType><xsd:sequence>
          <xsd:element name="OrderId" type="xsd:string"/>
        </xsd:sequence></xsd:complexType>
      </xsd:element>
      <xsd:element name="GetOrderResponse">
        <xsd:complexType><xsd:sequence>
          <xsd:element name="Total" type="xsd:decimal"/>
        </xsd:sequence></xsd:complexType>
      </xsd:element>
    </xsd:schema>
  </types>
  <message name="In"><part name="parameters" element="tns:GetOrder"/></message>
  <message name="Out"><part name="parameters" element="tns:GetOrderResponse"/></message>
  <portType name="OrdersSoap">
    <operation name="GetOrder"><input message="tns:In"/><output message="tns:Out"/></operation>
  </portType>
  <binding name="OrdersBinding" type="tns:OrdersSoap">
    <soap:binding transport="http://schemas.xmlsoap.org/soap/http"/>
    <operation name="GetOrder">
      <soap:operation soapAction="urn:acme:orders/GetOrder"/>
      <input><soap:body use="literal"/></input>
      <output><soap:body use="literal"/></output>
    </operation>
  </binding>
  <service name="OrdersService">
    <port name="OrdersSoap" binding="tns:OrdersBinding">
      <soap:address location="http://erp.example.org/Orders.svc"/>
    </port>
  </service>
</definitions>`

const specFormatOpenAPI = `openapi: 3.0.3
info: {title: Orders, version: "1"}
paths:
  /orders:
    get:
      operationId: listOrders
      responses: {"200": {description: ok}}
`

// prepareSpec is what every write path — inline, upload and refresh — runs
// between building the entry and storing it, so a WSDL saved by any of them
// gets the same conversion.
func TestPrepareSpecConvertsAWSDLAndCountsItsOperations(t *testing.T) {
	t.Parallel()
	entry := apicatalog.SpecEntry{
		SpecName:   "orders",
		Content:    specFormatWSDL,
		SourceKind: apicatalog.SourceInline,
		SpecFormat: apicatalog.FormatWSDL,
	}
	if err := prepareSpec(&entry); err != nil {
		t.Fatalf("prepareSpec: %v", err)
	}
	// The operator's document is untouched; the render sits beside it.
	if entry.Content != specFormatWSDL {
		t.Error("prepareSpec overwrote the document the operator supplied")
	}
	if !strings.Contains(entry.OpenAPIContent, `"openapi"`) {
		t.Errorf("the render is not an OpenAPI document:\n%s", entry.OpenAPIContent)
	}
	if entry.Effective() != entry.OpenAPIContent {
		t.Error("Effective() did not resolve to the rendered document")
	}
	// The count is of the RENDERED document, which is what the gateway
	// serves and what the embedding reconciler compares against.
	if entry.OperationCount != 1 {
		t.Errorf("operation_count = %d, want 1", entry.OperationCount)
	}
	if !strings.Contains(entry.OpenAPIContent, "GetOrder") {
		t.Errorf("the operation did not survive the render:\n%s", entry.OpenAPIContent)
	}
}

func TestPrepareSpecLeavesAnOpenAPIDocumentAlone(t *testing.T) {
	t.Parallel()
	entry := apicatalog.SpecEntry{
		SpecName: "orders", Content: specFormatOpenAPI, SourceKind: apicatalog.SourceInline,
	}
	if err := prepareSpec(&entry); err != nil {
		t.Fatalf("prepareSpec: %v", err)
	}
	if entry.OpenAPIContent != "" {
		t.Errorf("an OpenAPI spec acquired a render: %q", entry.OpenAPIContent)
	}
	if entry.Effective() != specFormatOpenAPI {
		t.Error("Effective() did not resolve to the content itself")
	}
	if entry.OperationCount != 1 {
		t.Errorf("operation_count = %d, want 1", entry.OperationCount)
	}
}

// Changing a spec back to openapi has to clear the render, or Effective()
// would keep returning the document the previous format produced.
func TestPrepareSpecClearsAStaleRenderWhenTheFormatChangesBack(t *testing.T) {
	t.Parallel()
	entry := apicatalog.SpecEntry{
		SpecName:       "orders",
		Content:        specFormatOpenAPI,
		SourceKind:     apicatalog.SourceInline,
		SpecFormat:     apicatalog.FormatOpenAPI,
		OpenAPIContent: `{"openapi":"3.0.3","info":{"title":"stale","version":"1"},"paths":{}}`,
	}
	if err := prepareSpec(&entry); err != nil {
		t.Fatalf("prepareSpec: %v", err)
	}
	if entry.OpenAPIContent != "" {
		t.Errorf("the stale render survived: %q", entry.OpenAPIContent)
	}
	if entry.Effective() != specFormatOpenAPI {
		t.Error("Effective() still resolves to the stale render")
	}
}

// A document that will not import fails the SAVE. The alternative is a
// connection that registers with no operations and no explanation.
func TestPrepareSpecRefusesADocumentThatIsNotAWSDL(t *testing.T) {
	t.Parallel()
	entry := apicatalog.SpecEntry{
		SpecName: "orders", Content: specFormatOpenAPI,
		SourceKind: apicatalog.SourceInline, SpecFormat: apicatalog.FormatWSDL,
	}
	err := prepareSpec(&entry)
	if err == nil {
		t.Fatal("prepareSpec accepted an OpenAPI document as a WSDL")
	}
	if !strings.Contains(err.Error(), "WSDL could not be imported") {
		t.Errorf("error = %q, want it to name the import", err)
	}
}

func TestPrepareSpecRefusesAnUnknownFormat(t *testing.T) {
	t.Parallel()
	entry := apicatalog.SpecEntry{
		SpecName: "orders", Content: specFormatOpenAPI,
		SourceKind: apicatalog.SourceInline, SpecFormat: "raml",
	}
	err := prepareSpec(&entry)
	if err == nil {
		t.Fatal("prepareSpec accepted an unknown spec_format")
	}
	if !strings.Contains(err.Error(), "raml") {
		t.Errorf("error = %q, want it to name the format", err)
	}
}

// A WSDL that imports but renders something the loader refuses would be a
// spec the gateway silently skips at registration, so the effective document
// is validated too rather than only the import being checked.
func TestPrepareSpecValidatesTheRenderedDocument(t *testing.T) {
	t.Parallel()
	entry := apicatalog.SpecEntry{
		SpecName: "orders", Content: specFormatWSDL,
		SourceKind: apicatalog.SourceInline, SpecFormat: apicatalog.FormatWSDL,
	}
	if err := prepareSpec(&entry); err != nil {
		t.Fatalf("prepareSpec: %v", err)
	}
	if err := apicatalog.ValidateContent(entry.Effective()); err != nil {
		t.Errorf("the rendered document does not pass the catalog's own validation: %v", err)
	}
}

// An XML document that is not a WSDL is the likeliest wrong-format mistake
// after an OpenAPI one, and the refusal names the format to pick instead.
func TestPrepareSpecNamesTheOtherFormatWhenTheContentIsNotAWSDL(t *testing.T) {
	t.Parallel()
	entry := apicatalog.SpecEntry{
		SpecName: "orders", Content: `<openapi version="3.0.3"/>`,
		SourceKind: apicatalog.SourceInline, SpecFormat: apicatalog.FormatWSDL,
	}
	err := prepareSpec(&entry)
	if err == nil {
		t.Fatal("prepareSpec accepted an XML document that is not a WSDL")
	}
	if !strings.Contains(err.Error(), "set spec_format to openapi") {
		t.Errorf("error = %q, want it to name the format to use instead", err)
	}
}
