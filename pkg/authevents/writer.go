package authevents

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/url"
	"time"
)

// Writer is the small, opinionated surface most callers want — emit a
// typed event without assembling an Event struct or worrying about a
// missing store. Wraps Store with structured logging and nil-safety so
// the producing code paths can call e.g. w.Refreshed(ctx, key, ...)
// inline without polluting their logic with NullStore checks.
//
// A nil Writer is valid and emits nothing — callers that don't have a
// configured store (memory-only dev, tests) can pass nil and the rest
// of the code path is unaffected.
type Writer struct {
	store Store
	// logger is the per-package logger; defaulting to slog.Default()
	// keeps tests' captured log output the same as production.
	logger *slog.Logger
}

// NewWriter wraps store. Pass logger=nil to use the global default.
func NewWriter(store Store, logger *slog.Logger) *Writer {
	if logger == nil {
		logger = slog.Default()
	}
	return &Writer{store: store, logger: logger}
}

// Emit is the low-level entry point that the typed helpers below
// delegate to. Callers should prefer the typed helpers; Emit exists
// for events whose detail shape doesn't fit one of the helpers
// (currently TypeConnectStarted with arbitrary detail).
//
// Emit returns no error: durability of the audit log is best-effort.
// A persist failure is logged at WARN but does NOT propagate — the
// caller's normal flow (refresh succeeded / row deleted / etc.) must
// not fail because the audit row couldn't be written.
func (w *Writer) Emit(ctx context.Context, ev Event) {
	if w == nil || w.store == nil {
		return
	}
	if err := w.store.Insert(ctx, ev); err != nil {
		w.logger.Warn("authevents: persist failed (in-memory event recorded but row not durable)",
			"event_type", string(ev.Type),
			"kind", ev.Kind, "name", ev.Name,
			"error", err)
	}
}

// idpHostOf trims a URL down to its host. Used by every helper that
// records an event with an associated IdP endpoint. Lives here rather
// than as a separate helper file because it's tiny and the callers
// are all in this file.
func idpHostOf(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return rawURL
	}
	return u.Host
}

// ConnectStarted records the operator initiating the authorization
// flow. tokenURL is recorded as idp_host so the History panel surfaces
// which IdP the operator hit.
func (w *Writer) ConnectStarted(ctx context.Context, kind, name, actor, tokenURL, returnURL string) {
	detail, _ := json.Marshal(map[string]any{
		"return_url": returnURL,
	})
	w.Emit(ctx, Event{
		Kind: kind, Name: name,
		Type:    TypeConnectStarted,
		Actor:   actor,
		IDPHost: idpHostOf(tokenURL),
		Detail:  detail,
	})
}

// ConnectCompletedDetail is the detail payload for ConnectCompleted.
// Captured server-side after the token exchange succeeds.
type ConnectCompletedDetail struct {
	Scope            string    `json:"scope,omitempty"`
	ExpiresAt        time.Time `json:"expires_at,omitzero"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at,omitzero"`
	HasRefreshToken  bool      `json:"has_refresh_token"`
}

// ConnectCompleted records a successful authorization_code exchange.
func (w *Writer) ConnectCompleted(ctx context.Context, kind, name, actor, tokenURL string, d ConnectCompletedDetail) {
	detail, _ := json.Marshal(d)
	w.Emit(ctx, Event{
		Kind: kind, Name: name,
		Type:    TypeConnectCompleted,
		Actor:   actor,
		IDPHost: idpHostOf(tokenURL),
		Detail:  detail,
	})
}

// RefreshDetail is the detail shape for refresh_succeeded /
// refresh_failed_transient / refresh_failed_revoked. Same shape across
// the three so dashboards can diff before/after the same way.
type RefreshDetail struct {
	BeforeExpiresAt        time.Time `json:"before_expires_at,omitzero"`
	BeforeRefreshExpiresAt time.Time `json:"before_refresh_expires_at,omitzero"`
	AfterExpiresAt         time.Time `json:"after_expires_at,omitzero"`
	AfterRefreshExpiresAt  time.Time `json:"after_refresh_expires_at,omitzero"`
	RotatedRefresh         bool      `json:"rotated_refresh,omitempty"`
	DurationMS             int64     `json:"duration_ms,omitempty"`
	// IDPErrorCode is the RFC 6749 `error` field from the IdP's error
	// body (e.g., "invalid_grant"). Empty on success. NEVER carries
	// error_description — that's per-IdP text that can leak user IDs.
	IDPErrorCode string `json:"idp_error_code,omitempty"`
	// ErrorClass distinguishes transient from revoked from
	// rotation-persistence-failure when reading the detail blob in
	// isolation (without the row's Type).
	ErrorClass string `json:"error_class,omitempty"`
}

// RefreshSucceeded records a successful refresh-token grant.
func (w *Writer) RefreshSucceeded(ctx context.Context, kind, name, actor, tokenURL string, d RefreshDetail) {
	detail, _ := json.Marshal(d)
	w.Emit(ctx, Event{
		Kind: kind, Name: name,
		Type:    TypeRefreshSucceeded,
		Actor:   actor,
		IDPHost: idpHostOf(tokenURL),
		Detail:  detail,
	})
}

// RefreshFailedTransient records a transient refresh failure (network
// / 5xx / ctx cancel). The row is NOT deleted; the caller's retry path
// can run.
func (w *Writer) RefreshFailedTransient(ctx context.Context, kind, name, actor, tokenURL string, d RefreshDetail) {
	d.ErrorClass = "transient"
	detail, _ := json.Marshal(d)
	w.Emit(ctx, Event{
		Kind: kind, Name: name,
		Type:    TypeRefreshFailedTransient,
		Actor:   actor,
		IDPHost: idpHostOf(tokenURL),
		Detail:  detail,
	})
}

// RefreshFailedRevoked records a definitive refresh rejection.
// Paired with a subsequent TokenDeletedRevoked.
func (w *Writer) RefreshFailedRevoked(ctx context.Context, kind, name, actor, tokenURL string, d RefreshDetail) {
	d.ErrorClass = "revoked"
	detail, _ := json.Marshal(d)
	w.Emit(ctx, Event{
		Kind: kind, Name: name,
		Type:    TypeRefreshFailedRevoked,
		Actor:   actor,
		IDPHost: idpHostOf(tokenURL),
		Detail:  detail,
	})
}

// RefreshSkippedNoToken records a refresh attempt with no
// refresh_token persisted.
func (w *Writer) RefreshSkippedNoToken(ctx context.Context, kind, name, actor, tokenURL string) {
	w.Emit(ctx, Event{
		Kind: kind, Name: name,
		Type:    TypeRefreshSkippedNoToken,
		Actor:   actor,
		IDPHost: idpHostOf(tokenURL),
	})
}

// RefreshSkippedExpired records a refresh attempt aborted because the
// IdP-disclosed refresh deadline had already passed.
func (w *Writer) RefreshSkippedExpired(ctx context.Context, kind, name, actor, tokenURL string) {
	w.Emit(ctx, Event{
		Kind: kind, Name: name,
		Type:    TypeRefreshSkippedExpired,
		Actor:   actor,
		IDPHost: idpHostOf(tokenURL),
	})
}

// RotationPersistenceFailed records the most serious failure class:
// the IdP issued a rotated token pair (old refresh is now invalid) but
// persisting the new pair failed. The caller MUST also emit an
// ERROR-level slog line so operators see the page.
func (w *Writer) RotationPersistenceFailed(ctx context.Context, kind, name, actor, tokenURL, persistError string) {
	detail, _ := json.Marshal(map[string]string{
		"persist_error": persistError,
	})
	w.Emit(ctx, Event{
		Kind: kind, Name: name,
		Type:    TypeRefreshRotationPersistenceFailed,
		Actor:   actor,
		IDPHost: idpHostOf(tokenURL),
		Detail:  detail,
	})
}

// TokenDeletedRevoked records the auto-deletion of a token row after
// a revoked-refresh signal from the IdP.
func (w *Writer) TokenDeletedRevoked(ctx context.Context, kind, name, actor, tokenURL, reason string) {
	detail, _ := json.Marshal(map[string]string{"reason": reason})
	w.Emit(ctx, Event{
		Kind: kind, Name: name,
		Type:    TypeTokenDeletedRevoked,
		Actor:   actor,
		IDPHost: idpHostOf(tokenURL),
		Detail:  detail,
	})
}

// TokenDeletedAdmin records an operator deleting the connection or
// otherwise clearing its token row.
func (w *Writer) TokenDeletedAdmin(ctx context.Context, kind, name, actor string) {
	w.Emit(ctx, Event{
		Kind: kind, Name: name,
		Type:  TypeTokenDeletedAdmin,
		Actor: actor,
	})
}
