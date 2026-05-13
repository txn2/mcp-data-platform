package authevents

import (
	"encoding/json"
	"time"
)

// Type names the event category. Closed set — callers must use the
// declared constants; arbitrary strings are rejected by the store.
// Ordering of constants is intentional: declaration order matches the
// typical timeline of a connection's lifecycle, which makes diffs
// against this file readable when new events are added.
type Type string

const (
	// TypeConnectStarted records the operator hitting Connect on the
	// portal. Emitted by the admin oauth-start handler immediately
	// before issuing the PKCE state. Paired with a later
	// TypeConnectCompleted on success, or no follow-up event when the
	// operator abandons the browser flow.
	TypeConnectStarted Type = "connect_started"
	// TypeConnectCompleted records a successful authorization_code
	// exchange (the IdP returned a token pair and the row was
	// persisted). Emitted by the admin callback handler.
	TypeConnectCompleted Type = "connect_completed"
	// TypeRefreshSucceeded records a successful refresh-token grant.
	// Emitted by the background refresher and by the toolkit-side
	// refresh paths (gateway/oauth.go, apigateway/auth.go) on success.
	TypeRefreshSucceeded Type = "refresh_succeeded"
	// TypeRefreshFailedTransient records a network/5xx/ctx-cancel
	// failure during refresh. The row is NOT deleted; the next attempt
	// can succeed. Emitted at WARN level by both refresh paths.
	TypeRefreshFailedTransient Type = "refresh_failed_transient"
	// TypeRefreshFailedRevoked records a definitive refresh-token
	// rejection (e.g. RFC 6749 §5.2 invalid_grant @ HTTP 400, local
	// expiry of the IdP-disclosed refresh deadline, or absence of a
	// refresh token in the persisted row). Paired with a subsequent
	// TypeTokenDeletedRevoked event.
	TypeRefreshFailedRevoked Type = "refresh_failed_revoked"
	// TypeRefreshSkippedNoToken records a refresh attempt that found
	// no refresh_token persisted. Reserved for future use by callers
	// that want to record a "checked but skipped" signal WITHOUT also
	// invoking the revoked-cleanup path. The current refresh paths
	// in connoauth/source.go and apigateway/auth.go DO NOT emit this
	// event — they fold the no-refresh-token case into the revoked
	// cascade via IDPErrorCode="no_refresh_token" on
	// TypeRefreshFailedRevoked so a single incident produces a
	// single (RefreshFailedRevoked, TokenDeletedRevoked) event pair
	// rather than three rows in the History panel.
	TypeRefreshSkippedNoToken Type = "refresh_skipped_no_token"
	// TypeRefreshSkippedExpired records a refresh attempt aborted
	// before any network call because the IdP-disclosed refresh
	// deadline had already passed. Same usage discipline as
	// TypeRefreshSkippedNoToken: the cause is captured as
	// IDPErrorCode="refresh_expired" on RefreshFailedRevoked rather
	// than emitted as a separate row.
	TypeRefreshSkippedExpired Type = "refresh_skipped_expired"
	// TypeRefreshRotationPersistenceFailed records the most serious
	// failure class: the IdP issued a rotated token pair (the old
	// refresh token is therefore invalid the instant the new one is
	// minted) but persisting the new pair to the store failed. The
	// connection is now permanently broken until reconnect. Emitted
	// at ERROR level.
	TypeRefreshRotationPersistenceFailed Type = "refresh_rotation_persistence_failed"
	// TypeTokenDeletedRevoked records the automatic deletion of a
	// token row following a TypeRefreshFailedRevoked. Distinct from
	// TypeTokenDeletedAdmin so operator-initiated and IdP-initiated
	// deletions are visually distinguishable in the History panel.
	TypeTokenDeletedRevoked Type = "token_deleted_revoked"
	// TypeTokenDeletedAdmin records an operator deleting the
	// connection (or otherwise clearing its token row) through the
	// admin API. Emitted by the connection delete handler.
	TypeTokenDeletedAdmin Type = "token_deleted_admin"
)

// IsValid reports whether t is one of the declared event types. The
// store rejects events with unknown types so a misconfigured caller
// cannot smuggle arbitrary strings into the history.
func (t Type) IsValid() bool {
	switch t {
	case TypeConnectStarted,
		TypeConnectCompleted,
		TypeRefreshSucceeded,
		TypeRefreshFailedTransient,
		TypeRefreshFailedRevoked,
		TypeRefreshSkippedNoToken,
		TypeRefreshSkippedExpired,
		TypeRefreshRotationPersistenceFailed,
		TypeTokenDeletedRevoked,
		TypeTokenDeletedAdmin:
		return true
	}
	return false
}

// Actor identifies who/what initiated the event. Operator-driven
// events carry the operator's email (or apikey:name); background
// refresher events carry the synthetic SystemBackgroundRefresh
// constant. Distinguishing the two answers the operator question:
// did a human click this, or did the platform keep things alive on
// its own.
const (
	// SystemBackgroundRefresh is the actor recorded when the
	// connoauth refresher loop (running on a server replica with no
	// associated human) refreshes a token.
	SystemBackgroundRefresh = "system:background-refresh"
	// SystemToolCall is the actor recorded when an outbound toolkit
	// request triggers refresh-on-access-token-expiry. Distinct from
	// SystemBackgroundRefresh so operators can see whether the
	// keepalive is doing its job (frequent background events =
	// healthy) or whether refreshes are only happening reactively
	// (the bug the keepalive was added to fix).
	SystemToolCall = "system:tool-call"
)

// Event is one row in connection_auth_events. Detail is per-Type JSON;
// see the doc on each TypeXxx constant for the shape callers should
// produce.
//
// JSON tags are snake_case to match the TypeScript ConnectionAuthEvent
// interface the portal History panel consumes. Without explicit tags
// Go marshals field names verbatim (ID/OccurredAt/...), the portal's
// `event.event_type` lookup returns undefined, and every row in the
// History panel renders empty.
type Event struct {
	// ID is the server-assigned row UUID. Empty on inserts; populated
	// by the store after the INSERT … RETURNING id.
	ID string `json:"id"`
	// OccurredAt is wall-clock time of the event. Empty on inserts;
	// the store stamps NOW().
	OccurredAt time.Time `json:"occurred_at"`
	// Kind is the connection kind (mcp, api, future).
	Kind string `json:"connection_kind"`
	// Name is the connection name within the kind.
	Name string `json:"connection_name"`
	// Type is one of the declared TypeXxx constants. Required.
	Type Type `json:"event_type"`
	// Actor is who/what initiated the event. See SystemXxx constants
	// for synthetic actors; operator-driven events use the email or
	// apikey:name. Required (empty string is rejected at insert).
	Actor string `json:"actor"`
	// IDPHost is the host portion of the IdP token endpoint. Empty
	// when not relevant to the event (e.g., TypeTokenDeletedAdmin).
	IDPHost string `json:"idp_host,omitempty"`
	// Detail is per-Type JSON. May be nil for events with no extra
	// payload.
	//
	// swaggertype tag tells swag (used by make swagger) to treat the
	// field as a generic object; without it swag can't resolve
	// json.RawMessage and the generation step fails.
	Detail json.RawMessage `json:"detail,omitempty" swaggertype:"object"`
}

// IsValid checks the required fields. Used by the store before insert.
func (e *Event) IsValid() bool {
	return e.Kind != "" && e.Name != "" && e.Type.IsValid() && e.Actor != ""
}
