package soap

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

// weatherWSDL is a document/literal SOAP 1.1 service carrying the shapes an
// importer has to get right: a required scalar, an optional one, a repeated
// element, an attribute, a restriction with an enumeration, a nested complex
// type, and documentation at both the service and the operation.
const weatherWSDL = `<?xml version="1.0" encoding="utf-8"?>
<wsdl:definitions targetNamespace="http://example.org/weather"
  xmlns:tns="http://example.org/weather"
  xmlns:xsd="http://www.w3.org/2001/XMLSchema"
  xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/"
  xmlns:wsdl="http://schemas.xmlsoap.org/wsdl/">
  <wsdl:types>
    <xsd:schema targetNamespace="http://example.org/weather">
      <xsd:element name="GetForecast">
        <xsd:complexType>
          <xsd:sequence>
            <xsd:element name="City" type="xsd:string"/>
            <xsd:element name="Days" type="xsd:int" minOccurs="0"/>
            <xsd:element name="Units" type="tns:UnitKind" minOccurs="0"/>
          </xsd:sequence>
          <xsd:attribute name="RequestId" type="xsd:string" use="required"/>
        </xsd:complexType>
      </xsd:element>
      <xsd:element name="GetForecastResponse">
        <xsd:complexType>
          <xsd:sequence>
            <xsd:element name="Day" type="tns:Day" minOccurs="0" maxOccurs="unbounded"/>
          </xsd:sequence>
        </xsd:complexType>
      </xsd:element>
      <xsd:simpleType name="UnitKind">
        <xsd:restriction base="xsd:string">
          <xsd:enumeration value="metric"/>
          <xsd:enumeration value="imperial"/>
        </xsd:restriction>
      </xsd:simpleType>
      <xsd:complexType name="Day">
        <xsd:sequence>
          <xsd:element name="Date" type="xsd:date"/>
          <xsd:element name="HighC" type="xsd:decimal"/>
          <xsd:element name="Summary" type="xsd:string" nillable="true" minOccurs="0"/>
        </xsd:sequence>
      </xsd:complexType>
      <xsd:element name="Ping">
        <xsd:complexType><xsd:sequence/></xsd:complexType>
      </xsd:element>
    </xsd:schema>
  </wsdl:types>
  <wsdl:message name="GetForecastIn">
    <wsdl:part name="parameters" element="tns:GetForecast"/>
  </wsdl:message>
  <wsdl:message name="GetForecastOut">
    <wsdl:part name="parameters" element="tns:GetForecastResponse"/>
  </wsdl:message>
  <wsdl:message name="PingIn">
    <wsdl:part name="parameters" element="tns:Ping"/>
  </wsdl:message>
  <wsdl:portType name="WeatherSoap">
    <wsdl:documentation>Forecasts for a city.</wsdl:documentation>
    <wsdl:operation name="GetForecast">
      <wsdl:documentation>Returns the forecast. Accuracy is not guaranteed.</wsdl:documentation>
      <wsdl:input message="tns:GetForecastIn"/>
      <wsdl:output message="tns:GetForecastOut"/>
    </wsdl:operation>
    <wsdl:operation name="Ping">
      <wsdl:input message="tns:PingIn"/>
    </wsdl:operation>
  </wsdl:portType>
  <wsdl:binding name="WeatherSoapBinding" type="tns:WeatherSoap">
    <soap:binding transport="http://schemas.xmlsoap.org/soap/http" style="document"/>
    <wsdl:operation name="GetForecast">
      <soap:operation soapAction="http://example.org/weather/GetForecast"/>
      <wsdl:input><soap:body use="literal"/></wsdl:input>
      <wsdl:output><soap:body use="literal"/></wsdl:output>
    </wsdl:operation>
    <wsdl:operation name="Ping">
      <soap:operation soapAction=""/>
      <wsdl:input><soap:body use="literal"/></wsdl:input>
    </wsdl:operation>
  </wsdl:binding>
  <wsdl:service name="WeatherService">
    <wsdl:documentation>A weather service.</wsdl:documentation>
    <wsdl:port name="WeatherSoap" binding="tns:WeatherSoapBinding">
      <soap:address location="http://weather.example.org/svc/Weather.asmx"/>
    </wsdl:port>
  </wsdl:service>
</wsdl:definitions>`

// mustParse parses the fixture, failing the test rather than returning.
func mustParse(t *testing.T, doc string) *Service {
	t.Helper()
	svc, err := Parse(doc)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return svc
}

func TestParseResolvesTheServiceAndItsAddress(t *testing.T) {
	svc := mustParse(t, weatherWSDL)

	if svc.Name != "WeatherService" {
		t.Errorf("service name = %q, want WeatherService", svc.Name)
	}
	if svc.SOAPVersion != Version11 {
		t.Errorf("SOAP version = %q, want %q", svc.SOAPVersion, Version11)
	}
	if svc.MediaType() != MediaType11 {
		t.Errorf("media type = %q, want %q", svc.MediaType(), MediaType11)
	}
	// The host is dropped: a catalog spec is shared across connections and
	// each one supplies its own base_url.
	if svc.Path != "/svc/Weather.asmx" {
		t.Errorf("path = %q, want /svc/Weather.asmx", svc.Path)
	}
	if svc.TargetNamespace != "http://example.org/weather" {
		t.Errorf("target namespace = %q", svc.TargetNamespace)
	}
	if len(svc.Operations) != 2 {
		t.Fatalf("operations = %d, want 2", len(svc.Operations))
	}
}

func TestParseResolvesEachOperationsActionAndBodyElements(t *testing.T) {
	svc := mustParse(t, weatherWSDL)
	op := svc.Operations[0]

	if op.Name != "GetForecast" {
		t.Fatalf("first operation = %q, want GetForecast", op.Name)
	}
	if op.SOAPAction != "http://example.org/weather/GetForecast" {
		t.Errorf("soapAction = %q", op.SOAPAction)
	}
	if op.Input.Name != "GetForecast" || op.Input.Namespace != "http://example.org/weather" {
		t.Errorf("input element = %q in %q", op.Input.Name, op.Input.Namespace)
	}
	if op.Output.Name != "GetForecastResponse" {
		t.Errorf("output element = %q", op.Output.Name)
	}
	if op.Documentation != "Returns the forecast. Accuracy is not guaranteed." {
		t.Errorf("documentation = %q", op.Documentation)
	}
}

// A one-way operation declares no output message. It must import as an
// operation with no output rather than being dropped or failing the import.
func TestParseKeepsAOneWayOperation(t *testing.T) {
	svc := mustParse(t, weatherWSDL)
	op := svc.Operations[1]

	if op.Name != "Ping" {
		t.Fatalf("second operation = %q, want Ping", op.Name)
	}
	if op.Output.Name != "" {
		t.Errorf("one-way operation has output element %q, want none", op.Output.Name)
	}
	if op.Input.Name != "Ping" {
		t.Errorf("input element = %q, want Ping", op.Input.Name)
	}
}

func TestParseConvertsXSDIntoJSONSchema(t *testing.T) {
	svc := mustParse(t, weatherWSDL)
	in := svc.Operations[0].Input.Schema

	if in == nil || in.Type != "object" {
		t.Fatalf("input schema = %+v, want an object", in)
	}
	if got := in.Properties["City"]; got == nil || got.Type != "string" {
		t.Errorf("City = %+v, want a string", got)
	}
	if got := in.Properties["Days"]; got == nil || got.Type != "integer" || got.Format != "int32" {
		t.Errorf("Days = %+v, want an int32 integer", got)
	}
	// minOccurs="0" makes a particle optional; an absent minOccurs leaves
	// it required, which is what the XSD default means.
	if !contains(in.Required, "City") {
		t.Errorf("required = %v, want City", in.Required)
	}
	if contains(in.Required, "Days") {
		t.Errorf("required = %v, want Days absent", in.Required)
	}
}

func TestParseRendersAnEnumerationAndAnAttribute(t *testing.T) {
	svc := mustParse(t, weatherWSDL)
	in := svc.Operations[0].Input.Schema

	units := in.Properties["Units"]
	if units == nil || units.Type != "string" {
		t.Fatalf("Units = %+v, want a string", units)
	}
	if len(units.Enum) != 2 || units.Enum[0] != "metric" || units.Enum[1] != "imperial" {
		t.Errorf("Units enum = %v, want [metric imperial]", units.Enum)
	}
	// An attribute is a property the envelope writer has to place
	// differently, so it is marked rather than silently mixed in with the
	// child elements.
	id := in.Properties["RequestId"]
	if id == nil || !id.Attribute {
		t.Fatalf("RequestId = %+v, want an attribute-marked property", id)
	}
	if !contains(in.Required, "RequestId") {
		t.Errorf("required = %v, want RequestId (use=required)", in.Required)
	}
}

func TestParseWrapsARepeatedElementInAnArray(t *testing.T) {
	svc := mustParse(t, weatherWSDL)
	out := svc.Operations[0].Output.Schema

	day := out.Properties["Day"]
	if day == nil || day.Type != "array" || day.Items == nil {
		t.Fatalf("Day = %+v, want an array", day)
	}
	if day.Items.Properties["HighC"] == nil || day.Items.Properties["HighC"].Type != "number" {
		t.Errorf("Day.HighC = %+v, want a number", day.Items.Properties["HighC"])
	}
	if got := day.Items.Properties["Date"]; got == nil || got.Format != "date" {
		t.Errorf("Day.Date = %+v, want a date-formatted string", got)
	}
	if got := day.Items.Properties["Summary"]; got == nil || !got.Nullable {
		t.Errorf("Day.Summary = %+v, want nullable", got)
	}
}

// The rendered document must satisfy the same loader and validator every
// operator-supplied spec goes through, or the catalog would store a spec the
// gateway silently skips at registration.
func TestRenderProducesAValidOpenAPIDocument(t *testing.T) {
	_, rendered := mustImport(t, weatherWSDL)

	loader := openapi3.Loader{IsExternalRefsAllowed: false}
	doc, err := loader.LoadFromData([]byte(rendered))
	if err != nil {
		t.Fatalf("loading the rendered document: %v\n%s", err, rendered)
	}
	if err := doc.Validate(loader.Context,
		openapi3.DisableExamplesValidation(),
		openapi3.DisableSchemaPatternValidation(),
		openapi3.DisableSchemaDefaultsValidation(),
	); err != nil {
		t.Fatalf("validating the rendered document: %v\n%s", err, rendered)
	}
	if doc.Info.Title != "WeatherService" {
		t.Errorf("info.title = %q", doc.Info.Title)
	}
}

// Every operation of a SOAP service is a POST to one address, which one path
// item cannot hold. Each gets its own key so api_discover lists them all.
func TestRenderGivesEachOperationItsOwnPathKey(t *testing.T) {
	_, rendered := mustImport(t, weatherWSDL)
	doc := decodeDocument(t, rendered)

	if len(doc.Paths) != 2 {
		t.Fatalf("path keys = %d, want 2: %v", len(doc.Paths), keysOf(doc.Paths))
	}
	for _, want := range []string{"/svc/Weather.asmx/GetForecast", "/svc/Weather.asmx/Ping"} {
		if _, ok := doc.Paths[want]; !ok {
			t.Errorf("missing path key %q, have %v", want, keysOf(doc.Paths))
		}
	}
}

func TestRenderCarriesTheWirePathAndActionInTheExtension(t *testing.T) {
	_, rendered := mustImport(t, weatherWSDL)
	doc := decodeDocument(t, rendered)

	ext := doc.Paths["/svc/Weather.asmx/GetForecast"].Post.Extension
	if ext == nil {
		t.Fatal("GetForecast has no x-soap extension")
	}
	if ext.Path != "/svc/Weather.asmx" {
		t.Errorf("x-soap.path = %q, want the one wire path", ext.Path)
	}
	if ext.Action != "http://example.org/weather/GetForecast" {
		t.Errorf("x-soap.action = %q", ext.Action)
	}
	if ext.Version != Version11 {
		t.Errorf("x-soap.version = %q", ext.Version)
	}
	if ext.Input.Element != "GetForecast" || ext.Input.Namespace != "http://example.org/weather" {
		t.Errorf("x-soap.input = %+v", ext.Input)
	}
	if ext.Output == nil || ext.Output.Element != "GetForecastResponse" {
		t.Errorf("x-soap.output = %+v", ext.Output)
	}
}

// An empty soapAction is legal and is not the same as no action: several
// stacks dispatch on the header's presence, so it has to survive the render.
func TestRenderKeepsAnEmptyActionAsAnEmptyString(t *testing.T) {
	_, rendered := mustImport(t, weatherWSDL)
	doc := decodeDocument(t, rendered)

	ext := doc.Paths["/svc/Weather.asmx/Ping"].Post.Extension
	if ext == nil {
		t.Fatal("Ping has no x-soap extension")
	}
	if ext.Action != "" {
		t.Errorf("x-soap.action = %q, want empty", ext.Action)
	}
	if !strings.Contains(rendered, `"action": ""`) {
		t.Error("the rendered document omits an empty action rather than writing it")
	}
}

func TestRenderDeclaresTheSOAPMediaTypeOnBothDirections(t *testing.T) {
	_, rendered := mustImport(t, weatherWSDL)
	doc := decodeDocument(t, rendered)

	op := doc.Paths["/svc/Weather.asmx/GetForecast"].Post
	if op.RequestBody == nil {
		t.Fatal("GetForecast has no request body")
	}
	if _, ok := op.RequestBody.Content[MediaType11]; !ok {
		t.Errorf("request media types = %v, want %q", keysOf(op.RequestBody.Content), MediaType11)
	}
	// The response media type is what makes the gateway's auto decode read
	// the answer as XML without the caller passing decode.
	if _, ok := op.Responses["200"].Content[MediaType11]; !ok {
		t.Errorf("200 media types = %v, want %q", keysOf(op.Responses["200"].Content), MediaType11)
	}
	if doc.Paths["/svc/Weather.asmx/Ping"].Post.Responses["204"].Description == "" {
		t.Error("a one-way operation should render a 204 with a description")
	}
}

func TestParseRefusesWhatItWouldImportWrongly(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		want string
	}{
		{"not a wsdl", `<openapi/>`, "not a WSDL 1.1"},
		{"rpc style", swap(weatherWSDL, `<soap:operation soapAction="http://example.org/weather/GetForecast"/>`,
			`<soap:operation soapAction="x" style="rpc"/>`), "rpc style"},
		{"encoded use", swap(weatherWSDL, `<wsdl:input><soap:body use="literal"/></wsdl:input>
      <wsdl:output><soap:body use="literal"/></wsdl:output>`,
			`<wsdl:input><soap:body use="encoded"/></wsdl:input>`), "encoded"},
		{
			"undefined message", swap(weatherWSDL, `message="tns:GetForecastIn"`, `message="tns:Missing"`),
			"which the document does not define",
		},
		{
			"element in an imported schema", swap(weatherWSDL, `element="tns:GetForecast"`, `element="tns:Elsewhere"`),
			"no inline schema declares",
		},
		{
			"no soap port", swap(weatherWSDL, `<soap:address location="http://weather.example.org/svc/Weather.asmx"/>`, ``),
			"no service port has a <soap:address>",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.doc)
			if err == nil {
				t.Fatal("Parse accepted a document it should refuse")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

// SOAP 1.2 changes the binding namespace and the Content-Type, and an upstream
// of either version rejects the other's.
func TestParseReadsASOAP12Binding(t *testing.T) {
	doc := strings.ReplaceAll(weatherWSDL,
		`xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/"`,
		`xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap12/"`)
	svc := mustParse(t, doc)

	if svc.SOAPVersion != Version12 {
		t.Errorf("SOAP version = %q, want %q", svc.SOAPVersion, Version12)
	}
	if svc.MediaType() != MediaType12 {
		t.Errorf("media type = %q, want %q", svc.MediaType(), MediaType12)
	}
}

// A document type declaration is refused by the shared parser, so a WSDL
// carrying one never reaches the importer's own logic.
func TestParseRefusesADocumentTypeDeclaration(t *testing.T) {
	doc := `<!DOCTYPE definitions [<!ENTITY x "y">]>` + weatherWSDL
	if _, err := Parse(doc); err == nil {
		t.Fatal("Parse accepted a document type declaration")
	}
}

// Helpers.

func mustImport(t *testing.T, doc string) (svc *Service, rendered string) {
	t.Helper()
	svc, rendered, err := Import(doc)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	return svc, rendered
}

func decodeDocument(t *testing.T, rendered string) document {
	t.Helper()
	var doc document
	if err := json.Unmarshal([]byte(rendered), &doc); err != nil {
		t.Fatalf("unmarshalling the rendered document: %v", err)
	}
	return doc
}

func swap(doc, old, replacement string) string {
	return strings.Replace(doc, old, replacement, 1)
}

func contains(list []string, want string) bool {
	return slices.Contains(list, want)
}

func keysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
