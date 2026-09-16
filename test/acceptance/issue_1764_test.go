//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

// Issue #1764: GET /api/v1/apis reported every api connection's base URL as
// its description, including connections whose operator wrote a sentence
// months ago. The description lives in the connection store's own column,
// which the toolkit never sees; the route answered what the toolkit derived
// from the configuration map, and that was the upstream root. #1757 fixed the
// same thing on list_connections and named this route as a reader of the same
// view, and the route kept answering the old value.
//
// What these hold, through the route a non-MCP client is sent to by the served
// OpenAPI reference: a connection reports the description its operator wrote,
// on the listing and on the per-connection operations route; the upstream root
// is reported beside it in base_url and nowhere else; list_connections and the
// route agree, since one inventory answers both; and a connection nobody
// described carries no description rather than a URL in its place.
//
// Wire forms: the admin registration body is a JSON object whose `config` is
// an object and whose `description` is a string, each admitting one form, and
// it is sent as literal request bytes. The two read routes take no body and no
// parameters. list_connections' `session_id` is a string the harness attaches.

// issue1764Description is the sentence an operator wrote, which is the whole
// subject: it is not derivable from anything the toolkit parsed.
const issue1764Description = "Acceptance 1764: the sentence an operator wrote, which is not the upstream root."

// issue1764Register saves an api connection with the description given (or
// none), and returns its name.
func issue1764Register(t *testing.T, c *client, label, description string) string {
	t.Helper()
	name := fmt.Sprintf("acc-1764-%s-%d", label, time.Now().UnixNano())
	body := map[string]any{
		"config": map[string]any{
			"base_url": issue1764BaseURL, "auth_mode": "none", "connection_name": name,
			"connect_timeout": "5s", "call_timeout": "15s", "trust_level": "untrusted",
		},
		"description": description,
	}
	if status := c.restJSON(http.MethodPut, "/api/v1/admin/connection-instances/api/"+name, body); status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("register connection: HTTP %d", status)
	}
	t.Cleanup(func() {
		c.rest(http.MethodDelete, "/api/v1/admin/connection-instances/api/"+name, http.NoBody)
	})
	return name
}

// issue1764BaseURL is the upstream the registered connections point at: the
// dev stack's Keycloak, which is up whenever the stack is. Nothing here calls
// it -- the criteria are about how a connection is described, not about what
// it answers.
const issue1764BaseURL = "http://localhost:9090"

// issue1764Listed finds one connection in GET /api/v1/apis.
func issue1764Listed(t *testing.T, c *client, name string) map[string]any {
	t.Helper()
	status, out := c.rest(http.MethodGet, "/api/v1/apis", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET /api/v1/apis: HTTP %d", status)
	}
	conns, _ := out["connections"].([]any)
	for _, entry := range conns {
		conn, _ := entry.(map[string]any)
		if n, _ := conn["name"].(string); n == name {
			return conn
		}
	}
	t.Fatalf("GET /api/v1/apis does not list %q: %v", name, out)
	return nil
}

// TestIssue1764_TheListingReportsTheOperatorsDescription is the ticket.
func TestIssue1764_TheListingReportsTheOperatorsDescription(t *testing.T) {
	c := connect(t)
	name := issue1764Register(t, c, "described", issue1764Description)

	conn := issue1764Listed(t, c, name)
	if got, _ := conn["description"].(string); got != issue1764Description {
		t.Fatalf("description = %q; want the sentence the operator wrote", got)
	}
	if got, _ := conn["base_url"].(string); got != issue1764BaseURL {
		t.Errorf("base_url = %q; the upstream root is still reported, in its own field", got)
	}
}

// TestIssue1764_TheOperationsRouteCarriesTheSameDescription holds the second
// reader: a page rendering one connection takes the connection from this route
// rather than from the listing, and the two must say the same thing.
func TestIssue1764_TheOperationsRouteCarriesTheSameDescription(t *testing.T) {
	c := connect(t)
	name := issue1764Register(t, c, "operations", issue1764Description)

	status, out := c.rest(http.MethodGet, "/api/v1/apis/"+name+"/operations", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET operations: HTTP %d: %v", status, out)
	}
	conn, _ := out["connection"].(map[string]any)
	if conn == nil {
		t.Fatalf("the operations route carried no connection: %v", out)
	}
	if got, _ := conn["description"].(string); got != issue1764Description {
		t.Fatalf("description = %q; want the sentence the operator wrote", got)
	}
	if got, _ := conn["base_url"].(string); got != issue1764BaseURL {
		t.Errorf("base_url = %q", got)
	}
}

// TestIssue1764_TheRouteAndListConnectionsAgree is why the fix is the
// inventory rather than a second derivation: one question about what a
// connection is called has one answer, whichever surface asks it.
func TestIssue1764_TheRouteAndListConnectionsAgree(t *testing.T) {
	c := connect(t)
	name := issue1764Register(t, c, "agree", issue1764Description)

	conn := issue1764Listed(t, c, name)
	routeDescription, _ := conn["description"].(string)

	out := c.call("list_connections", map[string]any{})
	entries, _ := out["connections"].([]any)
	for _, entry := range entries {
		e, _ := entry.(map[string]any)
		if n, _ := e["name"].(string); n != name {
			continue
		}
		if got, _ := e["description"].(string); got != routeDescription {
			t.Fatalf("list_connections says %q and the route says %q", got, routeDescription)
		}
		return
	}
	t.Fatalf("list_connections does not list %q: %v", name, out)
}

// TestIssue1764_ADescriptionlessConnectionCarriesNone is the other half of the
// ticket's ask: nothing is not a URL.
func TestIssue1764_ADescriptionlessConnectionCarriesNone(t *testing.T) {
	c := connect(t)
	name := issue1764Register(t, c, "bare", "")

	conn := issue1764Listed(t, c, name)
	if got, _ := conn["description"].(string); got != "" {
		t.Fatalf("description = %q; a connection nobody described carries none", got)
	}
	if got, _ := conn["base_url"].(string); got != issue1764BaseURL {
		t.Errorf("base_url = %q; the upstream root is unaffected", got)
	}
}
