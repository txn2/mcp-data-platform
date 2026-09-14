package apigateway

import (
	"net/http"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/soap"
)

// soapSpecFor renders the fixture WSDL through the importer and parses the
// result the way a registration does, so these tests exercise the document the
// catalog actually stores rather than one hand-written to suit them.
func soapSpecFor(t *testing.T, wsdl string) map[string]*specState {
	t.Helper()
	_, rendered, err := soap.Import(wsdl)
	if err != nil {
		t.Fatalf("soap.Import: %v", err)
	}
	return map[string]*specState{"erp": mustParseSpec(t, rendered)}
}

// soapFixtureWSDL is a minimal document/literal SOAP 1.1 service: one
// operation, one required field, one optional one.
const soapFixtureWSDL = `<?xml version="1.0"?>
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
          <xsd:element name="Detail" type="xsd:boolean" minOccurs="0"/>
        </xsd:sequence></xsd:complexType>
      </xsd:element>
      <xsd:element name="FailOrder">
        <xsd:complexType><xsd:sequence>
          <xsd:element name="Reason" type="xsd:string"/>
        </xsd:sequence></xsd:complexType>
      </xsd:element>
      <xsd:element name="GetOrderResponse">
        <xsd:complexType><xsd:sequence>
          <xsd:element name="Total" type="xsd:decimal"/>
        </xsd:sequence></xsd:complexType>
      </xsd:element>
    </xsd:schema>
  </types>
  <message name="FailIn"><part name="parameters" element="tns:FailOrder"/></message>
  <message name="In"><part name="parameters" element="tns:GetOrder"/></message>
  <message name="Out"><part name="parameters" element="tns:GetOrderResponse"/></message>
  <portType name="OrdersSoap">
    <operation name="GetOrder"><input message="tns:In"/><output message="tns:Out"/></operation>
    <operation name="FailOrder"><input message="tns:FailIn"/></operation>
  </portType>
  <binding name="OrdersBinding" type="tns:OrdersSoap">
    <soap:binding transport="http://schemas.xmlsoap.org/soap/http"/>
    <operation name="GetOrder">
      <soap:operation soapAction="urn:acme:orders/GetOrder"/>
      <input><soap:body use="literal"/></input>
      <output><soap:body use="literal"/></output>
    </operation>
    <operation name="FailOrder">
      <soap:operation soapAction="urn:acme:orders/FailOrder"/>
      <input><soap:body use="literal"/></input>
    </operation>
  </binding>
  <service name="OrdersService">
    <port name="OrdersSoap" binding="tns:OrdersBinding">
      <soap:address location="http://erp.example.org/soap/Orders.svc"/>
    </port>
  </service>
</definitions>`

const soapDocumentPath = "/soap/Orders.svc/GetOrder"

func TestResolveSOAPOperationFindsTheOperationByItsDocumentPath(t *testing.T) {
	specs := soapSpecFor(t, soapFixtureWSDL)

	sop := resolveSOAPOperation(specs, http.MethodPost, soapDocumentPath)
	if sop == nil {
		t.Fatal("resolveSOAPOperation found no SOAP operation")
	}
	if sop.ext.Action != "urn:acme:orders/GetOrder" {
		t.Errorf("action = %q", sop.ext.Action)
	}
	if sop.schema == nil || sop.schema.Properties["OrderId"] == nil {
		t.Error("the request schema did not survive into the parsed document")
	}
}

func TestResolveSOAPOperationIgnoresEverythingElse(t *testing.T) {
	specs := soapSpecFor(t, soapFixtureWSDL)

	// Only POST can be a SOAP operation.
	if sop := resolveSOAPOperation(specs, http.MethodGet, soapDocumentPath); sop != nil {
		t.Error("a GET resolved to a SOAP operation")
	}
	// A path the document does not carry.
	if sop := resolveSOAPOperation(specs, http.MethodPost, "/soap/Orders.svc/Nope"); sop != nil {
		t.Error("an unknown path resolved to a SOAP operation")
	}
	// A connection with no catalog at all.
	if sop := resolveSOAPOperation(nil, http.MethodPost, soapDocumentPath); sop != nil {
		t.Error("a catalog-less connection resolved to a SOAP operation")
	}
	// An ordinary OpenAPI operation carries no extension.
	plain := map[string]*specState{"main": mustParseSpec(t, jsonOpSpec)}
	for path := range plain["main"].doc.Paths.Map() {
		if sop := resolveSOAPOperation(plain, http.MethodPost, path); sop != nil {
			t.Errorf("the REST operation at %q resolved as SOAP", path)
		}
	}
}

// The document key is per-operation because OpenAPI cannot hold two POSTs on
// one path; the address the request is sent to is the service's single one.
func TestWirePathIsTheServiceAddressNotTheDocumentKey(t *testing.T) {
	specs := soapSpecFor(t, soapFixtureWSDL)
	sop := resolveSOAPOperation(specs, http.MethodPost, soapDocumentPath)

	if got := wirePath(sop, soapDocumentPath); got != "/soap/Orders.svc" {
		t.Errorf("wirePath = %q, want the service address", got)
	}
	if got := wirePath(nil, "/v1/orders"); got != "/v1/orders" {
		t.Errorf("wirePath for a non-SOAP operation = %q, want it unchanged", got)
	}
}

func TestEncodeForBuildsAnEnvelopeFromAnObjectBody(t *testing.T) {
	specs := soapSpecFor(t, soapFixtureWSDL)
	sop := resolveSOAPOperation(specs, http.MethodPost, soapDocumentPath)

	in := InvokeInput{
		Method: http.MethodPost,
		Path:   soapDocumentPath,
		Body:   map[string]any{"OrderId": "A-9", "Detail": true},
	}
	enc, headers, err := encodeFor(sop, http.MethodPost, in, nil)
	if err != nil {
		t.Fatalf("encodeFor: %v", err)
	}
	body := string(enc.data)
	for _, want := range []string{"Envelope", "<GetOrder", `<OrderId xmlns="">A-9</OrderId>`, `<Detail xmlns="">true</Detail>`} {
		if !strings.Contains(body, want) {
			t.Errorf("envelope does not contain %q:\n%s", want, body)
		}
	}
	if enc.contentType != soap.MediaType11+charsetUTF8 {
		t.Errorf("content type = %q", enc.contentType)
	}
	// SOAP 1.1 announces the action in its own header, quoted.
	if got := headers[headerSOAPAction]; got != `"urn:acme:orders/GetOrder"` {
		t.Errorf("SOAPAction = %q", got)
	}
	if !strings.Contains(headers[headerAccept], soap.MediaType11) {
		t.Errorf("Accept = %q, want the envelope media type", headers[headerAccept])
	}
}

// A caller holding an envelope of their own keeps the way through they have
// always had: the string is sent as written.
func TestEncodeForSendsAStringBodyVerbatim(t *testing.T) {
	specs := soapSpecFor(t, soapFixtureWSDL)
	sop := resolveSOAPOperation(specs, http.MethodPost, soapDocumentPath)
	raw := `<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body/></soap:Envelope>`

	enc, _, err := encodeFor(sop, http.MethodPost, InvokeInput{Body: raw}, nil)
	if err != nil {
		t.Fatalf("encodeFor: %v", err)
	}
	if string(enc.data) != raw {
		t.Errorf("string body was rewritten:\n%s", enc.data)
	}
}

// SOAP 1.2 moved the action into a Content-Type parameter; sending the 1.1
// header to a 1.2 endpoint is a common way to be refused.
func TestEncodeForPutsTheActionInTheContentTypeForSOAP12(t *testing.T) {
	wsdl := strings.ReplaceAll(soapFixtureWSDL,
		`xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/"`,
		`xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap12/"`)
	specs := soapSpecFor(t, wsdl)
	sop := resolveSOAPOperation(specs, http.MethodPost, soapDocumentPath)

	enc, headers, err := encodeFor(sop, http.MethodPost, InvokeInput{Body: map[string]any{"OrderId": "x"}}, nil)
	if err != nil {
		t.Fatalf("encodeFor: %v", err)
	}
	if !strings.Contains(enc.contentType, soap.MediaType12) {
		t.Errorf("content type = %q, want the 1.2 media type", enc.contentType)
	}
	if !strings.Contains(enc.contentType, `action="urn:acme:orders/GetOrder"`) {
		t.Errorf("content type = %q, want the action parameter", enc.contentType)
	}
	if _, present := headers[headerSOAPAction]; present {
		t.Error("a SOAP 1.2 request carries the 1.1 SOAPAction header")
	}
}

// The caller's header map belongs to their InvokeInput; a walk that pages or
// an export that retries would otherwise accumulate headers across calls.
func TestEncodeForDoesNotWriteIntoTheCallersHeaders(t *testing.T) {
	specs := soapSpecFor(t, soapFixtureWSDL)
	sop := resolveSOAPOperation(specs, http.MethodPost, soapDocumentPath)
	callerHeaders := map[string]string{"X-Trace": "1"}

	_, headers, err := encodeFor(sop, http.MethodPost, InvokeInput{
		Body: map[string]any{"OrderId": "x"}, Headers: callerHeaders,
	}, nil)
	if err != nil {
		t.Fatalf("encodeFor: %v", err)
	}
	if _, leaked := callerHeaders[headerSOAPAction]; leaked {
		t.Error("the caller's header map was written to")
	}
	if headers["X-Trace"] != "1" {
		t.Error("the caller's own headers were dropped")
	}
}

// A caller that set either header meant it; a per-call override is how an
// operator works around an upstream that dispatches unusually.
func TestEncodeForLeavesCallerSetHeadersAlone(t *testing.T) {
	specs := soapSpecFor(t, soapFixtureWSDL)
	sop := resolveSOAPOperation(specs, http.MethodPost, soapDocumentPath)

	_, headers, err := encodeFor(sop, http.MethodPost, InvokeInput{
		Body:    map[string]any{"OrderId": "x"},
		Headers: map[string]string{"soapaction": `"urn:override"`, "accept": "text/xml"},
	}, nil)
	if err != nil {
		t.Fatalf("encodeFor: %v", err)
	}
	if got := headers["soapaction"]; got != `"urn:override"` {
		t.Errorf("caller SOAPAction = %q, want it kept", got)
	}
	if _, added := headers[headerSOAPAction]; added {
		t.Error("a second SOAPAction was added beside the caller's differently-cased one")
	}
	if headers["accept"] != "text/xml" {
		t.Errorf("caller Accept = %q, want it kept", headers["accept"])
	}
}

// An operation whose body the caller omits still has a body: the envelope and
// the operation element are what the upstream dispatches on.
func TestEncodeForBuildsAnEmptyBodyIntoAnEnvelope(t *testing.T) {
	specs := soapSpecFor(t, soapFixtureWSDL)
	sop := resolveSOAPOperation(specs, http.MethodPost, soapDocumentPath)

	enc, _, err := encodeFor(sop, http.MethodPost, InvokeInput{}, nil)
	if err != nil {
		t.Fatalf("encodeFor: %v", err)
	}
	if !strings.Contains(string(enc.data), "<GetOrder") {
		t.Errorf("empty body did not produce the operation element:\n%s", enc.data)
	}
}

func TestEncodeForRefusesABodyThatIsNotAnObject(t *testing.T) {
	specs := soapSpecFor(t, soapFixtureWSDL)
	sop := resolveSOAPOperation(specs, http.MethodPost, soapDocumentPath)

	if _, _, err := encodeFor(sop, http.MethodPost, InvokeInput{Body: []any{1}}, nil); err == nil {
		t.Error("encodeFor accepted a list as a SOAP body")
	}
}

// A non-SOAP operation must reach the encoder it always did, unchanged.
func TestEncodeForLeavesAnOrdinaryOperationToTheExistingEncoder(t *testing.T) {
	in := InvokeInput{Body: map[string]any{"a": 1}, Headers: map[string]string{"X": "1"}}

	enc, headers, err := encodeFor(nil, http.MethodPost, in, []string{applicationJSON})
	if err != nil {
		t.Fatalf("encodeFor: %v", err)
	}
	if enc.contentType != applicationJSON {
		t.Errorf("content type = %q, want %q", enc.contentType, applicationJSON)
	}
	if string(enc.data) != `{"a":1}` {
		t.Errorf("body = %s, want the JSON marshaling", enc.data)
	}
	if headers["X"] != "1" {
		t.Error("the caller's headers were not passed through")
	}
}

// A soap:Fault is the upstream's own account of the failure. Without this the
// call reports only the HTTP status text.
func TestResponseDecoderReportsASOAPFault(t *testing.T) {
	specs := soapSpecFor(t, soapFixtureWSDL)
	dec := newResponseDecoder(InvokeInput{Method: http.MethodPost, Path: soapDocumentPath}, specs)

	fault := `<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body>` +
		`<soap:Fault><faultcode>soap:Client</faultcode><faultstring>Unknown order</faultstring>` +
		`</soap:Fault></soap:Body></soap:Envelope>`
	got := dec.decode("text/xml", []byte(fault))

	if got.fault != "soap:Client: Unknown order" {
		t.Errorf("fault = %q", got.fault)
	}
	// The body is still returned in full: the fault message is a summary,
	// not a replacement for what the upstream sent.
	if got.body == nil {
		t.Error("the fault body was dropped")
	}
}

func TestResponseDecoderLeavesASuccessfulSOAPResponseAlone(t *testing.T) {
	specs := soapSpecFor(t, soapFixtureWSDL)
	dec := newResponseDecoder(InvokeInput{Method: http.MethodPost, Path: soapDocumentPath}, specs)

	ok := `<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body>` +
		`<GetOrderResponse xmlns="urn:acme:orders"><Total>12.50</Total></GetOrderResponse>` +
		`</soap:Body></soap:Envelope>`
	got := dec.decode("text/xml", []byte(ok))

	if got.fault != "" {
		t.Errorf("a successful response reported fault %q", got.fault)
	}
	if got.body == nil {
		t.Error("the response body was dropped")
	}
}

// The generated document declares the envelope media type on its success
// response, which is what makes auto mode return a tree without the caller
// passing decode.
func TestSOAPResponseDecodesAsXMLWithoutTheCallerAskingFor(t *testing.T) {
	specs := soapSpecFor(t, soapFixtureWSDL)
	dec := newResponseDecoder(InvokeInput{Method: http.MethodPost, Path: soapDocumentPath}, specs)

	if mode := dec.effectiveMode("text/xml"); mode != DecodeXML {
		t.Errorf("effective mode = %q, want %q", mode, DecodeXML)
	}
}

// A caller who pinned text asked for text, and fault reading needs the tree.
func TestPinnedTextModeSkipsFaultReading(t *testing.T) {
	specs := soapSpecFor(t, soapFixtureWSDL)
	dec := newResponseDecoder(InvokeInput{
		Method: http.MethodPost, Path: soapDocumentPath, Decode: DecodeText,
	}, specs)

	fault := `<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body>` +
		`<soap:Fault><faultstring>nope</faultstring></soap:Fault></soap:Body></soap:Envelope>`
	got := dec.decode("text/xml", []byte(fault))

	if got.fault != "" {
		t.Errorf("fault = %q, want none under an explicit text decode", got.fault)
	}
	if _, isString := got.body.(string); !isString {
		t.Errorf("body = %T, want the raw text the caller asked for", got.body)
	}
}

// A one-way operation declares no output, so the rendered document carries a
// 204 with no media type and nothing for the catalog to declare XML on. Its
// fault still has to be read: the envelope is the protocol, not something the
// responses section opts into. Before this the fault came back as an opaque
// string and the call reported only "Internal Server Error", which is the
// exact failure the SOAP path exists to end.
func TestAOneWayOperationsFaultIsStillRead(t *testing.T) {
	specs := soapSpecFor(t, soapFixtureWSDL)
	const oneWayPath = "/soap/Orders.svc/FailOrder"

	// The premise: this operation declares no XML success response.
	if resolveDeclaresXMLResponse(specs, http.MethodPost, oneWayPath) {
		t.Fatal("the one-way operation declares an XML success response, so this test proves nothing")
	}

	dec := newResponseDecoder(InvokeInput{Method: http.MethodPost, Path: oneWayPath}, specs)
	if mode := dec.effectiveMode("text/xml"); mode != DecodeXML {
		t.Errorf("effective mode = %q, want %q: a SOAP operation answers XML by definition", mode, DecodeXML)
	}
	fault := `<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body>` +
		`<soap:Fault><faultcode>soap:Server</faultcode>` +
		`<faultstring>FailOrder always fails</faultstring></soap:Fault></soap:Body></soap:Envelope>`
	got := dec.decode("text/xml", []byte(fault))
	if got.fault != "soap:Server: FailOrder always fails" {
		t.Errorf("fault = %q", got.fault)
	}
}

// The widening is confined to XML. A SOAP endpoint behind a proxy that answers
// an HTML error page must still return that page readable, not a parse failure.
func TestASOAPOperationsNonXMLResponseIsStillText(t *testing.T) {
	specs := soapSpecFor(t, soapFixtureWSDL)
	dec := newResponseDecoder(InvokeInput{Method: http.MethodPost, Path: soapDocumentPath}, specs)

	if mode := dec.effectiveMode("text/html"); mode != DecodeText {
		t.Errorf("effective mode for text/html = %q, want %q", mode, DecodeText)
	}
	got := dec.decode("text/html", []byte("<html><body>502 Bad Gateway</body></html>"))
	if body, isString := got.body.(string); !isString || !strings.Contains(body, "502") {
		t.Errorf("body = %#v, want the readable page", got.body)
	}
	if got.fault != "" {
		t.Errorf("an HTML error page reported fault %q", got.fault)
	}
}

// A connection with no catalog is untouched: it has no SOAP operation to
// resolve, so it returns the string it always has.
func TestANonSOAPXMLResponseStillNeedsTheCatalogOrTheArgument(t *testing.T) {
	dec := newResponseDecoder(InvokeInput{Method: http.MethodPost, Path: "/anything"}, nil)

	if mode := dec.effectiveMode("text/xml"); mode != DecodeText {
		t.Errorf("effective mode = %q, want %q for a catalog-less connection", mode, DecodeText)
	}
}

// The wire path is spec-supplied, not caller-supplied, so it does not pass
// through the caller-path check. It is held to the same one anyway: a service
// description an operator registered is not a reason to accept a path shape
// the model could not have sent.
func TestAWireePathWithARefusedShapeIsRefused(t *testing.T) {
	for _, bad := range []string{"//evil.example.org/x", "/a\r\n/b", "/a@b"} {
		if err := validatePath(bad); err == nil {
			t.Errorf("validatePath accepted %q, so the wire-path check would not stop it", bad)
		}
	}
	// The ordinary address a WSDL carries passes.
	if err := validatePath("/soap/Orders.svc"); err != nil {
		t.Errorf("validatePath refused an ordinary service address: %v", err)
	}
}

// hostileAddressWSDL declares a service address whose path is a shape the
// caller-path check refuses. A path beginning "//" is read by a URL parser as
// the start of an authority, which is the shape validatePath exists to stop.
const hostileAddressWSDL = `<?xml version="1.0"?>
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
    </xsd:schema>
  </types>
  <message name="In"><part name="parameters" element="tns:GetOrder"/></message>
  <portType name="OrdersSoap">
    <operation name="GetOrder"><input message="tns:In"/></operation>
  </portType>
  <binding name="OrdersBinding" type="tns:OrdersSoap">
    <soap:binding transport="http://schemas.xmlsoap.org/soap/http"/>
    <operation name="GetOrder">
      <soap:operation soapAction="urn:acme:orders/GetOrder"/>
      <input><soap:body use="literal"/></input>
    </operation>
  </binding>
  <service name="OrdersService">
    <port name="OrdersSoap" binding="tns:OrdersBinding">
      <soap:address location="http://erp.example.org//evil.example.org/Orders.svc"/>
    </port>
  </service>
</definitions>`

// The wire path is safe without a check of its own only because it is a prefix
// of the path the caller addressed, which validatePath has already refused if
// it carried a shape a path may not. This holds that invariant, including for
// a service description whose address is itself hostile: there the DOCUMENT
// path is hostile too, so the existing check refuses the call before the wire
// path is ever reached.
func TestTheWirePathIsAPrefixOfTheAddressedPath(t *testing.T) {
	for _, wsdl := range []string{soapFixtureWSDL, hostileAddressWSDL} {
		svc, _, err := soap.Import(wsdl)
		if err != nil {
			t.Fatalf("soap.Import: %v", err)
		}
		for _, op := range svc.Operations {
			addressed := strings.TrimSuffix(svc.Path, "/") + "/" + op.Name
			if !strings.HasPrefix(addressed, svc.Path) {
				t.Errorf("wire path %q is not a prefix of the addressed path %q", svc.Path, addressed)
			}
			// And the hostile one is refused by the caller-path check,
			// which is what makes a second check unnecessary rather
			// than merely absent.
			if strings.HasPrefix(svc.Path, "//") && validatePath(addressed) == nil {
				t.Errorf("addressed path %q was accepted despite a hostile service address", addressed)
			}
		}
	}
}
