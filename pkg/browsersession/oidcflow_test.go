package browsersession

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// mockOIDCProvider creates a test OIDC server that returns discovery and token endpoints.
func mockOIDCProvider(t *testing.T, idTokenClaims map[string]any) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	var serverURL string

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"authorization_endpoint": serverURL + "/auth",
			"token_endpoint":         serverURL + "/token",
			"end_session_endpoint":   serverURL + "/logout",
			"userinfo_endpoint":      serverURL + "/userinfo",
		})
	})

	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Build a fake id_token (unsigned for testing)
		header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
		payload, _ := json.Marshal(idTokenClaims)
		payloadB64 := base64.RawURLEncoding.EncodeToString(payload)
		idToken := header + "." + payloadB64 + ".fakesig"

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "access-token-value",
			"id_token":     idToken,
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	})

	srv := httptest.NewServer(mux)
	serverURL = srv.URL
	return srv
}

func testFlowConfig(serverURL string) FlowConfig {
	return FlowConfig{
		Issuer:       serverURL,
		ClientID:     "test-client",
		ClientSecret: "test-secret",
		RedirectURI:  serverURL + "/portal/auth/callback",
		Scopes:       []string{"openid", "profile", "email"},
		RoleClaim:    "realm_access.roles",
		RolePrefix:   "dp_",
		Cookie:       CookieConfig{Key: testKey(), TTL: time.Hour},
		PostLoginRedirect: "/portal/",
	}
}

func TestNewFlowDiscovery(t *testing.T) {
	srv := mockOIDCProvider(t, nil)
	defer srv.Close()

	cfg := testFlowConfig(srv.URL)
	cfg.HTTPClient = srv.Client()

	flow, err := NewFlow(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewFlow: %v", err)
	}

	if flow.endpoints.AuthorizationEndpoint == "" {
		t.Error("expected AuthorizationEndpoint to be discovered")
	}
	if flow.endpoints.TokenEndpoint == "" {
		t.Error("expected TokenEndpoint to be discovered")
	}
	if flow.endpoints.EndSessionEndpoint == "" {
		t.Error("expected EndSessionEndpoint to be discovered")
	}
}

func TestNewFlowValidation(t *testing.T) {
	key := testKey()
	tests := []struct {
		name string
		cfg  FlowConfig
	}{
		{"missing issuer", FlowConfig{ClientID: "c", RedirectURI: "u", Cookie: CookieConfig{Key: key}}},
		{"missing client_id", FlowConfig{Issuer: "i", RedirectURI: "u", Cookie: CookieConfig{Key: key}}},
		{"missing redirect_uri", FlowConfig{Issuer: "i", ClientID: "c", Cookie: CookieConfig{Key: key}}},
		{"missing signing key", FlowConfig{Issuer: "i", ClientID: "c", RedirectURI: "u"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewFlow(context.Background(), tt.cfg)
			if err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestLoginHandler(t *testing.T) {
	srv := mockOIDCProvider(t, nil)
	defer srv.Close()

	cfg := testFlowConfig(srv.URL)
	cfg.HTTPClient = srv.Client()

	flow, err := NewFlow(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewFlow: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/portal/auth/login", http.NoBody)
	w := httptest.NewRecorder()

	flow.LoginHandler(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusFound)
	}

	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, srv.URL+"/auth") {
		t.Errorf("redirect location = %q, should start with auth endpoint", loc)
	}

	// Verify redirect URL has required params
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("parsing redirect URL: %v", err)
	}
	q := u.Query()
	if q.Get("response_type") != "code" {
		t.Error("missing response_type=code")
	}
	if q.Get("client_id") != "test-client" {
		t.Error("missing client_id")
	}
	if q.Get("code_challenge_method") != "S256" {
		t.Error("missing code_challenge_method=S256")
	}
	if q.Get("code_challenge") == "" {
		t.Error("missing code_challenge")
	}
	if q.Get("state") == "" {
		t.Error("missing state")
	}

	// Verify state cookie was set
	var stateCookieFound bool
	for _, c := range resp.Cookies() {
		if c.Name == stateCookieName {
			stateCookieFound = true
			if !c.HttpOnly {
				t.Error("state cookie should be HttpOnly")
			}
		}
	}
	if !stateCookieFound {
		t.Error("state cookie not set")
	}
}

func TestCallbackHandler(t *testing.T) {
	claims := map[string]any{
		"sub":   "user-42",
		"email": "user@example.com",
		"realm_access": map[string]any{
			"roles": []any{"dp_admin", "dp_analyst", "other"},
		},
	}
	srv := mockOIDCProvider(t, claims)
	defer srv.Close()

	cfg := testFlowConfig(srv.URL)
	cfg.HTTPClient = srv.Client()

	flow, err := NewFlow(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewFlow: %v", err)
	}

	// First, simulate the login to get state cookie
	loginReq := httptest.NewRequest(http.MethodGet, "/portal/auth/login", http.NoBody)
	loginW := httptest.NewRecorder()
	flow.LoginHandler(loginW, loginReq)

	loginResp := loginW.Result()
	loc, _ := url.Parse(loginResp.Header.Get("Location"))
	state := loc.Query().Get("state")

	// Get state cookie
	var stateCookie *http.Cookie
	for _, c := range loginResp.Cookies() {
		if c.Name == stateCookieName {
			stateCookie = c
		}
	}
	if stateCookie == nil {
		t.Fatal("no state cookie from login")
	}

	// Simulate callback with code and state
	callbackURL := "/portal/auth/callback?code=auth-code&state=" + state
	callbackReq := httptest.NewRequest(http.MethodGet, callbackURL, http.NoBody)
	callbackReq.AddCookie(stateCookie)
	callbackW := httptest.NewRecorder()

	flow.CallbackHandler(callbackW, callbackReq)

	callbackResp := callbackW.Result()
	if callbackResp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want %d", callbackResp.StatusCode, http.StatusFound)
	}

	if callbackResp.Header.Get("Location") != "/portal/" {
		t.Errorf("redirect = %q, want /portal/", callbackResp.Header.Get("Location"))
	}

	// Verify session cookie was set
	var sessionCookie *http.Cookie
	for _, c := range callbackResp.Cookies() {
		if c.Name == DefaultCookieName {
			sessionCookie = c
		}
	}
	if sessionCookie == nil {
		t.Fatal("session cookie not set")
	}

	// Verify session cookie content
	sessionClaims, err := VerifySession(sessionCookie.Value, cfg.Cookie.Key)
	if err != nil {
		t.Fatalf("VerifySession: %v", err)
	}
	if sessionClaims.UserID != "user-42" {
		t.Errorf("UserID = %q, want %q", sessionClaims.UserID, "user-42")
	}
	if sessionClaims.Email != "user@example.com" {
		t.Errorf("Email = %q, want %q", sessionClaims.Email, "user@example.com")
	}
	// Roles should be stripped of dp_ prefix
	if len(sessionClaims.Roles) != 2 || sessionClaims.Roles[0] != "admin" || sessionClaims.Roles[1] != "analyst" {
		t.Errorf("Roles = %v, want [admin analyst]", sessionClaims.Roles)
	}
}

func TestCallbackHandlerErrorParam(t *testing.T) {
	srv := mockOIDCProvider(t, nil)
	defer srv.Close()

	cfg := testFlowConfig(srv.URL)
	cfg.HTTPClient = srv.Client()

	flow, err := NewFlow(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewFlow: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet,
		"/portal/auth/callback?error=access_denied&error_description=user+cancelled", http.NoBody)
	w := httptest.NewRecorder()

	flow.CallbackHandler(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestCallbackHandlerMissingCode(t *testing.T) {
	srv := mockOIDCProvider(t, nil)
	defer srv.Close()

	cfg := testFlowConfig(srv.URL)
	cfg.HTTPClient = srv.Client()

	flow, err := NewFlow(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewFlow: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/portal/auth/callback?state=abc", http.NoBody)
	w := httptest.NewRecorder()

	flow.CallbackHandler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestCallbackHandlerMissingStateCookie(t *testing.T) {
	srv := mockOIDCProvider(t, nil)
	defer srv.Close()

	cfg := testFlowConfig(srv.URL)
	cfg.HTTPClient = srv.Client()

	flow, err := NewFlow(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewFlow: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/portal/auth/callback?code=abc&state=xyz", http.NoBody)
	w := httptest.NewRecorder()

	flow.CallbackHandler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestCallbackHandlerStateMismatch(t *testing.T) {
	srv := mockOIDCProvider(t, nil)
	defer srv.Close()

	cfg := testFlowConfig(srv.URL)
	cfg.HTTPClient = srv.Client()

	flow, err := NewFlow(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewFlow: %v", err)
	}

	// Login to get a valid state cookie
	loginReq := httptest.NewRequest(http.MethodGet, "/portal/auth/login", http.NoBody)
	loginW := httptest.NewRecorder()
	flow.LoginHandler(loginW, loginReq)

	var stateCookie *http.Cookie
	for _, c := range loginW.Result().Cookies() {
		if c.Name == stateCookieName {
			stateCookie = c
		}
	}

	// Callback with a different state value
	req := httptest.NewRequest(http.MethodGet, "/portal/auth/callback?code=abc&state=wrong", http.NoBody)
	req.AddCookie(stateCookie)
	w := httptest.NewRecorder()

	flow.CallbackHandler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestLogoutHandler(t *testing.T) {
	srv := mockOIDCProvider(t, nil)
	defer srv.Close()

	cfg := testFlowConfig(srv.URL)
	cfg.HTTPClient = srv.Client()

	flow, err := NewFlow(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewFlow: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/portal/auth/logout", http.NoBody)
	w := httptest.NewRecorder()

	flow.LogoutHandler(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusFound)
	}

	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, srv.URL+"/logout") {
		t.Errorf("redirect = %q, should start with end_session_endpoint", loc)
	}

	// Verify session cookie was cleared
	var cleared bool
	for _, c := range resp.Cookies() {
		if c.Name == DefaultCookieName && c.MaxAge == -1 {
			cleared = true
		}
	}
	if !cleared {
		t.Error("session cookie not cleared")
	}
}

func TestLogoutHandlerNoEndSession(t *testing.T) {
	// Provider without end_session_endpoint
	mux := http.NewServeMux()
	var serverURL string
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"authorization_endpoint": serverURL + "/auth",
			"token_endpoint":         serverURL + "/token",
		})
	})
	srv := httptest.NewServer(mux)
	serverURL = srv.URL
	defer srv.Close()

	cfg := testFlowConfig(srv.URL)
	cfg.HTTPClient = srv.Client()

	flow, err := NewFlow(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewFlow: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/portal/auth/logout", http.NoBody)
	w := httptest.NewRecorder()

	flow.LogoutHandler(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusFound)
	}

	if resp.Header.Get("Location") != "/portal/" {
		t.Errorf("redirect = %q, want /portal/", resp.Header.Get("Location"))
	}
}

func TestNewFlowDefaultScopes(t *testing.T) {
	srv := mockOIDCProvider(t, nil)
	defer srv.Close()

	cfg := FlowConfig{
		Issuer:      srv.URL,
		ClientID:    "c",
		RedirectURI: srv.URL + "/cb",
		Cookie:      CookieConfig{Key: testKey()},
		HTTPClient:  srv.Client(),
	}

	flow, err := NewFlow(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewFlow: %v", err)
	}

	if len(flow.cfg.Scopes) != 3 {
		t.Errorf("default scopes = %v, want 3 scopes", flow.cfg.Scopes)
	}
}

func TestExtractRolesNoRoleClaim(t *testing.T) {
	f := &Flow{cfg: FlowConfig{}}
	roles := f.extractRoles(map[string]any{"sub": "u"})
	if roles != nil {
		t.Errorf("roles = %v, want nil", roles)
	}
}

func TestExtractRolesNoPrefix(t *testing.T) {
	f := &Flow{cfg: FlowConfig{RoleClaim: "roles"}}
	roles := f.extractRoles(map[string]any{
		"roles": []any{"admin", "user"},
	})
	if len(roles) != 2 || roles[0] != "admin" || roles[1] != "user" {
		t.Errorf("roles = %v, want [admin user]", roles)
	}
}

func TestExtractRolesNestedPath(t *testing.T) {
	f := &Flow{cfg: FlowConfig{RoleClaim: "resource.roles", RolePrefix: "app_"}}
	roles := f.extractRoles(map[string]any{
		"resource": map[string]any{
			"roles": []any{"app_admin", "other", "app_user"},
		},
	})
	if len(roles) != 2 || roles[0] != "admin" || roles[1] != "user" {
		t.Errorf("roles = %v, want [admin user]", roles)
	}
}

func TestExtractRolesBadPath(t *testing.T) {
	f := &Flow{cfg: FlowConfig{RoleClaim: "nonexistent.path"}}
	roles := f.extractRoles(map[string]any{"sub": "u"})
	if roles != nil {
		t.Errorf("roles = %v, want nil", roles)
	}
}

func TestPKCEChallenge(t *testing.T) {
	// Verify S256 challenge is deterministic for same verifier
	verifier := "test-verifier-value"
	c1 := pkceChallenge(verifier)
	c2 := pkceChallenge(verifier)
	if c1 != c2 {
		t.Error("pkceChallenge should be deterministic")
	}
	if c1 == "" {
		t.Error("expected non-empty challenge")
	}
}

func TestRandomString(t *testing.T) {
	s1, err := randomString(32)
	if err != nil {
		t.Fatalf("randomString: %v", err)
	}
	s2, err := randomString(32)
	if err != nil {
		t.Fatalf("randomString: %v", err)
	}
	if s1 == s2 {
		t.Error("two random strings should differ")
	}
	if len(s1) == 0 {
		t.Error("expected non-empty string")
	}
}

func TestStateSignVerifyRoundTrip(t *testing.T) {
	cfg := &CookieConfig{Key: testKey()}
	data := "state=abc&verifier=xyz"

	signed, err := signStateData(data, cfg)
	if err != nil {
		t.Fatalf("signStateData: %v", err)
	}

	got, err := verifyStateData(signed, cfg.Key)
	if err != nil {
		t.Fatalf("verifyStateData: %v", err)
	}
	if got != data {
		t.Errorf("data = %q, want %q", got, data)
	}
}

func TestStateVerifyTampered(t *testing.T) {
	cfg := &CookieConfig{Key: testKey()}
	signed, _ := signStateData("data", cfg)

	_, err := verifyStateData(signed+"x", cfg.Key)
	if err == nil {
		t.Fatal("expected error for tampered state")
	}
}

func TestNewFlowDiscoveryFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := FlowConfig{
		Issuer:      srv.URL,
		ClientID:    "c",
		RedirectURI: srv.URL + "/cb",
		Cookie:      CookieConfig{Key: testKey()},
		HTTPClient:  srv.Client(),
	}

	_, err := NewFlow(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected error for failed discovery")
	}
}

func TestNewFlowDiscoveryMissingEndpoints(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{})
	}))
	defer srv.Close()

	cfg := FlowConfig{
		Issuer:      srv.URL,
		ClientID:    "c",
		RedirectURI: srv.URL + "/cb",
		Cookie:      CookieConfig{Key: testKey()},
		HTTPClient:  srv.Client(),
	}

	_, err := NewFlow(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected error for missing endpoints")
	}
}

func TestCallbackHandlerTokenExchangeFailure(t *testing.T) {
	// Provider that returns error on token exchange
	mux := http.NewServeMux()
	var serverURL string
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"authorization_endpoint": serverURL + "/auth",
			"token_endpoint":         serverURL + "/token",
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	})

	srv := httptest.NewServer(mux)
	serverURL = srv.URL
	defer srv.Close()

	cfg := testFlowConfig(srv.URL)
	cfg.HTTPClient = srv.Client()

	flow, err := NewFlow(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewFlow: %v", err)
	}

	// Login to get state
	loginReq := httptest.NewRequest(http.MethodGet, "/portal/auth/login", http.NoBody)
	loginW := httptest.NewRecorder()
	flow.LoginHandler(loginW, loginReq)

	loc, _ := url.Parse(loginW.Result().Header.Get("Location"))
	state := loc.Query().Get("state")

	var stateCookie *http.Cookie
	for _, c := range loginW.Result().Cookies() {
		if c.Name == stateCookieName {
			stateCookie = c
		}
	}

	// Callback
	req := httptest.NewRequest(http.MethodGet, "/portal/auth/callback?code=abc&state="+state, http.NoBody)
	req.AddCookie(stateCookie)
	w := httptest.NewRecorder()

	flow.CallbackHandler(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}
