package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

	mu        sync.Mutex
	state     tokenState
	loaded    bool
	loadErr   error
	lastError string
	authedBy  string
	authedAt  time.Time
}

// newOAuthTokenSource builds a token source backed by http.DefaultClient
// for the OAuth token exchange. Pass a non-nil store to enable persistent
// authorization_code flows; pass nil for client_credentials.
func newOAuthTokenSource(cfg OAuthConfig, connection string, store TokenStore) *oauthTokenSource {
	return &oauthTokenSource{
		cfg:            cfg,
		connectionName: connection,
		client:         http.DefaultClient,
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
		return t.state.AccessToken, nil
	}
	// Try refresh first if we have a refresh token.
	if t.state.RefreshToken != "" {
		if err := t.refreshLocked(ctx); err == nil {
			t.persistLocked(ctx)
			return t.state.AccessToken, nil
		}
	}
	// authorization_code with no usable refresh path: needs human re-auth.
	if t.cfg.Grant == OAuthGrantAuthorizationCode {
		err := errors.New("oauth: authorization_code grant requires reauthentication (no valid refresh token)")
		t.lastError = err.Error()
		return "", err
	}
	if err := t.acquireLocked(ctx); err != nil {
		t.lastError = err.Error()
		return "", err
	}
	t.lastError = ""
	return t.state.AccessToken, nil
}

// ensureLoadedLocked reads the persisted token row (if any) into the
// in-memory state. Caller must hold t.mu. Subsequent calls are no-ops.
func (t *oauthTokenSource) ensureLoadedLocked(ctx context.Context) {
	if t.loaded || t.store == nil {
		t.loaded = true
		return
	}
	t.loaded = true
	rec, err := t.store.Get(ctx, t.connectionName)
	if err != nil {
		if !errors.Is(err, ErrTokenNotFound) {
			t.loadErr = err
		}
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
		// in-memory token, the persistence is for restart durability.
		t.lastError = "persist token: " + err.Error()
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
func (t *oauthTokenSource) IngestTokenResponse(ctx context.Context, in IngestTokenResponseInput) error {
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
			t.lastError = err.Error()
			return err
		}
		t.persistLocked(ctx)
		t.lastError = ""
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
		Configured:      true,
		TokenAcquired:   t.state.AccessToken != "",
		ExpiresAt:       t.state.ExpiresAt,
		LastRefreshedAt: t.state.LastRefreshedAt,
		HasRefreshToken: t.state.RefreshToken != "",
		LastError:       t.lastError,
		Grant:           t.cfg.Grant,
		TokenURL:        t.cfg.TokenURL,
		Scope:           t.cfg.Scope,
		AuthenticatedBy: t.authedBy,
		AuthenticatedAt: t.authedAt,
		NeedsReauth:     needsReauth,
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

// refreshLocked uses a refresh_token grant. Caller must hold t.mu.
// Persists the rotated refresh token (if any) back to the store so a
// subsequent process restart picks up the freshest credentials.
func (t *oauthTokenSource) refreshLocked(ctx context.Context) error {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", t.state.RefreshToken)
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

	// #nosec G107 G704 -- TokenURL is operator-authored connection config, not user input.
	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("oauth: token request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("oauth: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return interpretTokenError(resp.StatusCode, body)
	}

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

// interpretTokenError tries to parse a structured OAuth error from the
// upstream's response body, falling back to status code + raw body.
func interpretTokenError(status int, body []byte) error {
	var tr tokenResponse
	if jerr := json.Unmarshal(body, &tr); jerr == nil && tr.Error != "" {
		return fmt.Errorf("oauth: %d %s: %s", status, tr.Error, tr.ErrorDescription)
	}
	return fmt.Errorf("oauth: token endpoint returned %d: %s", status, trimBody(body))
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
