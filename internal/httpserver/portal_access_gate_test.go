package httpserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/browsersession"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// Keys and roles for the assembled-chain tests below. The unmapped key is the
// account this gate exists for: a credential the platform accepts, carrying a
// role no persona names.
const (
	gateMappedKey     = "test-key-mapped"
	gateUnmappedKey   = "test-key-unmapped"
	gateMappedRole    = "dp_analyst"
	gateUnmappedRole  = "dp_nothing"
	gateKnowledgePath = "/api/v1/portal/knowledge-pages"

	// testJWKSModulus is a base64url RSA modulus for the stub identity
	// provider's key set. It is public-key material for a throwaway key and
	// verifies nothing in these tests.
	testJWKSModulus = "0vx7agoebGcQSuuPiLJXZptN9nndrQmbXEps2aiAFbWhM78LhWx4cbbfAAtVT86zwu1RK7aPFFxuhDR1L6tSoc_BJECPebWKRXjBZCiFV4n3oknjhMstn64tZ_2W-5JsGY4Hc5n9yBXArwl93lqt7_RN5w6Cf0h4QyQ5v-65YGjQR0_FDW2QvzqY368QQMicAtaSqzs8KJZgnYb9c7d0zgdAZHzu6qMQvRL5hajrn1n91CbOpbISD08qNLyrdkt-bFTWhAI4vMQFh6WeZu0fM4lFd2NcRwr3XPksINHaQ-G_xBniIqbw0Ls1jF44-csFCur-kEgU8awapJzKnqDKgw"
)

// refusingKnowledgeStore fails the test if the portal ever reaches it. Every
// method is present to satisfy knowledgepage.Store; the assertion is that none
// of them runs, because the gate refuses the request first.
type refusingKnowledgeStore struct{ t *testing.T }

func (s refusingKnowledgeStore) reached(method string) {
	s.t.Helper()
	s.t.Errorf("knowledge page store reached via %s: the request was not gated", method)
}

func (s refusingKnowledgeStore) Insert(context.Context, knowledgepage.Page) error {
	s.reached("Insert")
	return nil
}

func (s refusingKnowledgeStore) Get(context.Context, string) (*knowledgepage.Page, error) {
	s.reached("Get")
	return nil, knowledgepage.ErrNotFound
}

func (s refusingKnowledgeStore) GetBySlug(context.Context, string) (*knowledgepage.Page, error) {
	s.reached("GetBySlug")
	return nil, knowledgepage.ErrNotFound
}

func (s refusingKnowledgeStore) List(context.Context, knowledgepage.Filter) ([]knowledgepage.Page, int, error) {
	s.reached("List")
	return nil, 0, nil
}

func (s refusingKnowledgeStore) Update(context.Context, string, knowledgepage.Update) error {
	s.reached("Update")
	return nil
}

func (s refusingKnowledgeStore) SoftDelete(context.Context, string) error {
	s.reached("SoftDelete")
	return nil
}

func (s refusingKnowledgeStore) ListVersions(context.Context, string, int, int) ([]knowledgepage.Version, int, error) {
	s.reached("ListVersions")
	return nil, 0, nil
}

func (s refusingKnowledgeStore) GetVersion(context.Context, string, int) (*knowledgepage.Version, error) {
	s.reached("GetVersion")
	return nil, knowledgepage.ErrNotFound
}

func (s refusingKnowledgeStore) ListEntityRefs(context.Context, string) ([]knowledgepage.EntityRef, error) {
	s.reached("ListEntityRefs")
	return nil, nil
}

func (s refusingKnowledgeStore) ValidateRefTargets(context.Context, []knowledgepage.EntityRef) error {
	s.reached("ValidateRefTargets")
	return nil
}

func (s refusingKnowledgeStore) FilterExistingRefTargets(context.Context, []knowledgepage.EntityRef) ([]knowledgepage.EntityRef, error) {
	s.reached("FilterExistingRefTargets")
	return nil, nil
}

func (s refusingKnowledgeStore) AddEntityRefs(context.Context, string, []knowledgepage.EntityRef) error {
	s.reached("AddEntityRefs")
	return nil
}

func (s refusingKnowledgeStore) ReplaceEntityRefs(context.Context, string, []knowledgepage.EntityRef) error {
	s.reached("ReplaceEntityRefs")
	return nil
}

func (s refusingKnowledgeStore) ReplaceEntityRefsBySource(context.Context, string, string, []knowledgepage.EntityRef) error {
	s.reached("ReplaceEntityRefsBySource")
	return nil
}

func (s refusingKnowledgeStore) ListPagesReferencing(context.Context, knowledgepage.EntityRef) ([]knowledgepage.PageRef, error) {
	s.reached("ListPagesReferencing")
	return nil, nil
}

// countingKnowledgeStore is the same store for the allow case: it answers List
// and records that the handler was actually reached.
type countingKnowledgeStore struct {
	refusingKnowledgeStore
	listed bool
}

func (s *countingKnowledgeStore) List(context.Context, knowledgepage.Filter) ([]knowledgepage.Page, int, error) {
	s.listed = true
	return []knowledgepage.Page{}, 0, nil
}

// gatePlatform builds a platform whose API keys carry a mapped and an unmapped
// role, with one persona claiming only the mapped role.
func gatePlatform(t *testing.T) *platform.Platform {
	t.Helper()
	p := newTestPlatform(t, &platform.Config{
		Server: platform.ServerConfig{Name: "gate-test"},
		Portal: platform.PortalConfig{Enabled: new(true)},
		Auth: platform.AuthConfig{
			APIKeys: platform.APIKeyAuthConfig{
				Enabled: true,
				Keys: []platform.APIKeyDef{
					{Key: gateMappedKey, Name: "mapped", Email: "mapped@example.com", Roles: []string{gateMappedRole}},
					{Key: gateUnmappedKey, Name: "unmapped", Email: "unmapped@example.com", Roles: []string{gateUnmappedRole}},
				},
			},
		},
		Personas: platform.PersonasConfig{
			Definitions: map[string]platform.PersonaDef{
				"analyst": {
					DisplayName: "Data Analyst",
					Roles:       []string{gateMappedRole},
					Tools:       platform.ToolRulesDef{Allow: []string{"*"}},
				},
			},
		},
	})
	t.Cleanup(func() { _ = p.Close() })
	return p
}

// gateHandler assembles the portal handler exactly as mountPortalAPI does: the
// real authenticator, the real RequirePortalAuth, and the real gate.
func gateHandler(t *testing.T, p *platform.Platform, store knowledgepage.Store) http.Handler {
	t.Helper()
	deps := portal.Deps{
		KnowledgePageStore: store,
		PersonaResolver:    buildPersonaResolver(p.PersonaRegistry(), registry.NewRegistry()),
	}
	portalAuth := portal.NewAuthenticator(p.Authenticator())
	return portal.NewHandler(deps, portalAuthChain(portalAuth, portalAccessGate(p, deps.PersonaResolver)))
}

// TestPortalChain_UnmappedRoleCannotReadKnowledgePages is the acceptance test
// for the hole this change closes: an authenticated caller whose roles match no
// persona is refused before the knowledge-page handler runs. Org-shared
// knowledge pages are readable by any authenticated caller by design, so the
// persona gate is the only thing standing between a claim-less account and all
// of them.
func TestPortalChain_UnmappedRoleCannotReadKnowledgePages(t *testing.T) {
	p := gatePlatform(t)
	handler := gateHandler(t, p, refusingKnowledgeStore{t: t})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, gateKnowledgePath, http.NoBody)
	req.Header.Set("X-API-Key", gateUnmappedKey)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("Content-Type = %q, want application/problem+json", ct)
	}
	if !strings.Contains(rec.Body.String(), "unmapped@example.com") {
		t.Errorf("refusal did not name the refused account: %s", rec.Body.String())
	}
}

// The same chain must still admit a caller whose role a persona claims,
// otherwise the gate is just an outage.
func TestPortalChain_MappedRoleReadsKnowledgePages(t *testing.T) {
	p := gatePlatform(t)
	store := &countingKnowledgeStore{refusingKnowledgeStore: refusingKnowledgeStore{t: t}}
	handler := gateHandler(t, p, store)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, gateKnowledgePath, http.NoBody)
	req.Header.Set("X-API-Key", gateMappedKey)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !store.listed {
		t.Error("the knowledge page handler did not run for a mapped caller")
	}
}

// An unauthenticated caller is still 401, not 403: the gate must not turn
// "who are you" into "you are refused", which would send the SPA to its
// access-denied screen instead of the sign-in form.
func TestPortalChain_NoCredentialsIsUnauthorized(t *testing.T) {
	p := gatePlatform(t)
	handler := gateHandler(t, p, refusingKnowledgeStore{t: t})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, gateKnowledgePath, http.NoBody)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
}

// The registry's role matching is what the gate consults, so a persona added
// after boot (the admin API's hot path) admits its role without a restart.
func TestPortalAccessGate_FollowsRegistryChanges(t *testing.T) {
	p := gatePlatform(t)
	resolver := buildPersonaResolver(p.PersonaRegistry(), registry.NewRegistry())
	gate := portalAccessGate(p, resolver)

	if gate.Allows([]string{gateUnmappedRole}) {
		t.Fatal("gate admitted an unmapped role before its persona existed")
	}
	if err := p.PersonaRegistry().Register(&persona.Persona{
		Name:  "late",
		Roles: []string{gateUnmappedRole},
	}); err != nil {
		t.Fatalf("registering persona: %v", err)
	}
	if !gate.Allows([]string{gateUnmappedRole}) {
		t.Error("gate did not admit a role a newly registered persona claims")
	}
}

// gateSPAShell is what stops a signed-in-but-unmapped person from being handed
// an application shell whose every request will 403. Without a cookie
// authenticator there is no one to identify, so the shell stays unwrapped.
func TestGateSPAShell_NoBrowserAuthLeavesShellUnwrapped(t *testing.T) {
	p := gatePlatform(t) // API keys only; no browser session configured
	spa := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	if got := gateSPAShell(p, spa); got != nil {
		t.Error("gateSPAShell wrapped the shell with no cookie authenticator to identify callers")
	}
}

// fakeOIDCDiscovery serves the minimal discovery document browsersession's
// NewFlow fetches at construction, so a platform with a cookie authenticator
// can be built without a real identity provider.
func fakeOIDCDiscovery(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var serverURL string
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer":                 serverURL,
			"authorization_endpoint": serverURL + "/auth",
			"token_endpoint":         serverURL + "/token",
			"end_session_endpoint":   serverURL + "/logout",
			"userinfo_endpoint":      serverURL + "/userinfo",
			"jwks_uri":               serverURL + "/jwks",
		})
	})
	// The token authenticator fetches the key set at construction and rejects
	// an empty one, so serve a well-formed RSA public key. No test here
	// presents a bearer token, so the key is never used to verify anything.
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"keys":[{"kty":"RSA","kid":"k1","use":"sig","n":"` + testJWKSModulus + `","e":"AQAB"}]}`))
	})
	srv := httptest.NewServer(mux)
	serverURL = srv.URL
	t.Cleanup(srv.Close)
	return srv
}

// With browser auth configured the shell is wrapped, and a visitor with no
// cookie still reaches it: they have to, to be sent to the identity provider.
func TestGateSPAShell_UnauthenticatedVisitorReachesShell(t *testing.T) {
	p, _ := spaGatePlatform(t)
	if p.BrowserSessionAuth() == nil {
		t.Fatal("browser session authenticator was not wired")
	}

	served := false
	spa := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		served = true
		w.WriteHeader(http.StatusOK)
	})
	gated := gateSPAShell(p, spa)
	if gated == nil {
		t.Fatal("gateSPAShell returned nil with browser auth configured")
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/portal/", http.NoBody)
	req.Header.Set("Accept", "text/html")
	rec := httptest.NewRecorder()
	gated.ServeHTTP(rec, req)

	if !served {
		t.Error("a visitor with no session cookie was refused the shell and cannot reach sign-in")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

// The state the user hit: a real, valid session cookie for an account carrying
// no role any persona names. That browser must get the branded refusal, not an
// application shell it can navigate.
func TestGateSPAShell_UnmappedSessionGetsBrandedPage(t *testing.T) {
	p, cookieKey := spaGatePlatform(t)

	served := false
	gated := gateSPAShell(p, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		served = true
		w.WriteHeader(http.StatusOK)
	}))
	if gated == nil {
		t.Fatal("gateSPAShell returned nil with browser auth configured")
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/portal/", http.NoBody)
	req.Header.Set("Accept", "text/html")
	req.AddCookie(sessionCookie(t, cookieKey, "unmapped@example.com", []string{gateUnmappedRole}))
	rec := httptest.NewRecorder()
	gated.ServeHTTP(rec, req)

	if served {
		t.Error("an unmapped session was handed the portal shell")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "unmapped@example.com") {
		t.Errorf("page did not name the refused account: %s", body)
	}
	if !strings.Contains(body, "/portal/auth/logout") {
		t.Error("page offered no way to switch accounts")
	}
}

// The same cookie carrying a mapped role reaches the shell, so the gate is not
// simply refusing every session.
func TestGateSPAShell_MappedSessionReachesShell(t *testing.T) {
	p, cookieKey := spaGatePlatform(t)

	served := false
	gated := gateSPAShell(p, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		served = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/portal/", http.NoBody)
	req.Header.Set("Accept", "text/html")
	req.AddCookie(sessionCookie(t, cookieKey, "mapped@example.com", []string{gateMappedRole}))
	rec := httptest.NewRecorder()
	gated.ServeHTTP(rec, req)

	if !served {
		t.Error("a mapped session was refused the portal shell")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

// spaGatePlatform builds a platform with a cookie authenticator and one persona
// claiming gateMappedRole, returning the raw cookie signing key so a test can
// mint sessions the platform will accept.
func spaGatePlatform(t *testing.T) (p *platform.Platform, cookieKey []byte) {
	t.Helper()
	srv := fakeOIDCDiscovery(t)
	rawKey := []byte("0123456789abcdef0123456789abcdef")
	p = newTestPlatform(t, &platform.Config{
		Server: platform.ServerConfig{Name: "spa-gate-test"},
		Portal: platform.PortalConfig{Enabled: new(true), PublicBaseURL: "https://portal.example"},
		Auth: platform.AuthConfig{
			OIDC: platform.OIDCAuthConfig{Enabled: true, Issuer: srv.URL, ClientID: "c"},
			BrowserSession: platform.BrowserSessionConfig{
				Enabled:    true,
				CookieName: spaGateCookieName,
				SigningKey: base64.StdEncoding.EncodeToString(rawKey),
			},
		},
		Personas: platform.PersonasConfig{
			Definitions: map[string]platform.PersonaDef{
				"analyst": {Roles: []string{gateMappedRole}, Tools: platform.ToolRulesDef{Allow: []string{"*"}}},
			},
		},
	})
	t.Cleanup(func() { _ = p.Close() })
	return p, rawKey
}

const spaGateCookieName = "sess"

// sessionCookie mints a session cookie the platform's cookie authenticator
// accepts, signed with the same key the platform was configured with.
func sessionCookie(t *testing.T, key []byte, email string, roles []string) *http.Cookie {
	t.Helper()
	cfg := &browsersession.CookieConfig{Name: spaGateCookieName, Key: key}
	signed, err := browsersession.SignSession(browsersession.SessionClaims{
		UserID: email,
		Email:  email,
		Roles:  roles,
	}, cfg)
	if err != nil {
		t.Fatalf("signing session: %v", err)
	}
	return &http.Cookie{Name: spaGateCookieName, Value: signed}
}
