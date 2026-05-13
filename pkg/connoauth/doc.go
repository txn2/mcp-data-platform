// Package connoauth provides a single shared implementation of the
// OAuth 2.1 authorization_code flow for outbound connections (MCP
// gateways, HTTP API gateways, future connection kinds). The platform
// composes multiple toolkit families that all need the same flow:
// browser sign-in → callback → encrypted refresh token persisted →
// silent access-token minting on tool calls. Forking that flow per
// kind produced two parallel codepaths with subtly different bugs;
// this package replaces both.
//
// The package owns:
//
//   - Persistent token storage (connection_oauth_tokens), keyed by
//     (connection_kind, connection_name).
//   - Per-request access-token acquisition with silent refresh, via
//     golang.org/x/oauth2. After every successful refresh exchange
//     the package explicitly persists the result — including any
//     rotated refresh token — back to the store, solving the class
//     of bug where a rotated refresh token is not re-persisted and
//     the next process restart replays the dead one.
//   - Initial authorization_code exchange used by the admin OAuth
//     callback handler.
//   - Distinction between revoked refresh (RFC 6749 §5.2
//     invalid_grant at HTTP 400 → admin must reconnect) and transient
//     failures (network, 5xx → retry without deleting the row).
//
// The package does NOT own PKCE state, HTTP routing, or the
// authorization-URL builder — those live in pkg/admin where the
// public callback is registered. The per-kind connection config (auth
// URL, token URL, client id/secret, scopes) is supplied to this
// package by callers via the Config struct.
package connoauth
