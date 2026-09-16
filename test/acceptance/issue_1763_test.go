//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #1763: `decode: "json"` on a body that is not JSON returned the raw
// text, status 200, and no `hint` field at all. The caller asked for a JSON
// reading, did not get one, and was told nothing -- while the documented
// contract, and `decode: "xml"`, say the reason comes back in `hint`.
//
// What these hold, through api_invoke_endpoint against a real XML upstream:
// a forced JSON read of an XML document returns the document as text and names
// the reason in `hint`; a forced XML read still does the same, which is the
// behavior the contract was written from; `decode: "text"` parses nothing and
// says nothing; and `auto`, which nobody asked to parse, keeps its silence.
//
// The XML upstream is the dev stack's Keycloak, whose realm SAML descriptor is
// a real 200 application/xml document the deployments already serve. It is not
// a fixture written for this test.
//
// Wire forms: api_invoke_endpoint's `decode` is typed string with an enum, so
// it admits exactly one JSON form, and each value a criterion sends is sent as
// a literal tools/call parameter of that form, along with the absent form the
// default covers. `connection`, `method`, `path` and `purpose` are typed
// strings likewise. The connection is registered over REST with a JSON object
// body whose `config` is an object and whose `description` is a string.

const (
	// issue1763KeycloakBase is the dev stack's Keycloak and
	// issue1763SAMLPath its realm's SAML descriptor: a 200 XML document.
	// The port is the one dev/docker-compose.yml pins for it, which
	// start.sh's relocation offset does not move.
	issue1763KeycloakBase = "http://localhost:9090"
	issue1763SAMLPath     = "/realms/mcp-platform/protocol/saml/descriptor"

	// issue1763Purpose is the sentence the platform requires on a
	// data-access call, stated once because every criterion here serves the
	// same task.
	issue1763Purpose = "Checking what the gateway says when the decode a caller asked for cannot read the answer."
)

// issue1763Connection registers an api connection with no catalog against that
// upstream, so nothing but the caller's `decode` decides how a body is read.
func issue1763Connection(t *testing.T, c *client) string {
	t.Helper()
	name := fmt.Sprintf("acc-1763-%d", time.Now().UnixNano())
	if status := c.restJSON(http.MethodPut, "/api/v1/admin/connection-instances/api/"+name, map[string]any{
		"config": map[string]any{
			"base_url": issue1763KeycloakBase, "auth_mode": "none", "connection_name": name,
			"connect_timeout": "5s", "call_timeout": "15s", "trust_level": "untrusted",
		},
		"description": "Acceptance 1763: an upstream that answers in XML.",
	}); status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("register connection: HTTP %d", status)
	}
	t.Cleanup(func() {
		c.rest(http.MethodDelete, "/api/v1/admin/connection-instances/api/"+name, http.NoBody)
	})
	return name
}

// issue1763Invoke calls the descriptor route with the decode mode given, or
// with none at all when mode is empty.
func issue1763Invoke(t *testing.T, c *client, connection, mode string) map[string]any {
	t.Helper()
	args := map[string]any{
		"connection": connection, "method": "GET", "path": issue1763SAMLPath,
		"purpose": issue1763Purpose,
	}
	if mode != "" {
		args["decode"] = mode
	}
	return c.call("api_invoke_endpoint", args)
}

// issue1763TextBody reads the raw document off a result, failing when the body
// came back as anything but the string a failed parse falls back to.
func issue1763TextBody(t *testing.T, out map[string]any) string {
	t.Helper()
	body, ok := out["body"].(string)
	if !ok {
		t.Fatalf("body is %T, want the text a failed decode falls back to: %v", out["body"], out["body"])
	}
	if !strings.Contains(body, "EntityDescriptor") {
		t.Fatalf("body is not the descriptor document: %q", firstRunes(body, 200))
	}
	return body
}

// TestIssue1763_ForcedJSONSaysWhyTheBodyIsText is the ticket: the caller asked
// for a JSON reading of a document that is not JSON, and is owed the reason
// beside the body they were handed instead.
func TestIssue1763_ForcedJSONSaysWhyTheBodyIsText(t *testing.T) {
	c := connect(t)
	connection := issue1763Connection(t, c)

	out := issue1763Invoke(t, c, connection, "json")
	issue1763TextBody(t, out)

	hint, _ := out["hint"].(string)
	if hint == "" {
		t.Fatal("decode=json on an XML document carried no hint; the caller asked for JSON and was told nothing")
	}
	if !strings.Contains(hint, "Could not read the response as JSON") {
		t.Errorf("the hint does not name the failed reading: %q", hint)
	}
	if !strings.Contains(hint, "returned as text instead") {
		t.Errorf("the hint does not say where the body went: %q", hint)
	}
	if status, _ := out["status"].(float64); status != http.StatusOK {
		t.Errorf("status = %v; the upstream answered 200 and the decode is not an HTTP failure", out["status"])
	}
}

// TestIssue1763_ForcedXMLStillSaysWhy holds the behavior the contract was
// written from, which is the one this ticket makes `json` match.
func TestIssue1763_ForcedXMLStillSaysWhy(t *testing.T) {
	c := connect(t)
	connection := issue1763Connection(t, c)

	// A route that answers a document XML cannot read: Keycloak's OIDC
	// discovery document, which is JSON.
	out := c.call("api_invoke_endpoint", map[string]any{
		"connection": connection, "method": "GET",
		"path":    "/realms/mcp-platform/.well-known/openid-configuration",
		"decode":  "xml",
		"purpose": issue1763Purpose,
	})
	if _, ok := out["body"].(string); !ok {
		t.Fatalf("body is %T, want the text a failed XML decode falls back to", out["body"])
	}
	hint, _ := out["hint"].(string)
	if !strings.Contains(hint, "Could not read the response as XML") {
		t.Errorf("the forced XML read stopped naming its reason: %q", hint)
	}
}

// TestIssue1763_TheModesThatParseNothingSayNothing holds the other half: the
// hint belongs to a decode that was asked for and failed. `text` parses
// nothing, and `auto` was asked to parse nothing, so neither carries one.
func TestIssue1763_TheModesThatParseNothingSayNothing(t *testing.T) {
	c := connect(t)
	connection := issue1763Connection(t, c)

	for _, mode := range []string{"", "auto", "text"} {
		label := mode
		if label == "" {
			label = "omitted"
		}
		t.Run(label, func(t *testing.T) {
			out := issue1763Invoke(t, c, connection, mode)
			issue1763TextBody(t, out)
			if hint, _ := out["hint"].(string); hint != "" {
				t.Errorf("decode %s carried a hint: %q", label, hint)
			}
		})
	}
}

// TestIssue1763_AJSONBodyReadAsJSONCarriesNoHint keeps the note on the
// failure: the ordinary forced-JSON call, where the body is JSON, says nothing
// at all.
func TestIssue1763_AJSONBodyReadAsJSONCarriesNoHint(t *testing.T) {
	c := connect(t)
	connection := issue1763Connection(t, c)

	out := c.call("api_invoke_endpoint", map[string]any{
		"connection": connection, "method": "GET",
		"path":    "/realms/mcp-platform/.well-known/openid-configuration",
		"decode":  "json",
		"purpose": issue1763Purpose,
	})
	body, ok := out["body"].(map[string]any)
	if !ok {
		t.Fatalf("body is %T, want the parsed JSON document", out["body"])
	}
	if _, has := body["issuer"]; !has {
		t.Errorf("the parsed document is not the discovery document: %v", body)
	}
	if hint, _ := out["hint"].(string); hint != "" {
		t.Errorf("a JSON body read as JSON carried a hint: %q", hint)
	}
}
