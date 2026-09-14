// dev-soap-mock is a development-only SOAP upstream that dev/start.sh launches
// alongside the platform, so the WSDL catalog path (#1736) can be exercised
// end-to-end against a service that actually speaks SOAP.
//
// Nothing else in the stack does. The api-test fixture is a REST service, and
// before this the only SOAP in the repository was a string constant in a unit
// test — which is exactly the shape of gap that lets an envelope be assembled
// wrongly and pass every gate.
//
// Two services run from one process, because the two SOAP versions differ in
// the three things most easily got wrong:
//
//   - /Orders.svc    SOAP 1.1. Content-Type text/xml, the action in a quoted
//     SOAPAction header, and an UNQUALIFIED schema, so child
//     elements must carry no namespace.
//   - /Orders12.svc  SOAP 1.2. Content-Type application/soap+xml with the
//     action as a parameter, no SOAPAction header, and a
//     QUALIFIED schema, so child elements must be in the
//     target namespace.
//
// Each service refuses a request that gets any of those wrong, with a
// soap:Fault naming what was expected. That is the point: a mock that accepted
// anything would let a caller's envelope be wrong in every one of those ways
// and still report success.
//
// The XML here is parsed and written with encoding/xml rather than through the
// platform's own internal/soap. An upstream that shared the implementation it
// is meant to check would agree with it about a mistake.
//
// Operations, on both services:
//
//	GetOrder(OrderId, Detail?)     -> the order, echoing what it received
//	ListOrders(Customer, Limit?)   -> a repeated element, for list encoding
//	FailOrder(Reason)              -> always a soap:Fault, HTTP 500
//
// Usage:
//
//	go run ./cmd/dev-soap-mock
//	SOAP_MOCK_ADDR=:9999 go run ./cmd/dev-soap-mock
//
// Health:
//
//	curl http://localhost:9285/.health
//	curl 'http://localhost:9285/Orders.svc?wsdl'
package main

import (
	"encoding/xml"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/logsan"
)

// The namespaces the two services speak.
const (
	targetNS    = "urn:acme:orders"
	envelopeNS  = "http://schemas.xmlsoap.org/soap/envelope/"
	envelope12  = "http://www.w3.org/2003/05/soap-envelope"
	defaultAddr = ":9285"

	// The two fault codes this service raises, in their SOAP 1.1 spelling.
	// A caller's mistake is Client; the service's own refusal is Server.
	faultClient = "Client"
	faultServer = "Server"

	headerContentType = "Content-Type"
)

// The server's bounds. A dev fixture still sets them: an unbounded
// ReadHeaderTimeout is the one default that turns a local service into a way
// to hold a connection open indefinitely.
const (
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 30 * time.Second
	writeTimeout      = 30 * time.Second
)

// service is one of the two endpoints, differing only in the three things the
// versions disagree about.
type service struct {
	path string
	// version is "1.1" or "1.2".
	version string
	// envelopeNS is the namespace the request envelope must be in.
	envelopeNS string
	// mediaType is the Content-Type the request must carry.
	mediaType string
	// qualified is whether the schema declares elementFormDefault, and so
	// whether child elements must be in the target namespace.
	qualified bool
}

func main() {
	addr := os.Getenv("SOAP_MOCK_ADDR")
	if addr == "" {
		addr = defaultAddr
	}
	advertised = addr
	services := []service{
		{path: "/Orders.svc", version: "1.1", envelopeNS: envelopeNS, mediaType: "text/xml", qualified: false},
		{path: "/Orders12.svc", version: "1.2", envelopeNS: envelope12, mediaType: "application/soap+xml", qualified: true},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/.health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(headerContentType, "text/plain")
		_, _ = fmt.Fprintln(w, "ok")
	})
	for _, svc := range services {
		mux.HandleFunc(svc.path, svc.handle)
	}
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
	}
	log.Printf("dev-soap-mock listening on %s (SOAP 1.1 /Orders.svc, SOAP 1.2 /Orders12.svc)",
		logsan.SanitizeForLog(addr)) // #nosec G706 -- addr sanitized via logsan.SanitizeForLog; gosec does not model the helper
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("dev-soap-mock: %v", err)
	}
}

// handle serves one service's WSDL and its operations.
func (s service) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		if r.URL.Query().Has("wsdl") {
			w.Header().Set(headerContentType, "text/xml; charset=utf-8")
			_, _ = fmt.Fprint(w, s.wsdl())
			return
		}
		http.Error(w, "append ?wsdl for the service description", http.StatusNotFound)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "SOAP operations are POSTed", http.StatusMethodNotAllowed)
		return
	}
	s.serveOperation(w, r)
}

// serveOperation reads one request envelope and answers it.
func (s service) serveOperation(w http.ResponseWriter, r *http.Request) {
	if problem := s.checkTransport(r); problem != "" {
		s.writeFault(w, faultClient, problem)
		return
	}
	var env requestEnvelope
	if err := xml.NewDecoder(r.Body).Decode(&env); err != nil {
		s.writeFault(w, faultClient, "the request body is not XML: "+err.Error())
		return
	}
	if env.XMLName.Space != s.envelopeNS {
		s.writeFault(w, faultClient,
			fmt.Sprintf("envelope namespace %q, want %q for SOAP %s", env.XMLName.Space, s.envelopeNS, s.version))
		return
	}
	if len(env.Body.Operations) == 0 {
		s.writeFault(w, faultClient, "the envelope body carries no operation element")
		return
	}
	op := env.Body.Operations[0]
	if op.XMLName.Space != targetNS {
		s.writeFault(w, faultClient,
			fmt.Sprintf("operation element %q is in namespace %q, want %q", op.XMLName.Local, op.XMLName.Space, targetNS))
		return
	}
	if problem := s.checkChildNamespaces(op); problem != "" {
		s.writeFault(w, faultClient, problem)
		return
	}
	s.dispatch(w, op)
}

// checkTransport verifies what the version requires around the envelope: the
// media type, and where the action is announced.
//
// A 1.1 service that ignored a missing SOAPAction, or a 1.2 service that
// accepted the 1.1 header, would let a caller be wrong in the one way real
// upstreams are least forgiving about.
func (s service) checkTransport(r *http.Request) string {
	contentType := r.Header.Get(headerContentType)
	if !strings.HasPrefix(contentType, s.mediaType) {
		return fmt.Sprintf(headerContentType+" %q, want %s for SOAP %s", contentType, s.mediaType, s.version)
	}
	if s.version == "1.1" {
		if r.Header.Get("SOAPAction") == "" {
			return "SOAP 1.1 requires a SOAPAction header; this request sent none"
		}
		return ""
	}
	if !strings.Contains(contentType, "action=") {
		return "SOAP 1.2 announces the action as a Content-Type parameter; this request sent none"
	}
	if r.Header.Get("SOAPAction") != "" {
		return "SOAP 1.2 does not use the SOAPAction header; this request sent one"
	}
	return ""
}

// checkChildNamespaces holds the caller to the schema's elementFormDefault.
//
// This is the mistake an importer makes silently: a qualified schema whose
// children are written unqualified, or the reverse, produces a body every
// value of which is correct and which the upstream rejects entirely.
func (s service) checkChildNamespaces(op element) string {
	want := ""
	if s.qualified {
		want = targetNS
	}
	for _, child := range op.Children {
		if child.XMLName.Space != want {
			return fmt.Sprintf(
				"element %q is in namespace %q, want %q: this schema is elementFormDefault=%s",
				child.XMLName.Local, child.XMLName.Space, want, formDefault(s.qualified))
		}
	}
	return ""
}

// formDefault names a schema's element form, for the refusal text.
func formDefault(qualified bool) string {
	if qualified {
		return "qualified"
	}
	return "unqualified"
}

// dispatch answers one operation.
func (s service) dispatch(w http.ResponseWriter, op element) {
	switch op.XMLName.Local {
	case "GetOrder":
		s.handleGetOrder(w, op)
	case "ListOrders":
		s.handleListOrders(w, op)
	case "FailOrder":
		s.writeFault(w, faultServer, "FailOrder always fails: "+op.child("Reason"))
	default:
		s.writeFault(w, faultClient, "unknown operation "+op.XMLName.Local)
	}
}

// getOrder echoes back what it was sent, so a caller can prove the envelope
// carried the fields it meant to send rather than merely that a call succeeded.
func (s service) handleGetOrder(w http.ResponseWriter, op element) {
	orderID := op.child("OrderId")
	if orderID == "" {
		s.writeFault(w, faultClient, "OrderId is required")
		return
	}
	detail := op.child("Detail")
	total := "12.50"
	if detail == "true" {
		total = "12.50"
	}
	s.writeBody(w, "GetOrderResponse", ""+
		s.el("OrderId", orderID)+
		s.el("Total", total)+
		s.el("Status", "shipped")+
		s.el("Detailed", detail)+
		s.el("ReceivedAttribute", op.attr("RequestId")))
}

// listOrders answers with a repeated element, which is what proves a list in
// the caller's object became sibling elements rather than one nested value.
func (s service) handleListOrders(w http.ResponseWriter, op element) {
	customer := op.child("Customer")
	if customer == "" {
		s.writeFault(w, faultClient, "Customer is required")
		return
	}
	var body strings.Builder
	_, _ = body.WriteString(s.el("Customer", customer))
	for _, id := range []string{"A-1", "A-2", "A-3"} {
		_, _ = body.WriteString(s.el("Order", s.el("Id", id)+s.el("Total", "1.00")))
	}
	s.writeBody(w, "ListOrdersResponse", body.String())
}

// el writes one response element, in the namespace the schema declares.
func (s service) el(name, value string) string {
	if s.qualified {
		return fmt.Sprintf("<tns:%s>%s</tns:%s>", name, value, name)
	}
	return fmt.Sprintf("<%s>%s</%s>", name, value, name)
}

// writeBody writes a success envelope around one response element.
func (s service) writeBody(w http.ResponseWriter, name, inner string) {
	w.Header().Set(headerContentType, s.mediaType+"; charset=utf-8")
	_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="utf-8"?>`+
		`<soap:Envelope xmlns:soap=%q><soap:Body>`+
		`<tns:%s xmlns:tns=%q>%s</tns:%s>`+
		`</soap:Body></soap:Envelope>`,
		s.envelopeNS, name, targetNS, inner, name)
}

// writeFault answers with a soap:Fault, in the version's own spelling, at the
// HTTP 500 a SOAP fault is carried by.
func (s service) writeFault(w http.ResponseWriter, code, reason string) {
	w.Header().Set(headerContentType, s.mediaType+"; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	if s.version == "1.2" {
		_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="utf-8"?>`+
			`<soap:Envelope xmlns:soap=%q><soap:Body><soap:Fault>`+
			`<soap:Code><soap:Value>soap:%s</soap:Value></soap:Code>`+
			`<soap:Reason><soap:Text xml:lang="en">%s</soap:Text></soap:Reason>`+
			`</soap:Fault></soap:Body></soap:Envelope>`,
			s.envelopeNS, senderFor(code), xmlEscape(reason))
		return
	}
	_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="utf-8"?>`+
		`<soap:Envelope xmlns:soap=%q><soap:Body><soap:Fault>`+
		`<faultcode>soap:%s</faultcode><faultstring>%s</faultstring>`+
		`</soap:Fault></soap:Body></soap:Envelope>`,
		s.envelopeNS, code, xmlEscape(reason))
}

// senderFor maps the 1.1 fault codes onto their 1.2 names.
func senderFor(code string) string {
	if code == faultClient {
		return "Sender"
	}
	return "Receiver"
}

// xmlEscape escapes text for inclusion in a document.
func xmlEscape(s string) string {
	var b strings.Builder
	if err := xml.EscapeText(&b, []byte(s)); err != nil {
		return ""
	}
	return b.String()
}

// requestEnvelope is a SOAP request read loosely: the envelope's namespace is
// checked, and the body's first element is the operation. Body matches on its
// local name so either version's envelope decodes here.
type requestEnvelope struct {
	XMLName xml.Name
	Body    struct {
		Operations []element `xml:",any"`
	} `xml:"Body"`
}

// element is one element of the request, kept generic because the operation
// and its fields are what the request is announcing.
type element struct {
	XMLName  xml.Name
	Attrs    []xml.Attr `xml:",any,attr"`
	Text     string     `xml:",chardata"`
	Children []element  `xml:",any"`
}

// child returns the trimmed text of the first child with this local name.
func (e element) child(name string) string {
	for _, c := range e.Children {
		if c.XMLName.Local == name {
			return strings.TrimSpace(c.Text)
		}
	}
	return ""
}

// attr returns an attribute's value by local name.
func (e element) attr(name string) string {
	for _, a := range e.Attrs {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}
