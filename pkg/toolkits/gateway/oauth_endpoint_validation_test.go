package gateway

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/connoauth"
)

// TestAddConnectionRejectsMalformedOAuthEndpoint covers the load-time
// half of the endpoint validation: a connection_instances row written
// before the check exists (or edited directly in the database) must be
// refused when the toolkit loads it, not merely when an operator saves
// it through the admin API.
//
// Without this, the save-time check is only advisory: a stored row with
// a malformed oauth_token_url would still register a live connection
// and drive a credential-bearing POST on every refresh.
func TestAddConnectionRejectsMalformedOAuthEndpoint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		tokenURL string
		wantErr  string
	}{
		{
			name:     "embedded credentials",
			tokenURL: "https://svc:s3cr3t@idp.example.com/token",
			wantErr:  "must not embed credentials",
		},
		{
			name:     "not absolute",
			tokenURL: "idp.example.com/token",
			wantErr:  "must be an absolute URL",
		},
		{
			name:     "non-http scheme",
			tokenURL: "ftp://idp.example.com/token",
			wantErr:  "unsupported scheme",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tk := New("gateway")
			t.Cleanup(func() { _ = tk.Close() })

			err := tk.AddConnection("stored", map[string]any{
				"endpoint":                      "https://upstream.example.com/mcp",
				"auth_mode":                     connoauth.AuthModeOAuth,
				connoauth.ConfigKeyGrant:        connoauth.GrantClientCredentials,
				connoauth.ConfigKeyTokenURL:     tc.tokenURL,
				connoauth.ConfigKeyClientID:     "platform",
				connoauth.ConfigKeyClientSecret: "shh",
			})

			require.Error(t, err, "a stored row with a malformed OAuth endpoint must not load")
			assert.ErrorIs(t, err, connoauth.ErrInvalidConfig)
			assert.Contains(t, err.Error(), tc.wantErr)
			assert.Empty(t, tk.ListConnections(),
				"a refused connection must not be registered on the live toolkit")
		})
	}
}
