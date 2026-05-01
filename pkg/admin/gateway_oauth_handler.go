package admin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/platform"
	gatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/gateway"
)

// oauthErrorPageTemplate renders the stranded-tab fallback HTML with
// context-aware auto-escaping. Using html/template (rather than the
// previous custom escaper) is what CodeQL recognizes as a sound
// sanitizer for upstream-controlled error strings.
var oauthErrorPageTemplate = template.Must(template.New("oauth-error").Parse(
	`<!doctype html><html><head><title>OAuth error</title>
<style>body{font:14px/1.5 -apple-system,system-ui,sans-serif;max-width:480px;margin:6rem auto;padding:2rem;color:#333}h1{font-size:1.2rem;color:#c00}</style>
</head><body>
<h1>OAuth flow failed</h1>
<p>{{.Msg}}</p>
<p><a href="/portal/admin/connections">Return to admin</a></p>
</body></html>`))

// pkceTTL is how long an in-progress oauth-start hold-state remains
// valid before the operator must restart the flow. Salesforce and
// most providers complete the redirect in seconds; 10 minutes is a
// generous window that survives slow MFA prompts.
const pkceTTL = 10 * time.Minute

// PKCEState is the server-side hold for one pending OAuth flow. Maps
// the random state token to the data the callback handler needs.
//
// Exported so the PKCEStore interface (MemoryPKCEStore /
// PostgresPKCEStore) can carry pointers to it across implementations
// without revive flagging the methods as exported-but-returning-
// unexported. Fields stay package-private; consumers from outside
// admin should not need to introspect a state in flight.
type PKCEState struct {
	connection   string
	codeVerifier string
	startedBy    string
	createdAt    time.Time
	returnURL    string
	redirectURI  string
}

// pkceStoreFor returns the handler's injected PKCE store. The store is
// required: oauth-start fails 503 with "OAuth not available" when nil
// (e.g. when a Handler is built without wiring a store). main.go and
// the test helpers always inject one — this guard catches misuse.
func (h *Handler) pkceStoreFor() PKCEStore {
	return h.deps.PKCEStore
}

// registerGatewayOAuthRoutes adds the OAuth-flow endpoints. The callback
// is registered on the public mux so the OAuth provider's redirect can
// hit it without an admin auth header — the state token + PKCE verifier
// authenticate the callback instead.
func (h *Handler) registerGatewayOAuthRoutes() {
	if !h.isMutable() || h.deps.ConnectionStore == nil {
		return
	}
	h.mux.HandleFunc("POST /api/v1/admin/gateway/connections/{name}/oauth-start", h.startGatewayOAuth)
	h.publicMux.HandleFunc("GET /api/v1/admin/oauth/callback", h.gatewayOAuthCallback)
}

// startGatewayOAuthRequest is the optional body for the start endpoint.
type startGatewayOAuthRequest struct {
	ReturnURL string `json:"return_url,omitempty"`
}

// startGatewayOAuthResponse hands the admin UI the URL it should open in
// a new browser tab to begin the upstream's OAuth dance.
type startGatewayOAuthResponse struct {
	AuthorizationURL string `json:"authorization_url"`
	State            string `json:"state"`
	RedirectURI      string `json:"redirect_uri"`
	ExpiresAt        string `json:"expires_at"`
}

// startGatewayOAuth handles POST .../oauth-start.
//
// @Summary      Begin OAuth authorization-code flow for a gateway connection
// @Description  Generates a PKCE verifier, derives the SHA256 challenge, registers a state token, and returns the authorization URL the operator should open in their browser. The platform expects the upstream to redirect to /api/v1/admin/oauth/callback after the user authenticates.
// @Tags         Connections
// @Accept       json
// @Produce      json
// @Param        name  path  string                       true  "Gateway connection name"
// @Param        body  body  startGatewayOAuthRequest     false  "Optional return URL"
// @Success      200   {object}  startGatewayOAuthResponse
// @Failure      400   {object}  problemDetail
// @Failure      404   {object}  problemDetail
// @Failure      409   {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/gateway/connections/{name}/oauth-start [post]
func (h *Handler) startGatewayOAuth(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue(pathKeyName)
	cfg, ok := h.loadAuthCodeOAuthConfig(w, r, name)
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
	redirectURI := buildOAuthCallbackURL(r)
	authURL := buildAuthorizationURL(cfg.OAuth, state, verifier, redirectURI)

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
		slog.Error("oauth-start: failed to persist pkce state",
			logKeyName, name,
			logKeyStartedBy, startedBy,
			logKeyError, err)
		writeError(w, http.StatusInternalServerError, "failed to record OAuth state")
		return
	}

	// Log every oauth-start with enough context to correlate against
	// the matching callback without leaking secrets. The state token
	// is truncated (first 8 chars) — full state is what authenticates
	// the callback, so it must not appear in logs.
	slog.Info("oauth-start: PKCE state issued",
		logKeyName, name,
		logKeyStartedBy, startedBy,
		logKeyStatePrefix, truncateForLog(state),
		logKeyRedirectURI, redirectURI,
		"authorization_url_host", urlHost(cfg.OAuth.AuthorizationURL),
		"return_url", body.ReturnURL,
		"ttl", pkceTTL)

	writeJSON(w, http.StatusOK, startGatewayOAuthResponse{
		AuthorizationURL: authURL,
		State:            state,
		RedirectURI:      redirectURI,
		ExpiresAt:        time.Now().Add(pkceTTL).UTC().Format(time.RFC3339),
	})
}

// Structured-log field names used across oauth-start / oauth-callback /
// oauth-exchange so revive's add-constant rule is satisfied AND log
// fields stay consistent for grep / dashboard alignment.
const (
	logKeyStatePrefix  = "state_prefix"
	logKeyStartedBy    = "started_by"
	logKeyDuration     = "duration"
	logKeyTokenURLHost = "token_url_host" // #nosec G101 -- structured-log key name, not a credential
	logKeyRedirectURI  = "redirect_uri"
)

// truncateForLog returns the first 8 chars of s with "…" suffix when
// truncated. Used so the platform can log a state-token prefix for
// correlation across oauth-start / oauth-callback without exposing
// the full secret in log files.
func truncateForLog(s string) string {
	const truncateLen = 8
	if len(s) <= truncateLen {
		return s
	}
	return s[:truncateLen] + "…"
}

// urlHost returns just the host portion of u, falling back to the
// raw string when parsing fails. Used in logs to show "which IdP"
// without dragging the full path/query into log files.
func urlHost(u string) string {
	parsed, err := url.Parse(u)
	if err != nil || parsed.Host == "" {
		return u
	}
	return parsed.Host
}

// loadAuthCodeOAuthConfig looks up the named connection, parses its
// config, and verifies it's configured for authorization_code OAuth.
// Writes the appropriate HTTP error and returns ok=false on any
// failure path.
func (h *Handler) loadAuthCodeOAuthConfig(w http.ResponseWriter, r *http.Request, name string) (gatewaykit.Config, bool) {
	inst, err := h.deps.ConnectionStore.Get(r.Context(), gatewaykit.Kind, name)
	if err != nil {
		if errors.Is(err, platform.ErrConnectionNotFound) {
			writeError(w, http.StatusNotFound, "gateway connection not found")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to load connection")
		}
		return gatewaykit.Config{}, false
	}
	cfg, err := gatewaykit.ParseConfig(inst.Config)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return gatewaykit.Config{}, false
	}
	if cfg.AuthMode != gatewaykit.AuthModeOAuth ||
		cfg.OAuth.Grant != gatewaykit.OAuthGrantAuthorizationCode {
		writeError(w, http.StatusConflict, "connection is not configured for authorization_code OAuth")
		return gatewaykit.Config{}, false
	}
	return cfg, true
}

// generatePKCEPair returns (verifier, state) or writes a 500 and
// returns ok=false on entropy-source failure.
func generatePKCEPair(w http.ResponseWriter) (verifier, state string, ok bool) {
	verifier, err := generatePKCEVerifier()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate PKCE verifier")
		return "", "", false
	}
	state, err = generatePKCEState()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate state")
		return "", "", false
	}
	return verifier, state, true
}

// gatewayOAuthCallback handles GET /api/v1/admin/oauth/callback.
//
// The OAuth provider redirects the operator's browser here with `code`
// and `state` query parameters. We look up the PKCE state, exchange the
// code for tokens at the upstream's token endpoint, and persist the
// tokens via the gateway toolkit. On success the user is redirected to
// the original return URL (or /portal/admin/connections by default) so
// the admin UI can immediately reflect the connected status.
//
// @Summary      OAuth authorization-code callback
// @Description  Public endpoint hit by the upstream OAuth provider after the operator authenticates. Exchanges the code for tokens and stores them. Renders an HTML page on error so a stranded browser tab still gives a useful message.
// @Tags         Connections
// @Produce      html
// @Param        code   query  string  false  "OAuth authorization code"
// @Param        state  query  string  true   "PKCE state token from oauth-start"
// @Param        error  query  string  false  "OAuth error code from upstream"
// @Param        error_description  query  string  false  "Human-readable error from upstream"
// @Success      302
// @Failure      400  {object}  problemDetail
// @Router       /admin/oauth/callback [get]
func (h *Handler) gatewayOAuthCallback(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	q := r.URL.Query()
	state := q.Get("state")
	statePrefix := truncateForLog(state)
	slog.Info("oauth-callback: received",
		logKeyStatePrefix, statePrefix,
		"has_code", q.Get("code") != "",
		"has_error", q.Get("error") != "",
		"remote_addr", r.RemoteAddr)
	if state == "" {
		slog.Warn("oauth-callback: missing state parameter")
		writeOAuthError(w, "missing state parameter")
		return
	}
	store := h.pkceStoreFor()
	if store == nil {
		slog.Error("oauth-callback: PKCE store not configured")
		writeOAuthError(w, "OAuth not available: PKCE store not configured")
		return
	}
	pending, err := store.Take(r.Context(), state)
	if err != nil {
		if errors.Is(err, ErrPKCEStateNotFound) {
			slog.Warn("oauth-callback: PKCE state not found (expired or unknown)",
				logKeyStatePrefix, statePrefix)
			writeOAuthError(w, "OAuth state expired or unknown — please retry from the admin UI")
			return
		}
		slog.Error("oauth-callback: pkce store lookup failed",
			logKeyStatePrefix, statePrefix, logKeyError, err)
		writeOAuthError(w, "OAuth state lookup failed")
		return
	}
	slog.Info("oauth-callback: PKCE state retrieved",
		logKeyName, pending.connection,
		logKeyStartedBy, pending.startedBy,
		logKeyStatePrefix, statePrefix,
		"age", time.Since(pending.createdAt))

	if errCode := q.Get("error"); errCode != "" {
		errDesc := q.Get("error_description")
		slog.Warn("oauth-callback: IdP returned error",
			logKeyName, pending.connection,
			"idp_error", errCode,
			"idp_error_description", errDesc)
		writeOAuthError(w, fmt.Sprintf("upstream returned %s: %s", errCode, errDesc))
		return
	}
	code := q.Get("code")
	if code == "" {
		slog.Warn("oauth-callback: missing code parameter",
			logKeyName, pending.connection)
		writeOAuthError(w, "missing code parameter")
		return
	}

	if err := h.completeOAuthExchange(r.Context(), pending, code); err != nil {
		slog.Error("oauth-callback: token exchange failed",
			logKeyName, pending.connection,
			logKeyStartedBy, pending.startedBy,
			logKeyDuration, time.Since(start),
			logKeyError, err)
		writeOAuthError(w, "token exchange failed: "+err.Error())
		return
	}

	// safeReturnURL only ever returns either the constant fallback
	// "/portal/admin/connections" or a same-origin relative path that
	// begins with "/" and contains no ":" or backslash-protocol-relative
	// form (covered by TestSafeReturnURL). http.Redirect is fine here
	// because the destination cannot reach an external host; we use it
	// for the small "<a href>Found</a>" body it writes, which gives
	// non-browser HTTP clients (curl, scripts) a useful response body.
	dest := safeReturnURL(pending.returnURL)
	slog.Info("oauth-callback: success — tokens persisted, redirecting",
		logKeyName, pending.connection,
		logKeyStartedBy, pending.startedBy,
		logKeyDuration, time.Since(start),
		"dest", dest)
	http.Redirect(w, r, dest, http.StatusFound) // nosemgrep: go.lang.security.injection.open-redirect.open-redirect
}

// safeReturnURL constrains post-OAuth redirects to same-origin relative
// paths so a tampered or maliciously authored returnURL cannot bounce
// the browser to an attacker-controlled host.
//
// Must reject:
//   - Absolute URLs ("https://evil.example.com/x")
//   - Protocol-relative URLs ("//evil.example.com/x")
//   - Backslash-protocol-relative URLs ("/\evil.example.com/x") — some
//     browsers normalise the backslash to a forward slash before
//     parsing, turning the path into a host
//   - Anything that doesn't start with "/"
//   - Anything containing ":" (URL scheme indicator like
//     "javascript:alert(1)") even after the leading slash
func safeReturnURL(raw string) string {
	const fallback = "/portal/admin/connections"
	if raw == "" || !strings.HasPrefix(raw, "/") {
		return fallback
	}
	// Second character must not be "/" or "\\" — those forms can be
	// interpreted by browsers as protocol-relative URLs.
	if len(raw) > 1 && (raw[1] == '/' || raw[1] == '\\') {
		return fallback
	}
	// Block any URL-scheme-like content (covers "javascript:" and
	// stray colons in the redirect target). Same-origin relative
	// paths reach the admin shell without needing a colon.
	if strings.Contains(raw, ":") {
		return fallback
	}
	return raw
}

// completeOAuthExchange swaps the authorization code for tokens and
// hands them to the gateway toolkit. The toolkit re-adds the connection
// so the previously "needs reauth" entry becomes live with its
// discovered tools registered on the MCP server.
func (h *Handler) completeOAuthExchange(ctx context.Context, pending *PKCEState, code string) error {
	inst, err := h.deps.ConnectionStore.Get(ctx, gatewaykit.Kind, pending.connection)
	if err != nil {
		return fmt.Errorf("load connection: %w", err)
	}
	cfg, err := gatewaykit.ParseConfig(inst.Config)
	if err != nil {
		return fmt.Errorf("parse connection: %w", err)
	}
	tr, err := exchangeAuthorizationCode(ctx, cfg.OAuth, pending, code)
	if err != nil {
		return err
	}
	return h.persistOAuthTokens(ctx, pending, inst.Config, tr)
}

// authCodeTokenResponse is the parsed token-endpoint response.
type authCodeTokenResponse struct {
	AccessToken  string `json:"access_token"` //nolint:gosec // OAuth response shape, not a credential
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresIn    int    `json:"expires_in,omitempty"`
	Scope        string `json:"scope,omitempty"`
	Error        string `json:"error,omitempty"`
	ErrorDesc    string `json:"error_description,omitempty"`
}

// exchangeAuthorizationCode POSTs the code + PKCE verifier to the
// upstream's token endpoint and returns the parsed response.
func exchangeAuthorizationCode(ctx context.Context, oc gatewaykit.OAuthConfig,
	pending *PKCEState, code string,
) (*authCodeTokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("client_id", oc.ClientID)
	form.Set("client_secret", oc.ClientSecret)
	form.Set("redirect_uri", pending.redirectURI)
	form.Set("code_verifier", pending.codeVerifier)

	// #nosec G107 G704 -- TokenURL is operator-authored connection config.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, oc.TokenURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	exchangeStart := time.Now()
	slog.Debug("oauth-exchange: posting authorization_code grant",
		logKeyTokenURLHost, urlHost(oc.TokenURL),
		"client_id", oc.ClientID,
		logKeyRedirectURI, pending.redirectURI)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Error("oauth-exchange: token request transport error",
			logKeyTokenURLHost, urlHost(oc.TokenURL),
			logKeyDuration, time.Since(exchangeStart),
			logKeyError, err)
		return nil, fmt.Errorf("token request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	bodyBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		slog.Warn("oauth-exchange: non-200 from token endpoint",
			logKeyTokenURLHost, urlHost(oc.TokenURL),
			"status", resp.StatusCode,
			logKeyDuration, time.Since(exchangeStart),
			"body_excerpt", trimOAuthBody(bodyBytes))
		return nil, fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, trimOAuthBody(bodyBytes))
	}
	var tr authCodeTokenResponse
	if jerr := json.Unmarshal(bodyBytes, &tr); jerr != nil {
		slog.Error("oauth-exchange: decode response failed",
			logKeyTokenURLHost, urlHost(oc.TokenURL), logKeyError, jerr)
		return nil, fmt.Errorf("decode token response: %w", jerr)
	}
	if tr.Error != "" {
		slog.Warn("oauth-exchange: structured error in token response",
			logKeyTokenURLHost, urlHost(oc.TokenURL),
			"idp_error", tr.Error,
			"idp_error_description", tr.ErrorDesc)
		return nil, fmt.Errorf("upstream %s: %s", tr.Error, tr.ErrorDesc)
	}
	if tr.AccessToken == "" {
		slog.Warn("oauth-exchange: token response missing access_token",
			logKeyTokenURLHost, urlHost(oc.TokenURL))
		return nil, errors.New("token response missing access_token")
	}
	slog.Info("oauth-exchange: success",
		logKeyTokenURLHost, urlHost(oc.TokenURL),
		logKeyDuration, time.Since(exchangeStart),
		"access_token_len", len(tr.AccessToken),
		"refresh_token_present", tr.RefreshToken != "",
		"expires_in", tr.ExpiresIn,
		"scope", tr.Scope)
	return &tr, nil
}

// persistOAuthTokens hands the freshly-exchanged tokens to the live
// gateway toolkit. Re-creates the connection placeholder when missing
// (e.g. after a platform restart between oauth-start and callback).
func (h *Handler) persistOAuthTokens(ctx context.Context, pending *PKCEState,
	connConfig map[string]any, tr *authCodeTokenResponse,
) error {
	tk := h.findGatewayToolkit()
	if tk == nil {
		return errors.New("gateway toolkit is not registered")
	}
	if !tk.HasConnection(pending.connection) {
		if addErr := tk.AddConnection(pending.connection, connConfig); addErr != nil {
			return fmt.Errorf("seed connection placeholder: %w", addErr)
		}
	}
	if err := tk.IngestOAuthToken(ctx, gatewaykit.IngestOAuthTokenInput{
		Name:            pending.connection,
		AccessToken:     tr.AccessToken,
		RefreshToken:    tr.RefreshToken,
		ExpiresIn:       tr.ExpiresIn,
		Scope:           tr.Scope,
		AuthenticatedBy: pending.startedBy,
	}); err != nil {
		return fmt.Errorf("ingest oauth token: %w", err)
	}
	return nil
}

// generatePKCEVerifier creates a cryptographically random URL-safe
// string suitable as an RFC 7636 code_verifier (43-128 chars).
func generatePKCEVerifier() (string, error) {
	b := make([]byte, 64)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("rand: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// generatePKCEState creates a random URL-safe state token.
func generatePKCEState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("rand: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// pkceChallenge derives the S256 challenge from a verifier per RFC 7636.
func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// buildAuthorizationURL composes the upstream's authorization URL with
// the PKCE challenge. The redirect_uri must exactly match what's
// registered with the OAuth provider (Salesforce External Client App,
// etc.).
//
// Includes prompt=login (OIDC §3.1.2.1) so each Reconnect click forces
// the upstream to render a fresh credential form, even when the user
// holds an active SSO session against the realm. Without this, an
// operator who just authenticated in another tab can find themselves
// staring at a stale Keycloak login form whose hidden session_code
// was already consumed by the prior flow — submitting it silently
// does nothing, which is exactly the failure mode reported as
// "clicked Sign In and nothing happened."
//
// The trade-off: operators with an active SSO session are prompted
// for credentials again rather than being silently passed through.
// For an admin "authorize this gateway connection" flow, that's the
// correct security posture — explicit re-authentication on each
// Reconnect, not a transparent SSO grant.
func buildAuthorizationURL(cfg gatewaykit.OAuthConfig, state, verifier, redirectURI string) string {
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", cfg.ClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	q.Set("code_challenge", pkceChallenge(verifier))
	q.Set("code_challenge_method", "S256")
	q.Set("prompt", "login")
	if cfg.Scope != "" {
		q.Set("scope", cfg.Scope)
	}

	sep := "?"
	if strings.Contains(cfg.AuthorizationURL, "?") {
		sep = "&"
	}
	return cfg.AuthorizationURL + sep + q.Encode()
}

// buildOAuthCallbackURL derives the callback URL from the request's
// Host + scheme. Operators register this URL with their OAuth provider
// once, then never have to think about it.
func buildOAuthCallbackURL(r *http.Request) string {
	scheme := "https"
	if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") == "" {
		scheme = "http"
	}
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	}
	host := r.Host
	if fwd := r.Header.Get("X-Forwarded-Host"); fwd != "" {
		host = fwd
	}
	return fmt.Sprintf("%s://%s/api/v1/admin/oauth/callback", scheme, host)
}

// writeOAuthError renders a minimal HTML page for stranded browser tabs.
// Uses html/template auto-escaping so upstream-controlled strings
// (e.g. an OAuth provider's error_description) cannot inject markup.
func writeOAuthError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	_ = oauthErrorPageTemplate.Execute(w, struct{ Msg string }{Msg: msg})
}

// trimOAuthBody caps response body size in error messages so a noisy
// upstream can't fill an audit log.
func trimOAuthBody(body []byte) string {
	const maxBytes = 256
	if len(body) <= maxBytes {
		return string(body)
	}
	return string(body[:maxBytes]) + "..."
}
