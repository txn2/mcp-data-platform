package connoauth

import "errors"

// ErrTokenNotFound is returned by Store.Get when no token row exists
// for the supplied (kind, name). Callers treat this as "needs
// reauthentication" and surface a Connect button in the admin UI.
var ErrTokenNotFound = errors.New("connoauth: token not found")

// ErrNeedsReauth is the structured signal that no access token can be
// minted without operator interaction. Produced when:
//
//   - No refresh token is persisted (initial Connect required, or a
//     prior revoked-refresh cleared the row).
//   - The IdP-disclosed refresh deadline has passed.
//   - The IdP rejected the most recent refresh attempt with RFC 6749
//     §5.2 invalid_grant at HTTP 400 (revoked / expired refresh).
//
// Transient failures (network drops, 5xx, request cancellation) DO
// NOT produce this error — they surface as transport errors so the
// caller can retry without forcing the operator to reconnect.
var ErrNeedsReauth = errors.New("connoauth: connection needs admin reconnect")

// errRefreshTokenRevoked wraps the underlying error when the IdP
// definitively rejects a refresh_token grant — RFC 6749 §5.2
// invalid_grant at HTTP 400. Internal sentinel that callers detect
// with errors.Is to distinguish revoked refresh from transient
// failures. Misclassifying a transient failure as revoked would
// permanently invalidate a long-lived refresh token over a single
// flaky-network event.
var errRefreshTokenRevoked = errors.New("connoauth: refresh token rejected by IdP (invalid_grant)")

// errNoRefreshToken / errRefreshExpired are sentinel locals for
// pre-network checks. Both indicate state that won't recover without
// admin action; Source.Token() folds them into ErrNeedsReauth.
var (
	errNoRefreshToken = errors.New("connoauth: no refresh token persisted")
	errRefreshExpired = errors.New("connoauth: refresh token has expired")
)
