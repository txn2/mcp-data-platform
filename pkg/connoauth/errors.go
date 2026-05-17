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
// definitively rejects a refresh_token grant. Internal sentinel that
// callers detect with errors.Is to distinguish revoked refresh from
// transient failures. Misclassifying a transient failure as revoked
// would permanently invalidate a long-lived refresh token over a
// single flaky-network event.
//
// Wrapped responses are the terminal ones from RFC 6749 §5.2 that an
// automated retry cannot recover. The token row is deleted and the
// operator must reconnect (or, for client-credential failures, fix
// the secret on the connection THEN reconnect):
//
//   - 400 invalid_grant: refresh_token is invalid, expired, revoked,
//     or was issued to another client.
//   - 400 invalid_client: client authentication failed. Common cause
//     is a client_secret rotation on the IdP side; the operator must
//     update the connection's stored secret.
//   - 400 unauthorized_client: this client is not authorized to use
//     the refresh_token grant type at all.
//   - 400 unsupported_grant_type: server does not support refresh.
//   - 401 (any error code): unauthenticated; same operator action as
//     invalid_client.
//
// Network drops, 5xx responses, and request cancellations remain
// transient (no row deletion, no reauth signal) so a flaky upstream
// does not require an operator reconnect.
var errRefreshTokenRevoked = errors.New("connoauth: refresh token rejected by IdP")

// errNoRefreshToken / errRefreshExpired are sentinel locals for
// pre-network checks. Both indicate state that won't recover without
// admin action; Source.Token() folds them into ErrNeedsReauth.
var (
	errNoRefreshToken = errors.New("connoauth: no refresh token persisted")
	errRefreshExpired = errors.New("connoauth: refresh token has expired")
)
