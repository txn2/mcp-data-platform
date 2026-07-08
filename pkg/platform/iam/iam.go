// Package iam assembles the platform's authentication and authorization
// identity layer — the authenticator chain and the persona-based authorizer —
// from explicit, foreign-typed inputs rather than by mutating fields on the
// Platform god-object. Declaring dependencies as parameters makes the wiring
// type-checked data flow and lets the layer be built and tested without a whole
// Platform (issue #756).
//
// This package MUST NOT import pkg/platform: the platform-local config types
// would create an import cycle, and taking narrow inputs instead is the point —
// it makes each dependency explicit at the boundary.
package iam

import (
	"fmt"

	"github.com/txn2/mcp-data-platform/pkg/auth"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/persona"
)

// Input carries everything needed to build the authentication and authorization
// identity layer, translated from the platform config by the caller so this
// package never depends on pkg/platform.
type Input struct {
	// OAuthEnabled gates the OAuth JWT authenticator (for tokens issued by our
	// own OAuth server). It is only added when a signing key is also present.
	OAuthEnabled bool
	OAuthJWT     auth.OAuthJWTConfig

	// OIDCEnabled gates the OIDC authenticator (for tokens from an external IdP).
	OIDCEnabled bool
	OIDC        auth.OIDCConfig

	// APIKeysEnabled gates the API key authenticator.
	APIKeysEnabled bool
	APIKeys        []auth.APIKey

	// AllowAnonymous permits unauthenticated access through the chained
	// authenticator; ignored when no authenticators are configured (noop).
	AllowAnonymous bool

	// Authorizer inputs.
	PersonaRegistry *persona.Registry
	RoleClaimPath   string
	RolePrefix      string
	OIDCToPersona   map[string]string
}

// Identity is the authenticator half of the identity layer.
type Identity struct {
	// Authenticator is the assembled authenticator (a chain, or a noop when
	// nothing is configured).
	Authenticator middleware.Authenticator
	// APIKeyAuth is the API key authenticator when API keys are enabled, or nil.
	// The caller keeps it so DB-backed keys can be appended after startup.
	APIKeyAuth *auth.APIKeyAuthenticator
}

// NewIdentity builds the authenticator chain from in. Authenticators are ordered
// OAuth JWT → OIDC → API key so a token issued by our own OAuth server is
// checked first; when none are configured a permissive noop is returned.
func NewIdentity(in Input) (Identity, error) {
	var authenticators []middleware.Authenticator

	// OAuth JWT authenticator, checked first: tokens from our OAuth server use it.
	if in.OAuthEnabled && len(in.OAuthJWT.SigningKey) > 0 {
		oauthAuth, err := auth.NewOAuthJWTAuthenticator(in.OAuthJWT)
		if err != nil {
			return Identity{}, fmt.Errorf("creating OAuth JWT authenticator: %w", err)
		}
		authenticators = append(authenticators, oauthAuth)
	}

	// OIDC authenticator, for tokens from external IdPs (e.g. Keycloak).
	if in.OIDCEnabled {
		oidcAuth, err := auth.NewOIDCAuthenticator(in.OIDC)
		if err != nil {
			return Identity{}, fmt.Errorf("creating OIDC authenticator: %w", err)
		}
		authenticators = append(authenticators, oidcAuth)
	}

	// API key authenticator.
	var apiKeyAuth *auth.APIKeyAuthenticator
	if in.APIKeysEnabled {
		apiKeyAuth = auth.NewAPIKeyAuthenticator(auth.APIKeyConfig{Keys: in.APIKeys})
		authenticators = append(authenticators, apiKeyAuth)
	}

	// No authenticators configured: permissive noop (anonymous).
	if len(authenticators) == 0 {
		return Identity{
			Authenticator: &middleware.NoopAuthenticator{
				DefaultUserID: "anonymous",
				DefaultRoles:  []string{},
			},
		}, nil
	}

	return Identity{
		Authenticator: auth.NewChainedAuthenticator(
			auth.ChainedAuthConfig{AllowAnonymous: in.AllowAnonymous},
			authenticators...,
		),
		APIKeyAuth: apiKeyAuth,
	}, nil
}

// NewAuthorizer builds the persona-based authorizer from in. It is independent
// of NewIdentity so a caller can override one without constructing the other.
func NewAuthorizer(in Input) middleware.Authorizer {
	mapper := &persona.OIDCRoleMapper{
		ClaimPath:      in.RoleClaimPath,
		RolePrefix:     in.RolePrefix,
		PersonaMapping: in.OIDCToPersona,
		Registry:       in.PersonaRegistry,
	}
	return persona.NewAuthorizer(in.PersonaRegistry, mapper)
}
