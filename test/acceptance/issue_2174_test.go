//go:build integration

package acceptance

import (
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2174: the /endpoints/{name}/... surface. A public endpoint
// answers an anonymous request carrying a valid bot check answer and refuses
// one without; a signed_in endpoint answers an API key and a browser session,
// naming each caller, and refuses an anonymous request with 401; a browser's
// cross-origin request from an allowed origin passes its preflight and reads
// the response, and an unlisted origin's preflight is refused; a signed-in
// caller past their own limit gets 429 while another caller is answered; and
// the request metrics and the slow-request log carry the route template.
//
// Wire forms: a public request carries its answer in X-Bot-Check with a
// JSON body; a signed-in request authenticates with Authorization: Bearer
// and with the portal's session cookie; a preflight sends Origin,
// Access-Control-Request-Method and Access-Control-Request-Headers.

const issue2174Origin = "https://forms.example.com"

// browserSignIn signs email in to the portal through the dev stack's
// Keycloak, as a person at a browser does, and returns a client holding the
// session cookie the platform set.
func browserSignIn(t *testing.T, base, email, password string) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	hc := &http.Client{Jar: jar, Timeout: 30 * time.Second}
	res, err := hc.Get(base + "/portal/auth/login") // #nosec G704 -- the platform under test
	if err != nil {
		t.Fatalf("opening the sign-in: %v", err)
	}
	page, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	m := regexp.MustCompile(`<form[^>]*id="kc-form-login"[^>]*action="([^"]+)"`).FindSubmatch(page)
	if m == nil {
		t.Fatalf("the identity provider's sign-in page has no form: %d %.300s", res.StatusCode, page)
	}
	res, err = hc.PostForm(html.UnescapeString(string(m[1])), url.Values{"username": {email}, "password": {password}}) // #nosec G704 -- the dev stack's identity provider
	if err != nil {
		t.Fatalf("signing in: %v", err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
	u, _ := url.Parse(base)
	for _, c := range jar.Cookies(u) {
		if strings.Contains(c.Name, "session") {
			return hc
		}
	}
	t.Fatalf("signing in left no session cookie for %s: %v", base, jar.Cookies(u))
	return nil
}

// signedInEndpoint saves and binds a signed-in echo endpoint.
func signedInEndpoint(t *testing.T, admin *client, prefix string, args map[string]any) string {
	t.Helper()
	name := endpointName(prefix)
	saveEndpointScript(t, admin, name, issue2171EchoHandler, `{"params": {}, "caller": None}`)
	bind := map[string]any{"endpoint": name, "methods": []string{"GET", "POST"}, "auth_mode": "signed_in"}
	for k, v := range args {
		bind[k] = v
	}
	bindEndpoint(t, admin, name, "", bind)
	return name
}

// TestIssue2174_APublicEndpointAnswersAValidAnswerAndRefusesNone.
func TestIssue2174_APublicEndpointAnswersAValidAnswerAndRefusesNone(t *testing.T) {
	admin := connect(t)
	putBotCheck(t, admin, issue2173Quick)
	name := publicEndpoint(t, admin, "acc-2174-public")
	if got := postAnswered(t, admin.base, name, ""); got.status != http.StatusForbidden {
		t.Fatalf("anonymous with no answer: want 403, got %d %s", got.status, got.body)
	}
	ch := challengeFor(t, admin.base, name)
	got := postAnswered(t, admin.base, name, solvePoW(ch.Token, ch.Difficulty))
	if got.status != http.StatusOK || !strings.Contains(got.body, `"caller":null`) {
		t.Fatalf("anonymous with an answer: %d %s", got.status, got.body)
	}
}

// TestIssue2174_SignedInAnswersAKeyAndASessionAndRefusesNobody.
func TestIssue2174_SignedInAnswersAKeyAndASessionAndRefusesNobody(t *testing.T) {
	admin := connect(t)
	name := signedInEndpoint(t, admin, "acc-2174-signed", nil)

	got := endpointRequest(t, admin.base, http.MethodGet, "/endpoints/"+name, bearer(devOwnerAPIKey), "")
	if got.status != http.StatusOK || !strings.Contains(got.body, `"caller":"`+devOwnerEmail+`"`) {
		t.Fatalf("with the owner's API key: %d %s", got.status, got.body)
	}
	session := browserSignIn(t, admin.base, issue1759AnalystEmail, issue1759AnalystPass)
	got = endpointRequestWith(t, session, admin.base, http.MethodGet, "/endpoints/"+name, nil, "")
	if got.status != http.StatusOK || !strings.Contains(got.body, `"caller":"`+issue1759AnalystEmail+`"`) {
		t.Fatalf("with a browser session: %d %s", got.status, got.body)
	}
	if got := endpointRequest(t, admin.base, http.MethodGet, "/endpoints/"+name, nil, ""); got.status != http.StatusUnauthorized {
		t.Fatalf("anonymous: want 401, got %d %s", got.status, got.body)
	}
	header := bearer(devOwnerAPIKey)
	header.Set("Content-Type", "application/json")
	if got := endpointRequest(t, admin.base, http.MethodPost, "/endpoints/"+name, header, `{"caller":{"email":"someone@else.example.com"}}`); !strings.Contains(got.body, `"caller":"`+devOwnerEmail+`"`) {
		t.Fatalf("a body claiming another caller changed who the handler read: %d %s", got.status, got.body)
	}
}

// TestIssue2174_AnAllowedOriginsFetchPassesItsPreflightAndAnotherIsRefused.
func TestIssue2174_AnAllowedOriginsFetchPassesItsPreflightAndAnotherIsRefused(t *testing.T) {
	admin := connect(t)
	name := signedInEndpoint(t, admin, "acc-2174-cors", map[string]any{"allowed_origins": []string{issue2174Origin}})

	preflight := http.Header{
		"Origin": {issue2174Origin}, "Access-Control-Request-Method": {"POST"},
		"Access-Control-Request-Headers": {"authorization,content-type"},
	}
	got := endpointRequest(t, admin.base, http.MethodOptions, "/endpoints/"+name, preflight, "")
	if got.status != http.StatusNoContent || got.header.Get("Access-Control-Allow-Origin") != issue2174Origin ||
		!strings.Contains(got.header.Get("Access-Control-Allow-Methods"), "POST") ||
		!strings.Contains(strings.ToLower(got.header.Get("Access-Control-Allow-Headers")), "content-type") {
		t.Fatalf("the allowed origin's preflight: %d %v", got.status, got.header)
	}
	header := bearer(devOwnerAPIKey)
	header.Set("Origin", issue2174Origin)
	header.Set("Content-Type", "application/json")
	got = endpointRequest(t, admin.base, http.MethodPost, "/endpoints/"+name, header, `{"a":1}`)
	if got.status != http.StatusOK || got.header.Get("Access-Control-Allow-Origin") != issue2174Origin {
		t.Fatalf("the allowed origin's request: %d %v %s", got.status, got.header, got.body)
	}
	if got.header.Get("Access-Control-Allow-Credentials") != "" {
		t.Fatalf("credentials are allowed across origins: %v", got.header)
	}

	preflight.Set("Origin", "https://elsewhere.example.com")
	got = endpointRequest(t, admin.base, http.MethodOptions, "/endpoints/"+name, preflight, "")
	if got.status != http.StatusForbidden || got.header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("an unlisted origin's preflight: want 403 with no allow-origin, got %d %v", got.status, got.header)
	}
}

// TestIssue2174_ACallerPastTheirLimitGets429AndAnotherIsAnswered: the
// limits count on each replica, so the requests go to one.
func TestIssue2174_ACallerPastTheirLimitGets429AndAnotherIsAnswered(t *testing.T) {
	admin, _ := connectReplicaPair(t)
	name := signedInEndpoint(t, admin, "acc-2174-limit", map[string]any{"rate_limit": 100, "caller_rate_limit": 2})
	for i := range 2 {
		if got := endpointRequest(t, admin.base, http.MethodGet, "/endpoints/"+name, bearer(devOwnerAPIKey), ""); got.status != http.StatusOK {
			t.Fatalf("the owner's request %d: %d %s", i+1, got.status, got.body)
		}
	}
	got := endpointRequest(t, admin.base, http.MethodGet, "/endpoints/"+name, bearer(devOwnerAPIKey), "")
	if got.status != http.StatusTooManyRequests || got.header.Get("Retry-After") == "" {
		t.Fatalf("the owner past their limit: want 429 with Retry-After, got %d %v", got.status, got.header)
	}
	if got := endpointRequest(t, admin.base, http.MethodGet, "/endpoints/"+name, bearer(devPeerAPIKey), ""); got.status != http.StatusOK {
		t.Fatalf("another caller: %d %s", got.status, got.body)
	}
}

// TestIssue2174_TheMetricsAndTheSlowLogCarryTheRouteTemplate: a request to a
// path with an id in it is recorded under the template, and a request held
// past the dev stack's slow threshold (2s) logs the template.
func TestIssue2174_TheMetricsAndTheSlowLogCarryTheRouteTemplate(t *testing.T) {
	admin := connect(t)
	name := endpointName("acc-2174-template")
	saveEndpointScript(t, admin, name, `def handle(req):
    """Waits on an upstream for as long as the request asks."""
    platform.call("mcp-test-fixture__slow", {"milliseconds": int(req["params"].get("ms", "10")),
        "purpose": "Acceptance for #2174: hold one endpoint request past the slow threshold."})
    return {"json": req["params"]}
`, `{"params": {"ms": "10"}, "caller": None}`)
	bindEndpoint(t, admin, name, "", map[string]any{"endpoint": name, "path": "/{ms}", "auth_mode": "signed_in"})

	start := time.Now().Add(-time.Second)
	if got := endpointRequest(t, admin.base, http.MethodGet, "/endpoints/"+name+"/2600", bearer(admin.apiKey), ""); got.status != http.StatusOK {
		t.Fatalf("GET: %d %s", got.status, got.body)
	}
	const template = "/endpoints/{name}/{rest...}"
	body := scrapeRaw(t)
	if !strings.Contains(body, `route="`+template+`"`) {
		t.Fatalf("no http_server_request_duration_seconds series carries route=%q", template)
	}
	if strings.Contains(body, "/endpoints/"+name) {
		t.Fatalf("a series carries the requested path /endpoints/%s", name)
	}
	if !strings.Contains(body, `endpoint_requests_total{endpoint="`+name+`",status_class="2xx"}`) {
		t.Fatalf("no endpoint_requests_total series for %s", name)
	}
	deadline := time.Now().Add(spanWait)
	var rec map[string]any
	for rec == nil {
		rec = stderrRecordSince(t, start, func(r map[string]any) bool {
			return r["msg"] == "slow HTTP request" && r["route"] == template
		})
		if rec == nil && time.Now().After(deadline) {
			t.Fatalf("no slow HTTP request line for route %s", template)
		}
		if rec == nil {
			time.Sleep(250 * time.Millisecond)
		}
	}
	if line := strings.Join([]string{rec["route"].(string), rec["msg"].(string)}, " "); strings.Contains(line, "2600") {
		t.Fatalf("the slow log carries the path: %v", rec)
	}
}
