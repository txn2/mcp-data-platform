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

// handleRevoked is the shared revoked-refresh cleanup used by Token()
// and Reacquire(). The row is deleted (a dead credential must not be
// replayed across process restarts) AND the deletion is observable:
// an INFO log line replaces the previously silent `_ = Delete(...)`,
// and an authevents row pair (refresh_failed_revoked +
// token_deleted_revoked) lands in the History panel so operators see
// the why and when on the connection's status card.
func (s *Source) handleRevoked(ctx context.Context, persisted *PersistedToken, refreshErr error) {
	reason := classifyRevokedReason(refreshErr)
	idpHost := urlHost(s.cfg.TokenURL)
	s.events.RefreshFailedRevoked(ctx, s.key.Kind, s.key.Name, s.actor, s.cfg.TokenURL,
		authevents.RefreshDetail{
			BeforeExpiresAt:        persisted.ExpiresAt,
			BeforeRefreshExpiresAt: persisted.RefreshExpiresAt,
			IDPErrorCode:           reason,
		})
	if delErr := s.store.Delete(ctx, s.key); delErr != nil {
		slog.Warn("connoauth: delete revoked token row failed",
			logKeyKind, s.key.Kind, logKeyName, s.key.Name, logKeyError, delErr)
		return
	}
	// Log AFTER the delete succeeded so the "row deleted" claim
	// is factually true. The prior ordering emitted the INFO line
	// before attempting the delete, which produced a misleading
	// audit trail when the delete itself failed.
	slog.Info("connoauth: connection token row deleted: refresh rejected by IdP",
		logKeyKind, s.key.Kind, logKeyName, s.key.Name,
		"reason", reason, logKeyTokenURLHost, idpHost)
	s.events.TokenDeletedRevoked(ctx, s.key.Kind, s.key.Name, s.actor, s.cfg.TokenURL, reason)
}

// classifyRevokedReason maps the sentinel errors used internally to
// the short, machine-readable strings recorded in event details. The
// strings are stable across releases — the History panel and any SIEM
// dashboards consume them.
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
				Scope:       strings.Join(s.cfg.Scopes, " "),
			}
		}
		return OAuthStatus{
			Configured: true,
			LastError:  "load token: " + err.Error(),
			TokenURL:   s.cfg.TokenURL,
			Scope:      strings.Join(s.cfg.Scopes, " "),
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
		scope = strings.Join(cfg.Scopes, " ")
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
// re-run the full browser flow without operator interaction — that
// path is the regular Connect button instead.
func (s *Source) Reacquire(ctx context.Context) error {
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
		// No event emission here. Caller treats this sentinel as
		// "revoked" via isRevokedRefresh and calls handleRevoked,
		// which records the cause as IDPErrorCode="no_refresh_token"
		// in the RefreshFailedRevoked + TokenDeletedRevoked pair.
		// Emitting RefreshSkippedNoToken in addition would make the
		// History panel show three events for a single incident,
		// contradicting the "distinct from RefreshFailedRevoked"
		// contract on the Skipped types.
		return nil, errNoRefreshToken
	}
	if !persisted.RefreshExpiresAt.IsZero() && time.Now().After(persisted.RefreshExpiresAt) {
		// Same rationale: handleRevoked emits the cause via
		// IDPErrorCode="refresh_expired"; no additional event here.
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
			// caller, which also emits its own event.
			s.events.RefreshFailedTransient(ctx, s.key.Kind, s.key.Name, s.actor, s.cfg.TokenURL,
				authevents.RefreshDetail{
					BeforeExpiresAt:        persisted.ExpiresAt,
					BeforeRefreshExpiresAt: persisted.RefreshExpiresAt,
					DurationMS:             durationMS,
				})
		}
		return nil, classified
	}
	rotated := fresh.RefreshToken != "" && fresh.RefreshToken != persisted.RefreshToken
	if persistErr := s.persistRefreshed(ctx, persisted, fresh); persistErr != nil {
		if rotated {
			// Rotation-persistence-failure is permanent credential
			// loss for one-time-use-rotation IdPs (Blackbaud,
			// rotation-enabled Keycloak, etc.). Emit at ERROR so
			// operators see the page; emit the authevent so the
			// History panel shows the spot where the connection
			// died.
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

// classifyRefreshError distinguishes a definitively-revoked refresh
// token (RFC 6749 §5.2 invalid_grant at HTTP 400) from transient
// failures (network drops, 5xx, request cancellation). Wraps the
// revoked case with errRefreshTokenRevoked so callers can detect it
// via errors.Is; scrubs other errors via tokenFetchError so IdP
// response bodies and embedded URL credentials cannot leak into
// model output or logs.
func classifyRefreshError(err error) error {
	var retrieve *oauth2.RetrieveError
	if errors.As(err, &retrieve) &&
		retrieve.Response != nil &&
		retrieve.Response.StatusCode == http.StatusBadRequest &&
		retrieve.ErrorCode == "invalid_grant" {
		return fmt.Errorf("connoauth: refresh rejected by IdP: %s (%w)",
			tokenFetchError(err).Error(), errRefreshTokenRevoked)
	}
	return tokenFetchError(err)
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
// keeping only non-sensitive pieces.
func tokenFetchError(err error) error {
	var re *oauth2.RetrieveError
	if errors.As(err, &re) {
		if re.Response != nil {
			return fmt.Errorf("connoauth: token fetch failed: status=%d", re.Response.StatusCode)
		}
		return errors.New("connoauth: token fetch failed")
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
