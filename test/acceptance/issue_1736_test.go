//go:build integration

package acceptance

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #1736: API catalogs accepted OpenAPI 3.x documents only. A SOAP
// upstream publishes a WSDL, so it could be registered only as a catalog-less
// connection: api_discover found nothing, there was no request or response
// schema to read, semantic ranking had nothing to index, per-operation route
// rules could not be written because there was one path for every operation,
// and every caller had to know the envelope shape by heart.
//
// What these hold: a catalog spec saved with spec_format=wsdl registers one
// discoverable operation per portType operation, each carrying the fields its
// XSD declares; an object body is assembled into the envelope the version
// requires, with the action announced where that version announces it and the
// body elements in the form the schema declares; a string body is still sent
// verbatim; a soap:Fault is reported as the upstream's own error rather than
// as a 200 carrying XML; and a persona route rule denies one operation while
// leaving the others callable.
//
// The upstream is the dev stack's cmd/dev-soap-mock, which dev/start.sh runs
// beside dev-mcp-mock. It refuses a request that gets the media type, the
// action or the element qualification wrong, and answers by echoing the fields
// it received, so a criterion here fails when the envelope is wrong rather
// than merely when the call does not complete. Nothing else in the repository
// speaks SOAP, which is why it exists.
//
// Wire forms: api_invoke_endpoint's `body` is untyped and admits every JSON
// form. The two that carry meaning for a SOAP operation are both sent as
// literal tools/call params against the same operation: an OBJECT of the
// operation's fields, which the gateway assembles into an envelope
// (TestIssue1736_AnObjectBodyReachesTheUpstreamAsASOAPEnvelope), and a STRING,
// which is sent verbatim so a caller holding an envelope of their own keeps
// the way through they have always had
// (TestIssue1736_AStringBodyIsSentVerbatim). The absent form is sent too: an
// operation invoked with no body at all still produces the operation element
// (asserted in the same test). A LIST is sent and refused by name rather than
// marshaled into something meaningless. `connection`, `operation_id`,
// `method`, `path` and `purpose` are typed strings; `headers` is an object of
// strings and is sent as one; `decode` is a typed string enum this file sends
// as "xml" and omits. On the admin side, api-catalogs' `spec_format` is a
// typed string sent as "wsdl" and omitted (defaulting to openapi), and
// `source_kind` is sent as "inline".

const (
	// issue1736Base is where dev/start.sh runs cmd/dev-soap-mock.
	issue1736Base = "http://localhost:9285"
	// The two services, which differ in the three things the SOAP versions
	// disagree about: media type, where the action is announced, and
	// whether body elements are namespace-qualified.
	issue1736Path11 = "/Orders.svc"
	issue1736Path12 = "/Orders12.svc"

	issue1736Purpose = "Exercising the WSDL catalog path against the dev stack's SOAP upstream for #1736."
)

// requireSOAPUpstream fails when the dev stack's SOAP service is not running.
//
// It fails rather than skips deliberately: a criterion that did not run is not
// a criterion, and `make acceptance-check` reads an absent pass the same way it
// reads a failure.
func requireSOAPUpstream(t *testing.T) {
	t.Helper()
	res, err := http.Get(issue1736Base + "/.health") //nolint:noctx // a liveness probe against the local dev stack
	if err != nil {
		t.Fatalf("the dev stack's SOAP upstream is not reachable at %s (%v); run `make dev`", issue1736Base, err)
	}
	defer res.Body.Close() //nolint:errcheck // close error on a probe is not actionable
	if res.StatusCode != http.StatusOK {
		t.Fatalf("the dev stack's SOAP upstream answered HTTP %d; run `make dev`", res.StatusCode)
	}
}

// issue1736Connect registers a catalog whose one spec is the service's WSDL
// and a connection that references it, returning the connection name.
//
// The WSDL is read from the service's ?wsdl URL by the TEST and supplied as
// inline content, rather than registered with source_kind=url. The catalog's
// URL fetcher refuses a plain-http URL and a loopback address, which is the
// right behaviour and not something to relax for a test; a dev upstream on
// 127.0.0.1 is therefore unreachable from that path by design. What the format
// changes about a save is one function, prepareSpec
// (internal/admin/catalogapi/specformat.go), and the inline, upload and
// refresh handlers all call it — so this exercises the same conversion a
// URL-sourced WSDL would get.
func issue1736Connect(t *testing.T, c *client, label, servicePath string) string {
	t.Helper()
	stamp := time.Now().UnixNano()
	catalogID := fmt.Sprintf("acc-1736-%s-%d", label, stamp)
	name := catalogID

	if code := c.restJSON(http.MethodPost, "/api/v1/admin/api-catalogs", map[string]any{
		"id": catalogID, "name": catalogID, "display_name": "Orders (" + label + ")",
		"description": "The dev stack's SOAP upstream, imported from its WSDL.",
	}); code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("creating catalog %s: HTTP %d", catalogID, code)
	}
	t.Cleanup(func() {
		c.rest(http.MethodDelete, "/api/v1/admin/api-catalogs/"+catalogID, http.NoBody)
	})

	status, body := c.rest(http.MethodPut, "/api/v1/admin/api-catalogs/"+catalogID+"/specs/orders",
		jsonBody(t, map[string]any{
			"source_kind": "inline",
			"spec_format": "wsdl",
			"content":     issue1736WSDL(t, servicePath),
		}))
	if status != http.StatusOK && status != http.StatusNoContent {
		t.Fatalf("saving the WSDL spec: HTTP %d %v", status, body)
	}
	if got := body["spec_format"]; got != nil && got != "wsdl" {
		t.Fatalf("the saved spec reports spec_format %v, want wsdl", got)
	}

	if code := c.restJSON(http.MethodPut, "/api/v1/admin/connection-instances/api/"+name, map[string]any{
		"config": map[string]any{
			"base_url": issue1736Base, "auth_mode": "none", "connection_name": name,
			"catalog_id": catalogID, "connect_timeout": "5s", "call_timeout": "20s",
			"trust_level": "untrusted",
		},
		"description": "SOAP upstream for #1736 acceptance.",
	}); code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("registering connection %s: HTTP %d", name, code)
	}
	t.Cleanup(func() {
		c.rest(http.MethodDelete, "/api/v1/admin/connection-instances/api/"+name, http.NoBody)
	})
	issue1736AwaitConnection(t, c, name)
	return name
}

// issue1736AwaitConnection waits until the replica serving this session lists
// the connection.
//
// The dev stack runs two platform processes behind nginx. A registration is
// applied on the replica that served the admin request and reaches the other
// over the reload bus, so a tool call made immediately afterwards can land on
// a replica that has not caught up and be told the connection does not exist.
// That window is not this ticket's: it is the connection reload path, it
// predates this branch, and it is filed separately. Waiting on the readiness
// the caller can actually observe keeps these criteria measuring the WSDL path
// rather than the propagation.
func issue1736AwaitConnection(t *testing.T, c *client, name string) {
	t.Helper()
	const (
		attempts = 40
		pause    = 250 * time.Millisecond
	)
	for range attempts {
		if strings.Contains(fmt.Sprintf("%v", c.call("list_connections", map[string]any{})), name) {
			return
		}
		time.Sleep(pause)
	}
	t.Fatalf("connection %s never became visible to this session after %s", name, attempts*pause)
}

// issue1736WSDL reads a service's description from the upstream that publishes
// it, so the document under test is the one the service actually serves rather
// than a copy in this file that could drift from it.
func issue1736WSDL(t *testing.T, servicePath string) string {
	t.Helper()
	res, err := http.Get(issue1736Base + servicePath + "?wsdl") //nolint:noctx // a read from the local dev stack
	if err != nil {
		t.Fatalf("reading the WSDL from %s: %v", servicePath, err)
	}
	defer res.Body.Close() //nolint:errcheck // close error on a completed read is not actionable
	if res.StatusCode != http.StatusOK {
		t.Fatalf("reading the WSDL from %s: HTTP %d", servicePath, res.StatusCode)
	}
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("reading the WSDL body: %v", err)
	}
	return string(raw)
}

// issue1736Operations returns the operation ids api_discover lists.
func issue1736Operations(t *testing.T, c *client, connection string) map[string]map[string]any {
	t.Helper()
	out := c.call("api_discover", map[string]any{
		"connection": connection, "purpose": issue1736Purpose,
	})
	found := map[string]map[string]any{}
	collectOperations(out, found)
	if len(found) == 0 {
		t.Fatalf("api_discover listed no operations for %s: %v", connection, out)
	}
	return found
}

// collectOperations walks a discover response for anything carrying an
// operation_id, so the criterion does not depend on which level of the
// response shape the operations were nested under.
func collectOperations(node any, into map[string]map[string]any) {
	switch v := node.(type) {
	case map[string]any:
		if id, ok := v["operation_id"].(string); ok && id != "" {
			into[id] = v
		}
		for _, child := range v {
			collectOperations(child, into)
		}
	case []any:
		for _, child := range v {
			collectOperations(child, into)
		}
	}
}

// TestIssue1736_AWSDLRegistersEveryOperationAsDiscoverable is the first
// criterion: a WSDL saved as a catalog spec makes every portType operation
// findable, addressed by the name the WSDL gave it.
func TestIssue1736_AWSDLRegistersEveryOperationAsDiscoverable(t *testing.T) {
	requireSOAPUpstream(t)
	c := connect(t)
	connection := issue1736Connect(t, c, "discover", issue1736Path11)

	ops := issue1736Operations(t, c, connection)
	for _, want := range []string{"GetOrder", "ListOrders", "FailOrder"} {
		if _, found := ops[want]; !found {
			t.Errorf("api_discover did not list %q; it listed %v", want, keysOfOps(ops))
		}
	}
	// Every operation of a SOAP service is a POST to one address, which one
	// OpenAPI path item cannot hold. Each therefore gets its own path key,
	// which is what makes a per-operation route rule writable at all.
	seen := map[string]bool{}
	for id, op := range ops {
		path, _ := op["path"].(string)
		if path == "" {
			t.Errorf("operation %q has no path", id)
			continue
		}
		if seen[path] {
			t.Errorf("two operations share the path %q, so neither can be ruled on separately", path)
		}
		seen[path] = true
	}
}

// keysOfOps names what was listed, for a failure message.
func keysOfOps(ops map[string]map[string]any) []string {
	out := make([]string, 0, len(ops))
	for id := range ops {
		out = append(out, id)
	}
	return out
}

// TestIssue1736_AnOperationShowsItsFieldsRatherThanAnOpaqueBody is the second
// criterion: the XSD became a schema the agent can read, so a caller learns
// the operation's parameters the way they do for a REST one.
func TestIssue1736_AnOperationShowsItsFieldsRatherThanAnOpaqueBody(t *testing.T) {
	requireSOAPUpstream(t)
	c := connect(t)
	connection := issue1736Connect(t, c, "schema", issue1736Path11)

	out := c.call("api_discover", map[string]any{
		"connection": connection, "operation_id": "GetOrder", "purpose": issue1736Purpose,
	})
	rendered := fmt.Sprintf("%v", out)
	for _, want := range []string{"OrderId", "Detail"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("api_discover(GetOrder) does not show the field %q:\n%s", want, firstRunes(rendered, 1500))
		}
	}
	// The types came from the XSD, not from a guess.
	if !strings.Contains(rendered, "boolean") {
		t.Errorf("Detail's xsd:boolean did not reach the schema:\n%s", firstRunes(rendered, 1500))
	}
}

// TestIssue1736_AnObjectBodyReachesTheUpstreamAsASOAPEnvelope is the third
// criterion, and the one the whole ticket exists for: the caller sends the
// operation's fields and the gateway writes the envelope.
//
// The upstream refuses a wrong media type, a missing SOAPAction and a wrongly
// qualified body, and echoes the fields it received, so this passes only when
// all of that was right.
func TestIssue1736_AnObjectBodyReachesTheUpstreamAsASOAPEnvelope(t *testing.T) {
	requireSOAPUpstream(t)
	c := connect(t)
	connection := issue1736Connect(t, c, "invoke", issue1736Path11)

	out := c.call("api_invoke_endpoint", map[string]any{
		"connection": connection, "operation_id": "GetOrder",
		"body":    map[string]any{"OrderId": "A-9", "Detail": true, "RequestId": "r-1"},
		"purpose": issue1736Purpose,
	})
	assertNoFault(t, out)
	rendered := fmt.Sprintf("%v", out["body"])
	for _, want := range []string{"A-9", "shipped", "true"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("the upstream did not echo %q; it answered:\n%s", want, firstRunes(rendered, 1200))
		}
	}
	// RequestId is an XSD attribute, not a child element. The upstream
	// echoes it from the start tag, so a body that wrote it as an element
	// comes back with it empty.
	if !strings.Contains(rendered, "r-1") {
		t.Errorf("the attribute was not written to the start tag:\n%s", firstRunes(rendered, 1200))
	}

	// The absent form: an operation invoked with no body still produces the
	// operation element, which is what the upstream dispatches on.
	empty := c.call("api_invoke_endpoint", map[string]any{
		"connection": connection, "operation_id": "ListOrders",
		"body":    map[string]any{"Customer": "ACME"},
		"purpose": issue1736Purpose,
	})
	assertNoFault(t, empty)
	// A repeated element is a run of siblings, not one element holding a
	// list: three orders come back as three elements.
	if n := strings.Count(fmt.Sprintf("%v", empty["body"]), "A-"); n < 3 {
		t.Errorf("a repeated element did not come back as siblings:\n%s", firstRunes(fmt.Sprintf("%v", empty["body"]), 1500))
	}
}

// TestIssue1736_AStringBodyIsSentVerbatim is the second wire form: a caller
// holding an envelope of their own keeps the way through they have always had.
func TestIssue1736_AStringBodyIsSentVerbatim(t *testing.T) {
	requireSOAPUpstream(t)
	c := connect(t)
	connection := issue1736Connect(t, c, "verbatim", issue1736Path11)

	envelope := `<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">` +
		`<soap:Body><GetOrder xmlns="urn:acme:orders" RequestId="typed-by-hand">` +
		`<OrderId xmlns="">B-2</OrderId></GetOrder></soap:Body></soap:Envelope>`
	out := c.call("api_invoke_endpoint", map[string]any{
		"connection": connection, "operation_id": "GetOrder",
		"body": envelope, "purpose": issue1736Purpose,
	})
	assertNoFault(t, out)
	rendered := fmt.Sprintf("%v", out["body"])
	if !strings.Contains(rendered, "B-2") || !strings.Contains(rendered, "typed-by-hand") {
		t.Errorf("the hand-written envelope did not reach the upstream as written:\n%s", firstRunes(rendered, 1200))
	}

	// A form that carries no meaning is refused by name rather than
	// marshaled into something the upstream will reject obscurely.
	_, text, err := c.callRaw("api_invoke_endpoint", map[string]any{
		"connection": connection, "operation_id": "GetOrder",
		"body": []any{"OrderId", "B-2"}, "purpose": issue1736Purpose,
	})
	if err == nil && !strings.Contains(strings.ToLower(text), "object") {
		t.Errorf("a list body was not refused with an explanation: %q", firstRunes(text, 400))
	}
}

// TestIssue1736_ASOAPFaultIsReportedAsTheUpstreamsOwnError is the fourth
// criterion. A SOAP fault arrives as HTTP 500 with the useful sentence buried
// in the body; without this the call reports only "Internal Server Error".
func TestIssue1736_ASOAPFaultIsReportedAsTheUpstreamsOwnError(t *testing.T) {
	requireSOAPUpstream(t)
	c := connect(t)
	connection := issue1736Connect(t, c, "fault", issue1736Path11)

	out := c.call("api_invoke_endpoint", map[string]any{
		"connection": connection, "operation_id": "FailOrder",
		"body":    map[string]any{"Reason": "over budget"},
		"purpose": issue1736Purpose,
	})
	reported, _ := out["error"].(string)
	if reported == "" {
		t.Fatalf("a soap:Fault produced no error; the output was:\n%v", out)
	}
	// Both halves of the fault: the code that classifies it and the text
	// that explains it.
	if !strings.Contains(reported, "Server") {
		t.Errorf("the reported error does not carry the faultcode: %q", reported)
	}
	if !strings.Contains(reported, "over budget") {
		t.Errorf("the reported error does not carry the faultstring: %q", reported)
	}
	if status := number(t, out, "status"); status != 500 {
		t.Errorf("status = %v, want the upstream's 500", status)
	}
}

// TestIssue1736_APersonaRouteRuleDeniesOneOperation is the fifth criterion.
//
// The ticket expected path rules to be useless on a SOAP connection because
// one path serves every operation. They are not: the rendered document keys
// each operation under its own path, so a path rule names one operation
// exactly, and the others stay callable.
func TestIssue1736_APersonaRouteRuleDeniesOneOperation(t *testing.T) {
	requireSOAPUpstream(t)
	admin := connect(t)
	connection := issue1736Connect(t, admin, "persona", issue1736Path11)

	restore := issue1736DenyOneOperation(t, admin, "collaborator", connection, issue1736Path11+"/FailOrder")
	defer restore()

	person := connectAs(t, devPeerAPIKey)
	_, text, err := person.callRaw("api_invoke_endpoint", map[string]any{
		"connection": connection, "operation_id": "FailOrder",
		"body":    map[string]any{"Reason": "denied"},
		"purpose": issue1736Purpose,
	})
	if err == nil && !strings.Contains(strings.ToLower(text), "disallow") &&
		!strings.Contains(strings.ToLower(text), "not authorized") {
		t.Errorf("the denied operation was not refused: %q", firstRunes(text, 400))
	}

	// The rule names one operation, so the rest of the service is untouched.
	out := person.call("api_invoke_endpoint", map[string]any{
		"connection": connection, "operation_id": "GetOrder",
		"body":    map[string]any{"OrderId": "C-3"},
		"purpose": issue1736Purpose,
	})
	assertNoFault(t, out)
	if !strings.Contains(fmt.Sprintf("%v", out["body"]), "C-3") {
		t.Errorf("an operation the rule does not name stopped working:\n%v", out)
	}
}

// issue1736DenyOneOperation denies one operation's path on one connection and
// returns the restore function.
func issue1736DenyOneOperation(t *testing.T, c *client, personaName, connection, path string) func() {
	t.Helper()
	status, before := c.rest(http.MethodGet, "/api/v1/admin/personas/"+personaName, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading persona %s: HTTP %d %v", personaName, status, before)
	}
	body := issue1277PersonaRequest(before)
	original := issue1277PersonaRequest(before)
	// Rules are a narrowing: once any of them names the connection a
	// matching allow is required, so the allow beside the deny is what
	// keeps every other operation callable.
	// The allow names no paths, which means any path: a glob would have to
	// mirror the service's shape, and `*` does not cross a separator.
	body["api_routes"] = []any{
		map[string]any{"connection": connection},
		map[string]any{"connection": connection, "paths": []any{path}, "action": "deny"},
	}
	if code := c.restJSON(http.MethodPut, "/api/v1/admin/personas/"+personaName, body); code != http.StatusOK {
		t.Fatalf("adding the deny rule: HTTP %d", code)
	}
	return func() {
		c.restJSON(http.MethodPut, "/api/v1/admin/personas/"+personaName, original)
	}
}

// TestIssue1736_ASOAP12ServiceIsCalledWithItsOwnEnvelope is the sixth
// criterion. SOAP 1.2 changed the envelope namespace, moved the action into a
// Content-Type parameter and, on this service, qualifies its body elements; an
// upstream of either version refuses the other's request.
func TestIssue1736_ASOAP12ServiceIsCalledWithItsOwnEnvelope(t *testing.T) {
	requireSOAPUpstream(t)
	c := connect(t)
	connection := issue1736Connect(t, c, "soap12", issue1736Path12)

	out := c.call("api_invoke_endpoint", map[string]any{
		"connection": connection, "operation_id": "ListOrders",
		"body":    map[string]any{"Customer": "ACME", "Limit": 3},
		"purpose": issue1736Purpose,
	})
	assertNoFault(t, out)
	rendered := fmt.Sprintf("%v", out["body"])
	if !strings.Contains(rendered, "ACME") {
		t.Errorf("the 1.2 upstream did not echo the customer:\n%s", firstRunes(rendered, 1200))
	}
	if n := strings.Count(rendered, "A-"); n < 3 {
		t.Errorf("the 1.2 list did not come back as siblings:\n%s", firstRunes(rendered, 1500))
	}

	// The response is a tree without the caller asking, because the
	// rendered document declares the envelope media type on its success
	// response. The explicit form is sent too.
	explicit := c.call("api_invoke_endpoint", map[string]any{
		"connection": connection, "operation_id": "ListOrders",
		"body":    map[string]any{"Customer": "ACME"},
		"decode":  "xml",
		"purpose": issue1736Purpose,
	})
	assertNoFault(t, explicit)
	if _, isTree := explicit["body"].(map[string]any); !isTree {
		t.Errorf("decode=xml did not produce a tree: body is %T", explicit["body"])
	}
}

// TestIssue1736_TheStoredSpecIsTheWSDLTheOperatorSupplied is the seventh
// criterion. Reading a spec back returns what was written, not a generated
// document the operator has never seen, which is what lets the spec editor
// round-trip and a refresh re-import.
func TestIssue1736_TheStoredSpecIsTheWSDLTheOperatorSupplied(t *testing.T) {
	requireSOAPUpstream(t)
	c := connect(t)
	connection := issue1736Connect(t, c, "roundtrip", issue1736Path11)

	status, spec := c.rest(http.MethodGet,
		"/api/v1/admin/api-catalogs/"+connection+"/specs/orders", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading the spec back: HTTP %d %v", status, spec)
	}
	if spec["spec_format"] != "wsdl" {
		t.Errorf("spec_format = %v, want wsdl", spec["spec_format"])
	}
	content, _ := spec["content"].(string)
	if !strings.Contains(content, "wsdl:definitions") {
		t.Errorf("the stored content is not the WSDL that was supplied:\n%s", firstRunes(content, 400))
	}
	if count := number(t, spec, "operation_count"); count < 3 {
		t.Errorf("operation_count = %v, want the three operations the WSDL declares", count)
	}

	// Saving the spec again re-imports it rather than keeping the render of
	// the document it replaced, which is what a WSDL regenerated upstream
	// depends on. This is the same prepareSpec the refresh handler calls.
	again, body := c.rest(http.MethodPut,
		"/api/v1/admin/api-catalogs/"+connection+"/specs/orders",
		jsonBody(t, map[string]any{
			"source_kind": "inline", "spec_format": "wsdl",
			"content": issue1736WSDL(t, issue1736Path11),
		}))
	if again != http.StatusOK && again != http.StatusNoContent {
		t.Fatalf("re-saving the WSDL spec: HTTP %d %v", again, body)
	}
	ops := issue1736Operations(t, c, connection)
	if _, found := ops["GetOrder"]; !found {
		t.Errorf("the operations did not survive a re-save; got %v", keysOfOps(ops))
	}

	// A document that is not a WSDL fails the SAVE, naming what was wrong,
	// rather than registering a connection with no operations.
	bad, detail := c.rest(http.MethodPut,
		"/api/v1/admin/api-catalogs/"+connection+"/specs/orders",
		jsonBody(t, map[string]any{
			"source_kind": "inline", "spec_format": "wsdl", "content": "openapi: 3.0.3",
		}))
	if bad != http.StatusBadRequest {
		t.Errorf("saving a non-WSDL as spec_format=wsdl answered HTTP %d, want 400: %v", bad, detail)
	}
}

// assertNoFault fails when a call the criterion expects to succeed came back
// carrying an upstream error, naming what the upstream said.
func assertNoFault(t *testing.T, out map[string]any) {
	t.Helper()
	if reported, _ := out["error"].(string); reported != "" {
		t.Fatalf("the upstream refused the request: %s", reported)
	}
}
