package apigateway

import (
	"context"
	"errors"

	"golang.org/x/oauth2"

	"github.com/txn2/mcp-data-platform/pkg/connoauth"
)

// OAuthKindHandler adapts the HTTP API gateway toolkit to the unified
// connoauth flow. The admin layer's unified OAuth handler dispatches
// on the connection_kind path parameter ("api" for HTTP API gateway
// connections) and invokes this implementation for config extraction
// and post-auth side effects.
//
// AfterConnect is a no-op for this kind: the toolkit's Authenticator
// reads the persisted token from the store lazily on every outbound
// request, so once the token is persisted the connection is
// immediately usable without further toolkit-side work.
type OAuthKindHandler struct{}

// NewOAuthKindHandler returns the API gateway adapter. The toolkit
// argument is accepted for symmetry with the MCP gateway adapter but
// is intentionally unused — the API gateway needs no post-auth
// side effect.
func NewOAuthKindHandler(_ *Toolkit) *OAuthKindHandler {
	return &OAuthKindHandler{}
}

// ParseOAuthConfig validates the connection's stored config and maps
// the HTTP API gateway's per-kind OAuth shape into a connoauth.Config.
// Returns an error when the connection is not configured for the
// authorization_code grant — the unified handler maps that to HTTP
// 409 Conflict, matching the prior per-kind handler's response code.
func (*OAuthKindHandler) ParseOAuthConfig(connConfig map[string]any) (connoauth.Config, error) {
	cfg, err := ParseConfig(connConfig)
	if err != nil {
		return connoauth.Config{}, err
	}
	if cfg.AuthMode != AuthModeOAuth2AuthorizationCode {
		return connoauth.Config{}, errors.New("connection is not configured for authorization_code OAuth")
	}
	authStyle := oauth2.AuthStyleInHeader
	if cfg.OAuth2.EndpointAuthStyle == OAuth2AuthStyleParams {
		authStyle = oauth2.AuthStyleInParams
	}
	return connoauth.Config{
		Grant:             "authorization_code",
		AuthorizationURL:  cfg.OAuth2.AuthorizationURL,
		TokenURL:          cfg.OAuth2.TokenURL,
		ClientID:          cfg.OAuth2.ClientID,
		ClientSecret:      cfg.OAuth2.ClientSecret,
		Scopes:            cfg.OAuth2.Scopes,
		EndpointAuthStyle: authStyle,
		Prompt:            cfg.OAuth2.Prompt,
	}, nil
}

// AfterConnect is a no-op. The API gateway's Authenticator reads the
// persisted token from the store on every outbound request via
// connoauth.Source, so once the token is persisted by the unified
// callback handler the connection is immediately usable. The MCP
// gateway, in contrast, caches an in-memory client per connection
// and must rebuild it after Connect; that's the MCP-side reason
// AfterConnect exists on the interface at all.
func (*OAuthKindHandler) AfterConnect(_ context.Context, _ string, _ map[string]any) error {
	return nil
}
