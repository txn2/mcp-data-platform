//go:build integration

package acceptance

// Issue #1679: every request the api and graphql kinds sent carried Go's
// default User-Agent, Go-http-client/1.1, which a web application firewall in
// front of a public GraphQL endpoint refused with an HTML page, and the
// connection's schema state quoted the page as the error.
//
// The api criteria read the User-Agent where it lands: the api-test fixture
// (dev/docker-compose.yml) echoes the request it received. The graphql
// criteria run against DataHub's GMS at /api/graphql, the real endpoint
// #1277's criteria use, behind a firewall stand-in started by the test: no
// deployment runs a firewall the suite can point at, so the stand-in is the
// one part written here. It refuses Go's User-Agent with the 403 HTML page the
// reported endpoint answered, records what each request carried, and forwards
// everything else to DataHub unchanged.
//
// Wire forms: `static_headers` is a JSON object on the connection config and
// admits one form. api_invoke_endpoint's `headers` is an object of strings and
// admits one form; `connection`, `method`, `path` and `purpose` are strings.
// graphql_query's `variables` is not sent. No parameter this ticket touches
// admits a second form.

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const issue1679Purpose = "Acceptance for #1679: outbound requests carry the platform's User-Agent, and a firewall's refusal names it."

// issue1679BlockPage is what the reported endpoint's firewall answered: an
// HTML page that says nothing about the request.
const issue1679BlockPage = `<!DOCTYPE html><html><head><title>Access denied</title></head><body><h1>Access denied</h1><p>The request was blocked.</p></body></html>`

// issue1679ProductUserAgent is what the running platform must send: its
// product name and the version the admin system route reports.
func issue1679ProductUserAgent(t *testing.T, c *client) string {
	t.Helper()
	status, body := c.rest(http.MethodGet, "/api/v1/admin/system/info", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("system info: HTTP %d", status)
	}
	version, _ := body["version"].(string)
	if version == "" {
		t.Fatalf("system info reports no version: %v", body)
	}
	return "mcp-data-platform/" + version
}

// issue1679Firewall is the stand-in: a firewall in front of the real GraphQL
// endpoint. refuse decides, from the User-Agent, whether a request is blocked
// with the 403 page; every other request is forwarded to DataHub and its
// answer returned as is.
type issue1679Firewall struct {
	server *httptest.Server
	mu     sync.Mutex
	agents []string
}

func issue1679StartFirewall(t *testing.T, refuse func(userAgent string) bool) *issue1679Firewall {
	t.Helper()
	fw := &issue1679Firewall{}
	fw.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua := r.Header.Get("User-Agent")
		fw.mu.Lock()
		fw.agents = append(fw.agents, ua)
		fw.mu.Unlock()
		if refuse(ua) {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, issue1679BlockPage)
			return
		}
		payload, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, issue1277Endpoint(), bytes.NewReader(payload))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		req.Header.Set("Content-Type", r.Header.Get("Content-Type"))
		req.Header.Set("Accept", r.Header.Get("Accept"))
		if auth := r.Header.Get("Authorization"); auth != "" {
			req.Header.Set("Authorization", auth)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer res.Body.Close() //nolint:errcheck // best-effort close
		w.Header().Set("Content-Type", res.Header.Get("Content-Type"))
		w.WriteHeader(res.StatusCode)
		_, _ = io.Copy(w, res.Body)
	}))
	t.Cleanup(fw.server.Close)
	return fw
}

// seen returns the User-Agent of every request the firewall received.
func (fw *issue1679Firewall) seen() []string {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	return append([]string(nil), fw.agents...)
}

// issue1679ConnectGraphQL registers a graphql connection whose endpoint is the
// firewall, with any extra config, and returns its name. Registration is what
// triggers the introspection.
func issue1679ConnectGraphQL(t *testing.T, c *client, fw *issue1679Firewall, label string, extra map[string]any) string {
	t.Helper()
	name := fmt.Sprintf("acc-1679-%s-%d", label, issue1679Stamp())
	cfg := map[string]any{
		"endpoint_url":    fw.server.URL,
		"connection_name": name,
		"connect_timeout": "10s",
		"call_timeout":    "30s",
	}
	for k, v := range extra {
		cfg[k] = v
	}
	status := c.restJSON(http.MethodPut, "/api/v1/admin/connection-instances/graphql/"+name, map[string]any{
		"config":      cfg,
		"description": "Acceptance 1679: " + label,
	})
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("register the %s connection: HTTP %d", label, status)
	}
	t.Cleanup(func() {
		c.rest(http.MethodDelete, "/api/v1/admin/connection-instances/graphql/"+name, http.NoBody)
	})
	return name
}

var issue1679Counter struct {
	mu sync.Mutex
	n  int
}

// issue1679Stamp is a per-process unique suffix for connection names.
func issue1679Stamp() int {
	issue1679Counter.mu.Lock()
	defer issue1679Counter.mu.Unlock()
	issue1679Counter.n++
	return issue1679Counter.n
}

// issue1679SchemaState reads what the platform holds for a graphql
// connection: the admin route the ticket's operator read the block page from.
func issue1679SchemaState(t *testing.T, c *client, name string) map[string]any {
	t.Helper()
	status, body := c.rest(http.MethodGet, "/api/v1/admin/connection-instances/graphql/"+name+"/schema", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("schema state of %s: HTTP %d: %v", name, status, body)
	}
	return body
}

// TestIssue1679_ApiRequestsCarryTheProductUserAgent is the first criterion on
// the api kind: a request through a connection that pins nothing arrives at
// the upstream as mcp-data-platform/<version>, and a static_headers or
// per-call User-Agent arrives in its place.
func TestIssue1679_ApiRequestsCarryTheProductUserAgent(t *testing.T) {
	c := connect(t)
	want := issue1679ProductUserAgent(t, c)

	t.Run("the product string by default", func(t *testing.T) {
		conn := issue1647Connect(t, c, "1679-default", map[string]any{"auth_mode": "none"})
		got := issue1647Header(issue1647Echo(t, c, conn, map[string]any{"purpose": issue1679Purpose}), "User-Agent")
		if len(got) != 1 || got[0] != want {
			t.Fatalf("the upstream saw User-Agent %v; want %q", got, want)
		}
	})
	t.Run("static_headers pins another", func(t *testing.T) {
		conn := issue1647Connect(t, c, "1679-static", map[string]any{
			"auth_mode":      "none",
			"static_headers": map[string]any{"User-Agent": "acme-integrations/2"},
		})
		got := issue1647Header(issue1647Echo(t, c, conn, map[string]any{"purpose": issue1679Purpose}), "User-Agent")
		if len(got) != 1 || got[0] != "acme-integrations/2" {
			t.Fatalf("the upstream saw User-Agent %v; want the pinned value", got)
		}
	})
	t.Run("a per-call header is honored", func(t *testing.T) {
		conn := issue1647Connect(t, c, "1679-percall", map[string]any{"auth_mode": "none"})
		got := issue1647Header(issue1647Echo(t, c, conn, map[string]any{
			"purpose": issue1679Purpose,
			"headers": map[string]any{"User-Agent": "per-call/1"},
		}), "User-Agent")
		if len(got) != 1 || got[0] != "per-call/1" {
			t.Fatalf("the upstream saw User-Agent %v; want the per-call value", got)
		}
	})
}

// TestIssue1679_IntrospectionPassesAFirewallThatRefusesGoDefaultUserAgent is
// the ticket's outcome on the graphql kind: behind a firewall that refuses
// Go-http-client, as the reported endpoint's did, a connection that pins
// nothing reads its schema and answers a query, because what it sends is the
// product string.
func TestIssue1679_IntrospectionPassesAFirewallThatRefusesGoDefaultUserAgent(t *testing.T) {
	requireGraphQLUpstream(t)
	c := connect(t)
	want := issue1679ProductUserAgent(t, c)
	fw := issue1679StartFirewall(t, func(ua string) bool { return strings.HasPrefix(ua, "Go-http-client") })
	name := issue1679ConnectGraphQL(t, c, fw, "passes", nil)

	state := issue1679SchemaState(t, c, name)
	if cause, _ := state["error"].(string); cause != "" {
		t.Fatalf("the schema was not read through the firewall: %s", cause)
	}
	if number(t, state, "operation_count") <= 0 {
		t.Fatalf("the connection holds no operations: %v", state)
	}
	out := c.call(issue1277QueryTool, map[string]any{
		"connection": name,
		"query":      issue1675Document,
		"variables":  issue1675Variables(),
		"purpose":    issue1679Purpose,
	})
	if failed, _ := out["upstream_error"].(bool); failed {
		t.Fatalf("the query was refused through the firewall: %v", out)
	}
	for i, ua := range fw.seen() {
		if ua != want {
			t.Errorf("request %d carried User-Agent %q; want %q", i, ua, want)
		}
	}
	if len(fw.seen()) < 2 {
		t.Errorf("the firewall saw %d requests; want the introspection and the query", len(fw.seen()))
	}
}

// TestIssue1679_ARefusedIntrospectionNamesTheUserAgentAndTheKnob is the
// diagnosis: when the firewall refuses the product string too, the schema
// state names the User-Agent the request carried and static_headers as the
// key that changes it, in place of the HTML page; and with static_headers
// pinning one, it names that value and the firewall saw it.
func TestIssue1679_ARefusedIntrospectionNamesTheUserAgentAndTheKnob(t *testing.T) {
	requireGraphQLUpstream(t)
	c := connect(t)
	want := issue1679ProductUserAgent(t, c)
	fw := issue1679StartFirewall(t, func(string) bool { return true })

	t.Run("names the product string", func(t *testing.T) {
		name := issue1679ConnectGraphQL(t, c, fw, "refused", nil)
		cause, _ := issue1679SchemaState(t, c, name)["error"].(string)
		if !strings.Contains(cause, `User-Agent was "`+want+`"`) {
			t.Errorf("the schema state does not name the User-Agent sent:\n%s", cause)
		}
		if !strings.Contains(cause, "static_headers") {
			t.Errorf("the schema state does not name the knob:\n%s", cause)
		}
		if strings.Contains(cause, "<html") {
			t.Errorf("the schema state quotes the block page:\n%s", cause)
		}
		if seen := fw.seen(); len(seen) == 0 || seen[len(seen)-1] != want {
			t.Errorf("the firewall saw %v; the message must name what was sent", seen)
		}
	})
	t.Run("names the pinned value", func(t *testing.T) {
		name := issue1679ConnectGraphQL(t, c, fw, "pinned", map[string]any{
			"static_headers": map[string]any{"User-Agent": "acme-integrations/2"},
		})
		cause, _ := issue1679SchemaState(t, c, name)["error"].(string)
		if !strings.Contains(cause, `User-Agent was "acme-integrations/2"`) {
			t.Errorf("the schema state does not name the pinned User-Agent:\n%s", cause)
		}
		if seen := fw.seen(); len(seen) == 0 || seen[len(seen)-1] != "acme-integrations/2" {
			t.Errorf("the firewall saw %v; static_headers must override the product", seen)
		}
	})
}
