package apigateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"

	"github.com/txn2/mcp-data-platform/pkg/authevents"
	"github.com/txn2/mcp-data-platform/pkg/connoauth"
)

// Authenticator applies a connection's authentication scheme to an
// outbound HTTP request before it is sent. Implementations must be
// safe for concurrent use — a single Authenticator is shared across
// all in-flight invocations of a connection.
//
// Implementations MUST NOT log credential material. The toolkit's
// audit pipeline expects no Authorization or X-API-Key value to ever
// appear in slog output, error messages, or audit rows; carelessly
// formatted error strings are the most common leak path.
type Authenticator interface {
	Apply(req *http.Request) error
}

// NewAuthenticator returns the Authenticator implementation for a
// validated Config. ParseConfig has already rejected unknown auth
// modes, so the default branch only fires if a future mode is added
// without a matching case here.
func NewAuthenticator(c Config) (Authenticator, error) {
	switch c.AuthMode {
	case AuthModeNone:
		return noneAuth{}, nil
	case AuthModeBearer:
		return bearerAuth{credential: c.Credential}, nil
	case AuthModeAPIKey:
		return newAPIKeyAuth(c)
	case AuthModeOAuth2ClientCredentials:
		return newOAuth2ClientCredentialsAuth(c), nil
	case AuthModeOAuth2AuthorizationCode:
		// authorization_code requires a TokenStore. NewAuthenticator
		// alone cannot supply one (it has no DB handle); the toolkit's
		// addParsedConnection wires the TokenStore at materialization
		// time. Returning the un-stored variant here keeps the call
		// site consistent — the toolkit immediately calls
		// SetTokenStore on it.
		return newOAuth2AuthorizationCodeAuth(c), nil
	default:
		return nil, fmt.Errorf("apigateway: no authenticator for auth_mode %q", c.AuthMode)
	}
}

// noneAuth applies no credential. Distinct from a nil Authenticator so
// the toolkit's invocation path can call Apply unconditionally.
type noneAuth struct{}

// Apply is a no-op; the connection requested no outbound auth.
func (noneAuth) Apply(_ *http.Request) error { return nil }

// bearerAuth sets the Authorization header to "Bearer <credential>".
type bearerAuth struct {
	credential string
}

// Apply attaches the bearer token as the Authorization header.
func (b bearerAuth) Apply(req *http.Request) error {
	if b.credential == "" {
		return errors.New("apigateway: bearer credential is empty")
	}
	req.Header.Set(authorizationHeader, "Bearer "+b.credential)
	return nil
}

// apiKeyAuth attaches the credential as either a header or a query
// parameter, per the connection's APIKeyPlacement setting.
type apiKeyAuth struct {
	credential string
	placement  string
	header     string
	param      string
}

func newAPIKeyAuth(c Config) (apiKeyAuth, error) {
	a := apiKeyAuth{
		credential: c.Credential,
		placement:  c.APIKeyPlacement,
		header:     c.APIKeyHeader,
		param:      c.APIKeyParam,
	}
	if a.credential == "" {
		return apiKeyAuth{}, errors.New("apigateway: api_key credential is empty")
	}
	switch a.placement {
	case APIKeyPlacementHeader:
		if a.header == "" {
			return apiKeyAuth{}, errors.New("apigateway: api_key_header is empty")
		}
	case APIKeyPlacementQuery:
		if a.param == "" {
			return apiKeyAuth{}, errors.New("apigateway: api_key_param is empty")
		}
	default:
		return apiKeyAuth{}, fmt.Errorf("apigateway: invalid api_key_placement %q", a.placement)
	}
	return a, nil
}

// Apply attaches the API key as either a header or a query parameter,
// per the connection's APIKeyPlacement.
func (a apiKeyAuth) Apply(req *http.Request) error {
	switch a.placement {
	case APIKeyPlacementHeader:
		req.Header.Set(a.header, a.credential)
	case APIKeyPlacementQuery:
		q := req.URL.Query()
		q.Set(a.param, a.credential)
		req.URL.RawQuery = q.Encode()
	default:
		return fmt.Errorf("apigateway: invalid api_key_placement %q", a.placement)
	}
	return nil
}

// oauth2ClientCredentialsAuth applies an OAuth 2.1 access token
// acquired via the client_credentials grant. Token caching and
// refresh-on-expiry are delegated to the standard
// golang.org/x/oauth2 library: the underlying TokenSource fetches
// once, caches in memory, and re-fetches transparently when the
// token expires. No DB state is required because the authoritative
// inputs (client_id + client_secret) survive process restarts as
// part of the connection's encrypted credentials blob.
//
// SECURITY: this struct intentionally does NOT carry the client
// secret in its log/error string output. tokenFetchError below
// scrubs the token URL's query string and userinfo before
// surfacing transport errors to the model — without this, a bad
// network path or DNS failure during a token fetch would expose
// any credential the operator embedded in the URL (an unfortunate
// pattern but one that exists in the wild).
type oauth2ClientCredentialsAuth struct {
	src oauth2.TokenSource
}

// oauth2TokenFetchTimeout caps how long a single token-endpoint
// request can take. Without this the default Go http.Client has no
// timeout and an unreachable IdP would hang every Apply call until
// the OS-level dial timeout fires (minutes). 30 seconds matches
// the order of magnitude of the upstream call_timeout while
// staying generous for IdPs with slow first-token issuance.
const oauth2TokenFetchTimeout = 30 * time.Second

// newTokenExchangeClient builds the http.Client used for any
// credential-bearing POST to an OAuth token endpoint (initial code
// exchange, refresh-token grant, client_credentials acquire). The
// CheckRedirect hook refuses 3xx so a misconfigured or compromised
// IdP cannot redirect the form body — which carries client_secret
// and (on refresh) the long-lived refresh_token — to an attacker
// URL. Mirrors the MCP gateway's same-purpose helper.
func newTokenExchangeClient() *http.Client {
	return &http.Client{
		Timeout: oauth2TokenFetchTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func newOAuth2ClientCredentialsAuth(c Config) oauth2ClientCredentialsAuth {
	authStyle := oauth2.AuthStyleInHeader
	if c.OAuth2.EndpointAuthStyle == OAuth2AuthStyleParams {
		authStyle = oauth2.AuthStyleInParams
	}
	cfg := &clientcredentials.Config{
		ClientID:     c.OAuth2.ClientID,
		ClientSecret: c.OAuth2.ClientSecret,
		TokenURL:     c.OAuth2.TokenURL,
		Scopes:       c.OAuth2.Scopes,
		AuthStyle:    authStyle,
	}
	// Bound token-endpoint requests via a custom http.Client. The
	// oauth2 library reads ctx-value oauth2.HTTPClient and falls
	// back to http.DefaultClient (no timeout) otherwise. CheckRedirect
	// refuses to follow 3xx so a misconfigured or compromised IdP
	// cannot redirect this credential-bearing POST (carrying
	// client_secret in the form body or HTTP Basic header) to an
	// attacker URL.
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, newTokenExchangeClient())
	// The token source caches in-memory and re-fetches when expired.
	// Wrap with oauth2.ReuseTokenSource so the cache is reused
	// across Apply calls; clientcredentials.Config.TokenSource
	// already does this internally but the wrap is explicit
	// defense against future library changes.
	src := oauth2.ReuseTokenSource(nil, cfg.TokenSource(ctx))
	return oauth2ClientCredentialsAuth{src: src}
}

// Apply fetches (or returns the cached) access token and attaches
// it as the Authorization header. Errors from the token source —
// network failures, IdP rejections, expired credentials — are
// scrubbed via tokenFetchError before being returned, so the
// caller's error pipeline cannot leak the OAuth client secret or
// the token URL's userinfo.
func (a oauth2ClientCredentialsAuth) Apply(req *http.Request) error {
	tok, err := a.src.Token()
	if err != nil {
		return tokenFetchError(err)
	}
	if tok == nil || tok.AccessToken == "" {
		return errors.New("apigateway: oauth2 token source returned no access token")
	}
	req.Header.Set(authorizationHeader, "Bearer "+tok.AccessToken)
	return nil
}

// tokenFetchError sanitizes errors from oauth2.TokenSource.Token().
// The library's *oauth2.RetrieveError includes the IdP response
// body, which can contain sensitive material (e.g., a refresh
// token from a partial grant exchange). It also includes the
// token URL — if the operator embedded credentials in the URL
// (e.g., https://user:secret@idp.example/token), those would
// leak. We rebuild the message keeping only the non-sensitive
// pieces.
func tokenFetchError(err error) error {
	var re *oauth2.RetrieveError
	if errors.As(err, &re) {
		return fmt.Errorf("apigateway: oauth2 token fetch failed: status=%d", re.Response.StatusCode)
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		parsed, perr := url.Parse(ue.URL)
		if perr != nil {
			return fmt.Errorf("apigateway: oauth2 token fetch %s: %w", ue.Op, ue.Err)
		}
		parsed.RawQuery = ""
		parsed.User = nil
		return fmt.Errorf("apigateway: oauth2 token fetch %s %q: %w", ue.Op, parsed.String(), ue.Err)
	}
	// Fallback: redact anything that looks URL-shaped just in case
	// a future library version wraps in a different error type.
	msg := err.Error()
	if strings.Contains(msg, "://") {
		return errors.New("apigateway: oauth2 token fetch failed (details redacted)")
	}
	return fmt.Errorf("apigateway: oauth2 token fetch failed: %s", msg)
}

// ErrNeedsReauth is the structured error api_invoke_endpoint surfaces
// when an authorization_code connection's stored refresh token is
// missing, expired beyond refresh_expires_at, or definitively rejected
// by the IdP (RFC 6749 §5.2 invalid_grant on the refresh_token grant).
// Transient failures (network, 5xx, request cancellation) DO NOT
// produce this error — see Apply for the distinction.
//
// The error message intentionally points the operator at the
// platform's reauth path rather than echoing the underlying IdP
// response (which can include sensitive material from a partial
// grant exchange). See tokenFetchError for the parallel scrubber on
// transport failures.
var ErrNeedsReauth = errors.New("apigateway: oauth2 connection needs admin reconnect")

// errRefreshTokenRevoked wraps the underlying error when the IdP
// definitively rejects a refresh_token grant — RFC 6749 §5.2
// invalid_grant at HTTP 400. Distinguished from transient failures
// (network drops, 5xx, request cancellation) so Apply only deletes
// the persisted row when the IdP says the refresh is dead. Without
// this distinction a single flaky-network event during a tool call
// would permanently invalidate a long-lived refresh token.
var errRefreshTokenRevoked = errors.New("apigateway: refresh token rejected by IdP (invalid_grant)")

// oauth2AuthorizationCodeAuth applies an OAuth 2.1 access token
// acquired via the user-driven authorization_code grant. The
// initial token is fetched at admin "Connect" time by the API
// gateway's OAuth callback handler and persisted (with refresh
// token) to TokenStore. Subsequent calls read the cached access
// token; when it expires the underlying golang.org/x/oauth2
// TokenSource silently exchanges the refresh token for a fresh
// access token and writes both back to the store. When the IdP
// rejects the refresh token (revoked, refresh_expires_at passed),
// Apply returns ErrNeedsReauth and the admin must click Connect
// again.
type oauth2AuthorizationCodeAuth struct {
	cfg Config
	// mu guards store and events against concurrent SetTokenStore /
	// SetAuthEvents (called by the toolkit's threading paths) while
	// Apply reads them on every outbound request. Without this,
	// `go test -race` flags the field access pattern.
	mu     sync.RWMutex
	store  TokenStore
	events *authevents.Writer
}

func newOAuth2AuthorizationCodeAuth(c Config) *oauth2AuthorizationCodeAuth {
	return &oauth2AuthorizationCodeAuth{cfg: c}
}

// SetTokenStore wires the persistent token store. Required before
// Apply can be called — the toolkit's addParsedConnection invokes
// this immediately after constructing the Authenticator. Stored as
// a method (not a constructor argument) because NewAuthenticator
// is called from contexts (config validation, factory code) that
// don't have a database handle.
func (a *oauth2AuthorizationCodeAuth) SetTokenStore(s TokenStore) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.store = s
}

// SetAuthEvents wires the audit-event writer. nil-safe — the
// helper methods on *authevents.Writer short-circuit. Wired from
// the platform alongside SetTokenStore so every outbound refresh
// emits its lifecycle event.
func (a *oauth2AuthorizationCodeAuth) SetAuthEvents(w *authevents.Writer) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = w
}

// snapshot returns the current store and events under read lock so
// Apply / refresh / handleRevoked can read them without holding the
// lock across an outbound HTTP call (which would serialize every
// request through this single mutex).
func (a *oauth2AuthorizationCodeAuth) snapshot() (TokenStore, *authevents.Writer) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.store, a.events
}

// Apply attaches the cached or freshly-refreshed access token. The
// flow:
//  1. Read the persisted token. ErrTokenNotFound → ErrNeedsReauth.
//  2. If access token still valid (with the library's 10s buffer),
//     attach it as Authorization: Bearer <token>.
//  3. If expired, exchange refresh_token → fresh access token via
//     the IdP's token endpoint. On success, persist the new token
//     pair (the IdP MAY rotate the refresh token).
//  4. On REVOKED refresh (RFC 6749 §5.2 invalid_grant @ 400, OR
//     refresh_expires_at passed, OR refresh token absent): delete
//     the row and return ErrNeedsReauth so the admin sees the
//     reconnect prompt.
//  5. On TRANSIENT failure (network drop, 5xx, ctx cancellation):
//     return the scrubbed error WITHOUT deleting the row. The
//     persisted refresh stays intact so a retry on the next call
//     can succeed; misclassifying a transient failure as revoked
//     would force the operator to manually reconnect over a
//     network blip.
func (a *oauth2AuthorizationCodeAuth) Apply(req *http.Request) error {
	store, _ := a.snapshot()
	if store == nil {
		return errors.New("apigateway: oauth2 authorization_code: token store not wired")
	}
	ctx := req.Context()
	persisted, err := store.Get(ctx, a.cfg.ConnectionName)
	if err != nil {
		if errors.Is(err, ErrTokenNotFound) {
			return ErrNeedsReauth
		}
		return fmt.Errorf("apigateway: load oauth token: %w", err)
	}
	tok := persistedToOAuth2Token(*persisted)
	if tok.Valid() {
		req.Header.Set(authorizationHeader, "Bearer "+tok.AccessToken)
		return nil
	}
	// Access token expired (or about to). Refresh.
	fresh, refreshErr := a.refresh(ctx, persisted)
	if refreshErr != nil {
		// Distinguish revoked from transient. Revoked → delete row
		// (admin must reconnect). Transient → keep row, surface the
		// error so the caller can retry.
		if isRevokedRefresh(refreshErr) {
			a.handleRevoked(ctx, persisted, refreshErr)
			return ErrNeedsReauth
		}
		return refreshErr
	}
	req.Header.Set(authorizationHeader, "Bearer "+fresh.AccessToken)
	return nil
}

// handleRevoked is the shared revoked-refresh cleanup for the
// toolkit's Apply path. Mirrors connoauth.Source.handleRevoked: an
// INFO log replaces the previously silent `_ = Delete(...)` and the
// authevents row pair (refresh_failed_revoked + token_deleted_revoked)
// surfaces the deletion on the History panel so operators see exactly
// when and why the token vanished.
func (a *oauth2AuthorizationCodeAuth) handleRevoked(ctx context.Context, persisted *PersistedToken, refreshErr error) {
	store, events := a.snapshot()
	reason := classifyRevokedReason(refreshErr)
	tokenURL := a.cfg.OAuth2.TokenURL
	events.RefreshFailedRevoked(ctx, connoauth.KindAPI, a.cfg.ConnectionName,
		authevents.SystemToolCall, tokenURL, authevents.RefreshDetail{
			BeforeExpiresAt:        persisted.ExpiresAt,
			BeforeRefreshExpiresAt: persisted.RefreshExpiresAt,
			IDPErrorCode:           reason,
		})
	if store == nil {
		return
	}
	if delErr := store.Delete(ctx, a.cfg.ConnectionName); delErr != nil {
		slog.Warn("apigateway: delete revoked token row failed",
			logKeyConnection, a.cfg.ConnectionName, "error", delErr)
		return
	}
	// Log AFTER the delete so the "row deleted" claim is factually
	// true even when the store's Delete fails (which falls through
	// to the WARN above and the return).
	slog.Info("apigateway: connection token row deleted: refresh rejected by IdP",
		logKeyConnection, a.cfg.ConnectionName,
		"reason", reason, "token_url_host", urlHost(tokenURL))
	events.TokenDeletedRevoked(ctx, connoauth.KindAPI, a.cfg.ConnectionName,
		authevents.SystemToolCall, tokenURL, reason)
}

// classifyRevokedReason maps the toolkit's sentinel errors into the
// stable machine-readable strings the History panel renders. Mirrors
// connoauth.classifyRevokedReason so the panel can correlate events
// from either refresh path without per-source special-casing.
func classifyRevokedReason(err error) string {
	switch {
	case errors.Is(err, errRefreshTokenRevoked):
		return "invalid_grant"
	case errors.Is(err, errNoRefreshToken):
		return "no_refresh_token"
	case errors.Is(err, errRefreshExpired):
		return "refresh_expired"
	}
	return "revoked"
}

// urlHost returns the host portion of u for log fields, falling back
// to the raw value when parsing fails. Local helper so the package
// doesn't need to depend on net/url across more files.
func urlHost(u string) string {
	parsed, err := url.Parse(u)
	if err != nil || parsed.Host == "" {
		return u
	}
	return parsed.Host
}

// isRevokedRefresh reports whether the refresh-error indicates the
// IdP definitively rejected the refresh token (vs a transient
// failure that may succeed on retry). The local-side checks
// (no refresh token persisted, refresh_expires_at passed) are
// always definitive. For IdP responses, only RFC 6749 §5.2
// invalid_grant at HTTP 400 is treated as definitive — matching
// the MCP gateway's isRefreshDeadError. Keep this in sync with
// pkg/toolkits/gateway/oauth.go:isRefreshDeadError.
func isRevokedRefresh(err error) bool {
	if errors.Is(err, errRefreshTokenRevoked) {
		return true
	}
	// Local-side definitive errors (no refresh token, refresh
	// expired) — the refresh() helper produces these BEFORE any
	// network call, so they are safe to treat as revoked even
	// without an IdP response.
	if errors.Is(err, errNoRefreshToken) || errors.Is(err, errRefreshExpired) {
		return true
	}
	return false
}

// errNoRefreshToken / errRefreshExpired are sentinel locals so
// Apply can fold them into the "needs reauth" branch alongside
// errRefreshTokenRevoked. Both indicate state that won't recover
// without admin action.
var (
	errNoRefreshToken = errors.New("apigateway: no refresh token persisted")
	errRefreshExpired = errors.New("apigateway: refresh token has expired")
)

// refresh exchanges the persisted refresh_token for a fresh access
// token at the IdP's token endpoint and writes the result back to
// the store. The new refresh token (if rotated by the IdP) is
// persisted alongside the new access token, with RefreshExpiresAt
// updated when the IdP echoes refresh_expires_in (Keycloak-style).
// Errors are scrubbed via tokenFetchError before the caller sees
// them so IdP response bodies and embedded URL credentials don't
// leak; RFC 6749 §5.2 invalid_grant at HTTP 400 is wrapped with
// errRefreshTokenRevoked so Apply can distinguish it from
// transient failures.
func (a *oauth2AuthorizationCodeAuth) refresh(ctx context.Context, persisted *PersistedToken) (*oauth2.Token, error) {
	store, events := a.snapshot()
	tokenURL := a.cfg.OAuth2.TokenURL
	if persisted.RefreshToken == "" {
		// Caller treats this as "revoked" and handleRevoked emits
		// RefreshFailedRevoked + TokenDeletedRevoked with
		// IDPErrorCode="no_refresh_token". No skipped event here —
		// would duplicate the History panel entry for one incident.
		return nil, errNoRefreshToken
	}
	if !persisted.RefreshExpiresAt.IsZero() && time.Now().After(persisted.RefreshExpiresAt) {
		// Same: IDPErrorCode="refresh_expired" via handleRevoked.
		return nil, errRefreshExpired
	}
	endpoint := oauth2.Endpoint{
		TokenURL:  tokenURL,
		AuthStyle: oauth2.AuthStyleInHeader,
	}
	if a.cfg.OAuth2.EndpointAuthStyle == OAuth2AuthStyleParams {
		endpoint.AuthStyle = oauth2.AuthStyleInParams
	}
	cfg := &oauth2.Config{
		ClientID:     a.cfg.OAuth2.ClientID,
		ClientSecret: a.cfg.OAuth2.ClientSecret,
		Endpoint:     endpoint,
		Scopes:       a.cfg.OAuth2.Scopes,
	}
	refreshCtx := context.WithValue(ctx, oauth2.HTTPClient, newTokenExchangeClient())

	start := time.Now()
	src := cfg.TokenSource(refreshCtx, persistedToOAuth2Token(*persisted))
	fresh, err := src.Token()
	durationMS := time.Since(start).Milliseconds()
	if err != nil {
		classified := classifyRefreshError(err)
		if !isRevokedRefresh(classified) {
			events.RefreshFailedTransient(ctx, connoauth.KindAPI, a.cfg.ConnectionName,
				authevents.SystemToolCall, tokenURL, authevents.RefreshDetail{
					BeforeExpiresAt:        persisted.ExpiresAt,
					BeforeRefreshExpiresAt: persisted.RefreshExpiresAt,
					DurationMS:             durationMS,
				})
		}
		return nil, classified
	}
	// Persist the rotated token (IdP MAY have issued a new
	// refresh_token; keep the old one if the response didn't carry
	// one). RefreshExpiresAt is recomputed from the IdP's
	// refresh_expires_in extra-field when present; on rotation
	// without a fresh hint, it's reset to zero so a stale Keycloak
	// deadline doesn't outlive the rotation.
	updated := *persisted
	updated.AccessToken = fresh.AccessToken
	rotated := fresh.RefreshToken != "" && fresh.RefreshToken != persisted.RefreshToken
	if fresh.RefreshToken != "" {
		updated.RefreshToken = fresh.RefreshToken
	}
	updated.ExpiresAt = fresh.Expiry
	updated.RefreshExpiresAt = computeRefreshExpiresAt(fresh, persisted, time.Now())
	if setErr := store.Set(ctx, updated); setErr != nil {
		if rotated {
			slog.Error("apigateway: rotated refresh token issued but persist failed — connection may be unrecoverable",
				logKeyConnection, a.cfg.ConnectionName, "error", setErr)
			events.RotationPersistenceFailed(ctx, connoauth.KindAPI, a.cfg.ConnectionName,
				authevents.SystemToolCall, tokenURL, setErr.Error())
		} else {
			slog.Warn("apigateway: persisting refreshed oauth token failed",
				logKeyConnection, a.cfg.ConnectionName, "error", setErr)
		}
	} else {
		events.RefreshSucceeded(ctx, connoauth.KindAPI, a.cfg.ConnectionName,
			authevents.SystemToolCall, tokenURL, authevents.RefreshDetail{
				BeforeExpiresAt:        persisted.ExpiresAt,
				BeforeRefreshExpiresAt: persisted.RefreshExpiresAt,
				AfterExpiresAt:         fresh.Expiry,
				AfterRefreshExpiresAt:  updated.RefreshExpiresAt,
				RotatedRefresh:         rotated,
				DurationMS:             durationMS,
			})
	}
	return fresh, nil
}

// classifyRefreshError wraps the underlying refresh error with
// errRefreshTokenRevoked when the IdP's response is RFC 6749 §5.2
// invalid_grant @ HTTP 400 (the canonical "this refresh is dead"
// signal) and otherwise scrubs via tokenFetchError so the caller
// receives a transient-vs-revoked distinguishable error without
// any IdP body content leaking into logs or model output.
func classifyRefreshError(err error) error {
	var retrieve *oauth2.RetrieveError
	if errors.As(err, &retrieve) &&
		retrieve.Response != nil &&
		retrieve.Response.StatusCode == http.StatusBadRequest &&
		retrieve.ErrorCode == "invalid_grant" {
		// Wrap so errors.Is(err, errRefreshTokenRevoked) holds. The
		// scrubbed message comes through tokenFetchError so no IdP
		// body content leaks.
		return fmt.Errorf("apigateway: refresh dead: %s (%w)", tokenFetchError(err).Error(), errRefreshTokenRevoked)
	}
	return tokenFetchError(err)
}

// computeRefreshExpiresAt extracts the refresh-token deadline from
// the IdP's response. golang.org/x/oauth2 stores extension fields
// (refresh_expires_in is one) in tok.Extra. The returned time.Time
// follows the policy:
//   - refresh_expires_in > 0 → absolute deadline = now + that many seconds
//   - refresh token rotated AND no refresh_expires_in → zero (IdP
//     did not disclose a fresh deadline; do NOT keep the old one,
//     it belonged to the previous refresh token)
//   - refresh token NOT rotated AND no refresh_expires_in → keep
//     the prior deadline (still tracking the same refresh token)
func computeRefreshExpiresAt(fresh *oauth2.Token, prior *PersistedToken, now time.Time) time.Time {
	if secs := refreshExpiresInSeconds(fresh); secs > 0 {
		return now.Add(time.Duration(secs) * time.Second)
	}
	if fresh.RefreshToken != "" {
		// Rotated without a fresh deadline → clear the old one.
		return time.Time{}
	}
	return prior.RefreshExpiresAt
}

// refreshExpiresInSeconds reads refresh_expires_in from the
// oauth2.Token's Extra fields and coerces to int64 seconds. The
// JSON decoder in golang.org/x/oauth2 leaves numeric extension
// fields as float64 (the JSON-default), so the cast checks both
// shapes for safety.
func refreshExpiresInSeconds(tok *oauth2.Token) int64 {
	v := tok.Extra("refresh_expires_in")
	if v == nil {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	default:
		return 0
	}
}

// persistedToOAuth2Token converts the row form to the library form.
// AccessToken absent → token treated as expired (Valid() returns false)
// because the zero ExpiresAt time is in the past; the refresh path
// kicks in.
func persistedToOAuth2Token(p PersistedToken) *oauth2.Token {
	return &oauth2.Token{
		AccessToken:  p.AccessToken,
		RefreshToken: p.RefreshToken,
		Expiry:       p.ExpiresAt,
	}
}
