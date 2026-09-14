//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #1735: a managed script's globals were platform, json, date, run and
// sum. A script that called an upstream answering in XML -- a SOAP service, a
// WebDAV PROPFIND, an RSS or Atom feed -- got a string it could not take
// apart, and api_invoke_endpoint returned any response whose content type did
// not contain "json" as that same raw string.
//
// What these hold: the `xml` module is in the environment a script runs in and
// the one `validate` resolves against; it decodes a SOAP envelope by local
// name, searches it with the documented path subset, refuses a path outside
// that subset rather than answering it with no matches, refuses a DOCTYPE, and
// round-trips a document through encode. On the tool side,
// api_invoke_endpoint returns a parsed tree when the caller passes
// decode="xml", and when the connection's catalog declares an XML media type
// on the operation's success response with no decode argument at all -- while
// the same connection with no catalog and no argument returns the string it
// always did.
//
// The XML upstream is the dev stack's Keycloak, whose SAML descriptor route is
// a real 200 text/xml document the deployments already serve. It is not a
// fixture written for this test.
//
// Wire forms: api_invoke_endpoint's `decode` is typed string with an enum, so
// it admits one JSON form, and every value the enum names is sent as a literal
// tools/call param (TestIssue1735_DecodeXMLThroughTheToolSurface), along with
// the absent form the default covers and one value outside the enum. Its
// `connection`, `method` and `path` are strings. manage_script's `command`,
// `name`, `description` and `source` are strings, and `params` is an object
// this file does not use.

// issue1735SOAP is the document every script criterion reads. Its prefixes are
// the sender's choice, which is the whole reason the module matches on local
// names.
// issue1735Purpose is the sentence the platform requires on a data-access
// call, stated once because every criterion here serves the same task.
const issue1735Purpose = "Checking how the platform hands back an upstream answer that is XML rather than JSON."

const issue1735SOAP = `<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/">` +
	`<soapenv:Body><GetRatesResponse xmlns="urn:acme:rates">` +
	`<Rate currency="EUR">0.92</Rate><Rate currency="GBP">0.79</Rate><Rate currency="JPY">147.10</Rate>` +
	`</GetRatesResponse></soapenv:Body></soapenv:Envelope>`

// issue1735KeycloakBase is the dev stack's Keycloak, and issue1735SAMLPath its
// realm's SAML descriptor: a 200 application/xml document. The port is the one
// dev/docker-compose.yml pins for it, which start.sh's relocation offset does
// not move.
const (
	issue1735KeycloakBase = "http://localhost:9090"
	issue1735SAMLPath     = "/realms/mcp-platform/protocol/saml/descriptor"
)

// issue1735SAMLSpec declares that descriptor route as an operation answering
// text/xml, which is what auto mode keys on.
const issue1735SAMLSpec = `
openapi: 3.0.3
info:
  title: Realm metadata
  version: "1.0"
paths:
  /realms/mcp-platform/protocol/saml/descriptor:
    get:
      operationId: samlDescriptor
      responses:
        "200":
          description: SAML entity descriptor
          content:
            application/xml:
              schema:
                type: string
`

// issue1735Author saves a script under a fresh name and removes it after.
func issue1735Author(t *testing.T, c *client, label, source string) string {
	t.Helper()
	name := fmt.Sprintf("acceptance-1735-%s", label)
	_, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name})
	c.call("manage_script", map[string]any{
		"command":     "create",
		"name":        name,
		"description": "Acceptance #1735: reading XML in a managed script.",
		"source":      source,
	})
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name})
	})
	return name
}

// issue1735Draft executes a script as a draft and returns the tool's answer. A
// script that fails is a failed run reported normally, not a tool error, which
// is what the refusal criteria read.
func issue1735Draft(t *testing.T, c *client, name string) map[string]any {
	t.Helper()
	return c.call("manage_script", map[string]any{"command": "run_draft", "name": name})
}

// issue1735Log joins a run's log lines so a criterion asserts on what the
// script printed.
func issue1735Log(t *testing.T, ran map[string]any) string {
	t.Helper()
	var b strings.Builder
	lines, _ := ran["log"].([]any)
	for _, line := range lines {
		b.WriteString(fmt.Sprint(line))
		b.WriteString("\n")
	}
	if b.Len() == 0 {
		b.WriteString(fmt.Sprint(ran))
	}
	return b.String()
}

// TestIssue1735_TheXMLModuleIsInTheEnvironmentValidateResolvesAgainst is the
// #1414 parity half: a name the contract advertises has to be one the
// validator accepts and the run can bind.
func TestIssue1735_TheXMLModuleIsInTheEnvironmentValidateResolvesAgainst(t *testing.T) {
	c := connect(t)

	report := c.call("manage_script", map[string]any{
		"command": "validate",
		"source":  "doc = xml.decode(\"<a/>\")\nprint(xml.encode(doc))\nprint(xml.find(doc, \"//a\"))\nprint(xml.findall(doc, \"a\"))\n",
	})
	if ok, _ := report["ok"].(bool); !ok {
		t.Fatalf("validate refused a script using the xml module: %v", report)
	}

	help := c.call("manage_script", map[string]any{"command": "help"})
	contract := fmt.Sprint(help)
	for _, member := range []string{"xml.decode", "xml.find", "xml.findall", "xml.encode"} {
		if !strings.Contains(contract, member) {
			t.Errorf("the dialect contract never mentions %s", member)
		}
	}
}

// TestIssue1735_AScriptReadsASOAPEnvelopeByLocalName is the ticket's headline:
// a script takes a namespaced envelope apart without knowing the prefixes.
func TestIssue1735_AScriptReadsASOAPEnvelopeByLocalName(t *testing.T) {
	c := connect(t)
	name := issue1735Author(t, c, "read", `
DOC = "`+strings.ReplaceAll(issue1735SOAP, `"`, `\"`)+`"

doc = xml.decode(DOC)
print("root=" + doc.tag + " ns=" + doc.ns)

body = xml.find(doc, "//Body")
print("body=" + body.tag)

rates = xml.findall(doc, "//Rate")
print("count=" + str(len(rates)))
print("first=" + rates[0].attrs["currency"] + ":" + rates[0].text)

second = xml.find(doc, "//Rate[2]")
print("second=" + second.attrs["currency"])

gbp = xml.find(doc, "//Rate[@currency='GBP']")
print("gbp=" + gbp.text)

print("wildcard=" + xml.find(doc, "*/GetRatesResponse").ns)
print("missing=" + str(xml.find(doc, "//Fault")))
print("total=" + str(sum([float(r.text) for r in rates])))
`)

	ran := issue1735Draft(t, c, name)
	if status, _ := ran["status"].(string); status != "succeeded" {
		t.Fatalf("run status = %v, want succeeded: %v", ran["status"], ran)
	}
	log := issue1735Log(t, ran)
	for _, want := range []string{
		"root=Envelope ns=http://schemas.xmlsoap.org/soap/envelope/",
		"body=Body",
		"count=3",
		"first=EUR:0.92",
		"second=GBP",
		"gbp=0.79",
		"wildcard=urn:acme:rates",
		"missing=None",
		"total=148.81",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("run log is missing %q:\n%s", want, log)
		}
	}
}

// TestIssue1735_AScriptBuildsAndRoundTripsADocument covers the encode half:
// a request body assembled from data, and a decoded document that survives a
// trip through the encoder.
func TestIssue1735_AScriptBuildsAndRoundTripsADocument(t *testing.T) {
	c := connect(t)
	name := issue1735Author(t, c, "encode", `
DOC = "`+strings.ReplaceAll(issue1735SOAP, `"`, `\"`)+`"
SOAP_NS = "http://schemas.xmlsoap.org/soap/envelope/"

envelope = xml.encode({
    "tag": "Envelope",
    "ns": SOAP_NS,
    "children": [{
        "tag": "Body",
        "ns": SOAP_NS,
        "children": [{"tag": "GetRates", "ns": "urn:acme:rates", "attrs": {"base": "USD"}}],
    }],
})
print("built=" + envelope)

doc = xml.decode(DOC)
print("roundtrip=" + str(xml.encode(xml.decode(xml.encode(doc))) == xml.encode(doc)))
print("json=" + json.encode(xml.find(doc, "//Rate")))
`)

	ran := issue1735Draft(t, c, name)
	if status, _ := ran["status"].(string); status != "succeeded" {
		t.Fatalf("run status = %v, want succeeded: %v", ran["status"], ran)
	}
	log := issue1735Log(t, ran)
	for _, want := range []string{
		`built=<Envelope xmlns="http://schemas.xmlsoap.org/soap/envelope/"><Body><GetRates xmlns="urn:acme:rates" base="USD"/></Body></Envelope>`,
		"roundtrip=True",
		`"tag":"Rate"`,
		`"currency":"EUR"`,
		`"text":"0.92"`,
	} {
		if !strings.Contains(log, want) {
			t.Errorf("run log is missing %q:\n%s", want, log)
		}
	}
}

// TestIssue1735_AnUnsupportedPathAndAHostileDocumentAreRefused holds the two
// refusals the module is judged on: a path outside the subset fails the run
// where it was written rather than matching nothing, and a document type
// declaration is not read at all.
func TestIssue1735_AnUnsupportedPathAndAHostileDocumentAreRefused(t *testing.T) {
	cases := []struct {
		label  string
		source string
		want   string
	}{
		{
			label:  "path",
			source: "print(xml.findall(xml.decode(\"<a><b/></a>\"), \"//b[last()]\"))\n",
			want:   "unsupported path",
		},
		{
			label:  "doctype",
			source: "print(xml.decode('<!DOCTYPE lolz [<!ENTITY lol \"lol\">]><lolz>&lol;</lolz>'))\n",
			want:   "document type declarations are not accepted",
		},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			c := connect(t)
			name := issue1735Author(t, c, "refuse-"+tc.label, tc.source)
			ran := issue1735Draft(t, c, name)
			if status, _ := ran["status"].(string); status != "failed" {
				t.Fatalf("run status = %v, want failed: %v", ran["status"], ran)
			}
			if !strings.Contains(fmt.Sprint(ran), tc.want) {
				t.Fatalf("the failure never says %q: %v", tc.want, ran)
			}
		})
	}
}

// issue1735Connection registers an api connection over Keycloak, optionally
// with a catalog declaring the descriptor route as answering text/xml.
func issue1735Connection(t *testing.T, c *client, label string, withCatalog bool) string {
	t.Helper()
	stamp := time.Now().UnixNano()
	name := fmt.Sprintf("acc-1735-%s-%d", label, stamp)
	cfg := map[string]any{
		"base_url": issue1735KeycloakBase, "auth_mode": "none", "connection_name": name,
		"connect_timeout": "5s", "call_timeout": "15s", "trust_level": "untrusted",
	}
	if withCatalog {
		catalogID := fmt.Sprintf("acc-1735-%s-%d", label, stamp)
		if status := c.restJSON(http.MethodPost, "/api/v1/admin/api-catalogs", map[string]any{
			"id": catalogID, "name": catalogID, "display_name": "Acceptance 1735",
			"description": "The realm's SAML descriptor, declared as answering text/xml.",
		}); status != http.StatusCreated && status != http.StatusOK {
			t.Fatalf("create catalog: HTTP %d", status)
		}
		t.Cleanup(func() { c.rest(http.MethodDelete, "/api/v1/admin/api-catalogs/"+catalogID, http.NoBody) })
		if status := c.restJSON(http.MethodPut,
			"/api/v1/admin/api-catalogs/"+catalogID+"/specs/saml", map[string]any{
				"source_kind": "inline", "content": issue1735SAMLSpec,
			}); status != http.StatusCreated && status != http.StatusOK && status != http.StatusNoContent {
			t.Fatalf("upsert spec: HTTP %d", status)
		}
		cfg["catalog_id"] = catalogID
	}
	if status := c.restJSON(http.MethodPut, "/api/v1/admin/connection-instances/api/"+name, map[string]any{
		"config": cfg, "description": "Acceptance 1735: an upstream that answers in XML.",
	}); status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("register connection: HTTP %d", status)
	}
	t.Cleanup(func() {
		c.rest(http.MethodDelete, "/api/v1/admin/connection-instances/api/"+name, http.NoBody)
	})
	return name
}

// issue1735Tree reads the XML tree off an api_invoke_endpoint result, failing
// when the body came back as the string it used to be.
func issue1735Tree(t *testing.T, out map[string]any) map[string]any {
	t.Helper()
	body, ok := out["body"].(map[string]any)
	if !ok {
		t.Fatalf("body is %T, want a decoded XML tree: %v", out["body"], out["body"])
	}
	return body
}

// TestIssue1735_DecodeXMLThroughTheToolSurface sends every value the `decode`
// enum admits, and the absent form the default covers, against a real XML
// upstream on a connection with no catalog.
func TestIssue1735_DecodeXMLThroughTheToolSurface(t *testing.T) {
	c := connect(t)
	connection := issue1735Connection(t, c, "bare", false)
	base := map[string]any{
		"connection": connection, "method": "GET", "path": issue1735SAMLPath,
		"purpose": issue1735Purpose,
	}

	call := func(decode string) map[string]any {
		args := map[string]any{}
		for k, v := range base {
			args[k] = v
		}
		if decode != "" {
			args["decode"] = decode
		}
		return c.call("api_invoke_endpoint", args)
	}

	t.Run("decode omitted keeps the string body", func(t *testing.T) {
		out := call("")
		body, ok := out["body"].(string)
		if !ok {
			t.Fatalf("body is %T, want the string a catalog-less connection always returned", out["body"])
		}
		if !strings.Contains(body, "EntityDescriptor") {
			t.Fatalf("body is not the descriptor document: %q", firstRunes(body, 200))
		}
	})

	t.Run("decode=xml returns a tree", func(t *testing.T) {
		tree := issue1735Tree(t, call("xml"))
		if tag, _ := tree["tag"].(string); tag != "EntityDescriptor" {
			t.Fatalf("tag = %v, want EntityDescriptor: %v", tree["tag"], tree)
		}
		if ns, _ := tree["ns"].(string); ns != "urn:oasis:names:tc:SAML:2.0:metadata" {
			t.Errorf("ns = %v, want the SAML metadata namespace", tree["ns"])
		}
		attrs, _ := tree["attrs"].(map[string]any)
		if _, ok := attrs["entityID"]; !ok {
			t.Errorf("attrs has no entityID: %v", attrs)
		}
		children, _ := tree["children"].([]any)
		if len(children) == 0 {
			t.Errorf("children is empty; the descriptor has an IDPSSODescriptor")
		}
	})

	t.Run("decode=auto is the same as omitting it", func(t *testing.T) {
		if _, ok := call("auto")["body"].(string); !ok {
			t.Fatal("auto changed the body shape on a connection with no catalog")
		}
	})

	t.Run("decode=text returns the string", func(t *testing.T) {
		if _, ok := call("text")["body"].(string); !ok {
			t.Fatal("text did not return a string")
		}
	})

	t.Run("decode=json keeps the unparseable body and says why", func(t *testing.T) {
		out := call("json")
		if _, ok := out["body"].(string); !ok {
			t.Fatalf("body is %T, want the raw text a failed JSON parse falls back to", out["body"])
		}
	})

	t.Run("a decode outside the enum is refused", func(t *testing.T) {
		res, text, err := c.callRaw("api_invoke_endpoint", map[string]any{
			"connection": connection, "method": "GET", "path": issue1735SAMLPath,
			"decode": "tree", "purpose": issue1735Purpose,
		})
		if err != nil {
			t.Fatalf("transport error: %v", err)
		}
		if !res.IsError {
			t.Fatalf("an unknown decode mode was accepted: %s", text)
		}
		for _, mode := range []string{"auto", "json", "xml", "text"} {
			if !strings.Contains(text, mode) {
				t.Errorf("the refusal does not name %q, one of the modes that exist: %s", mode, text)
			}
		}
	})
}

// TestIssue1735_ACatalogDeclaredXMLResponseDecodesWithNoArgument is the other
// half of auto mode: the operator declares the media type once and every
// caller of that operation gets a tree.
func TestIssue1735_ACatalogDeclaredXMLResponseDecodesWithNoArgument(t *testing.T) {
	c := connect(t)
	connection := issue1735Connection(t, c, "catalog", true)

	tree := issue1735Tree(t, c.call("api_invoke_endpoint", map[string]any{
		"connection": connection, "operation_id": "samlDescriptor", "purpose": issue1735Purpose,
	}))
	if tag, _ := tree["tag"].(string); tag != "EntityDescriptor" {
		t.Fatalf("tag = %v, want EntityDescriptor: %v", tree["tag"], tree)
	}

	// The escape hatch: a caller that wants the document itself.
	raw := c.call("api_invoke_endpoint", map[string]any{
		"connection": connection, "operation_id": "samlDescriptor", "decode": "text",
		"purpose": issue1735Purpose,
	})
	if _, ok := raw["body"].(string); !ok {
		t.Fatalf("decode=text did not overrule the catalog: body is %T", raw["body"])
	}
}

// TestIssue1735_DecodeIsRefusedWithPaginate states the one combination that
// has no meaning, rather than accepting the argument and ignoring it.
func TestIssue1735_DecodeIsRefusedWithPaginate(t *testing.T) {
	c := connect(t)
	connection := issue1735Connection(t, c, "walk", false)

	res, text, err := c.callRaw("api_invoke_endpoint", map[string]any{
		"connection": connection, "method": "GET", "path": issue1735SAMLPath,
		"decode": "xml", "paginate": map[string]any{"items": "data", "max_pages": 1},
		"purpose": issue1735Purpose,
	})
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("decode with paginate was accepted: %s", text)
	}
	if !strings.Contains(text, "decode is not available with paginate") {
		t.Errorf("the refusal does not say why: %s", text)
	}
}

// TestIssue1735_AScriptReadsAToolCallsDecodedBody closes the loop the ticket
// describes: the gateway decodes, and the script reads the tree it was handed
// without parsing anything itself.
func TestIssue1735_AScriptReadsAToolCallsDecodedBody(t *testing.T) {
	c := connect(t)
	connection := issue1735Connection(t, c, "script", false)

	name := issue1735Author(t, c, "call", `
resp = platform.call("api_invoke_endpoint", {
    "connection": "`+connection+`",
    "method": "GET",
    "path": "`+issue1735SAMLPath+`",
    "decode": "xml",
    "purpose": "`+issue1735Purpose+`",
})
doc = resp["body"]
print("tag=" + doc["tag"])
print("decoded_by_the_tool=" + str(type(doc) == "dict"))

again = xml.decode(platform.call("api_invoke_endpoint", {
    "connection": "`+connection+`",
    "method": "GET",
    "path": "`+issue1735SAMLPath+`",
    "purpose": "`+issue1735Purpose+`",
})["body"])
print("same=" + str(again.tag == doc["tag"]))
`)

	ran := issue1735Draft(t, c, name)
	if status, _ := ran["status"].(string); status != "succeeded" {
		t.Fatalf("run status = %v, want succeeded: %v", ran["status"], ran)
	}
	log := issue1735Log(t, ran)
	for _, want := range []string{"tag=EntityDescriptor", "decoded_by_the_tool=True", "same=True"} {
		if !strings.Contains(log, want) {
			t.Errorf("run log is missing %q:\n%s", want, log)
		}
	}
}

// firstRunes trims a body for a failure message.
func firstRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
