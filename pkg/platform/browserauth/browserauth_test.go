package browserauth_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/browsersession"
	"github.com/txn2/mcp-data-platform/pkg/platform/browserauth"
)

func TestNewSession_Accessors(t *testing.T) {
	auth := &browsersession.Authenticator{}
	s := browserauth.NewSession(nil, auth)
	assert.Nil(t, s.Flow(), "flow was not provided")
	assert.Same(t, auth, s.Authenticator())
}

func TestNilSession_AccessorsAreSafe(t *testing.T) {
	var s *browserauth.Session // browser sessions disabled
	assert.Nil(t, s.Flow(), "nil Session must return a nil flow, not panic")
	assert.Nil(t, s.Authenticator(), "nil Session must return a nil authenticator, not panic")
}

// TestNew_MapsConfigAndSurfacesFlowError exercises the cookie/flow config
// mapping (including the SameSite=None cross-site warning branch) and the error
// path: an empty Issuer makes browsersession.NewFlow fail, and New must wrap it.
func TestNew_MapsConfigAndSurfacesFlowError(t *testing.T) {
	_, err := browserauth.New(context.Background(), browserauth.Config{
		CookieName: "sess",
		SameSite:   "none", // triggers the cross-site cookie warning branch
		Secure:     true,
		SigningKey: []byte("0123456789abcdef0123456789abcdef"),
		// Issuer omitted → NewFlow returns "issuer is required".
		ClientID:    "client",
		RedirectURI: "https://example/portal/auth/callback",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "creating OIDC flow")
	assert.True(t, strings.Contains(err.Error(), "issuer"),
		"the underlying validation error should be wrapped, got: %v", err)
}
