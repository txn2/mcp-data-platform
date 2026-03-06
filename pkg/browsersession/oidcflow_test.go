package browsersession

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// mockOIDCProvider creates a test OIDC server that returns discovery and token endpoints.
// If idTokenClaims does not contain iss, aud, or exp, they are injected automatically
// using the server URL and test-client defaults.
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

		// Inject standard OIDC claims if not explicitly set by the test.
		claims := make(map[string]any)
		maps.Copy(claims, idTokenClaims)
		if _, ok := claims["iss"]; !ok {
			claims["iss"] = serverURL
		}
		if _, ok := claims["aud"]; !ok {
			claims["aud"] = "test-client"
		}
		if _, ok := claims["exp"]; !ok {
			claims["exp"] = float64(time.Now().Add(time.Hour).Unix())
		}

		// Build a fake id_token (unsigned for testing)
		header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
		payload, _ := json.Marshal(claims)
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
		Issuer:            serverURL,
		ClientID:          "test-client",
		ClientSecret:      "test-secret",
		RedirectURI:       serverURL + "/portal/auth/callback",
		Scopes:            []string{"openid", "profile", "email"},
		RoleClaim:         "realm_access.roles",
		RolePrefix:        "dp_",
		Cookie:            CookieConfig{Key: testKey(), TTL: time.Hour},
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
	// Roles should be filtered by dp_ prefix but keep the full role name
	if len(sessionClaims.Roles) != 2 || sessionClaims.Roles[0] != "dp_admin" || sessionClaims.Roles[1] != "dp_analyst" {
		t.Errorf("Roles = %v, want [dp_admin dp_analyst]", sessionClaims.Roles)
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
		"/portal/auth/callback?error=access_denied&error_description=user+canceled", http.NoBody)
	w := httptest.NewRecorder()

	flow.CallbackHandler(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusFound)
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "error=access_denied") {
		t.Errorf("redirect should contain error param, got %q", loc)
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

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusFound)
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "error=invalid_request") {
		t.Errorf("redirect should contain error=invalid_request, got %q", loc)
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

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusFound)
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "error=invalid_state") {
		t.Errorf("redirect should contain error=invalid_state, got %q", loc)
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

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusFound)
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "error=invalid_state") {
		t.Errorf("redirect should contain error=invalid_state, got %q", loc)
	}
}

func TestCallbackHandlerBadIDToken(t *testing.T) {
	// Create a mock OIDC provider that returns a malformed id_token.
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

	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Return an id_token that is NOT a valid 3-part JWT.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "access-token",
			"id_token":     "not-a-jwt",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()
	serverURL = srv.URL

	cfg := testFlowConfig(srv.URL)
	cfg.HTTPClient = srv.Client()

	flow, err := NewFlow(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewFlow: %v", err)
	}

	// Login to get a valid state cookie + extract state param.
	loginReq := httptest.NewRequest(http.MethodGet, "/portal/auth/login", http.NoBody)
	loginW := httptest.NewRecorder()
	flow.LoginHandler(loginW, loginReq)

	var stateCookie *http.Cookie
	for _, c := range loginW.Result().Cookies() {
		if c.Name == stateCookieName {
			stateCookie = c
		}
	}
	if stateCookie == nil {
		t.Fatal("no state cookie from login")
	}

	// Extract state from redirect URL.
	loc := loginW.Header().Get("Location")
	u, _ := url.Parse(loc)
	state := u.Query().Get("state")

	req := httptest.NewRequest(http.MethodGet,
		"/portal/auth/callback?code=test-code&state="+state, http.NoBody)
	req.AddCookie(stateCookie)
	w := httptest.NewRecorder()

	flow.CallbackHandler(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusFound)
	}
	if !strings.Contains(w.Header().Get("Location"), "error=auth_failed") {
		t.Errorf("redirect should contain error=auth_failed, got %q", w.Header().Get("Location"))
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
	if len(roles) != 2 || roles[0] != "app_admin" || roles[1] != "app_user" {
		t.Errorf("roles = %v, want [app_admin app_user]", roles)
	}
}

func TestExtractRolesBadPath(t *testing.T) {
	f := &Flow{cfg: FlowConfig{RoleClaim: "nonexistent.path"}}
	roles := f.extractRoles(map[string]any{"sub": "u"})
	if roles != nil {
		t.Errorf("roles = %v, want nil", roles)
	}
}

func TestExtractRolesNonStringValues(t *testing.T) {
	f := &Flow{cfg: FlowConfig{RoleClaim: "roles"}}
	roles := f.extractRoles(map[string]any{
		"roles": []any{"admin", 42, "user", true},
	})
	if len(roles) != 2 || roles[0] != "admin" || roles[1] != "user" {
		t.Errorf("roles = %v, want [admin user]", roles)
	}
}

func TestExtractRolesNotAnArray(t *testing.T) {
	f := &Flow{cfg: FlowConfig{RoleClaim: "roles"}}
	roles := f.extractRoles(map[string]any{
		"roles": "not-an-array",
	})
	if roles != nil {
		t.Errorf("roles = %v, want nil", roles)
	}
}

func TestExtractRolesEmpty(t *testing.T) {
	f := &Flow{cfg: FlowConfig{}}
	roles := f.extractRoles(map[string]any{})
	if roles != nil {
		t.Errorf("roles = %v, want nil", roles)
	}
}

func TestHTTPClientDefault(t *testing.T) {
	f := &Flow{cfg: FlowConfig{}}
	client := f.httpClient()
	if client != http.DefaultClient {
		t.Error("expected default client")
	}
}

func TestHTTPClientCustom(t *testing.T) {
	custom := &http.Client{Timeout: time.Second}
	f := &Flow{cfg: FlowConfig{HTTPClient: custom}}
	client := f.httpClient()
	if client != custom {
		t.Error("expected custom client")
	}
}

func TestSanitizeLogValue(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"clean", "hello world", "hello world"},
		{"newlines", "line1\nline2\r\n", "line1 line2  "},
		{"tabs", "col1\tcol2", "col1 col2"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeLogValue(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeLogValue(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
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
	s1, err := randomString()
	if err != nil {
		t.Fatalf("randomString: %v", err)
	}
	s2, err := randomString()
	if err != nil {
		t.Fatalf("randomString: %v", err)
	}
	if s1 == s2 {
		t.Error("two random strings should differ")
	}
	if s1 == "" {
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

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusFound)
	}
	if !strings.Contains(w.Header().Get("Location"), "error=auth_failed") {
		t.Errorf("redirect should contain error=auth_failed, got %q", w.Header().Get("Location"))
	}
}

func TestParseIDTokenValidatesIssuer(t *testing.T) {
	f := &Flow{cfg: FlowConfig{Issuer: "https://correct.example.com", ClientID: "c"}}

	claims := map[string]any{
		"sub": "user-1",
		"iss": "https://wrong.example.com",
		"aud": "c",
		"exp": float64(time.Now().Add(time.Hour).Unix()),
	}
	payload, _ := json.Marshal(claims)
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	idToken := header + "." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"

	_, err := f.parseIDToken(idToken)
	if err == nil {
		t.Fatal("expected error for issuer mismatch")
	}
	if !strings.Contains(err.Error(), "issuer") {
		t.Errorf("error should mention issuer, got: %v", err)
	}
}

func TestParseIDTokenValidatesAudience(t *testing.T) {
	f := &Flow{cfg: FlowConfig{Issuer: "https://issuer.example.com", ClientID: "my-client"}}

	claims := map[string]any{
		"sub": "user-1",
		"iss": "https://issuer.example.com",
		"aud": "wrong-client",
		"exp": float64(time.Now().Add(time.Hour).Unix()),
	}
	payload, _ := json.Marshal(claims)
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	idToken := header + "." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"

	_, err := f.parseIDToken(idToken)
	if err == nil {
		t.Fatal("expected error for audience mismatch")
	}
	if !strings.Contains(err.Error(), "audience") {
		t.Errorf("error should mention audience, got: %v", err)
	}
}

func TestParseIDTokenValidatesAudienceArray(t *testing.T) {
	f := &Flow{cfg: FlowConfig{Issuer: "https://issuer.example.com", ClientID: "my-client"}}

	// aud as array containing the client_id — should succeed
	claims := map[string]any{
		"sub": "user-1",
		"iss": "https://issuer.example.com",
		"aud": []any{"other-client", "my-client"},
		"exp": float64(time.Now().Add(time.Hour).Unix()),
	}
	payload, _ := json.Marshal(claims)
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	idToken := header + "." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"

	sc, err := f.parseIDToken(idToken)
	if err != nil {
		t.Fatalf("expected success for audience array containing client_id, got: %v", err)
	}
	if sc.UserID != "user-1" {
		t.Errorf("UserID = %q, want %q", sc.UserID, "user-1")
	}

	// aud as array NOT containing the client_id — should fail
	claims["aud"] = []any{"other-client", "another-client"}
	payload, _ = json.Marshal(claims)
	idToken = header + "." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"

	_, err = f.parseIDToken(idToken)
	if err == nil {
		t.Fatal("expected error for audience array not containing client_id")
	}
}

func TestParseIDTokenValidatesExpiration(t *testing.T) {
	f := &Flow{cfg: FlowConfig{Issuer: "https://issuer.example.com", ClientID: "c"}}

	claims := map[string]any{
		"sub": "user-1",
		"iss": "https://issuer.example.com",
		"aud": "c",
		"exp": float64(time.Now().Add(-time.Hour).Unix()), // expired 1 hour ago
	}
	payload, _ := json.Marshal(claims)
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	idToken := header + "." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"

	_, err := f.parseIDToken(idToken)
	if err == nil {
		t.Fatal("expected error for expired token")
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Errorf("error should mention expired, got: %v", err)
	}
}

func TestParseIDTokenMissingClaims(t *testing.T) {
	tests := []struct {
		name   string
		claims map[string]any
		errMsg string
	}{
		{
			name:   "missing iss",
			claims: map[string]any{"sub": "u", "aud": "c", "exp": float64(time.Now().Add(time.Hour).Unix())},
			errMsg: "iss",
		},
		{
			name:   "missing aud",
			claims: map[string]any{"sub": "u", "iss": "https://issuer.example.com", "exp": float64(time.Now().Add(time.Hour).Unix())},
			errMsg: "aud",
		},
		{
			name:   "missing exp",
			claims: map[string]any{"sub": "u", "iss": "https://issuer.example.com", "aud": "c"},
			errMsg: "exp",
		},
	}

	f := &Flow{cfg: FlowConfig{Issuer: "https://issuer.example.com", ClientID: "c"}}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload, _ := json.Marshal(tt.claims)
			idToken := header + "." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"

			_, err := f.parseIDToken(idToken)
			if err == nil {
				t.Fatalf("expected error for %s", tt.name)
			}
			if !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("error should mention %q, got: %v", tt.errMsg, err)
			}
		})
	}
}

func TestLogoutHandlerWithIDTokenHint(t *testing.T) {
	claims := map[string]any{
		"sub":   "user-42",
		"email": "user@example.com",
		"realm_access": map[string]any{
			"roles": []any{"dp_admin"},
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

	// Complete a full login to get a session cookie with id_token stored.
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

	callbackURL := "/portal/auth/callback?code=auth-code&state=" + state
	callbackReq := httptest.NewRequest(http.MethodGet, callbackURL, http.NoBody)
	callbackReq.AddCookie(stateCookie)
	callbackW := httptest.NewRecorder()
	flow.CallbackHandler(callbackW, callbackReq)

	// Get the session cookie from the callback response.
	var sessionCookie *http.Cookie
	for _, c := range callbackW.Result().Cookies() {
		if c.Name == DefaultCookieName {
			sessionCookie = c
		}
	}
	if sessionCookie == nil {
		t.Fatal("no session cookie from callback")
	}

	// Now logout WITH the session cookie — should include id_token_hint.
	logoutReq := httptest.NewRequest(http.MethodGet, "/portal/auth/logout", http.NoBody)
	logoutReq.AddCookie(sessionCookie)
	logoutW := httptest.NewRecorder()

	flow.LogoutHandler(logoutW, logoutReq)

	logoutResp := logoutW.Result()
	if logoutResp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want %d", logoutResp.StatusCode, http.StatusFound)
	}

	logoutLoc := logoutResp.Header.Get("Location")
	if !strings.Contains(logoutLoc, "id_token_hint=") {
		t.Error("logout redirect should contain id_token_hint")
	}
}

func TestRedirectWithError(t *testing.T) {
	f := &Flow{cfg: FlowConfig{PostLoginRedirect: "/portal/"}}

	req := httptest.NewRequest(http.MethodGet, "/portal/auth/callback", http.NoBody)
	w := httptest.NewRecorder()

	f.redirectWithError(w, req, "test_error")

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusFound)
	}
	loc := w.Header().Get("Location")
	if loc != "/portal/?error=test_error" {
		t.Errorf("redirect = %q, want /portal/?error=test_error", loc)
	}
}
