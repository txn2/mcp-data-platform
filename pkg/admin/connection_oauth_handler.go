package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/authevents"
	"github.com/txn2/mcp-data-platform/pkg/connoauth"
	"github.com/txn2/mcp-data-platform/pkg/platform"
)

// pathKeyKind is the URL path parameter that selects which
// OAuthKindHandler handles the request. Matches the routes registered
// under /api/v1/admin/connections/{kind}/{name}/...
const pathKeyKind = "kind"

// logKeyKind is already defined in connection_handler.go for the
// connection-kind structured-log field; reused here so oauth-start /
// oauth-callback / oauth-status log lines align on the same key.

// registerConnectionOAuthRoutes installs the unified OAuth flow
// endpoints. Replaces the two prior per-kind route blocks
// (registerGatewayOAuthRoutes + registerAPIGatewayOAuthRoutes) with
// one shared set parameterized on {kind}. The callback URL is
// intentionally unchanged from the prior MCP path
// (/api/v1/admin/oauth/callback) — operators have already registered
// it with their upstream IdPs (Keycloak / Auth0 / Okta / Microsoft
// App Registrations); breaking the URL would force every customer to
// reconfigure their IdP client.
func (h *Handler) registerConnectionOAuthRoutes() {
	if !h.isMutable() || h.deps.ConnectionStore == nil || h.deps.ConnOAuthStore == nil {
		return
	}
	h.mux.HandleFunc("POST /api/v1/admin/connections/{kind}/{name}/oauth-start", h.startConnectionOAuth)
	h.mux.HandleFunc("GET /api/v1/admin/connections/{kind}/{name}/oauth-status", h.connectionOAuthStatus)
	h.mux.HandleFunc("GET /api/v1/admin/connections/{kind}/{name}/auth-events", h.connectionAuthEvents)
	h.mux.HandleFunc("POST /api/v1/admin/connections/{kind}/{name}/reacquire-oauth", h.reacquireConnectionOAuth)
	h.publicMux.HandleFunc("GET /api/v1/admin/oauth/callback", h.connectionOAuthCallback)
	// Legacy API-gateway callback URL. Existing deployments may have
	// this URL registered in customer IdP client configurations; keep
	// it as an alias for the unified callback so the IdP redirect
	// continues to resolve. Removed in a follow-up after one release.
	h.publicMux.HandleFunc("GET /api/v1/admin/api-gateway/oauth/callback", h.connectionOAuthCallback)
}

// startConnectionOAuthRequest is the optional POST body. The
// operator-supplied returnURL is run through safeReturnURL on the
// callback side to defeat open-redirect probes.
type startConnectionOAuthRequest struct {
	ReturnURL string `json:"return_url,omitempty"`
}

// startConnectionOAuthResponse hands the portal the URL it should
// open in a new browser tab to begin the upstream's OAuth flow.
type startConnectionOAuthResponse struct {
	AuthorizationURL string `json:"authorization_url"`
	State            string `json:"state"`
	RedirectURI      string `json:"redirect_uri"`
	ExpiresAt        string `json:"expires_at"`
}

// startConnectionOAuth handles POST /connections/{kind}/{name}/oauth-start.
// Replaces both startGatewayOAuth and startAPIGatewayOAuth.
//
// @Summary      Begin OAuth authorization-code flow for any connection
// @Description  Validates the connection is configured for authorization_code OAuth, generates a PKCE verifier, registers a state token (with kind), and returns the authorization URL the operator should open in their browser. Works for any connection kind registered in OAuthKinds.
// @Tags         Connections
// @Accept       json
// @Produce      json
// @Param        kind  path  string                          true  "Connection kind (mcp, api)"
// @Param        name  path  string                          true  "Connection name"
// @Param        body  body  startConnectionOAuthRequest     false "Optional return URL"
// @Success      200   {object}  startConnectionOAuthResponse
// @Failure      400   {object}  problemDetail
// @Failure      404   {object}  problemDetail
// @Failure      409   {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/connections/{kind}/{name}/oauth-start [post]
func (h *Handler) startConnectionOAuth(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue(pathKeyKind)
	name := r.PathValue(pathKeyName)
	handler, ok := h.lookupOAuthKindHandler(w, kind)
	if !ok {
		return
	}
	inst, ok := h.loadConnectionForOAuth(w, r, kind, name)
	if !ok {
		return
	}
	cfg, ok := h.parseConnectionOAuthConfig(w, handler, inst.Config)
	if !ok {
		return
	}

	var body startConnectionOAuthRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	verifier, state, ok := generatePKCEPair(w)
	if !ok {
		return
	}
	redirectURI := buildOAuthCallbackURL(r)
	authURL := buildConnectionAuthorizationURL(cfg, state, verifier, redirectURI)

	store := h.pkceStoreFor()
	if store == nil {
		writeError(w, http.StatusServiceUnavailable, "OAuth not available: PKCE store not configured")
		return
	}
	startedBy := authorEmailOrID(r.Context())
	if err := store.Put(r.Context(), state, &PKCEState{
		kind:         kind,
		connection:   name,
		codeVerifier: verifier,
		startedBy:    startedBy,
		createdAt:    time.Now(),
		returnURL:    body.ReturnURL,
		redirectURI:  redirectURI,
	}); err != nil {
		slog.Error("oauth-start: failed to persist pkce state",
			logKeyKind, kind, logKeyName, name, logKeyStartedBy, startedBy, logKeyError, err)
		writeError(w, http.StatusInternalServerError, "failed to record OAuth state")
		return
	}

	slog.Info("oauth-start: PKCE state issued",
		logKeyKind, kind,
		logKeyName, name,
		logKeyStartedBy, startedBy,
		logKeyStatePrefix, truncateForLog(state),
		logKeyRedirectURI, redirectURI,
		"authorization_url_host", urlHostForLog(cfg.AuthorizationURL),
		"return_url", body.ReturnURL,
		"ttl", pkceTTL)
	h.deps.AuthEvents.ConnectStarted(r.Context(), kind, name, startedBy, cfg.TokenURL, body.ReturnURL)

	writeJSON(w, http.StatusOK, startConnectionOAuthResponse{
		AuthorizationURL: authURL,
		State:            state,
		RedirectURI:      redirectURI,
		ExpiresAt:        time.Now().Add(pkceTTL).UTC().Format(time.RFC3339),
	})
}

// connectionOAuthStatus handles GET /connections/{kind}/{name}/oauth-status.
// Returns the connoauth.OAuthStatus snapshot the portal renders in
// OAuthStatusCard. Works for any registered kind.
//
// @Summary      OAuth status for a connection
// @Tags         Connections
// @Produce      json
// @Param        kind  path  string  true  "Connection kind"
// @Param        name  path  string  true  "Connection name"
// @Success      200   {object}  connoauth.OAuthStatus
// @Failure      404   {object}  problemDetail
// @Failure      409   {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/connections/{kind}/{name}/oauth-status [get]
func (h *Handler) connectionOAuthStatus(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue(pathKeyKind)
	name := r.PathValue(pathKeyName)
	handler, ok := h.lookupOAuthKindHandler(w, kind)
	if !ok {
		return
	}
	inst, ok := h.loadConnectionForOAuth(w, r, kind, name)
	if !ok {
		return
	}
	cfg, ok := h.parseConnectionOAuthConfig(w, handler, inst.Config)
	if !ok {
		return
	}
	src := connoauth.NewSource(h.deps.ConnOAuthStore, connoauth.Key{Kind: kind, Name: name}, cfg).
		WithEvents(h.deps.AuthEvents).
		WithActor(authorEmailOrID(r.Context()))
	status := src.Status(r.Context())
	status.LastRevocation = h.lastRevocationFor(r.Context(), kind, name)
	writeJSON(w, http.StatusOK, status)
}

// lastRevocationFor reads the most recent revocation event for the
// connection from the auth-events table, if any. Returns nil when the
// store is unavailable, the lookup fails, or no revocation event
// exists. Powering this from the events table (not transient
// in-memory state) means the answer survives process restarts —
// operators see "your previous session was killed at 10:42" even
// after a redeploy clears the in-memory view.
func (h *Handler) lastRevocationFor(ctx context.Context, kind, name string) *connoauth.RevocationEvent {
	if h.deps.AuthEventStore == nil {
		return nil
	}
	events, err := h.deps.AuthEventStore.List(ctx, authevents.Filter{Kind: kind, Name: name, Limit: 10})
	if err != nil {
		return nil
	}
	for _, ev := range events {
		if ev.Type != authevents.TypeTokenDeletedRevoked &&
			ev.Type != authevents.TypeRefreshFailedRevoked {
			continue
		}
		reason := extractRevocationReason(ev)
		return &connoauth.RevocationEvent{
			OccurredAt: ev.OccurredAt,
			Reason:     reason,
			IDPHost:    ev.IDPHost,
		}
	}
	return nil
}

// extractRevocationReason pulls the reason field from a revocation
// event's detail payload. The two relevant Types use different
// detail shapes: TypeTokenDeletedRevoked is {"reason": "..."} while
// TypeRefreshFailedRevoked is {... "idp_error_code": "..."} (the
// RefreshDetail shape). Either way, we want a single short string
// to surface in the UI.
func extractRevocationReason(ev authevents.Event) string {
	if len(ev.Detail) == 0 {
		return ""
	}
	// Try the deletion event's shape first.
	var byReason struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(ev.Detail, &byReason); err == nil && byReason.Reason != "" {
		return byReason.Reason
	}
	// Fall back to RefreshDetail.IDPErrorCode.
	var rd authevents.RefreshDetail
	if err := json.Unmarshal(ev.Detail, &rd); err == nil && rd.IDPErrorCode != "" {
		return rd.IDPErrorCode
	}
	return ""
}

// connectionAuthEvents handles GET /connections/{kind}/{name}/auth-events.
// Returns the most recent 30 events for the connection so the portal
// can render the History panel. Admin-only (the handler's auth
// middleware enforces this).
//
// @Summary      OAuth lifecycle history for a connection
// @Tags         Connections
// @Produce      json
// @Param        kind  path  string  true  "Connection kind"
// @Param        name  path  string  true  "Connection name"
// @Success      200   {array}   authevents.Event
// @Failure      404   {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/connections/{kind}/{name}/auth-events [get]
func (h *Handler) connectionAuthEvents(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue(pathKeyKind)
	name := r.PathValue(pathKeyName)
	if h.deps.AuthEventStore == nil {
		writeJSON(w, http.StatusOK, []authevents.Event{})
		return
	}
	events, err := h.deps.AuthEventStore.List(r.Context(), authevents.Filter{
		Kind: kind, Name: name, Limit: 30,
	})
	if err != nil {
		slog.Warn("auth-events: list failed",
			logKeyKind, kind, logKeyName, name, logKeyError, err)
		writeError(w, http.StatusInternalServerError, "failed to load auth events")
		return
	}
	if events == nil {
		events = []authevents.Event{}
	}
	writeJSON(w, http.StatusOK, events)
}

// reacquireConnectionOAuth handles POST /connections/{kind}/{name}/reacquire-oauth.
// Forces a refresh-token exchange against the upstream IdP. Useful
// for testing whether the persisted refresh token still works without
// waiting for the access token to expire naturally. Returns 200 on
// success; 409 with a NeedsReauth body when the IdP rejected the
// refresh (operator must complete a fresh Connect).
//
// @Summary      Force a refresh-token exchange for a connection
// @Tags         Connections
// @Produce      json
// @Param        kind  path  string  true  "Connection kind"
// @Param        name  path  string  true  "Connection name"
// @Success      200
// @Failure      404   {object}  problemDetail
// @Failure      409   {object}  problemDetail
// @Failure      502   {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/connections/{kind}/{name}/reacquire-oauth [post]
func (h *Handler) reacquireConnectionOAuth(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue(pathKeyKind)
	name := r.PathValue(pathKeyName)
	handler, ok := h.lookupOAuthKindHandler(w, kind)
	if !ok {
		return
	}
	inst, ok := h.loadConnectionForOAuth(w, r, kind, name)
	if !ok {
		return
	}
	cfg, ok := h.parseConnectionOAuthConfig(w, handler, inst.Config)
	if !ok {
		return
	}
	src := connoauth.NewSource(h.deps.ConnOAuthStore, connoauth.Key{Kind: kind, Name: name}, cfg).
		WithEvents(h.deps.AuthEvents).
		WithActor(authorEmailOrID(r.Context()))
	if err := src.Reacquire(r.Context()); err != nil {
		if errors.Is(err, connoauth.ErrNeedsReauth) {
			writeError(w, http.StatusConflict, "connection needs admin reconnect")
			return
		}
		writeError(w, http.StatusBadGateway, "refresh failed: "+err.Error())
		return
	}
	// Return the post-refresh status so the portal's status card updates
	// immediately without a follow-up GET. Empty 200 broke the portal's
	// apiFetch which assumes a JSON body on success.
	writeJSON(w, http.StatusOK, src.Status(r.Context()))
}

// connectionOAuthCallback handles GET /api/v1/admin/oauth/callback —
// the public endpoint the IdP redirects to after the operator
// authenticates. Replaces both prior callbacks (MCP and API);
// dispatches by reading connection_kind from the PKCE state row.
//
// URL is intentionally unchanged from the prior MCP callback — it is
// already registered in customer IdP configurations (Keycloak / Auth0
// / Okta / Microsoft App Registrations). Changing it would force a
// reconfigure on every customer.
//
// @Summary      OAuth authorization-code callback (unified)
// @Description  Public endpoint hit by the upstream IdP after operator authentication. The PKCE state row carries the connection_kind so this single endpoint handles every kind.
// @Tags         Connections
// @Produce      html
// @Param        code   query  string  false  "OAuth authorization code"
// @Param        state  query  string  true   "PKCE state token"
// @Param        error  query  string  false  "OAuth error code from upstream"
// @Success      302
// @Failure      400   {object}  problemDetail
// @Router       /admin/oauth/callback [get]
func (h *Handler) connectionOAuthCallback(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	pending := h.takeConnectionPKCEState(w, r)
	if pending == nil {
		return
	}
	q := r.URL.Query()
	if errCode := q.Get("error"); errCode != "" {
		slog.Warn("oauth-callback: IdP returned error",
			logKeyKind, pending.kind, logKeyName, pending.connection,
			"idp_error", errCode, "idp_error_description", q.Get("error_description"))
		writeOAuthError(w, fmt.Sprintf("upstream returned %s: %s", errCode, q.Get("error_description")))
		return
	}
	code := q.Get("code")
	if code == "" {
		writeOAuthError(w, "missing code parameter")
		return
	}
	if err := h.completeConnectionOAuthExchange(r.Context(), pending, code); err != nil {
		slog.Error("oauth-callback: exchange failed",
			logKeyKind, pending.kind, logKeyName, pending.connection,
			logKeyStartedBy, pending.startedBy, logKeyDuration, time.Since(start),
			logKeyError, err)
		writeOAuthError(w, "token exchange failed: "+err.Error())
		return
	}
	dest := safeReturnURL(pending.returnURL)
	if pending.returnURL != "" && dest != pending.returnURL {
		slog.Warn("oauth-callback: returnURL rewritten by safeReturnURL guard",
			logKeyKind, pending.kind, logKeyName, pending.connection,
			"requested_return_url", pending.returnURL, "rewritten_to", dest)
	}
	slog.Info("oauth-callback: success — tokens persisted",
		logKeyKind, pending.kind, logKeyName, pending.connection,
		logKeyStartedBy, pending.startedBy,
		logKeyDuration, time.Since(start), "dest", dest)
	// #nosec G710 -- safeReturnURL has already constrained dest to a
	// same-origin relative path or the constant fallback.
	http.Redirect(w, r, dest, http.StatusFound) // nosemgrep: go.lang.security.injection.open-redirect.open-redirect
}

// takeConnectionPKCEState consumes the pending PKCE row. Returns nil
// and writes the HTML error page on any failure path so callers can
// return early. Falls back to "mcp" for legacy rows that pre-date
// migration 000039's connection_kind column (those rows belong to
// the MCP gateway by construction — only kind that used this table
// before 000039).
func (h *Handler) takeConnectionPKCEState(w http.ResponseWriter, r *http.Request) *PKCEState {
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
		writeOAuthError(w, "OAuth state expired or unknown — please retry from the admin UI")
		return nil
	}
	if pending.kind == "" {
		// Pre-migration-000039 row (legacy MCP-only flow). Set the
		// default so downstream dispatch has a known kind.
		pending.kind = connectionKindMCP
	}
	return pending
}

// completeConnectionOAuthExchange runs the token exchange, persists
// the result via connoauth.Store, and invokes the per-kind
// AfterConnect hook so the connection becomes immediately usable.
func (h *Handler) completeConnectionOAuthExchange(ctx context.Context, pending *PKCEState, code string) error {
	handler, ok := h.deps.OAuthKinds[pending.kind]
	if !ok {
		return fmt.Errorf("unsupported connection kind: %s", pending.kind)
	}
	inst, err := h.deps.ConnectionStore.Get(ctx, pending.kind, pending.connection)
	if err != nil {
		return fmt.Errorf("load connection: %w", err)
	}
	cfg, err := handler.ParseOAuthConfig(inst.Config)
	if err != nil {
		return fmt.Errorf("parse config: %w", err)
	}
	result, err := connoauth.Exchange(ctx, connoauth.ExchangeInput{
		Config:       cfg,
		Code:         code,
		CodeVerifier: pending.codeVerifier,
		RedirectURI:  pending.redirectURI,
	})
	if err != nil {
		return fmt.Errorf("connoauth exchange: %w", err)
	}
	now := time.Now()
	persistErr := h.deps.ConnOAuthStore.Set(ctx, connoauth.PersistedToken{
		Key:              connoauth.Key{Kind: pending.kind, Name: pending.connection},
		AccessToken:      result.AccessToken,
		RefreshToken:     result.RefreshToken,
		ExpiresAt:        result.ExpiresAt,
		RefreshExpiresAt: result.RefreshExpiresAt,
		Scope:            result.Scope,
		AuthenticatedBy:  pending.startedBy,
		AuthenticatedAt:  now,
	})
	if persistErr != nil {
		// Persistence failure on Connect MUST surface — the in-memory
		// view from this turn would silently vanish on the next
		// process restart, leaving the operator perpetually clicking
		// Connect with no idea why it doesn't stick.
		return fmt.Errorf("persist token: %w", persistErr)
	}
	h.deps.AuthEvents.ConnectCompleted(ctx, pending.kind, pending.connection, pending.startedBy,
		cfg.TokenURL, authevents.ConnectCompletedDetail{
			Scope:            result.Scope,
			ExpiresAt:        result.ExpiresAt,
			RefreshExpiresAt: result.RefreshExpiresAt,
			HasRefreshToken:  result.RefreshToken != "",
		})
	if err := handler.AfterConnect(ctx, pending.connection, inst.Config); err != nil {
		// Log but do not fail the Connect — the token IS persisted;
		// the post-auth side effect (e.g., MCP gateway tool
		// registration) can be retried by the toolkit's normal
		// reconciliation paths. Failing the Connect here would force
		// the operator to repeat the browser flow even though the
		// credential is good.
		slog.Warn("oauth-callback: AfterConnect hook failed (token persisted, side effect deferred)",
			logKeyKind, pending.kind, logKeyName, pending.connection, logKeyError, err)
	}
	return nil
}

// lookupOAuthKindHandler resolves the kind path parameter to a
// registered OAuthKindHandler. Writes 400 and returns ok=false when
// the kind is missing or unsupported.
func (h *Handler) lookupOAuthKindHandler(w http.ResponseWriter, kind string) (OAuthKindHandler, bool) {
	if kind == "" {
		writeError(w, http.StatusBadRequest, "missing connection kind")
		return nil, false
	}
	if h.deps.OAuthKinds == nil {
		writeError(w, http.StatusServiceUnavailable, "OAuth not available: no kind handlers registered")
		return nil, false
	}
	handler, ok := h.deps.OAuthKinds[kind]
	if !ok {
		writeError(w, http.StatusBadRequest, "unsupported connection kind: "+kind)
		return nil, false
	}
	return handler, true
}

// loadConnectionForOAuth fetches the connection row, writing 404/500
// on the standard error paths. Centralized so every {kind,name}
// endpoint maps not-found and load failures identically.
func (h *Handler) loadConnectionForOAuth(w http.ResponseWriter, r *http.Request, kind, name string) (*platform.ConnectionInstance, bool) {
	inst, err := h.deps.ConnectionStore.Get(r.Context(), kind, name)
	if err != nil {
		if errors.Is(err, platform.ErrConnectionNotFound) {
			writeError(w, http.StatusNotFound, "connection not found")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to load connection")
		}
		return nil, false
	}
	return inst, true
}

// parseConnectionOAuthConfig invokes the kind-specific extractor and
// maps the configuration error to HTTP 409 ("not configured for
// authorization_code OAuth") matching the prior per-kind handlers'
// response code.
func (*Handler) parseConnectionOAuthConfig(w http.ResponseWriter, handler OAuthKindHandler, raw map[string]any) (connoauth.Config, bool) {
	cfg, err := handler.ParseOAuthConfig(raw)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return connoauth.Config{}, false
	}
	return cfg, true
}

// buildConnectionAuthorizationURL composes the IdP's authorize URL
// with PKCE attached. SHA-256 challenge per RFC 7636 §4.2 — the
// only method OAuth 2.1 mandates.
func buildConnectionAuthorizationURL(cfg connoauth.Config, state, verifier, redirectURI string) string {
	v := url.Values{}
	v.Set("response_type", "code")
	v.Set("client_id", cfg.ClientID)
	v.Set("redirect_uri", redirectURI)
	v.Set("state", state)
	v.Set("code_challenge", pkceChallenge(verifier))
	v.Set("code_challenge_method", "S256")
	if len(cfg.Scopes) > 0 {
		v.Set("scope", strings.Join(cfg.Scopes, " "))
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

// urlHostForLog returns the host portion of u for log fields, falling
// back to the raw value when parsing fails. Local to admin so the
// connoauth package's identically-named helper doesn't need to be
// exported.
func urlHostForLog(u string) string {
	parsed, err := url.Parse(u)
	if err != nil || parsed.Host == "" {
		return u
	}
	return parsed.Host
}
