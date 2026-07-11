package iam_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/iam"
	"github.com/txn2/mcp-data-platform/pkg/auth"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/persona"
)

// signingKey is any non-empty HMAC key; NewOAuthJWTAuthenticator only requires
// it to be non-empty.
var signingKey = []byte("0123456789abcdef0123456789abcdef")

// isNoop reports whether a is the permissive noop authenticator.
func isNoop(a middleware.Authenticator) bool {
	_, ok := a.(*middleware.NoopAuthenticator)
	return ok
}

func TestNewIdentity_NoopWhenNothingEnabled(t *testing.T) {
	res, err := iam.NewIdentity(iam.Input{})
	require.NoError(t, err)
	assert.True(t, isNoop(res.Authenticator),
		"no authenticators configured must yield the permissive noop")
	assert.Nil(t, res.APIKeyAuth)
}

func TestNewIdentity_APIKeyOnly(t *testing.T) {
	res, err := iam.NewIdentity(iam.Input{
		APIKeysEnabled: true,
		APIKeys:        []auth.APIKey{{Key: "k1", Name: "admin", Roles: []string{"admin"}}},
	})
	require.NoError(t, err)
	require.NotNil(t, res.APIKeyAuth, "API key authenticator must be returned for loadDBAPIKeys")
	assert.False(t, isNoop(res.Authenticator),
		"a configured authenticator must chain, not fall back to noop")
}

func TestNewIdentity_OAuthJWTIncludedOnlyWithSigningKey(t *testing.T) {
	// Enabled + key present: included, so the result is a real chain.
	res, err := iam.NewIdentity(iam.Input{
		OAuthEnabled: true,
		OAuthJWT:     auth.OAuthJWTConfig{Issuer: "https://issuer.example", SigningKey: signingKey},
	})
	require.NoError(t, err)
	assert.False(t, isNoop(res.Authenticator))

	// Enabled but no signing key: skipped, and with nothing else it is noop.
	res, err = iam.NewIdentity(iam.Input{
		OAuthEnabled: true,
		OAuthJWT:     auth.OAuthJWTConfig{Issuer: "https://issuer.example"},
	})
	require.NoError(t, err)
	assert.True(t, isNoop(res.Authenticator))
}

func TestNewIdentity_OIDCIncluded(t *testing.T) {
	// SkipSignatureVerification avoids the startup JWKS fetch so the OIDC
	// authenticator constructs without network in the test.
	res, err := iam.NewIdentity(iam.Input{
		OIDCEnabled: true,
		OIDC: auth.OIDCConfig{
			Issuer:                    "https://issuer.example",
			SkipSignatureVerification: true,
		},
	})
	require.NoError(t, err)
	assert.False(t, isNoop(res.Authenticator), "an enabled OIDC authenticator must chain")
}

func TestNewIdentity_PropagatesConstructionError(t *testing.T) {
	// OAuth enabled with a signing key but no issuer: NewOAuthJWTAuthenticator
	// rejects it, and NewIdentity must surface the error, not swallow it.
	_, err := iam.NewIdentity(iam.Input{
		OAuthEnabled: true,
		OAuthJWT:     auth.OAuthJWTConfig{SigningKey: signingKey},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "OAuth JWT authenticator")
}

func TestNewAuthorizer_WiresMappingAndRegistry(t *testing.T) {
	reg := persona.NewRegistry()
	require.NoError(t, reg.Register(&persona.Persona{Name: "special"}))

	authz := iam.NewAuthorizer(iam.Input{
		PersonaRegistry: reg,
		RoleClaimPath:   "realm_access.roles",
		RolePrefix:      "dp_",
		OIDCToPersona:   map[string]string{"role_x": "special"},
	})
	require.NotNil(t, authz)

	// A request carrying role_x must resolve to the "special" persona through the
	// OIDCToPersona mapping and the registry. If NewAuthorizer dropped either
	// field, this would fall back to a different persona — asserting non-nil alone
	// would not catch that.
	_, personaName, _ := authz.IsAuthorized(context.Background(), "", []string{"role_x"}, "any_tool", "any_conn")
	assert.Equal(t, "special", personaName,
		"OIDCToPersona mapping and persona registry must be wired into the authorizer")
}
