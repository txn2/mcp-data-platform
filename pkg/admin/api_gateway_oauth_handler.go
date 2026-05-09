package admin

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/platform"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
)

// registerAPIGatewayOAuthRoutes adds the authorization-code OAuth
// endpoints for the HTTP API gateway. Mirrors the MCP gateway's
// pair (POST oauth-start + GET callback) but reads from the api
// gateway's config shape and writes to the api gateway's
// TokenStore. The callback route is on the public mux so the
// upstream IdP's redirect can hit it without an admin auth header
// — the state token + PKCE verifier authenticate the callback.
func (h *Handler) registerAPIGatewayOAuthRoutes() {
	if !h.isMutable() || h.deps.ConnectionStore == nil {
		return
	}
	h.mux.HandleFunc("POST /api/v1/admin/api-gateway/connections/{name}/oauth-start", h.startAPIGatewayOAuth)
	h.publicMux.HandleFunc("GET /api/v1/admin/api-gateway/oauth/callback", h.apiGatewayOAuthCallback)
}

// startAPIGatewayOAuth handles POST .../api-gateway/connections/{name}/oauth-start.
//
// @Summary      Begin OAuth authorization-code flow for an API gateway connection
// @Description  Generates a PKCE verifier, derives the SHA256 challenge, registers a state token, and returns the authorization URL the operator should open in their browser. The upstream IdP redirects back to /api/v1/admin/api-gateway/oauth/callback after the user authenticates.
// @Tags         Connections
// @Accept       json
// @Produce      json
// @Param        name  path  string                   true  "API gateway connection name"
// @Param        body  body  startGatewayOAuthRequest  false  "Optional return URL"
// @Success      200   {object}  startGatewayOAuthResponse
// @Failure      400   {object}  problemDetail
// @Failure      404   {object}  problemDetail
// @Failure      409   {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/api-gateway/connections/{name}/oauth-start [post]
func (h *Handler) startAPIGatewayOAuth(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue(pathKeyName)
	cfg, ok := h.loadAPIGatewayAuthCodeConfig(w, r, name)
	if !ok {
		return
	}

	var body startGatewayOAuthRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}

	verifier, state, ok := generatePKCEPair(w)
	if !ok {
		return
	}
	redirectURI := buildAPIGatewayCallbackURL(r)
	authURL := buildAPIGatewayAuthorizationURL(cfg.OAuth2, state, verifier, redirectURI)

	store := h.pkceStoreFor()
	if store == nil {
		writeError(w, http.StatusServiceUnavailable, "OAuth not available: PKCE store not configured")
		return
	}
	startedBy := authorEmailOrID(r.Context())
	if err := store.Put(r.Context(), state, &PKCEState{
		connection:   name,
		codeVerifier: verifier,
		startedBy:    startedBy,
		createdAt:    time.Now(),
		returnURL:    body.ReturnURL,
		redirectURI:  redirectURI,
	}); err != nil {
		slog.Error("api-gateway oauth-start: failed to persist pkce state",
			logKeyName, name, logKeyStartedBy, startedBy, logKeyError, err)
		writeError(w, http.StatusInternalServerError, "failed to register oauth state")
		return
	}
	writeJSON(w, http.StatusOK, startGatewayOAuthResponse{
		AuthorizationURL: authURL,
		State:            state,
		RedirectURI:      redirectURI,
		ExpiresAt:        time.Now().Add(pkceTTL).UTC().Format(time.RFC3339),
	})
}

// apiGatewayOAuthCallback handles GET /api/v1/admin/api-gateway/oauth/callback.
//
// @Summary      OAuth authorization-code callback for API gateway connections
// @Description  Public endpoint hit by the upstream IdP after the operator authenticates. Exchanges the code for tokens and persists them via the API gateway's TokenStore. Renders an HTML error page on failure so a stranded browser tab still gives a useful message.
// @Tags         Connections
// @Produce      html
// @Param        code   query  string  false  "OAuth authorization code"
// @Param        state  query  string  true   "PKCE state token from oauth-start"
// @Param        error  query  string  false  "OAuth error code from upstream"
// @Success      302
// @Failure      400  {object}  problemDetail
// @Router       /admin/api-gateway/oauth/callback [get]
func (h *Handler) apiGatewayOAuthCallback(w http.ResponseWriter, r *http.Request) {
	pending := h.takeAPIGatewayCallbackState(w, r)
	if pending == nil {
		return
	}
	q := r.URL.Query()
	if !validateAPIGatewayCallbackQuery(w, q, pending) {
		return
	}
	if !h.completeAPIGatewayCallback(w, r, q.Get(formKeyCode), pending) {
		return
	}

	// safeReturnURL constrains the post-OAuth redirect to a same-origin
	// relative path or the constant fallback — without this, an admin
	// (or someone with admin-session XSRF) could register
	// `return_url: "https://evil.example/x"` via oauth-start and the
	// IdP redirect would bounce the operator's browser there.
	dest := safeReturnURL(pending.returnURL)
	if pending.returnURL != "" && dest != pending.returnURL {
		slog.Warn("api-gateway oauth-callback: returnURL rewritten by safeReturnURL guard",
			logKeyName, pending.connection,
			logKeyStartedBy, pending.startedBy,
			"requested_return_url", pending.returnURL,
			"rewritten_to", dest)
	}
	// #nosec G710 -- safeReturnURL has already constrained dest to a
	// same-origin relative path or the constant fallback (see
	// safeReturnURL contract in gateway_oauth_handler.go and
	// TestSafeReturnURL).
	http.Redirect(w, r, dest, http.StatusFound) // nosemgrep: go.lang.security.injection.open-redirect.open-redirect
}

// takeAPIGatewayCallbackState validates state, looks up the PKCE
// store, and consumes the pending entry. Returns nil and writes the
// HTML error page on any failure (so the caller can return early).
func (h *Handler) takeAPIGatewayCallbackState(w http.ResponseWriter, r *http.Request) *PKCEState {
	state := r.URL.Query().Get("state")
	if state == "" {
		writeOAuthError(w, "missing state parameter")
		return nil
	}
	store := h.pkceStoreFor()
	if store == nil {
		writeOAuthError(w, "OAuth not available: PKCE store not configured")
		return nil
	}
	pending, err := store.Take(r.Context(), state)
	if err != nil {
		// Replay (state already consumed) or expired. Either way the
		// safe response is to refuse without leaking which.
		writeOAuthError(w, "invalid or expired state token")
		return nil
	}
	return pending
}

// validateAPIGatewayCallbackQuery handles the upstream-error and
// missing-code cases. Returns false (and writes the HTML error page)
// when the callback should not proceed past this point.
func validateAPIGatewayCallbackQuery(w http.ResponseWriter, q url.Values, pending *PKCEState) bool {
	if oauthErr := q.Get("error"); oauthErr != "" {
		// Upstream signaled an error. Echo at warning level — the
		// description is operator-supplied so it's safe to surface
		// directly (unlike a 4xx response body, which we scrub).
		// Use "idp_error" here (matches gateway_oauth_handler.go's
		// log key for SIEM cross-grep), NOT formKeyCode (= "code").
		// The value is the upstream OAuth `error` query parameter
		// (e.g. access_denied), not the authorization code. Logging
		// it under `code` would mislead any SIEM rule grepping for
		// authorization-code reuse.
		slog.Warn("api-gateway oauth-callback: upstream error",
			logKeyName, pending.connection, "idp_error", oauthErr,
			"idp_error_description", q.Get("error_description"))
		writeOAuthError(w, "upstream OAuth error: "+oauthErr)
		return false
	}
	if q.Get(formKeyCode) == "" {
		writeOAuthError(w, "missing authorization code")
		return false
	}
	return true
}

// completeAPIGatewayCallback runs the connection-load → token-
// exchange → token-persist sequence. Returns false (and writes the
// HTML error page) on any sub-step failure; the caller should not
// proceed to the redirect when this returns false.
func (h *Handler) completeAPIGatewayCallback(w http.ResponseWriter, r *http.Request, code string, pending *PKCEState) bool {
	inst, err := h.deps.ConnectionStore.Get(r.Context(), apigatewaykit.Kind, pending.connection)
	if err != nil {
		writeOAuthError(w, "connection not found")
		return false
	}
	cfg, err := apigatewaykit.ParseConfig(inst.Config)
	if err != nil {
		writeOAuthError(w, "connection config invalid")
		return false
	}
	tok, err := exchangeAPIGatewayCode(r.Context(), cfg.OAuth2, code, pending.codeVerifier, pending.redirectURI)
	if err != nil {
		slog.Warn("api-gateway oauth-callback: token exchange failed",
			logKeyName, pending.connection, logKeyError, err)
		writeOAuthError(w, "token exchange failed")
		return false
	}
	tk := h.findAPIGatewayToolkit()
	if tk == nil || tk.TokenStore() == nil {
		writeOAuthError(w, "api gateway toolkit not configured for OAuth")
		return false
	}
	persisted := apigatewaykit.PersistedToken{
		ConnectionName:   pending.connection,
		AccessToken:      tok.AccessToken,
		RefreshToken:     tok.RefreshToken,
		ExpiresAt:        tok.Expiry,
		RefreshExpiresAt: tok.RefreshExpiresAt,
		Scope:            tok.Scope,
		AuthenticatedBy:  pending.startedBy,
		AuthenticatedAt:  time.Now(),
	}
	if err := tk.TokenStore().Set(r.Context(), persisted); err != nil {
		slog.Error("api-gateway oauth-callback: persist token failed",
			logKeyName, pending.connection, logKeyError, err)
		writeOAuthError(w, "failed to persist token")
		return false
	}
	return true
}

// loadAPIGatewayAuthCodeConfig is the api-gateway analog of
// loadAuthCodeOAuthConfig in gateway_oauth_handler.go. Verifies the
// connection exists, parses its config, and confirms it's set up
// for the authorization_code grant before any PKCE state is
// generated.
func (h *Handler) loadAPIGatewayAuthCodeConfig(w http.ResponseWriter, r *http.Request, name string) (apigatewaykit.Config, bool) {
	inst, err := h.deps.ConnectionStore.Get(r.Context(), apigatewaykit.Kind, name)
	if err != nil {
		if errors.Is(err, platform.ErrConnectionNotFound) {
			writeError(w, http.StatusNotFound, "api gateway connection not found")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to load connection")
		}
		return apigatewaykit.Config{}, false
	}
	cfg, err := apigatewaykit.ParseConfig(inst.Config)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return apigatewaykit.Config{}, false
	}
	if cfg.AuthMode != apigatewaykit.AuthModeOAuth2AuthorizationCode {
		writeError(w, http.StatusConflict, "connection is not configured for authorization_code OAuth")
		return apigatewaykit.Config{}, false
	}
	return cfg, true
}

// findAPIGatewayToolkit returns the live api gateway toolkit from
// the registry, or nil when none is registered. Mirrors
// findGatewayToolkit but for kind=api.
func (h *Handler) findAPIGatewayToolkit() *apigatewaykit.Toolkit {
	if h.deps.ToolkitRegistry == nil {
		return nil
	}
	for _, tk := range h.deps.ToolkitRegistry.All() {
		if api, ok := tk.(*apigatewaykit.Toolkit); ok {
			return api
		}
	}
	return nil
}

// buildAPIGatewayCallbackURL composes the callback URL the IdP
// should redirect to after authentication. Built from the request's
// scheme + host so deployments behind a reverse proxy (where the
// platform sees a different host than the operator's browser) Just
// Work as long as the proxy populates X-Forwarded-Host.
func buildAPIGatewayCallbackURL(r *http.Request) string {
	scheme := "https"
	if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") == "" {
		scheme = "http"
	}
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	}
	host := r.Host
	if fh := r.Header.Get("X-Forwarded-Host"); fh != "" {
		host = fh
	}
	return scheme + "://" + host + "/api/v1/admin/api-gateway/oauth/callback"
}

// buildAPIGatewayAuthorizationURL composes the IdP's authorization
// URL with the PKCE challenge attached. SHA-256 challenge per RFC
// 7636 §4.2 — the only method OAuth 2.1 mandates.
func buildAPIGatewayAuthorizationURL(cfg apigatewaykit.OAuth2Config, state, verifier, redirectURI string) string {
	challenge := pkceS256Challenge(verifier)
	v := url.Values{}
	v.Set("response_type", "code")
	v.Set("client_id", cfg.ClientID)
	v.Set("redirect_uri", redirectURI)
	v.Set("state", state)
	v.Set("code_challenge", challenge)
	v.Set("code_challenge_method", "S256")
	if len(cfg.Scopes) > 0 {
		v.Set("scope", joinScopes(cfg.Scopes))
	}
	if cfg.Prompt != "" {
		v.Set("prompt", cfg.Prompt)
	}
	sep := "?"
	if u, err := url.Parse(cfg.AuthorizationURL); err == nil && u.RawQuery != "" {
		sep = "&"
	}
	return cfg.AuthorizationURL + sep + v.Encode()
}

// pkceS256Challenge derives the code_challenge from the verifier
// using SHA-256 + base64url-no-padding (RFC 7636 §4.2).
func pkceS256Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// joinScopes assembles the space-delimited scope string OAuth 2.1
// expects on the authorize URL. Single-allocation for the common
// case of 0–4 scopes.
func joinScopes(scopes []string) string {
	if len(scopes) == 0 {
		return ""
	}
	out := scopes[0]
	for _, s := range scopes[1:] {
		out = out + " " + s
	}
	return out
}

// exchangeAPIGatewayCode performs the authorization-code → token
// exchange against the IdP. Mirrors the MCP gateway's
// exchangeAuthorizationCode in security guards:
//   - codeExchangeClient (CheckRedirect: ErrUseLastResponse) refuses
//     to follow 3xx so a misconfigured or compromised IdP cannot
//     redirect the credential-bearing POST (client_secret +
//     authorization_code + code_verifier) to an attacker URL.
//   - LimitReader caps the response read at maxCodeExchangeBodyBytes
//     so a hostile IdP cannot OOM the platform with a multi-GB
//     stream; oversize bodies are explicitly rejected (silently
//     parsing a truncated JSON document would let an attacker feed
//     attacker-controlled fields into the freshly-stored token row).
//   - The decode struct includes refresh_expires_in (Keycloak-style)
//     so RefreshExpiresAt is populated when the IdP discloses it.
func exchangeAPIGatewayCode(ctx context.Context, cfg apigatewaykit.OAuth2Config, code, verifier, redirectURI string) (*apiGatewayExchangeResult, error) {
	req, err := buildAPIGatewayTokenRequest(ctx, cfg, code, verifier, redirectURI)
	if err != nil {
		return nil, err
	}
	resp, err := codeExchangeClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request: %w", err)
	}
	// Drain remaining bytes before close so net/http can pool the TCP
	// connection. With LimitReader capping the read, an oversize body
	// would otherwise leave bytes on the wire and force a re-handshake.
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	bodyBytes, err := readCappedTokenBody(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		// Status only — body can include sensitive material.
		return nil, errors.New("token endpoint returned non-200")
	}
	return decodeAPIGatewayTokenResponse(bodyBytes)
}

// buildAPIGatewayTokenRequest assembles the POST that exchanges the
// authorization code (or, in callers that reuse it, any other grant
// fragment in `form`) for tokens. Split out so exchangeAPIGatewayCode
// stays under the cyclomatic complexity ceiling.
func buildAPIGatewayTokenRequest(ctx context.Context, cfg apigatewaykit.OAuth2Config, code, verifier, redirectURI string) (*http.Request, error) {
	form := url.Values{}
	form.Set(formKeyGrantType, grantTypeAuthorizationCode)
	form.Set(formKeyCode, code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", cfg.ClientID)
	form.Set("code_verifier", verifier)
	if cfg.EndpointAuthStyle == apigatewaykit.OAuth2AuthStyleParams {
		form.Set("client_secret", cfg.ClientSecret)
	}

	// #nosec G107 G704 -- TokenURL is operator-authored connection config
	// (admin endpoint, validated by ParseConfig). Same sink shape as
	// gateway exchangeAuthorizationCode (already audited).
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.TokenURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if cfg.EndpointAuthStyle != apigatewaykit.OAuth2AuthStyleParams {
		req.SetBasicAuth(cfg.ClientID, cfg.ClientSecret)
	}
	return req, nil
}

// readCappedTokenBody reads up to maxCodeExchangeBodyBytes+1 from
// the response body and rejects the read with an explicit
// cap-exceeded error if the body exceeded the limit. The +1 is
// what lets the caller DETECT truncation rather than silently
// parse a truncated JSON document.
func readCappedTokenBody(body io.Reader) ([]byte, error) {
	bodyBytes, err := io.ReadAll(io.LimitReader(body, maxCodeExchangeBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read token response: %w", err)
	}
	if int64(len(bodyBytes)) > maxCodeExchangeBodyBytes {
		return nil, fmt.Errorf("token response exceeds %d-byte cap (likely misbehaving IdP)", maxCodeExchangeBodyBytes)
	}
	return bodyBytes, nil
}

// decodeAPIGatewayTokenResponse parses the IdP's token-endpoint
// JSON. RefreshExpiresIn is Keycloak-style; absent on most other
// IdPs, in which case PersistedToken.RefreshExpiresAt stays zero.
func decodeAPIGatewayTokenResponse(bodyBytes []byte) (*apiGatewayExchangeResult, error) {
	var raw struct {
		AccessToken      string `json:"access_token"`
		RefreshToken     string `json:"refresh_token"`
		TokenType        string `json:"token_type"`
		ExpiresIn        int64  `json:"expires_in"`
		RefreshExpiresIn int64  `json:"refresh_expires_in"`
		Scope            string `json:"scope"`
	}
	if err := json.Unmarshal(bodyBytes, &raw); err != nil {
		return nil, errors.New("token endpoint returned malformed JSON")
	}
	if raw.AccessToken == "" {
		return nil, errors.New("token endpoint returned no access_token")
	}
	now := time.Now()
	expiry := time.Time{}
	if raw.ExpiresIn > 0 {
		expiry = now.Add(time.Duration(raw.ExpiresIn) * time.Second)
	}
	refreshExpiresAt := time.Time{}
	if raw.RefreshExpiresIn > 0 {
		refreshExpiresAt = now.Add(time.Duration(raw.RefreshExpiresIn) * time.Second)
	}
	return &apiGatewayExchangeResult{
		AccessToken:      raw.AccessToken,
		RefreshToken:     raw.RefreshToken,
		Expiry:           expiry,
		RefreshExpiresAt: refreshExpiresAt,
		Scope:            raw.Scope,
	}, nil
}

// formKeyCode / formKeyGrantType are the OAuth 2.1 form keys; these
// names appear in 4+ places (this file's exchange path plus tests
// that build their own fixtures), so a single source resolves the
// add-constant lint and prevents typos across the surface.
const (
	formKeyCode                = "code"
	formKeyGrantType           = "grant_type"
	grantTypeAuthorizationCode = "authorization_code"
)

// apiGatewayExchangeResult carries the parsed token-exchange
// response. Mirrors oauth2.Token but adds RefreshExpiresAt so a
// Keycloak-style refresh_expires_in is preserved through to
// PersistedToken (the auth.go refresh path checks RefreshExpiresAt
// to short-circuit dead-refresh attempts).
type apiGatewayExchangeResult struct {
	AccessToken      string
	RefreshToken     string
	Expiry           time.Time
	RefreshExpiresAt time.Time
	Scope            string
}
