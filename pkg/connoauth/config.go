package connoauth

import "golang.org/x/oauth2"

// Config carries the per-connection OAuth 2.1 settings required by
// the authorization_code flow. Built by callers (admin handler /
// toolkit Authenticator) from their respective per-kind connection
// config schemas — connoauth itself is kind-agnostic.
//
// SECURITY: Config carries the client secret in memory; do not log
// it. The Source's error returns are sanitized to keep secret
// material out of model/log output (see source.go:tokenFetchError).
type Config struct {
	// AuthorizationURL is the IdP's authorization endpoint (where the
	// operator's browser is sent to sign in). Used only by the admin
	// handler's authorization-URL builder, not by this package's
	// token operations, but kept here so a single Config value carries
	// every field the flow needs.
	AuthorizationURL string
	// TokenURL is the IdP's token endpoint. Used by both the initial
	// code→token exchange and every silent refresh.
	TokenURL string
	// ClientID identifies the platform's registration with the IdP.
	ClientID string
	// ClientSecret is the matching credential. Stored encrypted in
	// connection_instances; decrypted by the connection-store layer
	// before reaching this struct.
	ClientSecret string
	// Scopes is the space-delimited list of OAuth scopes negotiated
	// with the IdP. Operators of Keycloak/Auth0/Okta typically need
	// `offline_access` (or vendor equivalent) for the IdP to issue a
	// refresh token at all.
	Scopes []string
	// EndpointAuthStyle selects how the client credentials reach the
	// token endpoint. Defaults to AuthStyleInHeader (HTTP Basic) per
	// OAuth 2.1's recommended style; AuthStyleInParams sends them in
	// the form body (some legacy IdPs require this).
	EndpointAuthStyle oauth2.AuthStyle
	// Prompt is the optional OIDC `prompt` parameter
	// (RFC OIDC §3.1.2.1). Common values: empty (default), "login",
	// "consent", "select_account". Pure-OAuth providers that don't
	// recognize it should leave this empty so the IdP doesn't reject
	// the authorize request with invalid_request.
	Prompt string
}

// oauth2Config builds the golang.org/x/oauth2 Config the Source uses
// for refresh-token exchanges. Centralized here so every refresh
// uses the same client-secret / scope / auth-style settings the
// initial code exchange used.
func (c Config) oauth2Config() *oauth2.Config {
	return &oauth2.Config{
		ClientID:     c.ClientID,
		ClientSecret: c.ClientSecret,
		Scopes:       c.Scopes,
		Endpoint: oauth2.Endpoint{
			AuthURL:   c.AuthorizationURL,
			TokenURL:  c.TokenURL,
			AuthStyle: c.EndpointAuthStyle,
		},
	}
}
