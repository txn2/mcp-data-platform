// Package authevents provides durable audit history for the OAuth
// lifecycle of every connection — connect, refresh, rotation,
// revocation, and admin deletion — keyed on (connection_kind,
// connection_name).
//
// Two failure modes motivated this package:
//
//   - Before authevents, a revoked-refresh deletion left no trail:
//     the row vanished from connection_oauth_tokens and an operator
//     saw "Token: not yet acquired" with no way to distinguish "never
//     connected" from "the IdP rejected our refresh ten minutes ago
//     because the SSO session idled out." Now every delete is paired
//     with a refresh_failed_revoked + token_deleted_revoked event so
//     operators can correlate the symptom to a specific point in time.
//
//   - Rotation-required IdPs (Blackbaud-style one-time-use refresh
//     tokens) silently lose access permanently if a rotated token fails
//     to persist. authevents emits refresh_rotation_persistence_failed
//     at ERROR level the moment the DB write fails, before any
//     subsequent tool call exposes the dead connection.
//
// Event types are a closed set (see eventType constants in event.go);
// callers cannot invent new types at runtime. Detail payloads are
// per-type JSON and must NEVER include access or refresh tokens, IdP
// response bodies, or human-readable error_description strings (those
// vary per IdP and sometimes carry user identifiers — only the RFC 6749
// machine-readable `error` field is recorded).
//
// The package is kind-agnostic: the same table records events for the
// MCP gateway (kind=mcp), the HTTP API gateway (kind=api), and any
// future kind that participates in the unified OAuth flow.
package authevents
