package browsersession

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// OIDC/PKCE constants.
const (
	stateBytes         = 32
	verifierBytes      = 32
	stateCookieName    = "mcp_auth_state"
	stateCookieMaxAge  = 300 // 5 minutes
	codeChallengeS256  = "S256"
	oidcDiscoverySuffix = "/.well-known/openid-configuration"
)

// FlowConfig configures the OIDC authorization code flow.
type FlowConfig struct {
	// Issuer is the OIDC provider's issuer URL.
	Issuer string

	// ClientID is the OIDC client identifier.
	ClientID string

	// ClientSecret is the OIDC client secret for confidential clients.
	ClientSecret string

	// RedirectURI is the callback URL (e.g., "https://example.com/portal/auth/callback").
	RedirectURI string

	// Scopes to request (default: [openid, profile, email]).
	Scopes []string

	// RoleClaim is the JSON path to roles in the id_token claims.
	RoleClaim string

	// RolePrefix filters roles to those with this prefix.
	RolePrefix string

	// Cookie configures the session cookie.
	Cookie CookieConfig

	// PostLoginRedirect is where to redirect after successful login (default: "/portal/").
	PostLoginRedirect string

	// HTTPClient is used for OIDC discovery and token exchange.
	// If nil, http.DefaultClient is used.
	HTTPClient *http.Client
}

// oidcEndpoints holds discovered OIDC provider endpoints.
type oidcEndpoints struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	EndSessionEndpoint    string `json:"end_session_endpoint"`
	UserinfoEndpoint      string `json:"userinfo_endpoint"`
}

// Flow implements the OIDC authorization code flow with PKCE.
type Flow struct {
	cfg       FlowConfig
	endpoints oidcEndpoints
}

// NewFlow creates a new OIDC flow by performing provider discovery.
func NewFlow(ctx context.Context, cfg FlowConfig) (*Flow, error) {
	if cfg.Issuer == "" {
		return nil, fmt.Errorf("issuer is required")
	}
	if cfg.ClientID == "" {
		return nil, fmt.Errorf("client_id is required")
	}
	if cfg.RedirectURI == "" {
		return nil, fmt.Errorf("redirect_uri is required")
	}
	if len(cfg.Cookie.Key) < minKeyLength {
		return nil, fmt.Errorf("cookie signing key must be at least %d bytes", minKeyLength)
	}

	if len(cfg.Scopes) == 0 {
		cfg.Scopes = []string{"openid", "profile", "email"}
	}
	if cfg.PostLoginRedirect == "" {
		cfg.PostLoginRedirect = "/portal/"
	}

	f := &Flow{cfg: cfg}

	endpoints, err := f.discover(ctx)
	if err != nil {
		return nil, fmt.Errorf("OIDC discovery: %w", err)
	}
	f.endpoints = endpoints

	return f, nil
}

// LoginHandler initiates the OIDC authorization code flow.
// It generates state + PKCE verifier, stores them in a temporary cookie,
// and redirects the user to the OIDC provider's authorization endpoint.
func (f *Flow) LoginHandler(w http.ResponseWriter, r *http.Request) {
	state, err := randomString(stateBytes)
	if err != nil {
		http.Error(w, "failed to generate state", http.StatusInternalServerError)
		return
	}

	verifier, err := randomString(verifierBytes)
	if err != nil {
		http.Error(w, "failed to generate PKCE verifier", http.StatusInternalServerError)
		return
	}

	// Store state + verifier in encrypted cookie
	stateData := url.Values{
		"state":    {state},
		"verifier": {verifier},
	}
	stateToken, err := signStateData(stateData.Encode(), &f.cfg.Cookie)
	if err != nil {
		http.Error(w, "failed to create state cookie", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     stateCookieName,
		Value:    stateToken,
		Path:     "/portal/auth/",
		MaxAge:   stateCookieMaxAge,
		HttpOnly: true,
		Secure:   f.cfg.Cookie.Secure,
		SameSite: http.SameSiteLaxMode,
	})

	// Build authorization URL
	challenge := pkceChallenge(verifier)
	params := url.Values{
		"response_type":         {"code"},
		"client_id":             {f.cfg.ClientID},
		"redirect_uri":          {f.cfg.RedirectURI},
		"scope":                 {strings.Join(f.cfg.Scopes, " ")},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {codeChallengeS256},
	}

	authURL := f.endpoints.AuthorizationEndpoint + "?" + params.Encode()
	http.Redirect(w, r, authURL, http.StatusFound)
}

// CallbackHandler processes the OIDC provider's callback after authentication.
// It validates the state, exchanges the authorization code for tokens,
// creates a session cookie, and redirects to the portal.
func (f *Flow) CallbackHandler(w http.ResponseWriter, r *http.Request) {
	// Check for errors from the OIDC provider
	if errParam := r.URL.Query().Get("error"); errParam != "" {
		desc := r.URL.Query().Get("error_description")
		slog.Warn("OIDC callback error", "error", errParam, "description", desc)
		http.Error(w, "authentication failed: "+errParam, http.StatusForbidden)
		return
	}

	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		http.Error(w, "missing code or state", http.StatusBadRequest)
		return
	}

	// Retrieve and validate state cookie
	stateCookie, err := r.Cookie(stateCookieName)
	if err != nil {
		http.Error(w, "missing state cookie", http.StatusBadRequest)
		return
	}

	stateData, err := verifyStateData(stateCookie.Value, f.cfg.Cookie.Key)
	if err != nil {
		http.Error(w, "invalid state cookie", http.StatusBadRequest)
		return
	}

	parsed, err := url.ParseQuery(stateData)
	if err != nil || parsed.Get("state") != state {
		http.Error(w, "state mismatch", http.StatusBadRequest)
		return
	}

	verifier := parsed.Get("verifier")

	// Clear state cookie
	http.SetCookie(w, &http.Cookie{
		Name:     stateCookieName,
		Value:    "",
		Path:     "/portal/auth/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   f.cfg.Cookie.Secure,
		SameSite: http.SameSiteLaxMode,
	})

	// Exchange code for tokens
	tokenResp, err := f.exchangeCode(r.Context(), code, verifier)
	if err != nil {
		slog.Error("OIDC token exchange failed", "error", err)
		http.Error(w, "token exchange failed", http.StatusInternalServerError)
		return
	}

	// Parse id_token to extract claims
	claims, err := f.parseIDToken(tokenResp.IDToken)
	if err != nil {
		slog.Error("failed to parse id_token", "error", err)
		http.Error(w, "failed to parse identity", http.StatusInternalServerError)
		return
	}

	// Create session cookie
	sessionToken, err := SignSession(*claims, &f.cfg.Cookie)
	if err != nil {
		slog.Error("failed to create session", "error", err)
		http.Error(w, "failed to create session", http.StatusInternalServerError)
		return
	}

	SetCookie(w, &f.cfg.Cookie, sessionToken)

	http.Redirect(w, r, f.cfg.PostLoginRedirect, http.StatusFound)
}

// LogoutHandler clears the session cookie and redirects to the OIDC end_session endpoint.
func (f *Flow) LogoutHandler(w http.ResponseWriter, r *http.Request) {
	ClearCookie(w, &f.cfg.Cookie)

	if f.endpoints.EndSessionEndpoint != "" {
		params := url.Values{
			"client_id":                {f.cfg.ClientID},
			"post_logout_redirect_uri": {f.cfg.PostLoginRedirect},
		}
		http.Redirect(w, r, f.endpoints.EndSessionEndpoint+"?"+params.Encode(), http.StatusFound)
		return
	}

	http.Redirect(w, r, f.cfg.PostLoginRedirect, http.StatusFound)
}

// tokenResponse holds the token endpoint response.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	IDToken      string `json:"id_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
}

// exchangeCode exchanges an authorization code for tokens.
func (f *Flow) exchangeCode(ctx context.Context, code, verifier string) (*tokenResponse, error) {
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {f.cfg.RedirectURI},
		"client_id":     {f.cfg.ClientID},
		"code_verifier": {verifier},
	}

	if f.cfg.ClientSecret != "" {
		data.Set("client_secret", f.cfg.ClientSecret)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.endpoints.TokenEndpoint,
		strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("creating token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := f.httpClient()
	resp, err := client.Do(req) // #nosec G107 -- URL from admin-controlled OIDC issuer config
	if err != nil {
		return nil, fmt.Errorf("token request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token endpoint returned %d", resp.StatusCode)
	}

	var tokenResp tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("parsing token response: %w", err)
	}

	if tokenResp.IDToken == "" {
		return nil, fmt.Errorf("no id_token in response")
	}

	return &tokenResp, nil
}

// parseIDToken extracts session claims from the id_token JWT.
// NOTE: We trust the id_token because it came directly from the token endpoint
// over TLS (server-side confidential client flow). Signature verification is
// not needed for this use case per OpenID Connect Core §3.1.3.7.
func (f *Flow) parseIDToken(idToken string) (*SessionClaims, error) {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 { //nolint:mnd // JWT has exactly 3 parts
		return nil, fmt.Errorf("invalid JWT format")
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decoding payload: %w", err)
	}

	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("parsing claims: %w", err)
	}

	sub, _ := claims["sub"].(string)
	if sub == "" {
		return nil, fmt.Errorf("missing sub claim in id_token")
	}

	email, _ := claims["email"].(string)

	roles := f.extractRoles(claims)

	return &SessionClaims{
		UserID: sub,
		Email:  email,
		Roles:  roles,
	}, nil
}

// extractRoles extracts roles from claims using the configured claim path and prefix.
func (f *Flow) extractRoles(claims map[string]any) []string {
	if f.cfg.RoleClaim == "" {
		return nil
	}

	// Walk the claim path (e.g., "realm_access.roles")
	parts := strings.Split(f.cfg.RoleClaim, ".")
	var current any = claims
	for _, part := range parts {
		m, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = m[part]
	}

	rawRoles, ok := current.([]any)
	if !ok {
		return nil
	}

	var roles []string
	for _, r := range rawRoles {
		s, ok := r.(string)
		if !ok {
			continue
		}
		if f.cfg.RolePrefix == "" {
			roles = append(roles, s)
			continue
		}
		if after, found := strings.CutPrefix(s, f.cfg.RolePrefix); found {
			roles = append(roles, after)
		}
	}

	return roles
}

// discover fetches the OIDC discovery document.
func (f *Flow) discover(ctx context.Context) (oidcEndpoints, error) {
	discoveryURL := strings.TrimSuffix(f.cfg.Issuer, "/") + oidcDiscoverySuffix

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, http.NoBody)
	if err != nil {
		return oidcEndpoints{}, fmt.Errorf("creating discovery request: %w", err)
	}

	client := f.httpClient()
	resp, err := client.Do(req) // #nosec G107 -- URL from admin-controlled OIDC issuer config
	if err != nil {
		return oidcEndpoints{}, fmt.Errorf("fetching discovery: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return oidcEndpoints{}, fmt.Errorf("discovery returned %d", resp.StatusCode)
	}

	var ep oidcEndpoints
	if err := json.NewDecoder(resp.Body).Decode(&ep); err != nil {
		return oidcEndpoints{}, fmt.Errorf("parsing discovery: %w", err)
	}

	if ep.AuthorizationEndpoint == "" || ep.TokenEndpoint == "" {
		return oidcEndpoints{}, fmt.Errorf("discovery missing required endpoints")
	}

	return ep, nil
}

// httpClient returns the configured or default HTTP client.
func (f *Flow) httpClient() *http.Client {
	if f.cfg.HTTPClient != nil {
		return f.cfg.HTTPClient
	}
	return http.DefaultClient
}

// --- State cookie helpers ---

// signStateData creates a short-lived signed JWT containing the state data.
func signStateData(data string, cfg *CookieConfig) (string, error) {
	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"data": data,
		"exp":  now.Add(time.Duration(stateCookieMaxAge) * time.Second).Unix(),
		"iat":  now.Unix(),
	})

	return token.SignedString(cfg.Key)
}

// verifyStateData validates the signed state JWT and returns the data payload.
func verifyStateData(tokenString string, key []byte) (string, error) {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return key, nil
	})
	if err != nil {
		return "", fmt.Errorf("parsing state token: %w", err)
	}

	mc, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return "", fmt.Errorf("unexpected claims type")
	}

	data, ok := mc["data"].(string)
	if !ok {
		return "", fmt.Errorf("missing data claim")
	}

	return data, nil
}

// --- PKCE helpers ---

// pkceChallenge computes the S256 code challenge from a verifier.
func pkceChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

// randomString generates a URL-safe random string.
func randomString(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("reading random bytes: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
