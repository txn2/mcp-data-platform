package oauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/oidcdiscovery"
)

// newDiscoveryServer starts an httptest server that serves an OIDC discovery
// document at <issuer>/.well-known/openid-configuration whose endpoints point
// back at itself. It returns the server and a counter of discovery-doc hits.
func newDiscoveryServer(t *testing.T) (srv *httptest.Server, hits *int32) {
	t.Helper()
	var count int32
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host + "/realms/test"
		switch r.URL.Path {
		case "/realms/test" + oidcdiscovery.WellKnownPath:
			atomic.AddInt32(&count, 1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"issuer": "` + base + `",
				"authorization_endpoint": "` + base + `/protocol/openid-connect/auth",
				"token_endpoint": "` + base + `/protocol/openid-connect/token"
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &count
}

func newResolver(cfg *UpstreamConfig) *upstreamEndpointResolver {
	return newUpstreamEndpointResolver(cfg, &http.Client{Timeout: 5 * time.Second})
}

// TestUpstreamDiscovery_ResolvesEndpoints verifies the broker discovers the
// authorization and token endpoints from the issuer's well-known document, and
// that the resolved values are cached (a single discovery hit across many
// resolutions).
func TestUpstreamDiscovery_ResolvesEndpoints(t *testing.T) {
	srv, hits := newDiscoveryServer(t)

	r := newResolver(&UpstreamConfig{Issuer: srv.URL + "/realms/test"})

	authEP, err := r.authorizationEndpoint(context.Background())
	if err != nil {
		t.Fatalf("authorizationEndpoint: %v", err)
	}
	if want := srv.URL + "/realms/test/protocol/openid-connect/auth"; authEP != want {
		t.Errorf("authorization endpoint = %q, want %q", authEP, want)
	}

	tokenEP, err := r.tokenEndpoint(context.Background())
	if err != nil {
		t.Fatalf("tokenEndpoint: %v", err)
	}
	if want := srv.URL + "/realms/test/protocol/openid-connect/token"; tokenEP != want {
		t.Errorf("token endpoint = %q, want %q", tokenEP, want)
	}

	// Resolve again; cache should serve it without another discovery fetch.
	if _, err := r.authorizationEndpoint(context.Background()); err != nil {
		t.Fatalf("second authorizationEndpoint: %v", err)
	}
	if got := atomic.LoadInt32(hits); got != 1 {
		t.Errorf("discovery hits = %d, want 1 (result must be cached)", got)
	}
}

// TestUpstreamDiscovery_AuthorizeRedirectTargetsDiscoveredEndpoint drives the
// full /oauth/authorize path and asserts the redirect targets the DISCOVERED
// authorization endpoint, not a hardcoded Keycloak path.
func TestUpstreamDiscovery_AuthorizeRedirectTargetsDiscoveredEndpoint(t *testing.T) {
	srv, _ := newDiscoveryServer(t)

	storage := NewMemoryStorage()
	client := &Client{
		ClientID:     "client-123",
		RedirectURIs: []string{"http://localhost:8080/callback"},
		Active:       true,
		RequirePKCE:  false,
	}
	_ = storage.CreateClient(context.Background(), client)

	server, err := NewServer(ServerConfig{
		Issuer: "http://localhost:8080",
		Upstream: &UpstreamConfig{
			Issuer:       srv.URL + "/realms/test",
			ClientID:     testUpstreamClient,
			ClientSecret: "secret",
			RedirectURI:  "http://localhost:8080/oauth/callback",
		},
	}, storage)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/oauth/authorize?response_type=code&client_id=client-123&redirect_uri=http://localhost:8080/callback&state=mystate", http.NoBody)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d: %s", w.Code, w.Body.String())
	}
	loc, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parsing Location: %v", err)
	}
	if loc.Path != "/realms/test/protocol/openid-connect/auth" {
		t.Errorf("redirect path = %q, want discovered auth endpoint path", loc.Path)
	}
	srvURL, _ := url.Parse(srv.URL)
	if loc.Host != srvURL.Host {
		t.Errorf("redirect host = %q, want discovery server host %q", loc.Host, srvURL.Host)
	}
}

// TestBuildUpstreamAuthURL_PreservesEndpointQuery verifies that an authorization
// endpoint carrying its own query parameter (permitted by RFC 6749) is merged
// with, not clobbered by, the OAuth parameters.
func TestBuildUpstreamAuthURL_PreservesEndpointQuery(t *testing.T) {
	server, err := NewServer(ServerConfig{
		Issuer: "http://localhost:8080",
		Upstream: &UpstreamConfig{
			Issuer:                "http://keycloak:8180/realms/test",
			ClientID:              testUpstreamClient,
			ClientSecret:          "secret",
			RedirectURI:           "http://localhost:8080/oauth/callback",
			AuthorizationEndpoint: "https://idp.example.com/authorize?tenant=acme",
			TokenEndpoint:         "https://idp.example.com/token",
		},
	}, NewMemoryStorage())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	raw, err := server.buildUpstreamAuthURL(context.Background(), "test-state")
	if err != nil {
		t.Fatalf("buildUpstreamAuthURL: %v", err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("invalid URL: %v", err)
	}
	if u.Query().Get("tenant") != "acme" {
		t.Errorf("expected preserved tenant=acme, got %q", u.Query().Get("tenant"))
	}
	if u.Query().Get("state") != "test-state" {
		t.Errorf("expected state=test-state, got %q", u.Query().Get("state"))
	}
	if u.Query().Get("client_id") != testUpstreamClient {
		t.Errorf("expected client_id=%s, got %q", testUpstreamClient, u.Query().Get("client_id"))
	}
}

// TestUpstreamDiscovery_ExplicitAuthUsableWhileDiscoveryDown verifies that an
// explicitly configured authorization endpoint is returned even when the
// discovery document is unreachable — the authorize redirect does not depend on
// token-endpoint discovery.
func TestUpstreamDiscovery_ExplicitAuthUsableWhileDiscoveryDown(t *testing.T) {
	// Discovery always fails.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	r := newResolver(&UpstreamConfig{
		Issuer:                srv.URL + "/realms/test",
		AuthorizationEndpoint: "https://idp.example.com/authorize",
		// token_endpoint intentionally left to discovery.
	})

	authEP, err := r.authorizationEndpoint(context.Background())
	if err != nil {
		t.Fatalf("authorizationEndpoint should succeed from explicit config: %v", err)
	}
	if authEP != "https://idp.example.com/authorize" {
		t.Errorf("authorization endpoint = %q, want explicit value", authEP)
	}

	// The token endpoint, which relies on discovery, still fails.
	if _, err := r.tokenEndpoint(context.Background()); err == nil {
		t.Error("expected token endpoint to fail while discovery is down")
	}
}

// TestUpstreamDiscovery_ExplicitEndpointsBypassDiscovery verifies that when both
// endpoints are configured explicitly, no discovery request is made.
func TestUpstreamDiscovery_ExplicitEndpointsBypassDiscovery(t *testing.T) {
	srv, hits := newDiscoveryServer(t)

	r := newResolver(&UpstreamConfig{
		Issuer:                srv.URL + "/realms/test",
		AuthorizationEndpoint: "https://idp.example.com/authorize",
		TokenEndpoint:         "https://idp.example.com/token",
	})

	authEP, err := r.authorizationEndpoint(context.Background())
	if err != nil {
		t.Fatalf("authorizationEndpoint: %v", err)
	}
	tokenEP, err := r.tokenEndpoint(context.Background())
	if err != nil {
		t.Fatalf("tokenEndpoint: %v", err)
	}
	if authEP != "https://idp.example.com/authorize" {
		t.Errorf("authorization endpoint = %q, want explicit value", authEP)
	}
	if tokenEP != "https://idp.example.com/token" {
		t.Errorf("token endpoint = %q, want explicit value", tokenEP)
	}
	if got := atomic.LoadInt32(hits); got != 0 {
		t.Errorf("discovery hits = %d, want 0 (explicit config must bypass discovery)", got)
	}
}

// TestUpstreamDiscovery_PartialExplicitStillDiscovers verifies that configuring
// only one endpoint still triggers discovery to fill the other, and the
// explicit value wins for the one that was set.
func TestUpstreamDiscovery_PartialExplicitStillDiscovers(t *testing.T) {
	srv, hits := newDiscoveryServer(t)

	r := newResolver(&UpstreamConfig{
		Issuer:        srv.URL + "/realms/test",
		TokenEndpoint: "https://idp.example.com/token",
	})

	authEP, err := r.authorizationEndpoint(context.Background())
	if err != nil {
		t.Fatalf("authorizationEndpoint: %v", err)
	}
	tokenEP, err := r.tokenEndpoint(context.Background())
	if err != nil {
		t.Fatalf("tokenEndpoint: %v", err)
	}
	if want := srv.URL + "/realms/test/protocol/openid-connect/auth"; authEP != want {
		t.Errorf("authorization endpoint = %q, want discovered %q", authEP, want)
	}
	if tokenEP != "https://idp.example.com/token" {
		t.Errorf("token endpoint = %q, want explicit override", tokenEP)
	}
	if got := atomic.LoadInt32(hits); got != 1 {
		t.Errorf("discovery hits = %d, want 1", got)
	}
}

// TestUpstreamDiscovery_FailureIsRetryable verifies that a discovery failure at
// request time returns an error (not a hardcoded-path fallback) and is not
// cached: a subsequent successful fetch resolves normally.
func TestUpstreamDiscovery_FailureIsRetryable(t *testing.T) {
	var up atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !up.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		base := "http://" + r.Host + "/realms/test"
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"authorization_endpoint":"` + base + `/auth","token_endpoint":"` + base + `/token"}`))
	}))
	t.Cleanup(srv.Close)

	r := newResolver(&UpstreamConfig{Issuer: srv.URL + "/realms/test"})

	// First attempt: upstream is down -> error, nothing cached.
	if _, err := r.authorizationEndpoint(context.Background()); err == nil {
		t.Fatal("expected error while discovery endpoint is down")
	}

	// Recover the upstream; the next attempt must succeed (failure not cached).
	up.Store(true)
	authEP, err := r.authorizationEndpoint(context.Background())
	if err != nil {
		t.Fatalf("authorizationEndpoint after recovery: %v", err)
	}
	if want := srv.URL + "/realms/test/auth"; authEP != want {
		t.Errorf("authorization endpoint = %q, want %q", authEP, want)
	}
}

// TestUpstreamDiscovery_AuthorizeFailureIsServerError verifies that a discovery
// failure during /oauth/authorize surfaces as a retryable 500 server_error.
func TestUpstreamDiscovery_AuthorizeFailureIsServerError(t *testing.T) {
	// Point at a discovery server that always fails.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	storage := NewMemoryStorage()
	client := &Client{
		ClientID:     "client-123",
		RedirectURIs: []string{"http://localhost:8080/callback"},
		Active:       true,
		RequirePKCE:  false,
	}
	_ = storage.CreateClient(context.Background(), client)

	server, err := NewServer(ServerConfig{
		Issuer: "http://localhost:8080",
		Upstream: &UpstreamConfig{
			Issuer:       srv.URL + "/realms/test",
			ClientID:     testUpstreamClient,
			ClientSecret: "secret",
			RedirectURI:  "http://localhost:8080/oauth/callback",
		},
	}, storage)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/oauth/authorize?response_type=code&client_id=client-123&redirect_uri=http://localhost:8080/callback&state=mystate", http.NoBody)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 on discovery failure, got %d: %s", w.Code, w.Body.String())
	}
	var errResp ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("decoding error response: %v", err)
	}
	if errResp.Error != errServerError {
		t.Errorf("error code = %q, want %q", errResp.Error, errServerError)
	}
	// The internal issuer host and network-error detail must not leak to the
	// unauthenticated client.
	if strings.Contains(w.Body.String(), srv.URL) || strings.Contains(w.Body.String(), ".well-known") {
		t.Errorf("response body leaked internal discovery detail: %s", w.Body.String())
	}
	if errResp.ErrorDescription != errUpstreamUnavailable {
		t.Errorf("error_description = %q, want generic %q", errResp.ErrorDescription, errUpstreamUnavailable)
	}
}

// TestUpstreamDiscovery_MissingEndpointsInDoc verifies that a discovery document
// lacking a required endpoint yields a clear error rather than an empty URL.
func TestUpstreamDiscovery_MissingEndpointsInDoc(t *testing.T) {
	// Each endpoint is resolved independently, so the error surfaces on the
	// resolver for the endpoint the document omits.
	cases := []struct {
		name    string
		body    string
		resolve func(*upstreamEndpointResolver) (string, error)
		wantErr string
	}{
		{
			"missing authorization_endpoint",
			`{"token_endpoint":"https://idp/token"}`,
			func(r *upstreamEndpointResolver) (string, error) {
				return r.authorizationEndpoint(context.Background())
			},
			"authorization_endpoint not found",
		},
		{
			"missing token_endpoint",
			`{"authorization_endpoint":"https://idp/auth"}`,
			func(r *upstreamEndpointResolver) (string, error) { return r.tokenEndpoint(context.Background()) },
			"token_endpoint not found",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(srv.Close)

			r := newResolver(&UpstreamConfig{Issuer: srv.URL})
			_, err := tc.resolve(r)
			if err == nil {
				t.Fatal("expected error for incomplete discovery document")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want to contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestUpstreamDiscovery_FetchErrors covers the discovery fetch failure modes:
// an unbuildable request URL, an unreachable host, and an undecodable body. Each
// must surface as an error (token endpoint resolution included), never a
// hardcoded-path fallback.
func TestUpstreamDiscovery_FetchErrors(t *testing.T) {
	t.Run("invalid issuer URL", func(t *testing.T) {
		// A control character makes http.NewRequestWithContext fail to build.
		r := newResolver(&UpstreamConfig{Issuer: "http://example.com/\x7f"})
		if _, err := r.authorizationEndpoint(context.Background()); err == nil {
			t.Fatal("expected error for invalid issuer URL")
		}
	})

	t.Run("connection refused", func(t *testing.T) {
		// Port 1 is not listening: httpClient.Do returns a transport error.
		r := newResolver(&UpstreamConfig{Issuer: "http://127.0.0.1:1/realms/test"})
		if _, err := r.tokenEndpoint(context.Background()); err == nil {
			t.Fatal("expected error for unreachable discovery endpoint")
		}
	})

	t.Run("malformed JSON body", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{not json`))
		}))
		t.Cleanup(srv.Close)

		r := newResolver(&UpstreamConfig{Issuer: srv.URL})
		if _, err := r.authorizationEndpoint(context.Background()); err == nil {
			t.Fatal("expected error for malformed discovery document")
		}
	})
}

// TestExchangeUpstreamCode_DiscoveryFailure verifies the token exchange path
// surfaces a discovery failure as an error rather than posting to a hardcoded
// Keycloak token path.
func TestExchangeUpstreamCode_DiscoveryFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	server, err := NewServer(ServerConfig{
		Issuer: "http://localhost:8080",
		Upstream: &UpstreamConfig{
			Issuer:       srv.URL + "/realms/test",
			ClientID:     testUpstreamClient,
			ClientSecret: "secret",
			RedirectURI:  "http://localhost:8080/oauth/callback",
		},
	}, NewMemoryStorage())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	if _, err := server.exchangeUpstreamCode(context.Background(), "upstream-code"); err == nil {
		t.Fatal("expected error when token endpoint discovery fails")
	}
}

// TestUpstreamResolverNilGuards verifies the defensive guards that reject the
// brokered paths when no upstream resolver is configured.
func TestUpstreamResolverNilGuards(t *testing.T) {
	server, err := NewServer(ServerConfig{Issuer: "http://localhost:8080"}, NewMemoryStorage())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	if server.upstreamResolver != nil {
		t.Fatal("expected nil upstreamResolver when no upstream configured")
	}

	if _, err := server.buildUpstreamAuthURLWithPrompt(context.Background(), "state", true); err == nil {
		t.Error("expected error from buildUpstreamAuthURLWithPrompt with no resolver")
	}
	if _, err := server.exchangeUpstreamCode(context.Background(), "code"); err == nil {
		t.Error("expected error from exchangeUpstreamCode with no resolver")
	}
}

// TestUpstreamDiscovery_CallerContextCanceled verifies that resolveDocument
// honors the caller's context: when it is already canceled and the discovery
// fetch is in flight, the caller returns promptly with the context error rather
// than blocking on the detached fetch.
func TestUpstreamDiscovery_CallerContextCanceled(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		<-block // hold the request open until the test tears down
	}))
	// LIFO: unblock the handler before closing the server.
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(block) })

	r := newResolver(&UpstreamConfig{Issuer: srv.URL + "/realms/test"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already canceled

	if _, err := r.authorizationEndpoint(ctx); err == nil {
		t.Fatal("expected error when caller context is canceled")
	}
}

// TestUpstreamDiscovery_KeycloakFixture verifies the resolver parses a discovery
// document matching Keycloak's real shape (copied from a Keycloak realm), so the
// brokered flow keeps working against Keycloak via its published document.
func TestUpstreamDiscovery_KeycloakFixture(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("testdata", "keycloak-openid-configuration.json"))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/realms/myrealm"+oidcdiscovery.WellKnownPath {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(doc)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	r := newResolver(&UpstreamConfig{Issuer: srv.URL + "/realms/myrealm"})

	authEP, err := r.authorizationEndpoint(context.Background())
	if err != nil {
		t.Fatalf("authorizationEndpoint: %v", err)
	}
	tokenEP, err := r.tokenEndpoint(context.Background())
	if err != nil {
		t.Fatalf("tokenEndpoint: %v", err)
	}
	if want := "https://keycloak.example.com/realms/myrealm/protocol/openid-connect/auth"; authEP != want {
		t.Errorf("authorization endpoint = %q, want %q", authEP, want)
	}
	if want := "https://keycloak.example.com/realms/myrealm/protocol/openid-connect/token"; tokenEP != want {
		t.Errorf("token endpoint = %q, want %q", tokenEP, want)
	}
}
