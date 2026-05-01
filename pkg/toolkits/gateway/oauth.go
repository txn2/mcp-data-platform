package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// expiryBuffer is the safety margin before token expiry at which we'll
// proactively refresh. Keeps in-flight calls from racing the clock.
const expiryBuffer = 30 * time.Second

// tokenState is the cached result of a successful OAuth token exchange.
// Zero value (zero ExpiresAt) means "never acquired" — the next Token()
// call will perform a full acquisition.
type tokenState struct {
	AccessToken     string
	RefreshToken    string
	ExpiresAt       time.Time
	LastRefreshedAt time.Time
}

// OAuthStatus is a snapshot of token state suitable for the admin status
// endpoint. All fields are safe to expose to operators (no secret material).
type OAuthStatus struct {
	Configured      bool      `json:"configured"`
	TokenAcquired   bool      `json:"token_acquired"`
	ExpiresAt       time.Time `json:"expires_at,omitzero"`
	LastRefreshedAt time.Time `json:"last_refreshed_at,omitzero"`
	HasRefreshToken bool      `json:"has_refresh_token"`
	LastError       string    `json:"last_error,omitempty"`
	Grant           string    `json:"grant,omitempty"`
	TokenURL        string    `json:"token_url,omitempty"`
	Scope           string    `json:"scope,omitempty"`
	// AuthenticatedBy is the email/id of the operator who completed the
	// browser flow. Empty for client_credentials and for connections that
	// have not yet been authorized.
	AuthenticatedBy string `json:"authenticated_by,omitempty"`
	// AuthenticatedAt records when the most recent successful exchange
	// (initial OAuth dance or refresh) completed.
	AuthenticatedAt time.Time `json:"authenticated_at,omitzero"`
	// NeedsReauth is true when the platform cannot mint an access token
	// without operator interaction. For authorization_code grants this
	// means: no stored token, or the refresh token has been revoked.
	// The admin UI surfaces a "Connect" button when this is true.
	NeedsReauth bool `json:"needs_reauth,omitempty"`
	// RefreshTokenRevoked is true when the most recent refresh attempt
	// got a definitive RFC 6749 §5.2 invalid_grant response — meaning
	// the IdP has invalidated the stored refresh token (idle session
	// timeout, admin revocation, password change, etc.) and the
	// operator must complete a fresh browser-side authorization. The
	// admin UI uses this to distinguish "click Connect to reauthorize"
	// from a transient "click Reacquire to retry" state.
	RefreshTokenRevoked bool `json:"refresh_token_revoked,omitempty"`
}

// oauthTokenSource holds the state for a single connection's OAuth flow.
// It is safe for concurrent use; Token() serializes refresh attempts so
// callers don't double-fetch in a thundering-herd.
//
// For client_credentials, state lives only in memory — every restart
// re-acquires from the persisted client_secret on first use.
//
// For authorization_code, state is loaded from store at construction
// and persisted back on every successful refresh, so a one-time browser
// authentication grants long-running background access (cron jobs, etc.)
// until the upstream invalidates the refresh token.
type oauthTokenSource struct {
	cfg            OAuthConfig
	connectionName string
	client         *http.Client
	store          TokenStore // nil for client_credentials grants

	mu                  sync.Mutex
	state               tokenState
	loaded              bool
	lastError           string
	refreshTokenRevoked bool
	authedBy            string
	authedAt            time.Time
}

// defaultTokenExchangeTimeout bounds every token-endpoint POST. Without
// this, http.DefaultClient (Timeout: 0) plus a Keycloak/Auth0 endpoint
// that accepts the connection then hangs would keep the OAuth callback
// open for the platform's full server timeout. 30s is generous for any
// healthy IdP — TLS handshake + form POST + token mint typically fits
// in <2s; tail latency at 30s indicates the IdP itself is unhealthy.
const defaultTokenExchangeTimeout = 30 * time.Second

// maxTokenResponseBytes caps the response body read from a token
// endpoint. Real responses are KB at most; a 1 MiB ceiling prevents an
// OOM if a misbehaving (or malicious) IdP streams indefinitely.
const maxTokenResponseBytes = 1 << 20

// newOAuthTokenSource builds a token source with a dedicated http.Client
// that has an explicit overall timeout (defaultTokenExchangeTimeout).
// Pass a non-nil store to enable persistent authorization_code flows;
// pass nil for client_credentials.
//
// The client is NOT shared with http.DefaultClient — sharing would let
// any package-level mutation of DefaultClient affect token exchanges.
func newOAuthTokenSource(cfg OAuthConfig, connection string, store TokenStore) *oauthTokenSource {
	return &oauthTokenSource{
		cfg:            cfg,
		connectionName: connection,
		client:         &http.Client{Timeout: defaultTokenExchangeTimeout},
		store:          store,
	}
}

// Token returns a non-expired access token, refreshing or re-acquiring
// as needed. Callers should treat the returned token as opaque and not
// cache it themselves — the source already caches and refreshes.
//
// For authorization_code grants, when no token has been acquired yet
// (first use after platform startup or after a fresh setup) Token()
// returns an error pointing the operator at the Connect button —
// authorization_code requires a one-time human-in-the-loop step that
// can't happen lazily inside a tool call.
func (t *oauthTokenSource) Token(ctx context.Context) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.ensureLoadedLocked(ctx)

	if t.state.AccessToken != "" && time.Until(t.state.ExpiresAt) > expiryBuffer {
		slog.Debug("gateway/oauth: cached access token still valid",
			logKeyConnection, t.connectionName,
			"expires_in_seconds", int(time.Until(t.state.ExpiresAt).Seconds()))
		return t.state.AccessToken, nil
	}
	// Try refresh first if we have a refresh token.
	if t.state.RefreshToken != "" {
		slog.Debug("gateway/oauth: cached token expired, attempting refresh",
			logKeyConnection, t.connectionName)
		err := t.refreshLocked(ctx)
		switch {
		case err == nil:
			t.persistLocked(ctx)
			t.refreshTokenRevoked = false
			return t.state.AccessToken, nil
		case errors.Is(err, errRefreshTokenRevoked):
			// IdP definitively said the refresh token is dead. Clear the
			// in-memory state AND the persisted row so subsequent restarts
			// don't replay the same dead token against the IdP. Without
			// this, every redeploy after the IdP's session-idle window
			// produces a noisy REFRESH_TOKEN_ERROR audit event for an
			// already-known-dead credential.
			slog.Info("gateway/oauth: refresh token rejected by IdP — clearing stale state",
				logKeyConnection, t.connectionName,
				logKeyError, err)
			t.clearStaleStateLocked(ctx)
			// Also clear lastError — Token() falls through below and
			// sets the generic "needs reauth" message; preserving
			// the wrapped sentinel from refreshLocked here just
			// confuses operators since the cleanup already ran.
			t.lastError = ""
		default:
			slog.Warn("gateway/oauth: refresh failed (transient or unrecognized)",
				logKeyConnection, t.connectionName,
				logKeyError, err)
		}
	}
	// authorization_code with no usable refresh path: needs human re-auth.
	if t.cfg.Grant == OAuthGrantAuthorizationCode {
		err := errors.New("oauth: authorization_code grant requires reauthentication (no valid refresh token)")
		slog.Info("gateway/oauth: requires human reauthentication",
			logKeyConnection, t.connectionName,
			"reason", "authorization_code grant has no valid cached or refresh token")
		t.lastError = err.Error()
		return "", err
	}
	slog.Debug("gateway/oauth: client_credentials acquire",
		logKeyConnection, t.connectionName)
	if err := t.acquireLocked(ctx); err != nil {
		t.lastError = err.Error()
		return "", err
	}
	t.lastError = ""
	return t.state.AccessToken, nil
}

// IngestTokenResponseInput collects the result of an out-of-band OAuth
// exchange (e.g. the platform's authorization-code callback handler).
// Used to keep IngestTokenResponse to a single argument.
type IngestTokenResponseInput struct {
	AccessToken     string
	RefreshToken    string
	ExpiresIn       int
	Scope           string
	AuthenticatedBy string
}

// IngestTokenResponse stores a token set obtained out-of-band into the
// source's state and persistent store. AuthenticatedBy is the operator
// who completed the browser flow; recorded for the admin status panel.
//
// Returns an error WITHOUT mutating any state when in.AccessToken is
// empty — an empty token would silently flip TokenAcquired=true on the
// admin status panel even though no credential was actually granted,
// hiding upstream bugs in the calling code.
func (t *oauthTokenSource) IngestTokenResponse(ctx context.Context, in IngestTokenResponseInput) error {
	if in.AccessToken == "" {
		return errors.New("oauth: IngestTokenResponse: access_token is required")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.state.AccessToken = in.AccessToken
	if in.RefreshToken != "" {
		t.state.RefreshToken = in.RefreshToken
	}
	if in.ExpiresIn > 0 {
		t.state.ExpiresAt = time.Now().Add(time.Duration(in.ExpiresIn) * time.Second)
	} else {
		t.state.ExpiresAt = time.Now().Add(1 * time.Hour)
	}
	t.state.LastRefreshedAt = time.Now()
	if in.Scope != "" {
		t.cfg.Scope = in.Scope
	}
	t.authedBy = in.AuthenticatedBy
	t.authedAt = time.Now().UTC()
	t.lastError = ""
	t.refreshTokenRevoked = false
	t.loaded = true
	t.persistLocked(ctx)
	if t.lastError != "" {
		return errors.New(t.lastError)
	}
	return nil
}

// Reacquire forces a fresh token exchange even when the cached token is
// still valid. For client_credentials this re-runs the grant; for
// authorization_code it attempts a refresh — re-running the full grant
// requires a browser, which Reacquire cannot do.
//
// Mirrors Token()'s dead-refresh handling: if refreshLocked returns an
// errRefreshTokenRevoked-wrapped error the persisted row is cleared so
// the next manual Connect (or implicit Token() call) starts fresh
// instead of replaying the dead credential.
func (t *oauthTokenSource) Reacquire(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ensureLoadedLocked(ctx)

	if t.cfg.Grant == OAuthGrantAuthorizationCode {
		if t.state.RefreshToken == "" {
			err := errors.New("oauth: no refresh token; click Connect to authorize")
			t.lastError = err.Error()
			return err
		}
		if err := t.refreshLocked(ctx); err != nil {
			if errors.Is(err, errRefreshTokenRevoked) {
				slog.Info("gateway/oauth: Reacquire — refresh token rejected by IdP, clearing stale state",
					logKeyConnection, t.connectionName,
					logKeyError, err)
				t.clearStaleStateLocked(ctx)
			}
			// Set lastError AFTER clearStaleStateLocked so its
			// best-effort cleanup-error reporting (via the dedicated
			// slog.Warn) can't clobber the IdP rejection text that
			// operators actually need to see in Status.
			t.lastError = err.Error()
			return err
		}
		t.persistLocked(ctx)
		t.lastError = ""
		t.refreshTokenRevoked = false
		return nil
	}
	if err := t.acquireLocked(ctx); err != nil {
		t.lastError = err.Error()
		return err
	}
	t.lastError = ""
	return nil
}

// Status snapshots the current state for the admin endpoint.
func (t *oauthTokenSource) Status() OAuthStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ensureLoadedLocked(context.Background())
	needsReauth := t.cfg.Grant == OAuthGrantAuthorizationCode &&
		t.state.AccessToken == "" && t.state.RefreshToken == ""
	return OAuthStatus{
		Configured:          true,
		TokenAcquired:       t.state.AccessToken != "",
		ExpiresAt:           t.state.ExpiresAt,
		LastRefreshedAt:     t.state.LastRefreshedAt,
		HasRefreshToken:     t.state.RefreshToken != "",
		LastError:           t.lastError,
		Grant:               t.cfg.Grant,
		TokenURL:            t.cfg.TokenURL,
		Scope:               t.cfg.Scope,
		AuthenticatedBy:     t.authedBy,
		AuthenticatedAt:     t.authedAt,
		NeedsReauth:         needsReauth,
		RefreshTokenRevoked: t.refreshTokenRevoked,
	}
}

// acquireLocked performs the configured OAuth grant. Caller must hold t.mu.
func (t *oauthTokenSource) acquireLocked(ctx context.Context) error {
	form := url.Values{}
	form.Set("grant_type", t.cfg.Grant)
	form.Set("client_id", t.cfg.ClientID)
	form.Set("client_secret", t.cfg.ClientSecret)
	if t.cfg.Scope != "" {
		form.Set("scope", t.cfg.Scope)
	}
	return t.exchangeLocked(ctx, form)
}

// grantTypeRefreshToken is the value of the OAuth `grant_type` form
// parameter for refresh-token exchanges (RFC 6749 §6). Internal — not
// exposed as a configurable grant since you cannot start an OAuth
// flow with a refresh-token grant; it only follows an earlier
// authorization_code or client_credentials acquisition.
const grantTypeRefreshToken = "refresh_token"

// formFieldRefreshToken is the form-field NAME (RFC 6749 §6) carrying
// the refresh token VALUE in the POST body. Distinct from
// grantTypeRefreshToken — they happen to share the literal "refresh_token"
// per RFC, but they are different OAuth concepts and we name them
// separately so a future divergence (or a typo) doesn't silently fan
// out between the two callsites.
const formFieldRefreshToken = "refresh_token"

// refreshLocked uses a refresh_token grant. Caller must hold t.mu.
// Persists the rotated refresh token (if any) back to the store so a
// subsequent process restart picks up the freshest credentials.
func (t *oauthTokenSource) refreshLocked(ctx context.Context) error {
	form := url.Values{}
	form.Set("grant_type", grantTypeRefreshToken)
	form.Set(formFieldRefreshToken, t.state.RefreshToken)
	form.Set("client_id", t.cfg.ClientID)
	form.Set("client_secret", t.cfg.ClientSecret)
	if t.cfg.Scope != "" {
		form.Set("scope", t.cfg.Scope)
	}
	return t.exchangeLocked(ctx, form)
}

// tokenResponse is the standard OAuth 2.1 token endpoint response.
type tokenResponse struct {
	AccessToken      string `json:"access_token"` //nolint:gosec // G117 false positive: this is the OAuth response shape, not a hardcoded credential
	TokenType        string `json:"token_type"`
	ExpiresIn        int    `json:"expires_in"`
	RefreshToken     string `json:"refresh_token,omitempty"`
	Scope            string `json:"scope,omitempty"`
	Error            string `json:"error,omitempty"`
	ErrorDescription string `json:"error_description,omitempty"`
}

// exchangeLocked POSTs the form to the token endpoint and updates t.state
// on success. Caller must hold t.mu.
func (t *oauthTokenSource) exchangeLocked(ctx context.Context, form url.Values) error {
	// #nosec G107 G704 -- the token URL comes from operator-authored
	// connection config, not from request input; the OAuth feature is
	// useless without it being a runtime parameter.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.cfg.TokenURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("oauth: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	grantType := form.Get("grant_type")
	tokenHost := URLHost(t.cfg.TokenURL)
	exchangeStart := time.Now()
	slog.Debug("gateway/oauth: token exchange start",
		logKeyConnection, t.connectionName,
		LogKeyGrantType, grantType,
		LogKeyTokenURLHost, tokenHost,
		"client_id", t.cfg.ClientID)

	// #nosec G107 G704 -- TokenURL is operator-authored connection config, not user input.
	resp, err := t.client.Do(req)
	if err != nil {
		slog.Warn("gateway/oauth: token request transport error",
			logKeyConnection, t.connectionName,
			LogKeyGrantType, grantType,
			LogKeyTokenURLHost, tokenHost,
			"duration", time.Since(exchangeStart),
			logKeyError, err)
		return fmt.Errorf("oauth: token request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Bound the body read so a misbehaving (or malicious) IdP that
	// streams indefinitely cannot OOM the platform. Real OAuth token
	// responses are KB at most; maxTokenResponseBytes (1 MiB) is a
	// generous ceiling.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxTokenResponseBytes))
	if err != nil {
		return fmt.Errorf("oauth: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		slog.Warn("gateway/oauth: non-200 from token endpoint",
			logKeyConnection, t.connectionName,
			LogKeyGrantType, grantType,
			LogKeyTokenURLHost, tokenHost,
			"status", resp.StatusCode,
			"duration", time.Since(exchangeStart),
			"body_excerpt", trimBody(body))
		return interpretTokenError(grantType, resp.StatusCode, body)
	}
	slog.Info("gateway/oauth: token exchange success",
		logKeyConnection, t.connectionName,
		LogKeyGrantType, grantType,
		LogKeyTokenURLHost, tokenHost,
		"duration", time.Since(exchangeStart))

	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return fmt.Errorf("oauth: decode response: %w", err)
	}
	if tr.Error != "" {
		return fmt.Errorf("oauth: %s: %s", tr.Error, tr.ErrorDescription)
	}
	if tr.AccessToken == "" {
		return errors.New("oauth: token response missing access_token")
	}

	t.state.AccessToken = tr.AccessToken
	if tr.RefreshToken != "" {
		t.state.RefreshToken = tr.RefreshToken
	}
	if tr.ExpiresIn > 0 {
		t.state.ExpiresAt = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	} else {
		// Default to one hour when the upstream omits expires_in. Spec
		// recommends always including it but real implementations vary.
		t.state.ExpiresAt = time.Now().Add(1 * time.Hour)
	}
	t.state.LastRefreshedAt = time.Now()
	return nil
}

// errRefreshTokenRevoked indicates the IdP definitively rejected a
// refresh_token grant — the stored refresh token cannot be used to mint
// further access tokens, and the operator must complete a fresh
// browser-side authorization to recover.
//
// Wrapped by interpretTokenError ONLY when grantType == "refresh_token"
// AND the upstream returns 400 with error="invalid_grant" (RFC 6749
// §5.2). Other grant types receive the same status/code verbatim —
// invalid_grant on a code-exchange or client_credentials request has a
// different meaning (bad code, bad client_secret) that callers must not
// confuse with a dead refresh.
//
// Callers (Token(), Reacquire()) detect this with errors.Is and clear
// the persisted state — without that step, every subsequent restart
// would replay the dead refresh against the IdP, producing log noise
// and a per-restart REFRESH_TOKEN_ERROR event in the IdP's audit log.
var errRefreshTokenRevoked = errors.New("oauth: refresh token revoked by issuer")

// interpretTokenError tries to parse a structured OAuth error from the
// upstream's response body, falling back to status code + raw body.
//
// grantType is the value of the form's grant_type — passed through so
// the sentinel-wrapping is scoped to the only grant where it makes
// sense (refresh_token). Other grants get the verbatim status/code —
// they may signal transient or operator-config errors that the caller
// must NOT respond to by deleting stored credentials.
func interpretTokenError(grantType string, status int, body []byte) error {
	var tr tokenResponse
	if jerr := json.Unmarshal(body, &tr); jerr == nil && tr.Error != "" {
		if grantType == grantTypeRefreshToken && isRefreshDeadError(status, tr.Error) {
			// revive's string-format rule prefers messages start with
			// recognizable %s/%v rather than %w; opening the format
			// with the IdP's literal status/code keeps the rule happy
			// while still wrapping the sentinel for errors.Is.
			return fmt.Errorf("oauth: %d %s: %s (%w)",
				status, tr.Error, tr.ErrorDescription, errRefreshTokenRevoked)
		}
		return fmt.Errorf("oauth: %d %s: %s", status, tr.Error, tr.ErrorDescription)
	}
	return fmt.Errorf("oauth: token endpoint returned %d: %s", status, trimBody(body))
}

// isRefreshDeadError reports whether the IdP's structured error
// indicates the refresh token cannot be used. RFC 6749 §5.2 defines
// invalid_grant as the canonical "the provided refresh token is
// invalid, expired, revoked, or doesn't match" signal — that's the
// only error code we treat as definitively dead. Other codes
// (invalid_request, invalid_client, etc.) and other status classes
// (5xx, network) may be transient and the caller must NOT clear
// stored credentials on a transient signal.
func isRefreshDeadError(status int, errCode string) bool {
	return status == http.StatusBadRequest && errCode == "invalid_grant"
}

// URLHost returns the host portion of u so logs can show "which IdP"
// without dragging the full URL (which contains the path, and
// sometimes query) into log files. Falls back to the raw value when
// parsing fails so logs are never empty.
//
// Exported so packages composing with this one (admin handlers,
// orchestration code) emit the same shape of host-only field as
// internal exchange / refresh logs — operators can grep one
// connection's full lifecycle by `token_url_host=<host>` regardless
// of which package emitted the line.
func URLHost(u string) string {
	parsed, err := url.Parse(u)
	if err != nil || parsed.Host == "" {
		return u
	}
	return parsed.Host
}

// trimBodyLimit caps the size of upstream error bodies surfaced in error
// strings so a misbehaving upstream can't blow up an audit log.
const trimBodyLimit = 256

func trimBody(body []byte) string {
	if len(body) <= trimBodyLimit {
		return string(body)
	}
	return string(body[:trimBodyLimit]) + "..."
}

// staleCleanupTimeout bounds the Delete call inside
// clearStaleStateLocked so a slow / hung token store cannot block the
// connection's mutex (held by Token / Reacquire / Status). Five
// seconds is generous for any healthy postgres; anything slower
// indicates the operator should be looking at the DB anyway.
const staleCleanupTimeout = 5 * time.Second

// clearStaleStateLocked drops the in-memory token state AND deletes the
// persisted row from the store after the IdP has signaled the refresh
// token is dead (RFC 6749 §5.2 invalid_grant at 400). Caller must hold
// t.mu; refreshTokenRevoked is also set so Status() can surface the
// distinction to the admin UI without a separate query.
//
// The Delete uses a fresh context derived from context.Background()
// with a short timeout rather than mutating the caller's ctx — the
// cleanup is best-effort and a tool-call ctx that expired during the
// upstream's slow rejection must NOT also kill the cleanup that
// prevents the dead refresh from being replayed. We accept ctx as a
// parameter (even though we don't pass it down) so contextcheck can
// see the parent context exists; ctx is reserved for future use
// (e.g. carrying trace IDs into the cleanup log).
//
// IMPORTANT: this method does NOT touch t.lastError on Delete failure
// so the caller's already-recorded error (the IdP rejection text) is
// preserved for Status display. The Delete failure is surfaced via a
// dedicated slog.Warn for log-aggregator alerts.
func (t *oauthTokenSource) clearStaleStateLocked(_ context.Context) {
	t.state = tokenState{}
	t.authedBy = ""
	t.authedAt = time.Time{}
	t.refreshTokenRevoked = true
	if t.store == nil {
		return
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), staleCleanupTimeout)
	defer cancel()
	if err := t.store.Delete(cleanupCtx, t.connectionName); err != nil && !errors.Is(err, ErrTokenNotFound) {
		slog.Warn("gateway/oauth: failed to delete stale token row",
			logKeyConnection, t.connectionName,
			logKeyError, err)
	}
}

// ensureLoadedLocked reads the persisted token row (if any) into the
// in-memory state. Caller must hold t.mu.
//
// On success or ErrTokenNotFound the source is marked loaded so
// subsequent calls are no-ops. On a transient store error
// (DB hiccup, encryption-service blip) the source is NOT marked
// loaded so the next call retries — leaving an unloaded source with
// a real persisted row would lock the operator into "click Connect"
// even though the row exists.
func (t *oauthTokenSource) ensureLoadedLocked(ctx context.Context) {
	if t.loaded {
		return
	}
	if t.store == nil {
		t.loaded = true
		return
	}
	rec, err := t.store.Get(ctx, t.connectionName)
	if err != nil {
		if errors.Is(err, ErrTokenNotFound) {
			t.loaded = true
			return
		}
		// Transient store error — leave loaded=false so the next call
		// retries. Surface the failure on lastError so Status shows it.
		t.lastError = "load token: " + err.Error()
		slog.Warn("gateway/oauth: failed to load persisted token (will retry)",
			logKeyConnection, t.connectionName,
			logKeyError, err)
		return
	}
	t.state = tokenState{
		AccessToken:     rec.AccessToken,
		RefreshToken:    rec.RefreshToken,
		ExpiresAt:       rec.ExpiresAt,
		LastRefreshedAt: rec.UpdatedAt,
	}
	t.authedBy = rec.AuthenticatedBy
	t.authedAt = rec.AuthenticatedAt
	t.loaded = true
}

// persistLocked writes the current token state back to the store. Used
// after a successful refresh so the new access/refresh tokens survive
// process restarts. Caller must hold t.mu.
func (t *oauthTokenSource) persistLocked(ctx context.Context) {
	if t.store == nil {
		return
	}
	if err := t.store.Set(ctx, PersistedToken{
		ConnectionName:  t.connectionName,
		AccessToken:     t.state.AccessToken,
		RefreshToken:    t.state.RefreshToken,
		ExpiresAt:       t.state.ExpiresAt,
		Scope:           t.cfg.Scope,
		AuthenticatedBy: t.authedBy,
		AuthenticatedAt: t.authedAt,
	}); err != nil {
		// Log but don't fail the token return — we still have a usable
		// in-memory token; persistence is for restart durability only.
		t.lastError = "persist token: " + err.Error()
	}
}
