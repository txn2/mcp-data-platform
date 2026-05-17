package connoauth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/txn2/mcp-data-platform/pkg/authevents"
)

// tokenFetchTimeout caps how long a single token-endpoint request
// can take. The underlying golang.org/x/oauth2 library uses
// http.DefaultClient (no timeout) unless the caller injects
// oauth2.HTTPClient on the context. Without this an unreachable IdP
// would hang every Token() call until the OS-level dial timeout
// fires (minutes). 30 seconds matches the security-hardened MCP /
// API gateway clients this code replaces.
const tokenFetchTimeout = 30 * time.Second

// expiryBuffer is the safety margin before token expiry at which we
// proactively refresh. Keeps in-flight calls from racing the clock.
// Mirrors the prior MCP gateway constant so behavior is unchanged
// after the refactor.
const expiryBuffer = 30 * time.Second

// Log field names. Named so revive's add-constant lint doesn't flag
// the repeated string literals across every audit and refresh log
// line — and so a typo can't silently re-label a field across the
// dozen+ slog call sites in this file.
const (
	logKeyKind  = "kind"
	logKeyName  = "name"
	logKeyError = "error"
)

// scopeSep is the OAuth 2.0 scope-list separator (a single space per
// RFC 6749 §3.3) and also the separator used when joining the
// status / error / error_description parts of a sanitized
// RetrieveError message. Named so revive's add-constant lint stops
// flagging the literal as it accumulates call sites.
const scopeSep = " "

// newTokenExchangeClient builds the http.Client used for any
// credential-bearing POST to an OAuth token endpoint. CheckRedirect
// refuses 3xx so a misconfigured or compromised IdP cannot redirect
// the form body — which carries client_secret and the long-lived
// refresh_token — to an attacker URL. Identical to the prior
// per-kind helpers; consolidated here so the security guard cannot
// drift between kinds.
func newTokenExchangeClient() *http.Client {
	return &http.Client{
		Timeout: tokenFetchTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// Source is the per-connection access-token getter. Toolkits call
// Token(ctx) on every outbound request; the Source reads the persisted
// row, returns the cached access_token when still valid, or refreshes
// it (and persists the result) when expired.
//
// Source is safe for concurrent use; the underlying Store is the
// source of truth and Set/Get serialize through the database (or the
// MemoryStore mutex). The Source itself is stateless across calls —
// every Token() round-trips the store, which is what makes multi-
// replica deployments correct (replica A's refresh becomes visible
// to replica B on the next call without inter-replica coordination).
type Source struct {
	store Store
	key   Key
	cfg   Config
	// client is the http.Client used for refresh exchanges. Per-Source
	// rather than package-global so tests can inject a fake transport
	// without mutating shared state.
	client *http.Client
	// events writes the lifecycle audit trail. nil-safe — the helper
	// methods on *authevents.Writer short-circuit when the receiver
	// is nil. Set via WithEvents.
	events *authevents.Writer
	// actor is recorded on emitted events so operators can tell
	// background-refresh from tool-call-triggered refresh apart in
	// the History panel. Defaults to SystemToolCall (the historical
	// caller); the background refresher sets SystemBackgroundRefresh.
	actor string
}

// NewSource builds a Source for the (key, cfg) pair. The store is
// the persistence backend (Postgres in production, Memory in tests).
// Callers reuse a single Source per connection — construction is
// cheap, but pooling avoids re-creating the http.Client per request.
func NewSource(store Store, key Key, cfg Config) *Source {
	return &Source{
		store:  store,
		key:    key,
		cfg:    cfg,
		client: newTokenExchangeClient(),
		actor:  authevents.SystemToolCall,
	}
}

// WithEvents wires the audit-event writer. Returns s so callers can
// chain construction inline. nil is accepted (events become a no-op).
func (s *Source) WithEvents(w *authevents.Writer) *Source {
	s.events = w
	return s
}

// WithActor sets the audit-event actor for events this Source emits.
// Used by the background refresher to record events as
// SystemBackgroundRefresh; the default (SystemToolCall) is correct
// for callers that hand the Source out per outbound request.
func (s *Source) WithActor(actor string) *Source {
	if actor != "" {
		s.actor = actor
	}
	return s
}

// Token returns a non-expired access token, refreshing transparently
// when the cached one has expired (or is within expiryBuffer of
// expiry). On unrecoverable failure (no refresh token, refresh
// rejected by IdP, refresh deadline passed), returns ErrNeedsReauth
// so callers can short-circuit retries and surface the Connect prompt
// to operators. Transient transport failures surface as the
// underlying error (sanitized) so the caller's retry path can run.
func (s *Source) Token(ctx context.Context) (string, error) {
	persisted, err := s.store.Get(ctx, s.key)
	if err != nil {
		if errors.Is(err, ErrTokenNotFound) {
			return "", ErrNeedsReauth
		}
		return "", fmt.Errorf("connoauth: load token: %w", err)
	}
	if accessTokenStillValid(persisted) {
		return persisted.AccessToken, nil
	}
	// Refresh under the store's distributed lock so two callers
	// (background refresher + tool call, or two replicas) cannot
	// POST the same refresh_token to a rotation-enforcing IdP and
	// race each other. After acquiring the lock we re-read the row:
	// another caller may have rotated under us, in which case the
	// cached access token is now valid and no IdP round-trip runs.
	unlock, err := s.store.Lock(ctx, s.key)
	if err != nil {
		return "", fmt.Errorf("connoauth: acquire refresh lock: %w", err)
	}
	defer unlock()
	persisted, err = s.store.Get(ctx, s.key)
	if err != nil {
		if errors.Is(err, ErrTokenNotFound) {
			return "", ErrNeedsReauth
		}
		return "", fmt.Errorf("connoauth: reload token after lock: %w", err)
	}
	if accessTokenStillValid(persisted) {
		return persisted.AccessToken, nil
	}
	fresh, refreshErr := s.refresh(ctx, persisted)
	if refreshErr != nil {
		if isRevokedRefresh(refreshErr) {
			s.handleRevoked(ctx, persisted, refreshErr)
			return "", ErrNeedsReauth
		}
		return "", refreshErr
	}
	return fresh.AccessToken, nil
}

// handleRevoked is the shared cleanup used by Token() and Reacquire()
// when the persisted credential cannot be used to obtain a fresh
// access token. The row is deleted (a dead credential must not be
// replayed across process restarts) AND the deletion is observable
// via an INFO log line and an authevents row pair in the History
// panel.
//
// The leading event differs by cause and that distinction matters in
// the History panel:
//
//   - errRefreshTokenRevoked: the IdP was called and returned RFC 6749
//     §5.2 invalid_grant. Emits TypeRefreshFailedRevoked.
//   - errRefreshExpired: the IdP-disclosed refresh deadline arrived;
//     the platform skipped the refresh call entirely. Emits
//     TypeRefreshSkippedExpired so the row does not falsely claim an
//     IdP rejection.
//   - errNoRefreshToken: no refresh token was persisted; nothing to
//     exchange. Emits TypeRefreshSkippedNoToken.
//
// The trailing TypeTokenDeletedRevoked event is emitted in all three
// branches because the end state is the same: the row is gone and the
// operator must re-authorize.
func (s *Source) handleRevoked(ctx context.Context, persisted *PersistedToken, refreshErr error) {
	reason := classifyRevokedReason(refreshErr)
	idpHost := urlHost(s.cfg.TokenURL)
	s.emitRevokedLeadEvent(ctx, persisted, refreshErr, reason)
	if delErr := s.store.Delete(ctx, s.key); delErr != nil {
		slog.Warn("connoauth: delete revoked token row failed",
			logKeyKind, s.key.Kind, logKeyName, s.key.Name, logKeyError, delErr)
		return
	}
	// Log AFTER the delete succeeded so the "row deleted" claim
	// is factually true. The prior ordering emitted the INFO line
	// before attempting the delete, which produced a misleading
	// audit trail when the delete itself failed.
	slog.Info("connoauth: connection token row deleted",
		logKeyKind, s.key.Kind, logKeyName, s.key.Name,
		"reason", reason, logKeyTokenURLHost, idpHost)
	s.events.TokenDeletedRevoked(ctx, s.key.Kind, s.key.Name, s.actor, s.cfg.TokenURL, reason)
}

// emitRevokedLeadEvent emits the first event of the revocation pair,
// choosing the type that accurately describes how the platform reached
// the verdict. Extracted from handleRevoked so the dispatch is
// testable in isolation and so handleRevoked stays under the gocognit
// ceiling.
func (s *Source) emitRevokedLeadEvent(ctx context.Context, persisted *PersistedToken, refreshErr error, reason string) {
	switch {
	case errors.Is(refreshErr, errRefreshExpired):
		s.events.RefreshSkippedExpired(ctx, s.key.Kind, s.key.Name, s.actor, s.cfg.TokenURL)
	case errors.Is(refreshErr, errNoRefreshToken):
		s.events.RefreshSkippedNoToken(ctx, s.key.Kind, s.key.Name, s.actor, s.cfg.TokenURL)
	default:
		s.events.RefreshFailedRevoked(ctx, s.key.Kind, s.key.Name, s.actor, s.cfg.TokenURL,
			authevents.RefreshDetail{
				BeforeExpiresAt:        persisted.ExpiresAt,
				BeforeRefreshExpiresAt: persisted.RefreshExpiresAt,
				IDPErrorCode:           reason,
			})
	}
}

// classifyRevokedReason maps the sentinel errors used internally to
// the short, machine-readable strings recorded in event details. The
// strings are stable across releases — the History panel and any SIEM
// dashboards consume them.
//
// For errRefreshTokenRevoked we look through the wrap chain for an
// *oauth2.RetrieveError and surface the RFC 6749 `error` field
// directly (e.g. "invalid_client", "invalid_grant", "unauthorized_client").
// That way the History row distinguishes "refresh_token revoked"
// from "client_secret no longer valid": operationally different
// remediations.
func classifyRevokedReason(err error) string {
	switch {
	case errors.Is(err, errRefreshTokenRevoked):
		if code := idpErrorCodeOf(err); code != "" {
			return code
		}
		return "invalid_grant"
	case errors.Is(err, errNoRefreshToken):
		return "no_refresh_token"
	case errors.Is(err, errRefreshExpired):
		return "refresh_expired"
	}
	return "revoked"
}

// accessTokenStillValid reports whether the cached access token can
// be reused for at least expiryBuffer more time. Tokens at or past
// the expiry deadline (or with a zero ExpiresAt) are treated as
// invalid and trigger a refresh.
func accessTokenStillValid(p *PersistedToken) bool {
	if p == nil || p.AccessToken == "" {
		return false
	}
	if p.ExpiresAt.IsZero() {
		return false
	}
	return time.Until(p.ExpiresAt) > expiryBuffer
}

// Status returns a snapshot of the current persisted state, for the
// admin status endpoint. Reads the row from the store; transient
// store errors are surfaced via LastError so operators see "DB
// unreachable" rather than a misleading "Connect needed" prompt.
//
// Status does NOT trigger a refresh — the admin UI calls Status
// every few seconds (or on demand) and a refresh-per-status-call
// would generate IdP-side load with no benefit. Refresh happens
// lazily on the next Token() call.
func (s *Source) Status(ctx context.Context) OAuthStatus {
	persisted, err := s.store.Get(ctx, s.key)
	if err != nil {
		if errors.Is(err, ErrTokenNotFound) {
			return OAuthStatus{
				Configured:  true,
				NeedsReauth: true,
				TokenURL:    s.cfg.TokenURL,
				Scope:       strings.Join(s.cfg.Scopes, scopeSep),
				Grant:       s.cfg.Grant,
			}
		}
		return OAuthStatus{
			Configured: true,
			LastError:  "load token: " + err.Error(),
			TokenURL:   s.cfg.TokenURL,
			Scope:      strings.Join(s.cfg.Scopes, scopeSep),
			Grant:      s.cfg.Grant,
		}
	}
	return statusFromPersisted(persisted, s.cfg)
}

// statusFromPersisted maps a non-nil PersistedToken into an
// OAuthStatus. Extracted from Status() so the assembly logic is
// covered by a focused unit test that doesn't need a Store.
func statusFromPersisted(p *PersistedToken, cfg Config) OAuthStatus {
	scope := p.Scope
	if scope == "" {
		scope = strings.Join(cfg.Scopes, scopeSep)
	}
	st := OAuthStatus{
		Configured:       true,
		TokenAcquired:    p.AccessToken != "",
		ExpiresAt:        p.ExpiresAt,
		LastRefreshedAt:  p.UpdatedAt,
		HasRefreshToken:  p.RefreshToken != "",
		RefreshExpiresAt: p.RefreshExpiresAt,
		TokenURL:         cfg.TokenURL,
		Scope:            scope,
		AuthenticatedBy:  p.AuthenticatedBy,
		AuthenticatedAt:  p.AuthenticatedAt,
		Grant:            cfg.Grant,
	}
	// NeedsReauth when the refresh path has nothing to work with OR
	// when the IdP-disclosed refresh deadline has passed. Surfaced
	// proactively so the UI prompts BEFORE the next tool call fails.
	if p.RefreshToken == "" && p.AccessToken == "" {
		st.NeedsReauth = true
	}
	if !p.RefreshExpiresAt.IsZero() && time.Now().After(p.RefreshExpiresAt) {
		st.NeedsReauth = true
	}
	return st
}

// Reacquire forces a refresh-token exchange even when the cached
// token is still valid. Used by the admin "Reacquire" button to test
// the refresh path on demand. authorization_code grants cannot
// re-run the full browser flow without operator interaction. That
// path is the regular Connect button instead.
//
// Holds the same distributed refresh lock as Token so a manual
// reacquire cannot race the background refresher (or another replica)
// into a rotation conflict.
func (s *Source) Reacquire(ctx context.Context) error {
	unlock, err := s.store.Lock(ctx, s.key)
	if err != nil {
		return fmt.Errorf("connoauth: acquire refresh lock: %w", err)
	}
	defer unlock()
	persisted, err := s.store.Get(ctx, s.key)
	if err != nil {
		if errors.Is(err, ErrTokenNotFound) {
			return ErrNeedsReauth
		}
		return fmt.Errorf("connoauth: load token: %w", err)
	}
	_, refreshErr := s.refresh(ctx, persisted)
	if refreshErr != nil {
		if isRevokedRefresh(refreshErr) {
			s.handleRevoked(ctx, persisted, refreshErr)
			return ErrNeedsReauth
		}
		return refreshErr
	}
	return nil
}

// refresh exchanges the persisted refresh_token for a fresh access
// token at the IdP, and persists the result. The IdP MAY rotate the
// refresh_token (RFC 6749 §6 allows this); when rotated, the new
// refresh_token is persisted. When the IdP echoes the existing
// refresh_token or omits the field, the prior refresh_token is
// preserved unchanged (RFC 6749 §6 "still valid" semantics).
//
// This is the bug-#3 fix: the prior MCP custom state machine left a
// surface where a rotated refresh_token could land in the in-memory
// state but never reach the store. By delegating to
// golang.org/x/oauth2 and explicitly persisting the result on every
// refresh, the rotation is durable across process restarts.
func (s *Source) refresh(ctx context.Context, persisted *PersistedToken) (*oauth2.Token, error) {
	if persisted.RefreshToken == "" {
		// No event emission here — the caller's handleRevoked path
		// emits the TypeRefreshSkippedNoToken lead and the
		// TypeTokenDeletedRevoked trail for this sentinel. Emitting
		// from refresh() too would produce three rows for a single
		// incident.
		return nil, errNoRefreshToken
	}
	if !persisted.RefreshExpiresAt.IsZero() && time.Now().After(persisted.RefreshExpiresAt) {
		// Same rationale — handleRevoked emits TypeRefreshSkippedExpired
		// + TypeTokenDeletedRevoked. No additional event here.
		return nil, errRefreshExpired
	}
	refreshCtx := context.WithValue(ctx, oauth2.HTTPClient, s.client)
	cfg := s.cfg.oauth2Config()
	// Force the library to refresh: pass a token with Expiry in the
	// past so oauth2.Config.TokenSource always hits the IdP rather
	// than returning the cached value. This makes the call uniform
	// whether the caller is Token() (which only enters refresh()
	// after detecting expiry) or Reacquire() (which forces refresh
	// even on a still-valid token).
	start := time.Now()
	src := cfg.TokenSource(refreshCtx, &oauth2.Token{
		AccessToken:  persisted.AccessToken,
		RefreshToken: persisted.RefreshToken,
		Expiry:       time.Now().Add(-time.Hour),
	})
	fresh, err := src.Token()
	durationMS := time.Since(start).Milliseconds()
	if err != nil {
		classified := classifyRefreshError(err)
		if !isRevokedRefresh(classified) {
			// Transient: record it but do not delete the row. The
			// revoked branch is handled by handleRevoked() in the
			// caller, which also emits its own event. IDPErrorCode
			// surfaces the RFC 6749 `error` field (e.g.
			// "server_error", "temporarily_unavailable") when the IdP
			// did respond, so the History row shows what the IdP
			// said instead of an empty detail box.
			s.events.RefreshFailedTransient(ctx, s.key.Kind, s.key.Name, s.actor, s.cfg.TokenURL,
				authevents.RefreshDetail{
					BeforeExpiresAt:        persisted.ExpiresAt,
					BeforeRefreshExpiresAt: persisted.RefreshExpiresAt,
					DurationMS:             durationMS,
					// sanitizeOAuthErrorField bounds length and
					// strips URLs / control characters: same
					// rationale as the terminal-classify path.
					IDPErrorCode: sanitizeOAuthErrorField(idpErrorCodeOf(err)),
				})
		}
		return nil, classified
	}
	rotated := fresh.RefreshToken != "" && fresh.RefreshToken != persisted.RefreshToken
	if persistErr := s.persistRefreshed(ctx, persisted, fresh); persistErr != nil {
		if rotated {
			// Rotation-persistence-failure is permanent credential
			// loss for one-time-use-rotation IdPs (Microsoft Entra
			// with rotation enforced, rotation-enabled Keycloak,
			// etc.). Emit at ERROR so operators see the page; emit
			// the authevent so the History panel shows the spot
			// where the connection died.
			slog.Error("connoauth: rotated refresh token issued but persist failed — connection may be unrecoverable",
				logKeyKind, s.key.Kind, logKeyName, s.key.Name, logKeyError, persistErr)
			s.events.RotationPersistenceFailed(ctx, s.key.Kind, s.key.Name, s.actor, s.cfg.TokenURL,
				persistErr.Error())
		} else {
			// Non-rotation persist failure: in-memory token works
			// for this turn; next refresh re-persists. Warn so
			// operators can spot DB issues.
			slog.Warn("connoauth: persist refreshed token failed (in-memory token still valid)",
				logKeyKind, s.key.Kind, logKeyName, s.key.Name, logKeyError, persistErr)
		}
	} else {
		s.events.RefreshSucceeded(ctx, s.key.Kind, s.key.Name, s.actor, s.cfg.TokenURL,
			authevents.RefreshDetail{
				BeforeExpiresAt:        persisted.ExpiresAt,
				BeforeRefreshExpiresAt: persisted.RefreshExpiresAt,
				AfterExpiresAt:         fresh.Expiry,
				AfterRefreshExpiresAt:  refreshDeadlineFromToken(fresh, time.Now()),
				RotatedRefresh:         rotated,
				DurationMS:             durationMS,
			})
	}
	return fresh, nil
}

// persistRefreshed writes the rotated token set back to the store,
// preserving the prior refresh_token when the IdP omitted one and
// recomputing RefreshExpiresAt from the IdP's response (or clearing
// it on rotation-without-deadline). Extracted from refresh() so the
// RFC 6749 §6 semantics are testable in isolation.
func (s *Source) persistRefreshed(ctx context.Context, prior *PersistedToken, fresh *oauth2.Token) error {
	updated := *prior
	updated.AccessToken = fresh.AccessToken
	// IdP omitted refresh_token → RFC 6749 §6: prior one is still
	// valid; preserve it AND its deadline (no signal to revise either).
	// Rotated refresh token → persist the new value and recompute the
	// deadline from the IdP's hint (the prior deadline belonged to
	// the prior refresh_token's lifecycle).
	if fresh.RefreshToken != "" {
		updated.RefreshToken = fresh.RefreshToken
		updated.RefreshExpiresAt = refreshDeadlineFromToken(fresh, time.Now())
	}
	updated.ExpiresAt = fresh.Expiry
	updated.UpdatedAt = time.Now().UTC()
	if err := s.store.Set(ctx, updated); err != nil {
		return fmt.Errorf("connoauth: persist refreshed token: %w", err)
	}
	return nil
}

// refreshDeadlineFromToken reads refresh_expires_in from the
// oauth2.Token's Extra fields and returns the absolute deadline.
// Zero when the IdP did not disclose one — callers must not
// interpret zero as "never expires" (it means "unknown").
//
// golang.org/x/oauth2 stores extension fields in tok.Extra and
// JSON-decoded numerics arrive as float64; the cast handles both
// shapes defensively.
func refreshDeadlineFromToken(tok *oauth2.Token, now time.Time) time.Time {
	v := tok.Extra("refresh_expires_in")
	if v == nil {
		return time.Time{}
	}
	var secs int64
	switch n := v.(type) {
	case float64:
		secs = int64(n)
	case int64:
		secs = n
	case int:
		secs = int64(n)
	default:
		return time.Time{}
	}
	if secs <= 0 {
		return time.Time{}
	}
	return now.Add(time.Duration(secs) * time.Second)
}

// terminalRefreshError is the wrapped sentinel returned by
// classifyRefreshError for terminal IdP rejections. Carries the
// sanitized operator-facing message AND the RFC 6749 error code
// the IdP returned, so downstream callers (classifyRevokedReason,
// idpErrorCodeOf, History event details) can show the specific
// code instead of a generic "invalid_grant" stand-in.
//
// `code` is always run through sanitizeOAuthErrorField at
// construction time (see classifyRefreshError) so a chatty or
// hostile IdP cannot inject a 100KB blob, URL-shaped content, or
// control characters into the auth_events JSONB column or the UI
// tooltip surface.
type terminalRefreshError struct {
	msg  string
	code string
}

func (e *terminalRefreshError) Error() string { return e.msg }

// Unwrap reports errRefreshTokenRevoked so errors.Is on that
// sentinel still works.
func (*terminalRefreshError) Unwrap() error { return errRefreshTokenRevoked }

// classifyRefreshError distinguishes definitively-terminal IdP
// rejections from transient failures (network drops, 5xx, request
// cancellation). The terminal set (see errors.go errRefreshTokenRevoked
// docs) wraps with that sentinel so callers can detect it via
// errors.Is and trigger handleRevoked. Everything else passes through
// tokenFetchError so IdP response bodies and embedded URL credentials
// cannot leak into model output or logs.
func classifyRefreshError(err error) error {
	var retrieve *oauth2.RetrieveError
	if errors.As(err, &retrieve) && isTerminalIDPRejection(retrieve) {
		return &terminalRefreshError{
			msg: fmt.Sprintf("connoauth: refresh rejected by IdP: %s (%s)",
				tokenFetchError(err).Error(), errRefreshTokenRevoked.Error()),
			// Sanitize at the boundary: this code lands in the
			// auth_events.detail JSON blob, the bulk-health API
			// response, and the UI badge tooltip. A hostile or
			// chatty IdP could otherwise return a multi-KB blob
			// with embedded URLs or control characters that would
			// inflate storage and pollute logs.
			code: sanitizeOAuthErrorField(retrieve.ErrorCode),
		}
	}
	return tokenFetchError(err)
}

// terminalRefreshErrorCodes is the set of RFC 6749 §5.2 error codes
// at HTTP 400 that an automated retry cannot recover. The classifier
// treats them all as revoked so the connection moves to
// needs_reauth instead of silent-retry-forever. HTTP 401 is treated
// as terminal regardless of error code: by spec, 401 means client
// authentication failed (same operator action as invalid_client).
var terminalRefreshErrorCodes = map[string]struct{}{
	"invalid_grant":          {},
	"invalid_client":         {},
	"unauthorized_client":    {},
	"unsupported_grant_type": {},
}

// isTerminalIDPRejection reports whether an *oauth2.RetrieveError
// represents a state the operator must intervene to clear. Extracted
// so the predicate is testable in isolation and so the set of
// terminal codes is maintained in one place.
func isTerminalIDPRejection(re *oauth2.RetrieveError) bool {
	if re.Response == nil {
		return false
	}
	if re.Response.StatusCode == http.StatusUnauthorized {
		return true
	}
	if re.Response.StatusCode != http.StatusBadRequest {
		return false
	}
	_, ok := terminalRefreshErrorCodes[re.ErrorCode]
	return ok
}

// idpErrorCodeOf extracts the RFC 6749 `error` field from an OAuth
// library error, if present. Empty when the error is not a
// RetrieveError (network drop, ctx cancel, etc.) so the caller can
// distinguish "IdP said something" from "never reached the IdP."
//
// Looks through terminalRefreshError first (the post-classify path)
// then through *oauth2.RetrieveError (the pre-classify path).
func idpErrorCodeOf(err error) string {
	var term *terminalRefreshError
	if errors.As(err, &term) {
		return term.code
	}
	var re *oauth2.RetrieveError
	if errors.As(err, &re) {
		return re.ErrorCode
	}
	return ""
}

// isRevokedRefresh reports whether the error is one of the
// definitively-revoked sentinels. Used by Token() / Reacquire() to
// decide whether to delete the persisted row and surface
// ErrNeedsReauth.
func isRevokedRefresh(err error) bool {
	return errors.Is(err, errRefreshTokenRevoked) ||
		errors.Is(err, errNoRefreshToken) ||
		errors.Is(err, errRefreshExpired)
}

// tokenFetchError sanitizes errors from oauth2.TokenSource.Token().
// The library's *oauth2.RetrieveError includes the IdP response body
// (which can carry sensitive material) and the request URL (which
// can carry credentials in the userinfo). Rebuild the message
// keeping the structured RFC 6749 error fields (error, error_description)
// which the oauth2 library already parsed out of the response body
// and stored on the RetrieveError. These two fields are operator-
// facing diagnostic surface by spec; preserving them is the
// difference between "status=400" (useless) and
// "status=400 error=invalid_grant error_description=Token is not active"
// (immediately actionable). The full raw Response.Body is still
// dropped.
func tokenFetchError(err error) error {
	var re *oauth2.RetrieveError
	if errors.As(err, &re) {
		return retrieveErrorMessage(re)
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		parsed, perr := url.Parse(ue.URL)
		if perr != nil {
			return fmt.Errorf("connoauth: token fetch %s: %w", ue.Op, ue.Err)
		}
		parsed.RawQuery = ""
		parsed.User = nil
		return fmt.Errorf("connoauth: token fetch %s %q: %w", ue.Op, parsed.String(), ue.Err)
	}
	// Fallback: redact anything that looks URL-shaped just in case a
	// future library version wraps in a different error type.
	msg := err.Error()
	if strings.Contains(msg, "://") {
		return errors.New("connoauth: token fetch failed (details redacted)")
	}
	return fmt.Errorf("connoauth: token fetch failed: %s", msg)
}

// retrieveErrorMessage assembles the sanitized message for an
// oauth2.RetrieveError. Includes the HTTP status, the RFC 6749
// `error` code, and a length-bounded `error_description`. Drops
// any other body content. Returns "connoauth: token fetch failed"
// when no fields are populated so the caller never sees an empty
// message.
func retrieveErrorMessage(re *oauth2.RetrieveError) error {
	parts := make([]string, 0, 3)
	if re.Response != nil {
		parts = append(parts, fmt.Sprintf("status=%d", re.Response.StatusCode))
	}
	if re.ErrorCode != "" {
		parts = append(parts, fmt.Sprintf("error=%s", sanitizeOAuthErrorField(re.ErrorCode)))
	}
	if re.ErrorDescription != "" {
		parts = append(parts, fmt.Sprintf("error_description=%s",
			sanitizeOAuthErrorField(re.ErrorDescription)))
	}
	if len(parts) == 0 {
		return errors.New("connoauth: token fetch failed")
	}
	return fmt.Errorf("connoauth: token fetch failed: %s", strings.Join(parts, scopeSep))
}

// sanitizeOAuthErrorField bounds the length of an RFC 6749 error
// field and replaces any URL-looking substrings, CR/LF, and other
// control characters so a hostile or chatty IdP cannot inject log
// noise or leak embedded credentials through the diagnostic string.
// Length cap matches typical operator-readable description ceilings.
func sanitizeOAuthErrorField(s string) string {
	const maxLen = 200
	if strings.Contains(s, "://") {
		return "(redacted: URL-shaped content)"
	}
	cleaned := strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return ' '
		}
		return r
	}, s)
	cleaned = strings.TrimSpace(cleaned)
	if len(cleaned) > maxLen {
		cleaned = cleaned[:maxLen] + "..."
	}
	return cleaned
}
