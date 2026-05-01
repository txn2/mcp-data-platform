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

// newTokenExchangeHTTPClient returns the http.Client used for OAuth
// token-endpoint POSTs. Centralized so the gateway and any other
// composing package use identical security-hardened configuration:
//
//   - Timeout: bounds the total request duration (security: slow IdP
//     can't pin a goroutine indefinitely).
//   - CheckRedirect: ErrUseLastResponse — DO NOT follow redirects.
//     RFC 6749 doesn't specify token-endpoint redirect handling, but
//     following them lets a misconfigured or compromised IdP redirect
//     a credential-bearing POST (client_secret, refresh_token,
//     authorization_code) to an attacker URL. The redirect surfaces as
//     a 3xx response which the caller treats as an error.
func newTokenExchangeHTTPClient() *http.Client {
	return &http.Client{
		Timeout: defaultTokenExchangeTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// newOAuthTokenSource builds a token source with a dedicated http.Client
// hardened against follow-the-redirect credential leaks and slow-loris
// hangs. Pass a non-nil store to enable persistent authorization_code
// flows; pass nil for client_credentials.
//
// The client is NOT shared with http.DefaultClient — sharing would let
// any package-level mutation of DefaultClient affect token exchanges.
func newOAuthTokenSource(cfg OAuthConfig, connection string, store TokenStore) *oauthTokenSource {
	return newOAuthTokenSourceWithClient(cfg, connection, store, newTokenExchangeHTTPClient())
}

// newOAuthTokenSourceWithClient is the constructor variant that takes
// a custom http.Client. Used by tests to point the source at a fake /
// closed token server without mutating the source's `client` field
// after construction (which would be an unsynchronized write to a
// field the production code reads under t.mu).
func newOAuthTokenSourceWithClient(cfg OAuthConfig, connection string, store TokenStore, client *http.Client) *oauthTokenSource {
	if client == nil {
		client = newTokenExchangeHTTPClient()
	}
	return &oauthTokenSource{
		cfg:            cfg,
		connectionName: connection,
		client:         client,
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

	// Propagate transient load errors verbatim instead of falling
	// through to grant-specific reauth messaging — operators must
	// see "DB unreachable" not "click Connect" when the store is
	// the actual problem.
	if err := t.ensureLoadedLocked(ctx); err != nil {
		return "", err
	}

	// Cached path: token still valid.
	if t.state.AccessToken != "" && time.Until(t.state.ExpiresAt) > expiryBuffer {
		slog.Debug("gateway/oauth: cached access token still valid",
			logKeyConnection, t.connectionName,
			"expires_in_seconds", int(time.Until(t.state.ExpiresAt).Seconds()))
		// Clear stale lastError — any prior failure is no longer the
		// current operational state and must not stick on Status.
		t.lastError = ""
		return t.state.AccessToken, nil
	}
	// Try refresh first if we have a refresh token.
	if t.state.RefreshToken != "" {
		token, returned, err := t.tryRefreshLocked(ctx)
		if returned {
			return token, err
		}
		// returned==false means refresh hit the dead-refresh sentinel
		// and clearStaleStateLocked already ran. Fall through to the
		// grant-specific path which will set lastError.
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

// tryRefreshLocked attempts a refresh-token grant against the upstream
// and reports the outcome via three values:
//
//   - (token, true, nil)       — refresh succeeded; caller returns the token.
//   - ("", true, err)          — refresh failed transient/unrecognized; caller returns err.
//   - ("", false, nil)         — refresh failed with errRefreshTokenRevoked
//     and stale state was cleared; caller falls
//     through to the grant-specific reauth path.
//
// The third tri-state lets Token() drop a level of nesting and keep
// cyclomatic complexity under the project ceiling. Caller must hold t.mu.
func (t *oauthTokenSource) tryRefreshLocked(ctx context.Context) (token string, returned bool, err error) {
	slog.Debug("gateway/oauth: cached token expired, attempting refresh",
		logKeyConnection, t.connectionName)
	err = t.refreshLocked(ctx)
	switch {
	case err == nil:
		// Persist failures are non-fatal: the in-memory token works
		// for this process's lifetime, and the next refresh re-persists
		// (self-healing). Log so operators can spot DB issues; don't
		// surface on lastError (would be cleared by the next success
		// anyway, and Status would flap).
		if pErr := t.persistLocked(ctx); pErr != nil {
			slog.Warn("gateway/oauth: persist after refresh failed (in-memory token still valid)",
				logKeyConnection, t.connectionName,
				logKeyError, pErr)
		}
		t.lastError = ""
		return t.state.AccessToken, true, nil
	case errors.Is(err, errRefreshTokenRevoked):
		// IdP definitively said the refresh token is dead. Clear
		// in-memory state AND the persisted row so subsequent restarts
		// don't replay the dead token. Caller falls through to the
		// grant-specific reauth path which sets lastError.
		slog.Info("gateway/oauth: refresh token rejected by IdP — clearing stale state",
			logKeyConnection, t.connectionName,
			logKeyError, err)
		t.clearStaleStateLocked(ctx)
		return "", false, nil
	default:
		// Transient failure (503/network/timeout/etc): refresh token
		// is still valid in state and store. Return the actual error
		// so the caller knows to retry — DO NOT fall through to the
		// "needs reauth" path which would mislead operators into
		// invalidating a working refresh.
		slog.Warn("gateway/oauth: refresh failed (transient or unrecognized)",
			logKeyConnection, t.connectionName,
			logKeyError, err)
		t.lastError = err.Error()
		return "", true, err
	}
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
	now := time.Now().UTC()
	if in.ExpiresIn > 0 {
		t.state.ExpiresAt = now.Add(time.Duration(in.ExpiresIn) * time.Second)
	} else {
		t.state.ExpiresAt = now.Add(1 * time.Hour)
	}
	t.state.LastRefreshedAt = now
	if in.Scope != "" {
		t.cfg.Scope = in.Scope
	}
	t.authedBy = in.AuthenticatedBy
	t.authedAt = now
	t.lastError = ""
	t.refreshTokenRevoked = false
	t.loaded = true
	// IngestTokenResponse is the operator's Connect-flow finale —
	// they MUST learn if the token didn't reach storage, otherwise
	// it will silently disappear on the next process restart and the
	// operator will repeat Connect tomorrow without ever knowing why.
	// Surface persist failures via lastError AND the returned error.
	if err := t.persistLocked(ctx); err != nil {
		t.lastError = err.Error()
		return err
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
	// Propagate transient load errors instead of falling through to
	// the "no refresh token; click Connect" message — operators must
	// see "DB unreachable" not "click Connect" when the store is
	// the actual problem.
	if err := t.ensureLoadedLocked(ctx); err != nil {
		return err
	}

	if t.cfg.Grant == OAuthGrantAuthorizationCode {
		return t.reacquireAuthCodeLocked(ctx)
	}
	if err := t.acquireLocked(ctx); err != nil {
		t.lastError = err.Error()
		return err
	}
	t.lastError = ""
	// Symmetry with the auth_code branch: a client_credentials
	// reacquire clears any prior dead-refresh signal too. For pure
	// client_credentials connections the field is never set true so
	// this is a no-op; defensive against future refactors that allow
	// mixed grant usage on a single source.
	t.refreshTokenRevoked = false
	return nil
}

// reacquireAuthCodeLocked is the auth_code branch of Reacquire,
// extracted to keep nestif under the project's complexity ceiling.
// Caller must hold t.mu.
func (t *oauthTokenSource) reacquireAuthCodeLocked(ctx context.Context) error {
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
		// Set lastError AFTER clearStaleStateLocked so its best-effort
		// cleanup-error reporting can't clobber the IdP rejection text
		// that operators need to see in Status.
		t.lastError = err.Error()
		return err
	}
	// Persist failures are non-fatal: the in-memory token works, the
	// next refresh will re-persist. Log so operators can spot DB
	// issues; don't surface on lastError (would be cleared by the
	// next success).
	if err := t.persistLocked(ctx); err != nil {
		slog.Warn("gateway/oauth: persist after Reacquire failed (in-memory token still valid)",
			logKeyConnection, t.connectionName,
			logKeyError, err)
	}
	t.lastError = ""
	t.refreshTokenRevoked = false
	return nil
}

// Status snapshots the current state for the admin endpoint. A
// transient store error during the implicit load is recorded on
// lastError so it surfaces in the response (operators see the actual
// cause); the returned snapshot reports "no token" until the next
// retry succeeds.
func (t *oauthTokenSource) Status() OAuthStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	_ = t.ensureLoadedLocked(context.Background())
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
	AccessToken      string `json:"access_token"`
	TokenType        string `json:"token_type"`
	ExpiresIn        int    `json:"expires_in"`
	RefreshToken     string `json:"refresh_token,omitempty"`
	Scope            string `json:"scope,omitempty"`
	Error            string `json:"error,omitempty"`
	ErrorDescription string `json:"error_description,omitempty"`
}

// exchangeLocked POSTs the form to the token endpoint and updates t.state
// on success. Caller must hold t.mu. The transport / read / parse stages
// are split into helpers below so each function stays under
// gocyclo's complexity ceiling AND each stage has a clear name in
// stack traces / pprof.
func (t *oauthTokenSource) exchangeLocked(ctx context.Context, form url.Values) error {
	grantType := form.Get("grant_type")
	tokenHost := URLHost(t.cfg.TokenURL)
	body, err := t.postTokenRequest(ctx, form, grantType, tokenHost)
	if err != nil {
		return err
	}
	tr, err := decodeTokenResponse(body)
	if err != nil {
		return err
	}
	t.applyTokenResponseLocked(tr)
	return nil
}

// postTokenRequest performs the HTTP POST, drains and bounds the body,
// and translates non-2xx into a structured error. Returns the (capped)
// raw body on success so the caller can JSON-decode it.
func (t *oauthTokenSource) postTokenRequest(
	ctx context.Context, form url.Values, grantType, tokenHost string,
) ([]byte, error) {
	// #nosec G107 G704 -- the token URL comes from operator-authored
	// connection config, not from request input.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.cfg.TokenURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("oauth: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	exchangeStart := time.Now()
	slog.Debug("gateway/oauth: token exchange start",
		logKeyConnection, t.connectionName,
		LogKeyGrantType, grantType,
		LogKeyTokenURLHost, tokenHost,
		"client_id", t.cfg.ClientID)

	// #nosec G107 G704 -- TokenURL is operator-authored connection config.
	resp, err := t.client.Do(req)
	if err != nil {
		slog.Warn("gateway/oauth: token request transport error",
			logKeyConnection, t.connectionName,
			LogKeyGrantType, grantType,
			LogKeyTokenURLHost, tokenHost,
			"duration", time.Since(exchangeStart),
			logKeyError, err)
		return nil, fmt.Errorf("oauth: token request: %w", err)
	}
	// Drain remaining bytes before close so net/http can pool the
	// connection. Without the drain, an oversize body leaves bytes on
	// the wire and every refresh re-handshakes TCP+TLS.
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	body, err := readCappedBody(resp.Body, maxTokenResponseBytes)
	if err != nil {
		return nil, t.logAndWrapBodyError(err, grantType, tokenHost)
	}
	if resp.StatusCode != http.StatusOK {
		slog.Warn("gateway/oauth: non-200 from token endpoint",
			logKeyConnection, t.connectionName,
			LogKeyGrantType, grantType,
			LogKeyTokenURLHost, tokenHost,
			"status", resp.StatusCode,
			"duration", time.Since(exchangeStart),
			"body_excerpt", trimBody(body))
		return nil, interpretTokenError(grantType, resp.StatusCode, body)
	}
	slog.Info("gateway/oauth: token exchange success",
		logKeyConnection, t.connectionName,
		LogKeyGrantType, grantType,
		LogKeyTokenURLHost, tokenHost,
		"duration", time.Since(exchangeStart))
	return body, nil
}

// logAndWrapBodyError is the small helper used by postTokenRequest to
// log and return body-read errors (transport read failure OR oversize-
// body cap exceeded). Kept tiny so postTokenRequest stays readable.
func (t *oauthTokenSource) logAndWrapBodyError(err error, grantType, tokenHost string) error {
	if errors.Is(err, errTokenResponseTooLarge) {
		slog.Warn("gateway/oauth: token response exceeds size cap",
			logKeyConnection, t.connectionName,
			LogKeyGrantType, grantType,
			LogKeyTokenURLHost, tokenHost,
			"limit_bytes", maxTokenResponseBytes)
	}
	return err
}

// errTokenResponseTooLarge is the sentinel for "IdP returned more than
// maxTokenResponseBytes." Wrapped (not the user-facing message) so
// callers can detect the cap-exceeded case without string matching.
var errTokenResponseTooLarge = errors.New("oauth: response exceeds size cap")

// readCappedBody reads up to limit+1 bytes and rejects with
// errTokenResponseTooLarge if the +1 byte was reached. The +1 lets us
// detect truncation rather than silently parse a partial JSON
// document; without it, a malicious IdP could feed attacker-controlled
// fields by stuffing extra bytes after the genuine response.
func readCappedBody(r io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("oauth: read response: %w", err)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("oauth: %d-byte cap exceeded (likely misbehaving idp): %w", limit, errTokenResponseTooLarge)
	}
	return body, nil
}

// decodeTokenResponse parses an OAuth-2.1 token response body and
// validates the minimum fields the platform requires before treating
// the exchange as successful.
func decodeTokenResponse(body []byte) (tokenResponse, error) {
	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return tokenResponse{}, fmt.Errorf("oauth: decode response: %w", err)
	}
	if tr.Error != "" {
		return tokenResponse{}, fmt.Errorf("oauth: %s: %s", tr.Error, tr.ErrorDescription)
	}
	if tr.AccessToken == "" {
		return tokenResponse{}, errors.New("oauth: token response missing access_token")
	}
	return tr, nil
}

// applyTokenResponseLocked writes a valid token response into the
// source's in-memory state. Caller must hold t.mu.
//
// Always clears refreshTokenRevoked: a successful exchange means the
// source has fresh credentials regardless of what state preceded it.
// Embedding the clear here keeps the state-machine invariant
// "successful exchange → not revoked" enforced at the helper level
// rather than relying on every caller to remember it.
func (t *oauthTokenSource) applyTokenResponseLocked(tr tokenResponse) {
	now := time.Now().UTC()
	t.state.AccessToken = tr.AccessToken
	if tr.RefreshToken != "" {
		t.state.RefreshToken = tr.RefreshToken
	}
	if tr.ExpiresIn > 0 {
		t.state.ExpiresAt = now.Add(time.Duration(tr.ExpiresIn) * time.Second)
	} else {
		// Default to one hour when the upstream omits expires_in. Spec
		// recommends always including it but real implementations vary.
		t.state.ExpiresAt = now.Add(1 * time.Hour)
	}
	t.state.LastRefreshedAt = now
	t.refreshTokenRevoked = false
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
	// Intentional: caller's ctx may already be expiring (the rejection
	// we're cleaning up after often arrives just before deadline).
	// The cleanup must outlive the caller's ctx to actually delete
	// the dead row.
	cleanupCtx, cancel := context.WithTimeout(context.Background(), staleCleanupTimeout)
	defer cancel()
	if err := t.store.Delete(cleanupCtx, t.connectionName); err != nil && !errors.Is(err, ErrTokenNotFound) { //nolint:contextcheck // intentional: cleanupCtx is deliberately non-inherited so the cleanup outlives the caller's ctx
		slog.Warn("gateway/oauth: failed to delete stale token row",
			logKeyConnection, t.connectionName,
			logKeyError, err)
	}
}

// ensureLoadedLocked reads the persisted token row (if any) into the
// in-memory state. Caller must hold t.mu. Returns nil on success
// (including ErrTokenNotFound — empty row is a valid load result).
//
// On a transient store error (DB hiccup, encryption-service blip) the
// source is NOT marked loaded so the next call retries; the error is
// also returned so the caller propagates it instead of falling through
// to grant-specific logic and producing a misleading "click Connect"
// message that hides the actual DB problem.
func (t *oauthTokenSource) ensureLoadedLocked(ctx context.Context) error {
	if t.loaded {
		return nil
	}
	if t.store == nil {
		t.loaded = true
		return nil
	}
	rec, err := t.store.Get(ctx, t.connectionName)
	if err != nil {
		if errors.Is(err, ErrTokenNotFound) {
			t.loaded = true
			return nil
		}
		// Transient store error — leave loaded=false so the next call
		// retries. Surface the failure on lastError so Status shows it,
		// AND return it so the caller doesn't fall through to a
		// misleading grant-specific error.
		t.lastError = "load token: " + err.Error()
		slog.Warn("gateway/oauth: failed to load persisted token (will retry)",
			logKeyConnection, t.connectionName,
			logKeyError, err)
		return fmt.Errorf("oauth: load token: %w", err)
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
	return nil
}

// persistLocked writes the current token state back to the store and
// returns the Set error so callers can decide whether persistence
// failure is fatal. Caller must hold t.mu.
//
// Caller policy:
//
//   - IngestTokenResponse propagates the error: an operator who just
//     completed a Connect flow MUST learn that the token didn't reach
//     storage, otherwise it'll silently disappear on the next restart.
//   - Token() and Reacquire()'s refresh-success paths LOG the error
//     and continue: the in-memory token works for this process's
//     lifetime; the next refresh attempt will re-persist; self-heals.
func (t *oauthTokenSource) persistLocked(ctx context.Context) error {
	if t.store == nil {
		return nil
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
		return fmt.Errorf("oauth: persist token: %w", err)
	}
	return nil
}
