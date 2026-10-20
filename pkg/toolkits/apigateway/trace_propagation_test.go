package apigateway

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestParseConfig_TracePropagationRoundTrips: the flag Parse reads reaches
// the shared transport config the client is built from, in both the default
// and the turned-off form (#1895). The two copies, configFromUpstream and
// upstream(), are what a dropped field hides in.
func TestParseConfig_TracePropagationRoundTrips(t *testing.T) {
	on, err := ParseConfig(map[string]any{"base_url": "https://api.example.com"})
	require.NoError(t, err)
	require.True(t, on.TracePropagation)
	require.True(t, on.upstream().TracePropagation)

	off, err := ParseConfig(map[string]any{"base_url": "https://api.example.com", "trace_propagation": false})
	require.NoError(t, err)
	require.False(t, off.TracePropagation)
	require.False(t, off.upstream().TracePropagation)
}

// TestParseConfig_OAuthTokenLifetimeRoundTrips: oauth_token_lifetime reaches
// the authenticator's config through both copies, and an unset one is the
// default (#2218). A connection's 30s used to reach the authenticator as zero.
func TestParseConfig_OAuthTokenLifetimeRoundTrips(t *testing.T) {
	set, err := ParseConfig(map[string]any{"base_url": "https://api.example.com", "auth_mode": "oauth", "oauth_grant": "client_credentials", "oauth_token_url": "https://idp.example/token", "oauth_client_id": "id", "oauth_client_secret": "secret", "oauth_token_lifetime": "30s"})
	require.NoError(t, err)
	require.Equal(t, 30*time.Second, set.OAuth2.UnstatedTokenLifetime)
	require.Equal(t, 30*time.Second, set.upstream().OAuth2.UnstatedTokenLifetime)

	unset, err := ParseConfig(map[string]any{"base_url": "https://api.example.com", "auth_mode": "oauth", "oauth_grant": "client_credentials", "oauth_token_url": "https://idp.example/token", "oauth_client_id": "id", "oauth_client_secret": "secret"})
	require.NoError(t, err)
	require.Equal(t, 15*time.Minute, unset.upstream().OAuth2.UnstatedTokenLifetime)
}
