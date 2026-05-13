package connoauth

import "time"

// Kind constants for the connection_kind column. New connection kinds
// that participate in this shared OAuth flow add a constant here and
// extend the per-kind dispatch in pkg/admin's connection OAuth handler.
const (
	// KindMCP is the MCP gateway toolkit family (composes upstream MCP
	// servers behind one platform-side server).
	KindMCP = "mcp"
	// KindAPI is the HTTP API gateway toolkit family (treats an OpenAPI
	// REST service as a tool surface).
	KindAPI = "api"
)

// Key uniquely identifies one (connection_kind, connection_name)
// row in connection_oauth_tokens. Distinct from a bare string so
// callers can't accidentally pass the wrong identifier — every Store
// method takes a Key.
type Key struct {
	// Kind is one of the KindXxx constants. Empty is invalid; the
	// Store.Get / Set / Delete methods reject zero-value keys with a
	// validation error rather than silently writing to an empty-kind
	// row that would never be read back by a real consumer.
	Kind string
	// Name matches the connection_instances.name column for the same
	// (kind, name) pair. Stable across saves of the same connection.
	Name string
}

// IsValid reports whether the Key is fully populated. Callers should
// validate before mutating the store; the Postgres impl also rejects
// zero values with an error to defend against misuse.
func (k Key) IsValid() bool {
	return k.Kind != "" && k.Name != ""
}

// PersistedToken is the row shape stored in connection_oauth_tokens.
// Tokens are stored encrypted at rest by the platform's FieldEncryptor;
// this struct carries plaintext values across the Store API boundary.
//
// RefreshExpiresAt is optional — populated only when the IdP returned
// refresh_expires_in (Keycloak does; many others do not). Zero means
// the row column is NULL; callers MUST NOT interpret zero as "never
// expires" — it means the IdP did not disclose a deadline.
type PersistedToken struct {
	Key              Key
	AccessToken      string
	RefreshToken     string
	ExpiresAt        time.Time
	RefreshExpiresAt time.Time
	Scope            string
	AuthenticatedBy  string
	AuthenticatedAt  time.Time
	UpdatedAt        time.Time
}

// OAuthStatus is the snapshot exposed via the admin status endpoint.
// All fields are safe to expose to operators (no secret material).
// Mirrors the union of the two prior per-kind status structs so the
// frontend status card renders identically for any kind.
type OAuthStatus struct {
	// Configured indicates the connection is set up for the
	// authorization_code grant. False means the status endpoint was
	// hit for a non-OAuth connection (or one with a different grant)
	// and the UI should hide the OAuth block entirely.
	Configured bool `json:"configured"`
	// TokenAcquired is true when a non-empty access_token sits in the
	// persisted row. False after a revoked-refresh cleanup or before
	// the first Connect.
	TokenAcquired bool `json:"token_acquired"`
	// ExpiresAt is the access-token deadline. Zero when no token has
	// been acquired.
	ExpiresAt time.Time `json:"expires_at,omitzero"`
	// LastRefreshedAt is the persisted row's UpdatedAt — the time of
	// the most recent successful write (initial Connect or silent
	// refresh).
	LastRefreshedAt time.Time `json:"last_refreshed_at,omitzero"`
	// HasRefreshToken is true when a refresh token sits in the row.
	// False for IdPs that don't issue refresh tokens, OR after a
	// revoked-refresh cleanup.
	HasRefreshToken bool `json:"has_refresh_token"`
	// RefreshExpiresAt is the IdP-disclosed refresh-token deadline.
	// Zero when the IdP did not disclose one. The admin UI renders an
	// em dash for zero, not "never".
	RefreshExpiresAt time.Time `json:"refresh_expires_at,omitzero"`
	// LastError is the most recent surfaced failure (transport,
	// IdP-rejected, persistence). Cleared by a subsequent success.
	LastError string `json:"last_error,omitempty"`
	// TokenURL is the IdP's token endpoint, surfaced so the admin
	// status panel can show "auth against https://iam.example.com/..."
	// at a glance. Operator-authored config; safe to expose.
	TokenURL string `json:"token_url,omitempty"`
	// Scope is the space-delimited scope string negotiated with the
	// IdP. Surfaced so operators can verify offline_access is present
	// on Keycloak/Auth0/Okta IdPs.
	Scope string `json:"scope,omitempty"`
	// AuthenticatedBy is the email/id of the operator who completed
	// the browser flow. Empty for never-authorized connections.
	AuthenticatedBy string `json:"authenticated_by,omitempty"`
	// AuthenticatedAt is when the most recent successful Connect
	// completed. Initial-Connect time, not last-refresh time.
	AuthenticatedAt time.Time `json:"authenticated_at,omitzero"`
	// NeedsReauth is true when no access token can be minted without
	// operator interaction. The admin UI surfaces a Connect button
	// when this is true.
	NeedsReauth bool `json:"needs_reauth,omitempty"`
}
