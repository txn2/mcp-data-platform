package oauth

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/oidcdiscovery"
)

const (
	consentClientID = "dcr-client"
	consentRedirect = "https://claude.ai/api/mcp/auth_callback"
)

// consentUpstream is a mock IdP serving discovery and a token whose ID token
// names test@example.com.
func consentUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host + "/realms/test"
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/realms/test" + oidcdiscovery.WellKnownPath:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer":                 base,
				"authorization_endpoint": base + "/auth",
				"token_endpoint":         base + "/token",
			})
		case "/realms/test/token":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "upstream-access-token", "token_type": "Bearer", "expires_in": 3600,
				"id_token": createMockIDToken(map[string]any{"sub": "user-123", "email": "test@example.com"}),
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// consentServer is a server with DCR on and one dynamically registered client
// named name, plus the storage behind it.
func consentServer(t *testing.T, name string, branding ConsentBranding) (*Server, *MemoryStorage) {
	t.Helper()
	upstream := consentUpstream(t)
	storage := NewMemoryStorage()
	if err := storage.CreateClient(context.Background(), &Client{
		ClientID: consentClientID, Name: name, RedirectURIs: []string{consentRedirect},
		Active: true, DynamicallyRegistered: true,
	}); err != nil {
		t.Fatalf("CreateClient: %v", err)
	}
	server, err := NewServer(ServerConfig{
		Issuer: "https://mcp.example.com",
		DCR: DCRConfig{
			Enabled:                 true,
			AllowedRedirectPatterns: []string{`https://claude\.ai/api/mcp/auth_callback`},
		},
		Upstream: &UpstreamConfig{
			Issuer: upstream.URL + "/realms/test", ClientID: testUpstreamClient,
			ClientSecret: "secret", RedirectURI: "https://mcp.example.com/oauth/callback",
		},
		Branding: branding,
	}, storage)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return server, storage
}

// signIn runs the upstream callback for a pending authorization and returns
// the response.
func signIn(t *testing.T, server *Server) *httptest.ResponseRecorder {
	t.Helper()
	if err := server.stateStore.SaveState(context.Background(), "upstream-state", &AuthorizationState{
		ClientID: consentClientID, RedirectURI: consentRedirect, State: "client-state", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/oauth/callback?code=upstream-code&state=upstream-state", http.NoBody)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)
	return w
}

var consentIDPattern = regexp.MustCompile(`name="consent_id" value="([^"]+)"`)

// answer posts a decision for the page in page, carrying cookies.
func answer(t *testing.T, server *Server, page *httptest.ResponseRecorder, decision string, cookies []*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	m := consentIDPattern.FindStringSubmatch(page.Body.String())
	if m == nil {
		t.Fatalf("consent page has no consent_id: %s", page.Body.String())
	}
	form := url.Values{"consent_id": {m[1]}, "decision": {decision}}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, ConsentPath, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)
	return w
}

func TestConsent_SelfRegisteredClientIsAskedBeforeAnyCode(t *testing.T) {
	server, storage := consentServer(t, `Claude <script>alert(1)</script>`, ConsentBranding{})
	page := signIn(t, server)

	if page.Code != http.StatusOK {
		t.Fatalf("callback = %d, want the consent page: %s", page.Code, page.Body.String())
	}
	if loc := page.Header().Get("Location"); loc != "" {
		t.Fatalf("callback redirected before consent: %s", loc)
	}
	if n := len(storage.codes); n != 0 {
		t.Fatalf("%d authorization codes minted before consent", n)
	}
	h := page.Header()
	if h.Get("X-Frame-Options") != "DENY" {
		t.Errorf("X-Frame-Options = %q", h.Get("X-Frame-Options"))
	}
	csp := h.Get("Content-Security-Policy")
	for _, want := range []string{"frame-ancestors 'none'", "form-action 'self' https://claude.ai", "default-src 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q lacks %q", csp, want)
		}
	}
	if h.Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q", h.Get("Cache-Control"))
	}
	body := page.Body.String()
	for _, want := range []string{"test@example.com", "claude.ai", consentRedirect, "MCP Data Platform", "&lt;script&gt;"} {
		if !strings.Contains(body, want) {
			t.Errorf("consent page lacks %q", want)
		}
	}
	if strings.Contains(body, "<script>") {
		t.Error("the client's name was rendered unescaped")
	}
	cookies := page.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != consentCookieSecure || !cookies[0].HttpOnly || !cookies[0].Secure ||
		cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("consent cookie = %+v", cookies)
	}
	if strings.Contains(body, cookies[0].Value) {
		t.Error("the browser binding appears in the page")
	}
}

func TestConsent_AllowSendsTheCodeOnce(t *testing.T) {
	server, _ := consentServer(t, "Claude", ConsentBranding{})
	page := signIn(t, server)
	cookies := page.Result().Cookies()

	allowed := answer(t, server, page, consentAllow, cookies)
	if allowed.Code != http.StatusFound {
		t.Fatalf("allow = %d: %s", allowed.Code, allowed.Body.String())
	}
	loc, err := url.Parse(allowed.Header().Get("Location"))
	if err != nil || !strings.HasPrefix(loc.String(), consentRedirect+"?") {
		t.Fatalf("allow redirected to %q", allowed.Header().Get("Location"))
	}
	if loc.Query().Get("code") == "" || loc.Query().Get("state") != "client-state" {
		t.Fatalf("allow redirect query = %v", loc.Query())
	}

	again := answer(t, server, page, consentAllow, cookies)
	if again.Code != http.StatusBadRequest || again.Header().Get("Location") != "" {
		t.Fatalf("a second answer = %d %q, want the ended page", again.Code, again.Header().Get("Location"))
	}
	if !strings.Contains(again.Body.String(), "This request has ended") {
		t.Error("the second answer did not render the ended page")
	}
}

// TestConsent_AnAnswerClearsTheBindingCookie: the answer expires the binding
// cookie it was given, so the browser does not keep it.
func TestConsent_AnAnswerClearsTheBindingCookie(t *testing.T) {
	server, _ := consentServer(t, "Claude", ConsentBranding{})
	page := signIn(t, server)
	allowed := answer(t, server, page, consentAllow, page.Result().Cookies())
	var cleared *http.Cookie
	for _, c := range allowed.Result().Cookies() {
		if c.Name == consentCookieSecure {
			cleared = c
		}
	}
	if cleared == nil || cleared.Value != "" || cleared.MaxAge >= 0 {
		t.Fatalf("the answer's binding cookie = %+v, want it expired", cleared)
	}
}

// TestConsentExpired: a request exactly consentMaxAge old is still answerable,
// and one a nanosecond older is not.
func TestConsentExpired(t *testing.T) {
	created := time.Date(2026, 11, 21, 8, 0, 0, 0, time.UTC)
	if consentExpired(created, created.Add(consentMaxAge)) {
		t.Error("a request exactly consentMaxAge old was expired")
	}
	if !consentExpired(created, created.Add(consentMaxAge+time.Nanosecond)) {
		t.Error("a request past consentMaxAge was answerable")
	}
}

// TestSecureCookies: the binding cookie is TLS-only when the issuer is https
// or the request itself arrived over TLS.
func TestSecureCookies(t *testing.T) {
	for name, tc := range map[string]struct {
		issuer string
		tls    bool
		want   bool
	}{
		"https issuer":        {issuer: "https://platform.example.com", want: true},
		"http issuer":         {issuer: "http://localhost:8080"},
		"http issuer, on TLS": {issuer: "http://localhost:8080", tls: true, want: true},
	} {
		t.Run(name, func(t *testing.T) {
			s := &Server{config: ServerConfig{Issuer: tc.issuer}}
			r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, ConsentPath, http.NoBody)
			if tc.tls {
				r.TLS = &tls.ConnectionState{}
			}
			if got := s.secureCookies(r); got != tc.want {
				t.Errorf("secureCookies = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestConsent_DenySendsAccessDenied(t *testing.T) {
	server, storage := consentServer(t, "Claude", ConsentBranding{})
	page := signIn(t, server)

	denied := answer(t, server, page, consentDeny, page.Result().Cookies())
	if denied.Code != http.StatusFound {
		t.Fatalf("deny = %d", denied.Code)
	}
	loc, _ := url.Parse(denied.Header().Get("Location"))
	if loc.Query().Get("error") != "access_denied" || loc.Query().Get("state") != "client-state" || loc.Query().Get("code") != "" {
		t.Fatalf("deny redirect query = %v", loc.Query())
	}
	if n := len(storage.codes); n != 0 {
		t.Fatalf("deny minted %d codes", n)
	}
}

func TestConsent_RefusedFromAnotherBrowserAndConsumed(t *testing.T) {
	server, storage := consentServer(t, "Claude", ConsentBranding{})
	page := signIn(t, server)

	for name, cookies := range map[string][]*http.Cookie{
		"no cookie":    nil,
		"wrong cookie": {{Name: consentCookieSecure, Value: "not-the-binding", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode}},
	} {
		t.Run(name, func(t *testing.T) {
			got := answer(t, server, page, consentAllow, cookies)
			if got.Code != http.StatusBadRequest || got.Header().Get("Location") != "" {
				t.Fatalf("answer = %d %q, want the ended page", got.Code, got.Header().Get("Location"))
			}
		})
	}
	// The first refused answer consumed the request: the right cookie is too late.
	if got := answer(t, server, page, consentAllow, page.Result().Cookies()); got.Code != http.StatusBadRequest {
		t.Fatalf("answer after a refused one = %d", got.Code)
	}
	if n := len(storage.codes); n != 0 {
		t.Fatalf("%d codes minted", n)
	}
}

func TestConsent_ExpiredAndMalformedAnswers(t *testing.T) {
	server, _ := consentServer(t, "Claude", ConsentBranding{})
	page := signIn(t, server)
	id := consentIDPattern.FindStringSubmatch(page.Body.String())[1]
	pending, err := server.stateStore.GetState(context.Background(), consentKeyPrefix+id)
	if err != nil {
		t.Fatalf("pending consent not stored: %v", err)
	}
	if pending.UserID != "user-123" || pending.ConsentBinding == "" {
		t.Fatalf("pending consent = %+v", pending)
	}
	pending.CreatedAt = time.Now().Add(-consentMaxAge - time.Minute)
	_ = server.stateStore.SaveState(context.Background(), consentKeyPrefix+id, pending)
	if got := answer(t, server, page, consentAllow, page.Result().Cookies()); got.Code != http.StatusBadRequest {
		t.Fatalf("expired answer = %d", got.Code)
	}

	page2 := signIn(t, server)
	if got := answer(t, server, page2, "maybe", page2.Result().Cookies()); got.Code != http.StatusBadRequest {
		t.Fatalf("unknown decision = %d", got.Code)
	}

	for _, req := range []*http.Request{
		httptest.NewRequestWithContext(context.Background(), http.MethodGet, ConsentPath, http.NoBody),
	} {
		w := httptest.NewRecorder()
		server.ServeHTTP(w, req)
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET %s = %d", ConsentPath, w.Code)
		}
	}
	empty := httptest.NewRequestWithContext(context.Background(), http.MethodPost, ConsentPath, strings.NewReader(""))
	empty.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	server.ServeHTTP(w, empty)
	if w.Code != http.StatusBadRequest {
		t.Errorf("empty form = %d", w.Code)
	}
}

func TestConsent_ConfiguredClientIsNotAsked(t *testing.T) {
	server, storage := consentServer(t, "Claude", ConsentBranding{})
	client, _ := storage.GetClient(context.Background(), consentClientID)
	client.DynamicallyRegistered = false
	page := signIn(t, server)
	if page.Code != http.StatusFound || !strings.Contains(page.Header().Get("Location"), "code=") {
		t.Fatalf("configured client = %d %q, want the code redirect", page.Code, page.Header().Get("Location"))
	}
}

func TestConsent_Branding(t *testing.T) {
	server, _ := consentServer(t, "", ConsentBranding{
		Title: "Plexara", LogoLight: "https://cdn.example.net/light.svg", LogoDark: "/portal/brand/dark.svg",
	})
	page := signIn(t, server)
	body := page.Body.String()
	for _, want := range []string{"Plexara", "https://cdn.example.net/light.svg", "/portal/brand/dark.svg", "No name given"} {
		if !strings.Contains(body, want) {
			t.Errorf("branded page lacks %q", want)
		}
	}
	if strings.Contains(body, "MCP Data Platform") {
		t.Error("branded page shows the default title")
	}
	if csp := page.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "img-src 'self' https://cdn.example.net") {
		t.Errorf("CSP %q does not admit the logo's origin", csp)
	}
}

func TestConsent_AuthorizeRefusesARegisteredRedirectThePolicyNoLongerAllows(t *testing.T) {
	server, storage := consentServer(t, "Claude", ConsentBranding{})
	client, _ := storage.GetClient(context.Background(), consentClientID)
	client.RedirectURIs = append(client.RedirectURIs, "https://evil.example/x.anthropic.com/callback")
	req := AuthorizationRequest{ResponseType: paramCode, ClientID: consentClientID, RedirectURI: "https://evil.example/x.anthropic.com/callback"}
	if _, err := server.validateAuthorizationRequest(context.Background(), req); err == nil {
		t.Fatal("a self-registered client's redirect outside the policy was accepted")
	}
	req.RedirectURI = consentRedirect
	if _, err := server.validateAuthorizationRequest(context.Background(), req); err != nil {
		t.Fatalf("an allowed redirect was refused: %v", err)
	}
	server.dcr = nil
	if _, err := server.validateAuthorizationRequest(context.Background(), req); err == nil {
		t.Fatal("with registration off, a self-registered client was accepted")
	}
}

func TestDescribeRedirectAndOrigin(t *testing.T) {
	cases := []struct{ uri, dest, origin string }{
		{"https://claude.ai/cb", "claude.ai", "https://claude.ai"},
		{"http://localhost:53682/callback", "An application on this computer", "http://localhost:53682"},
		{"com.example.app:/callback", "An application on this device (com.example.app)", "com.example.app:"},
	}
	for _, c := range cases {
		if dest, full := describeRedirect(c.uri); dest != c.dest || full != c.uri {
			t.Errorf("describeRedirect(%q) = %q, %q", c.uri, dest, full)
		}
		if o := redirectOrigin(c.uri); o != c.origin {
			t.Errorf("redirectOrigin(%q) = %q, want %q", c.uri, o, c.origin)
		}
	}
	if o := redirectOrigin("/relative/logo.svg"); o != "" {
		t.Errorf("a relative URL has origin %q", o)
	}
	if o := redirectOrigin("https://x.example/a;b"); o != "" {
		t.Errorf("a URL with a CSP separator has origin %q", o)
	}
	if dest, _ := describeRedirect("::bad"); dest != "::bad" {
		t.Errorf("unparseable redirect = %q", dest)
	}
	if got := signedInAs("sub-1", map[string]any{"name": "Dana"}); got != "Dana" {
		t.Errorf("signedInAs fell to %q", got)
	}
	if got := signedInAs("sub-1", nil); got != "sub-1" {
		t.Errorf("signedInAs with no claims = %q", got)
	}
	if consentCookieName(false) != consentCookie {
		t.Error("an http issuer uses the __Host- cookie")
	}
}

// failingClients is storage whose client read fails after the first call.
type failingClients struct {
	*MemoryStorage
	reads int
}

func (f *failingClients) GetClient(ctx context.Context, id string) (*Client, error) {
	f.reads++
	if f.reads > 1 {
		return nil, ErrStorageFailure
	}
	return f.MemoryStorage.GetClient(ctx, id)
}

// TestConsent_AFailedClientReadNeverSkipsThePage: a client the store cannot
// read at the callback is not handed a code without the page.
func TestConsent_AFailedClientReadNeverSkipsThePage(t *testing.T) {
	server, storage := consentServer(t, "Claude", ConsentBranding{})
	server.storage = &failingClients{MemoryStorage: storage, reads: 1}
	page := signIn(t, server)
	if page.Code == http.StatusFound || strings.Contains(page.Header().Get("Location"), "code=") {
		t.Fatalf("a code was sent when the client could not be read: %d %q", page.Code, page.Header().Get("Location"))
	}
	if n := len(storage.codes); n != 0 {
		t.Fatalf("%d codes minted", n)
	}
}

// refusingConsentStore saves the IdP state and refuses the consent request.
type refusingConsentStore struct{ StateStore }

func (r refusingConsentStore) SaveState(ctx context.Context, key string, st *AuthorizationState) error {
	if strings.HasPrefix(key, consentKeyPrefix) {
		return ErrStorageFailure
	}
	return r.StateStore.SaveState(ctx, key, st) //nolint:wrapcheck // the wrapped store's own error
}

func TestConsent_ARequestThatCannotBeHeldSendsNoPage(t *testing.T) {
	server, storage := consentServer(t, "Claude", ConsentBranding{})
	server.stateStore = refusingConsentStore{StateStore: server.stateStore}
	page := signIn(t, server)
	if page.Code != http.StatusInternalServerError || strings.Contains(page.Body.String(), "consent_id") {
		t.Fatalf("a consent request that could not be saved answered %d", page.Code)
	}
	if n := len(storage.codes); n != 0 {
		t.Fatalf("%d codes minted", n)
	}
	if got := page.Result().Cookies(); len(got) != 0 {
		t.Fatalf("a cookie was set for a page that was not served: %v", got)
	}
}
