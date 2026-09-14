//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Issue #1737: an auth_mode: oauth connection on the client_credentials grant
// caches its access token in memory. RFC 6749 section 5.1 makes expires_in
// RECOMMENDED rather than required, and golang.org/x/oauth2 treats a token
// with no expiry as valid forever, so an upstream that omits it had its first
// access token presented for the life of the process: once the session behind
// that token ended, every call through the connection failed with the
// upstream's 401 until the connection was saved again or the replica
// restarted. The token is now reused for a bounded window and obtained again
// after it.
//
// The token endpoint is a fixture this file starts, for the reason #1734's is:
// the upstreams the deployments run either send expires_in or are
// account-scoped, and the criterion is about the response that omits it. The
// fixture authenticates the client, issues a fresh access token per request
// with or without expires_in as the label was registered, and serves a
// resource route that accepts only a token it issued -- so "which token did
// the call carry" is answered by the upstream rather than by the platform.
//
// Every criterion pins its session to one replica: two replicas behind the dev
// proxy hold their own token caches, and a count of token requests is only
// meaningful against the process that made them.
//
// Wire forms: api_invoke_endpoint's `body` is untyped and admits an object and
// a string of JSON; both are sent as literal tools/call params against a
// client_credentials connection whose token endpoint omits expires_in
// (TestIssue1737_AcrossWireForms). The connection config this ticket touches
// (`auth_mode`, `oauth_grant`, `oauth_token_url`, `oauth_client_id`,
// `oauth_client_secret`) is written through the admin route as strings, the
// one form each admits.

const (
	issue1737Purpose  = "Acceptance for #1737: a client_credentials token with no expires_in is not reused forever."
	issue1737ClientID = "acc-1737-client"
	issue1737Secret   = "acc-1737-secret"

	// issue1737Window is the platform's fallback lifetime for a token with no
	// expires_in (internal/upstreamauth.DefaultUpstreamAccessTokenLifetime).
	// The acceptance suite is a client of the running platform and does not
	// import it, so the value is repeated here and asserted by waiting it out.
	issue1737Window = 15 * time.Minute
	// issue1737PastTheWindow is how long the refetch criterion waits before
	// its second call: the window, plus room for the oauth2 library's own
	// early-refresh delta and for a call landing either side of a second.
	issue1737PastTheWindow = issue1737Window + 45*time.Second
	// issue1737Session sizes the refetch criterion's session to that wait
	// plus the calls around it (#1738).
	issue1737Session = issue1737PastTheWindow + 3*time.Minute
)

// issue1737Fixture is a client_credentials token endpoint and the resource it
// issues tokens for, both addressed under a per-criterion label.
type issue1737Fixture struct {
	url string

	mu        sync.Mutex
	expiresIn map[string]int // label -> expires_in; 0 omits it
	requests  map[string]int // label -> token requests received
	issued    map[string]string
	counter   atomic.Int64
}

func issue1737StartFixture(t *testing.T) *issue1737Fixture {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for the client_credentials fixture: %v", err)
	}
	f := &issue1737Fixture{
		url:       "http://" + ln.Addr().String(),
		expiresIn: map[string]int{},
		requests:  map[string]int{},
		issued:    map[string]string{},
	}
	srv := &http.Server{Handler: f, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return f
}

// register declares a label whose token responses carry expiresIn, or omit
// expires_in entirely when it is zero, and returns its base and token URLs.
func (f *issue1737Fixture) register(label string, expiresIn int) (base, tokenURL string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.expiresIn[label] = expiresIn
	return f.url + "/" + label, f.url + "/" + label + "/token"
}

// tokenRequests is how many times the platform asked this label's token
// endpoint for an access token.
func (f *issue1737Fixture) tokenRequests(label string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[label]
}

func (f *issue1737Fixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 2)
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	label, route := parts[0], parts[1]
	f.mu.Lock()
	_, known := f.expiresIn[label]
	f.mu.Unlock()
	if !known {
		http.Error(w, "no label registered for "+label, http.StatusNotFound)
		return
	}
	if route == "token" {
		f.token(w, r, label)
		return
	}
	f.resource(w, r, label)
}

// token is the RFC 6749 section 4.4 token endpoint. It authenticates the
// client, refuses any grant but client_credentials, and issues a new access
// token on every request so a reused token is distinguishable from a fresh
// one at the resource.
func (f *issue1737Fixture) token(w http.ResponseWriter, r *http.Request, label string) {
	_ = r.ParseForm()
	user, pass, ok := r.BasicAuth()
	if !ok {
		user, pass = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}

	f.mu.Lock()
	f.requests[label]++
	expiresIn := f.expiresIn[label]
	f.mu.Unlock()

	if user != issue1737ClientID || pass != issue1737Secret {
		issue1737Error(w, http.StatusUnauthorized, "invalid_client", "the client did not authenticate")
		return
	}
	if grant := r.PostForm.Get("grant_type"); grant != "client_credentials" {
		issue1737Error(w, http.StatusBadRequest, "unsupported_grant_type", "grant_type was "+grant)
		return
	}

	token := fmt.Sprintf("acc-1737-%s-at-%d", label, f.counter.Add(1))
	f.mu.Lock()
	f.issued[token] = label
	f.mu.Unlock()

	answer := map[string]any{"access_token": token, "token_type": "Bearer"}
	if expiresIn > 0 {
		answer["expires_in"] = expiresIn
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(answer)
}

// resource accepts only an access token this label's token endpoint issued,
// and answers with the token and the body it received.
func (f *issue1737Fixture) resource(w http.ResponseWriter, r *http.Request, label string) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	f.mu.Lock()
	issuedFor := f.issued[token]
	f.mu.Unlock()
	if issuedFor != label {
		http.Error(w, "not a token this endpoint issued", http.StatusUnauthorized)
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"token": token, "request_body": string(body)})
}

func issue1737Error(w http.ResponseWriter, status int, code, description string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "error_description": description})
}

// issue1737Connect saves a client_credentials api connection against the
// label's token endpoint and returns its name.
func issue1737Connect(t *testing.T, c *client, label, base, tokenURL string) string {
	t.Helper()
	name := fmt.Sprintf("acc-1737-%s-%d", label, time.Now().UnixNano())
	path := "/api/v1/admin/connection-instances/api/" + name
	status, body := c.rest(http.MethodPut, path, jsonBody(t, map[string]any{
		"config": map[string]any{
			"connection_name":     name,
			"base_url":            base,
			"connect_timeout":     "5s",
			"call_timeout":        "10s",
			"auth_mode":           "oauth",
			"oauth_grant":         "client_credentials",
			"oauth_token_url":     tokenURL,
			"oauth_client_id":     issue1737ClientID,
			"oauth_client_secret": issue1737Secret,
		},
		"description": "Acceptance 1737: " + label,
	}))
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("register api/%s: HTTP %d (%v)", label, status, body)
	}
	t.Cleanup(func() { c.rest(http.MethodDelete, path, http.NoBody) })
	return name
}

// issue1737Reached calls the resource through the connection and returns the
// access token the call carried, failing unless the upstream accepted it.
func issue1737Reached(t *testing.T, c *client, connection string, extra map[string]any) string {
	t.Helper()
	args := map[string]any{
		"connection": connection,
		"method":     http.MethodGet,
		"path":       "/echo",
		"purpose":    issue1737Purpose,
	}
	for k, v := range extra {
		args[k] = v
	}
	res, text, err := c.callRaw("api_invoke_endpoint", args)
	if err != nil {
		t.Fatalf("api_invoke_endpoint: transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("the call failed: %s", text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("result is not JSON: %v\n%s", err, text)
	}
	if got := number(t, out, "status"); got != http.StatusOK {
		t.Fatalf("the resource refused the call: HTTP %v, body %v", got, out["body"])
	}
	body, ok := out["body"].(map[string]any)
	if !ok {
		t.Fatalf("the resource's answer is not an object: %v", out["body"])
	}
	token, _ := body["token"].(string)
	if token == "" {
		t.Fatalf("the resource reported no token: %v", body)
	}
	return token
}

// issue1737OneReplica pins a session to a single platform process for the
// length of the criterion, since the token cache under test is that process's.
func issue1737OneReplica(t *testing.T, timeout time.Duration) *client {
	t.Helper()
	return connectAtFor(t, replicas(t)[0].base, devAPIKey(), timeout)
}

// TestIssue1737_ATokenWithNoExpiresInIsRefetched is the ticket's criterion: a
// connection whose token endpoint omits expires_in obtains a new access token
// once the fallback window has passed, instead of presenting the first one for
// the life of the process. It waits the window out, which is the only way the
// running platform shows it: nothing on any surface reports a cached token's
// expiry, and the window is deliberately not an operator key.
//
// The cache under test is the replica process's, so a restart inside the wait
// empties it and the second token request proves nothing. Nothing the platform
// serves names the process's start time, so this criterion cannot detect that
// itself: run it against a stack nobody is rebuilding, and read the platform
// binary's modification time either side of the run. `make dev` rebuilds on
// every Go file saved, which includes saving this one.
func TestIssue1737_ATokenWithNoExpiresInIsRefetched(t *testing.T) {
	c := issue1737OneReplica(t, issue1737Session)
	f := issue1737StartFixture(t)
	base, tokenURL := f.register("refetched", 0)
	conn := issue1737Connect(t, c, "refetched", base, tokenURL)

	first := issue1737Reached(t, c, conn, nil)
	if got := f.tokenRequests("refetched"); got != 1 {
		t.Fatalf("the first call made %d token requests, want 1", got)
	}

	time.Sleep(issue1737PastTheWindow)

	second := issue1737Reached(t, c, conn, nil)
	if got := f.tokenRequests("refetched"); got != 2 {
		t.Fatalf("token requests = %d after the window, want 2: the token with no expires_in was reused past it", got)
	}
	if second == first {
		t.Errorf("the call after the window carried %q again, the token the upstream issued before it", first)
	}
}

// TestIssue1737_ATokenWithNoExpiresInIsReusedInsideTheWindow is the other half
// of the bound: giving a token an expiry must not turn every outbound call
// into a token request against the upstream's IdP.
func TestIssue1737_ATokenWithNoExpiresInIsReusedInsideTheWindow(t *testing.T) {
	c := issue1737OneReplica(t, sessionTimeout)
	f := issue1737StartFixture(t)
	base, tokenURL := f.register("reused", 0)
	conn := issue1737Connect(t, c, "reused", base, tokenURL)

	first := issue1737Reached(t, c, conn, nil)
	for range 3 {
		if got := issue1737Reached(t, c, conn, nil); got != first {
			t.Fatalf("a call inside the window carried %q, want the held token %q", got, first)
		}
	}
	if got := f.tokenRequests("reused"); got != 1 {
		t.Errorf("token requests = %d for four calls inside the window, want 1", got)
	}
}

// TestIssue1737_AnUpstreamExpiryStillWins keeps the fallback off the path of an
// upstream that does send expires_in: a token endpoint answering with a
// two-second lifetime is asked again on the next call, not held for the
// fallback window.
func TestIssue1737_AnUpstreamExpiryStillWins(t *testing.T) {
	c := issue1737OneReplica(t, sessionTimeout)
	f := issue1737StartFixture(t)
	base, tokenURL := f.register("short", 2)
	conn := issue1737Connect(t, c, "short", base, tokenURL)

	first := issue1737Reached(t, c, conn, nil)
	time.Sleep(15 * time.Second)
	second := issue1737Reached(t, c, conn, nil)

	if second == first {
		t.Errorf("the call after the upstream's own expiry carried %q again", first)
	}
	if got := f.tokenRequests("short"); got != 2 {
		t.Errorf("token requests = %d across an upstream expiry, want 2", got)
	}
}

// TestIssue1737_AcrossWireForms sends api_invoke_endpoint's untyped body in
// both forms its schema admits against a connection whose token endpoint omits
// expires_in, and asserts each reaches the resource with a token the endpoint
// issued.
func TestIssue1737_AcrossWireForms(t *testing.T) {
	c := issue1737OneReplica(t, sessionTimeout)
	f := issue1737StartFixture(t)
	base, tokenURL := f.register("wire-forms", 0)
	conn := issue1737Connect(t, c, "wire-forms", base, tokenURL)

	for _, form := range []struct {
		name string
		body any
	}{
		{"body as an object", map[string]any{"note": "acc-1737"}},
		{"body as a string of JSON", `{"note":"acc-1737"}`},
	} {
		t.Run(form.name, func(t *testing.T) {
			token := issue1737Reached(t, c, conn, map[string]any{"method": http.MethodPost, "body": form.body})
			if !strings.HasPrefix(token, "acc-1737-wire-forms-at-") {
				t.Errorf("the resource received %q, want a token its endpoint issued", token)
			}
		})
	}
}
