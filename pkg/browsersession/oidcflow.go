package browsersession

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// DefaultPortalPath is the default redirect path for the portal UI.
const DefaultPortalPath = "/portal/"

// OIDC/PKCE constants.
const (
	randomStringBytes   = 32 // size for random state/verifier strings
	jwtPartCount        = 3  // JWTs have exactly 3 dot-separated parts
	stateCookieName     = "mcp_auth_state"
	stateCookieMaxAge   = 300 // 5 minutes
	codeChallengeS256   = "S256"
	oidcDiscoverySuffix = "/.well-known/openid-configuration"
	logKeyError         = "error"
	logKeyState         = "state"
	maxResponseBytes    = 1 << 20 // 1 MB limit for OIDC HTTP responses
)

// FlowConfig configures the OIDC authorization code flow.
type FlowConfig struct {
	// Issuer is the OIDC provider's issuer URL.
	Issuer string

	// ClientID is the OIDC client identifier.
	ClientID string

	// ClientSecret is the OIDC client secret for confidential clients.
	ClientSecret string // #nosec G117 -- OIDC client secret from admin config

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

	// PostLoginRedirect is where to redirect after successful login (default: DefaultPortalPath).
	PostLoginRedirect string

	// PostLogoutRedirect is the absolute URL sent as post_logout_redirect_uri
	// to the OIDC provider. Must be an absolute URL (Keycloak rejects relative paths).
	// Falls back to PostLoginRedirect if empty.
	PostLogoutRedirect string

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
		cfg.PostLoginRedirect = DefaultPortalPath
	}
	if cfg.PostLogoutRedirect == "" {
		cfg.PostLogoutRedirect = cfg.PostLoginRedirect
	}

	f := &Flow{cfg: cfg}

	endpoints, err := f.discover(ctx)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}
	f.endpoints = endpoints

	return f, nil
}

// LoginHandler initiates the OIDC authorization code flow.
// It generates state + PKCE verifier, stores them in a temporary cookie,
// and redirects the user to the OIDC provider's authorization endpoint.
func (f *Flow) LoginHandler(w http.ResponseWriter, r *http.Request) {
	state, err := randomString()
	if err != nil {
		http.Error(w, "failed to generate state", http.StatusInternalServerError)
		return
	}

	verifier, err := randomString()
	if err != nil {
		http.Error(w, "failed to generate PKCE verifier", http.StatusInternalServerError)
		return
	}

	// Store state + verifier in encrypted cookie
	stateData := url.Values{
		logKeyState: {state},
		"verifier":  {verifier},
	}
	stateToken, err := signStateData(stateData.Encode(), &f.cfg.Cookie)
	if err != nil {
		http.Error(w, "failed to create state cookie", http.StatusInternalServerError)
		return
	}

	// nosemgrep: go.lang.security.audit.net.cookie-missing-secure.cookie-missing-secure
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- Secure is cfg-driven (defaults true, opt-out for local dev without TLS)
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
		logKeyState:             {state},
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
	if errParam := r.URL.Query().Get(logKeyError); errParam != "" {
		desc := r.URL.Query().Get("error_description")
		safeErr := sanitizeLogValue(errParam)
		safeDesc := sanitizeLogValue(desc)
		slog.Warn("OIDC callback error", // #nosec G706 -- values are sanitized above
			logKeyError, safeErr,
			"description", safeDesc)
		f.redirectWithError(w, r, "access_denied")
		return
	}

	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get(logKeyState)
	if code == "" || state == "" {
		f.redirectWithError(w, r, "invalid_request")
		return
	}

	// Validate state and extract PKCE verifier.
	verifier, err := f.validateCallbackState(r, state)
	if err != nil {
		f.redirectWithError(w, r, "invalid_state")
		return
	}

	// Clear state cookie
	f.clearStateCookie(w)

	// Exchange code for tokens, parse identity, create session.
	if err := f.completeLogin(r.Context(), w, code, verifier); err != nil {
		f.redirectWithError(w, r, "auth_failed")
		return
	}

	http.Redirect(w, r, f.cfg.PostLoginRedirect, http.StatusFound)
}

// redirectWithError redirects the user to the portal with an error query parameter
// so the SPA can display a friendly error message.
func (f *Flow) redirectWithError(w http.ResponseWriter, r *http.Request, errCode string) {
	dest := f.cfg.PostLoginRedirect + "?error=" + url.QueryEscape(errCode)
	http.Redirect(w, r, dest, http.StatusFound)
}

// validateCallbackState verifies the state cookie matches the callback state
// parameter and returns the PKCE code verifier.
func (f *Flow) validateCallbackState(r *http.Request, state string) (string, error) {
	stateCookie, err := r.Cookie(stateCookieName)
	if err != nil {
		return "", fmt.Errorf("missing state cookie")
	}

	stateData, err := verifyStateData(stateCookie.Value, f.cfg.Cookie.Key)
	if err != nil {
		return "", fmt.Errorf("invalid state cookie")
	}

	parsed, err := url.ParseQuery(stateData)
	if err != nil || parsed.Get(logKeyState) != state {
		return "", fmt.Errorf("state mismatch")
	}

	return parsed.Get("verifier"), nil
}

// clearStateCookie removes the temporary OIDC state cookie.
func (f *Flow) clearStateCookie(w http.ResponseWriter) {
	// nosemgrep: go.lang.security.audit.net.cookie-missing-secure.cookie-missing-secure
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- Secure is cfg-driven (defaults true, opt-out for local dev without TLS)
		Name:     stateCookieName,
		Value:    "",
		Path:     "/portal/auth/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   f.cfg.Cookie.Secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// completeLogin exchanges the authorization code for tokens, parses the
// id_token, and sets the session cookie. It writes HTTP errors directly
// on failure and returns a non-nil error to signal the caller to stop.
func (f *Flow) completeLogin(ctx context.Context, w http.ResponseWriter, code, verifier string) error {
	tokenResp, err := f.exchangeCode(ctx, code, verifier)
	if err != nil {
		slog.Error("OIDC token exchange failed", logKeyError, err)
		return err
	}

	claims, err := f.parseIDToken(tokenResp.IDToken)
	if err != nil {
		slog.Error("failed to parse id_token", logKeyError, err)
		return err
	}

	// Store raw id_token for logout id_token_hint.
	claims.IDToken = tokenResp.IDToken

	sessionToken, err := SignSession(*claims, &f.cfg.Cookie)
	if err != nil {
		slog.Error("failed to create session", logKeyError, err)
		return err
	}

	SetCookie(w, &f.cfg.Cookie, sessionToken)
	return nil
}

// LogoutHandler clears the session cookie and redirects to the OIDC end_session endpoint.
func (f *Flow) LogoutHandler(w http.ResponseWriter, r *http.Request) {
	// Extract id_token_hint from session cookie before clearing it.
	var idTokenHint string
	if claims, _ := ParseFromRequest(r, &f.cfg.Cookie); claims != nil {
		idTokenHint = claims.IDToken
	}

	ClearCookie(w, &f.cfg.Cookie)

	if f.endpoints.EndSessionEndpoint != "" {
		params := url.Values{
			"client_id":                {f.cfg.ClientID},
			"post_logout_redirect_uri": {f.cfg.PostLogoutRedirect},
		}
		if idTokenHint != "" {
			params.Set("id_token_hint", idTokenHint)
		}
		http.Redirect(w, r, f.endpoints.EndSessionEndpoint+"?"+params.Encode(), http.StatusFound)
		return
	}

	http.Redirect(w, r, f.cfg.PostLogoutRedirect, http.StatusFound)
}

// tokenResponse holds the token endpoint response.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`  // #nosec G117 -- token from OIDC provider
	IDToken      string `json:"id_token"`      //nolint:tagliatelle // OIDC standard field name
	TokenType    string `json:"token_type"`    //nolint:tagliatelle // OIDC standard field name
	ExpiresIn    int    `json:"expires_in"`    //nolint:tagliatelle // OIDC standard field name
	RefreshToken string `json:"refresh_token"` // #nosec G117 -- token from OIDC provider
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
	resp, err := client.Do(req) // #nosec G107 G704 -- URL from admin-controlled OIDC issuer config
	if err != nil {
		return nil, fmt.Errorf("token request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token endpoint returned %d", resp.StatusCode)
	}

	var tokenResp tokenResponse
	limited := io.LimitReader(resp.Body, maxResponseBytes)
	if err := json.NewDecoder(limited).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("parsing token response: %w", err)
	}

	if tokenResp.IDToken == "" {
		return nil, fmt.Errorf("no id_token in response")
	}

	return &tokenResp, nil
}

// parseIDToken extracts and validates session claims from the id_token JWT.
// NOTE: We trust the id_token because it came directly from the token endpoint
// over TLS (server-side confidential client flow). Signature verification is
// not needed for this use case per OpenID Connect Core §3.1.3.7.
// However, iss, aud, and exp claims are still validated per the spec.
func (f *Flow) parseIDToken(idToken string) (*SessionClaims, error) {
	parts := strings.Split(idToken, ".")
	if len(parts) != jwtPartCount {
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

	// Validate issuer (OIDC Core §3.1.3.7 #2).
	if err := f.validateIssuer(claims); err != nil {
		return nil, err
	}

	// Validate audience (OIDC Core §3.1.3.7 #3).
	if err := f.validateAudience(claims); err != nil {
		return nil, err
	}

	// Validate expiration (OIDC Core §3.1.3.7 #9).
	if err := validateExpiration(claims); err != nil {
		return nil, err
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

// validateIssuer checks that the id_token iss claim matches the configured issuer.
func (f *Flow) validateIssuer(claims map[string]any) error {
	iss, _ := claims["iss"].(string)
	if iss == "" {
		return fmt.Errorf("missing iss claim in id_token")
	}
	if iss != f.cfg.Issuer {
		return fmt.Errorf("id_token issuer %q does not match configured issuer %q", iss, f.cfg.Issuer)
	}
	return nil
}

// validateAudience checks that the id_token aud claim contains the configured client_id.
// Per OIDC Core, aud can be a string or an array of strings.
func (f *Flow) validateAudience(claims map[string]any) error {
	switch aud := claims["aud"].(type) {
	case string:
		if aud != f.cfg.ClientID {
			return fmt.Errorf("id_token audience %q does not match client_id %q", aud, f.cfg.ClientID)
		}
	case []any:
		found := false
		for _, a := range aud {
			if s, ok := a.(string); ok && s == f.cfg.ClientID {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("id_token audience does not contain client_id %q", f.cfg.ClientID)
		}
	default:
		return fmt.Errorf("missing or invalid aud claim in id_token")
	}
	return nil
}

// validateExpiration checks that the id_token has not expired.
func validateExpiration(claims map[string]any) error {
	exp, ok := claims["exp"].(float64)
	if !ok {
		return fmt.Errorf("missing exp claim in id_token")
	}
	if time.Now().Unix() > int64(exp) {
		return fmt.Errorf("id_token has expired")
	}
	return nil
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
		// Filter to roles with the prefix but keep the full role name.
		// This matches the MCP auth path (pkg/auth/claims.go filterByPrefix)
		// so that persona role matching works consistently across both
		// browser sessions and MCP OAuth/OIDC flows.
		if strings.HasPrefix(s, f.cfg.RolePrefix) {
			roles = append(roles, s)
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
	resp, err := client.Do(req) // #nosec G107 G704 -- URL from admin-controlled OIDC issuer config
	if err != nil {
		return oidcEndpoints{}, fmt.Errorf("fetching discovery: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return oidcEndpoints{}, fmt.Errorf("discovery returned %d", resp.StatusCode)
	}

	var ep oidcEndpoints
	limited := io.LimitReader(resp.Body, maxResponseBytes)
	if err := json.NewDecoder(limited).Decode(&ep); err != nil {
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

	signed, err := token.SignedString(cfg.Key)
	if err != nil {
		return "", fmt.Errorf("signing state data: %w", err)
	}
	return signed, nil
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
func randomString() (string, error) {
	b := make([]byte, randomStringBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("reading random bytes: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// sanitizeLogValue strips control characters from a string to prevent log injection.
func sanitizeLogValue(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		return r
	}, s)
}
