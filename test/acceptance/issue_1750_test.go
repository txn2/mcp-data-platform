//go:build integration

package acceptance

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// Issue #1750: both readers over the platform's REST surface -- the API
// Reference page in the portal and the Swagger UI at /api/v1/admin/docs/ --
// render one document, and that document told a reader 276 paths and nothing
// about how to call one. Its info.description was a single sentence listing
// section names, its `host` was `localhost:8080` (the machine the swag
// annotations were authored on, served unmodified to every deployment), and
// its two security schemes were bare names, so an operation's Authorizations
// block read "ApiKeyAuth or BearerAuth" and expanded to the same two words.
//
// What these criteria hold: the served document names the origin that served
// it; the landing section carries the headings ReDoc turns into navigation
// entries; both schemes describe their credential; and the first call the
// introduction tells a reader to make actually works, exactly as written.
//
// Wire forms: the route under test is GET /api/v1/admin/docs/doc.json, which
// takes no parameters -- the input that varies is the request's Host, and the
// forms it admits are a bare name, a name with a port, an IPv6 literal with a
// port, and a value that is not a host at all. All four are sent below as
// literal Host headers; the fourth has two acceptable outcomes over the wire,
// because a Host carrying a quote is refused by the proxy in front of the
// platform before the handler runs, and both are asserted. The introduction's own example call
// (GET /api/v1/portal/me with X-API-Key) admits one form and is sent as
// written, with the header the introduction names rather than the Bearer
// header the rest of this suite uses.

// specRoute is where both readers fetch the document from.
const specRoute = "/api/v1/admin/docs/doc.json"

// getSpecAsHost requests the served document with the given Host and returns
// the status and the raw body. Host is set on the request rather than as a
// header because that is the field Go's client writes into the request line.
func getSpecAsHost(t *testing.T, c *client, host string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(c.ctx, http.MethodGet, c.base+specRoute, nil)
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	if host != "" {
		req.Host = host
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s as Host %q: %v", specRoute, host, err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close after read
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("reading the served document: %v", err)
	}
	return res.StatusCode, raw
}

// fetchSpecAsHost is getSpecAsHost for the cases that must succeed, decoded.
func fetchSpecAsHost(t *testing.T, c *client, host string) map[string]any {
	t.Helper()
	status, raw := getSpecAsHost(t, c, host)
	if status != http.StatusOK {
		t.Fatalf("GET %s as Host %q = %d; want 200", specRoute, host, status)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("the served document is not JSON: %v", err)
	}
	return doc
}

// TestIssue1750_TheServedDocumentNamesTheOriginThatServedIt is the ticket's
// second defect: every deployment's document named a developer's laptop as its
// server, and a reader following it called localhost.
func TestIssue1750_TheServedDocumentNamesTheOriginThatServedIt(t *testing.T) {
	c := connect(t)

	for _, host := range []string{
		"mcp.example.com",
		"mcp.example.com:8443",
		"[::1]:8080",
	} {
		doc := fetchSpecAsHost(t, c, host)
		if doc["host"] != host {
			t.Errorf("Host %q: the document says host %v", host, doc["host"])
		}
		if doc["basePath"] != "/api/v1" {
			t.Errorf("Host %q: basePath = %v, want /api/v1", host, doc["basePath"])
		}
	}

	// The Host header is written by the client and read by everyone who opens
	// the reference, so a value that is not a host never reaches the served
	// document. Over the wire that has two outcomes and the criterion accepts
	// either: the value is refused before the platform sees it (the dev stack's
	// proxy answers 400 on a Host carrying a quote), or it reaches the handler
	// and the embedded value is served instead.
	status, raw := getSpecAsHost(t, c, `evil", "x-injected": "`)
	switch {
	case status >= 400 && status < 500:
		t.Logf("a malformed Host was refused before the platform saw it: %d", status)
	case status == http.StatusOK:
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("a malformed Host left the served document unparseable: %v", err)
		}
		if got, _ := doc["host"].(string); strings.ContainsAny(got, `" `) {
			t.Errorf("a Host that is not a host reached the served document: %q", got)
		}
		if doc["basePath"] != "/api/v1" {
			t.Errorf("basePath = %v; the document was corrupted", doc["basePath"])
		}
	default:
		t.Errorf("GET %s with a malformed Host = %d; want a refusal or the document",
			specRoute, status)
	}
}

// TestIssue1750_TheLandingSectionIsNavigable is the ticket's first defect. ReDoc
// splits info.description on its top-level headings and gives each one its own
// navigation entry, so an introduction written there is a section of the
// reference rather than a paragraph in front of it.
func TestIssue1750_TheLandingSectionIsNavigable(t *testing.T) {
	c := connect(t)
	doc := fetchSpecAsHost(t, c, "")

	info, ok := doc["info"].(map[string]any)
	if !ok {
		t.Fatal("the served document has no info object")
	}
	desc, _ := info["description"].(string)

	for _, heading := range []string{
		"# Getting started",
		"# Authentication",
		"# Calling a connection through the gateway",
		"# Conventions",
	} {
		if !strings.Contains(desc, "\n"+heading+"\n") {
			t.Errorf("the landing section has no top-level %q heading, "+
				"so it gets no navigation entry", heading)
		}
	}
	// Where the MCP surface is, so a reader who wanted that one stops reading
	// this one.
	if !strings.Contains(desc, "/sse") || !strings.Contains(desc, "tools/list") {
		t.Error("the landing section does not say where the MCP surface is")
	}
	// The sentence the description used to be, in full, is not an introduction.
	if len(desc) < 1000 {
		t.Errorf("info.description is %d bytes; that is the old one-liner", len(desc))
	}
}

// TestIssue1750_BothSchemesDescribeTheirCredential is the ticket's third
// defect: an Authorizations block read "ApiKeyAuth or BearerAuth" and expanded
// to the same two words, saying nothing about what either credential is, where
// one is obtained, or which a given caller should hold.
func TestIssue1750_BothSchemesDescribeTheirCredential(t *testing.T) {
	c := connect(t)
	doc := fetchSpecAsHost(t, c, "")

	defs, ok := doc["securityDefinitions"].(map[string]any)
	if !ok {
		t.Fatal("the served document has no securityDefinitions")
	}
	for scheme, wantHeader := range map[string]string{
		"ApiKeyAuth": "X-API-Key",
		"BearerAuth": "Authorization",
	} {
		def, ok := defs[scheme].(map[string]any)
		if !ok {
			t.Errorf("the document has no %s scheme", scheme)
			continue
		}
		desc, _ := def["description"].(string)
		if strings.TrimSpace(desc) == "" {
			t.Errorf("%s carries no description", scheme)
			continue
		}
		// It stands on its own where ReDoc renders it: the header the
		// credential goes in, with the value's real shape.
		if !strings.Contains(desc, wantHeader) {
			t.Errorf("%s's description does not name the %s header", scheme, wantHeader)
		}
	}
	if d, _ := defs["BearerAuth"].(map[string]any)["description"].(string); !strings.Contains(d, "Bearer ") {
		t.Error("BearerAuth's description does not say the value is prefixed `Bearer `")
	}
}

// TestIssue1750_TheFirstCallTheIntroductionShowsWorks is the ticket's end
// state: a reader who opens the reference cold can make their first successful
// call from what is on the page. The call below is the introduction's own
// example, with the header the introduction names -- not the one the rest of
// this suite happens to use -- and the fields asserted are the ones its sample
// response shows.
func TestIssue1750_TheFirstCallTheIntroductionShowsWorks(t *testing.T) {
	c := connect(t)
	doc := fetchSpecAsHost(t, c, "")
	info, _ := doc["info"].(map[string]any)
	desc, _ := info["description"].(string)

	const route = "/api/v1/portal/me"
	if !strings.Contains(desc, route) {
		t.Fatalf("the introduction no longer shows %s as the first call", route)
	}
	if !strings.Contains(desc, "X-API-Key:") {
		t.Fatal("the introduction no longer shows the X-API-Key header on that call")
	}

	req, err := http.NewRequestWithContext(c.ctx, http.MethodGet, c.base+route, nil)
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	req.Header.Set("X-API-Key", c.apiKey)

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", route, err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close after read
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("reading %s: %v", route, err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET %s with the header the introduction names = %d; want 200. Body: %s",
			route, res.StatusCode, strings.TrimSpace(string(raw)))
	}
	var me map[string]any
	if err := json.Unmarshal(raw, &me); err != nil {
		t.Fatalf("%s did not answer JSON: %v", route, err)
	}
	// The fields the introduction's sample response shows. `persona` is the
	// one it tells the reader to read first, because it decides which tools
	// and connections the credential reaches.
	for _, field := range []string{"user_id", "email", "roles", "persona", "is_admin", "tools"} {
		if _, ok := me[field]; !ok {
			t.Errorf("%s has no %q; the introduction's sample response shows it", route, field)
		}
	}
}
