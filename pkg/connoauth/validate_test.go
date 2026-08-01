package connoauth

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValidateEndpointURL covers the shapes an operator can type into
// oauth_token_url / oauth_authorization_url. The host is deliberately
// unconstrained (self-hosted IdPs on loopback and RFC1918 are
// supported), so the private-address cases must be accepted.
func TestValidateEndpointURL(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr string // substring; empty means the value must be accepted
	}{
		{name: "empty is the caller's concern", raw: ""},
		{name: "https accepted", raw: "https://idp.example.com/realms/x/token"},
		{name: "http accepted", raw: "http://keycloak.identity.svc:8080/token"},
		{name: "loopback accepted", raw: "http://localhost:8080/realms/dev/token"},
		{name: "rfc1918 accepted", raw: "http://10.0.0.5/token"},
		{name: "query string accepted", raw: "https://idp.example.com/token?tenant=a"},
		{name: "uppercase scheme accepted", raw: "HTTPS://IdP.Example.com/Token"},

		{name: "relative path", raw: "/realms/x/token", wantErr: "must be an absolute URL"},
		{name: "host and port without scheme", raw: "keycloak:8080/token", wantErr: "unsupported scheme"},
		{name: "ftp scheme", raw: "ftp://idp.example.com/token", wantErr: "unsupported scheme"},
		{name: "file scheme", raw: "file:///etc/passwd", wantErr: "unsupported scheme"},
		{name: "scheme with no host", raw: "https://", wantErr: "missing a host"},
		{
			name: "embedded credentials", raw: "https://svc:s3cr3t@idp.example.com/token",
			wantErr: "must not embed credentials",
		},
		{
			name: "embedded username only", raw: "https://svc@idp.example.com/token",
			wantErr: "must not embed credentials",
		},
		{name: "fragment", raw: "https://idp.example.com/token#frag", wantErr: "must not contain a fragment"},
		{name: "unparseable", raw: "://idp.example.com", wantErr: "not a parseable URL"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateEndpointURL(ConfigKeyTokenURL, tc.raw)
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrInvalidConfig,
				"validation failures must wrap ErrInvalidConfig so callers can classify them")
			assert.Contains(t, err.Error(), tc.wantErr)
			assert.Contains(t, err.Error(), ConfigKeyTokenURL,
				"the error must name the offending config key so the operator knows what to fix")
		})
	}
}

// TestValidateEndpointURLDoesNotLeakCredentials proves the rejection
// message never carries the secret half of an embedded userinfo
// section. The error reaches the admin API response body and the log,
// so echoing the raw value would publish the password.
func TestValidateEndpointURLDoesNotLeakCredentials(t *testing.T) {
	err := validateEndpointURL(ConfigKeyTokenURL, "https://svc:s3cr3t@idp.example.com/token")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "s3cr3t")
	assert.NotContains(t, err.Error(), "svc")
}

// TestParseConfigRejectsMalformedEndpoints proves the validation runs
// at the shared door rather than at one call site: a config map that
// reaches ParseConfig with a bad endpoint yields no Config at all.
func TestParseConfigRejectsMalformedEndpoints(t *testing.T) {
	tests := []struct {
		name    string
		cfg     map[string]any
		wantErr string
	}{
		{
			name: "token url with embedded credentials",
			cfg: map[string]any{
				ConfigKeyGrant:        GrantClientCredentials,
				ConfigKeyTokenURL:     "https://svc:s3cr3t@idp.example.com/token",
				ConfigKeyClientID:     "platform",
				ConfigKeyClientSecret: "shh",
			},
			wantErr: "must not embed credentials",
		},
		{
			name: "authorization url is validated too",
			cfg: map[string]any{
				ConfigKeyGrant:            GrantAuthorizationCode,
				ConfigKeyTokenURL:         "https://idp.example.com/token",
				ConfigKeyAuthorizationURL: "javascript:alert(1)",
				ConfigKeyClientID:         "platform",
			},
			wantErr: "unsupported scheme",
		},
		{
			name: "legacy key path is validated too",
			cfg: map[string]any{
				ConfigKeyGrant:     GrantClientCredentials,
				"oauth2_token_url": "notaurl",
				ConfigKeyClientID:  "platform",
			},
			wantErr: "must be an absolute URL",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseConfig("mcp", "acme", tc.cfg)
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrInvalidConfig)
			assert.Contains(t, err.Error(), tc.wantErr)
			assert.Equal(t, Config{}, got,
				"a rejected config must not hand back a partially populated Config "+
					"that a caller could still build a request from")
		})
	}
}

// TestParseConfigAcceptsPrivateAndCleartextEndpoints is the
// non-regression guard for the deployments the validation must not
// break: an in-cluster Keycloak reached over plain http on an RFC1918
// address is a supported shape, and blocking it here would refuse a
// legitimate connection.
func TestParseConfigAcceptsPrivateAndCleartextEndpoints(t *testing.T) {
	got, err := ParseConfig("mcp", "internal-idp", map[string]any{
		ConfigKeyGrant:            GrantAuthorizationCode,
		ConfigKeyTokenURL:         "http://keycloak.identity.svc.cluster.local:8080/realms/p/protocol/openid-connect/token",
		ConfigKeyAuthorizationURL: "http://10.4.2.9:8080/realms/p/protocol/openid-connect/auth",
		ConfigKeyClientID:         "platform",
	})
	require.NoError(t, err)
	assert.Equal(t, "http://keycloak.identity.svc.cluster.local:8080/realms/p/protocol/openid-connect/token", got.TokenURL)
	assert.Equal(t, "http://10.4.2.9:8080/realms/p/protocol/openid-connect/auth", got.AuthorizationURL)
}

// TestWarnCleartextEndpoint asserts the operator is told once, with the
// host, that credentials cross the wire unencrypted -- and that an
// https endpoint stays silent.
func TestWarnCleartextEndpoint(t *testing.T) {
	t.Run("http warns once per connection and key", func(t *testing.T) {
		cleartextWarned.Clear()
		var buf bytes.Buffer
		restore := swapDefaultLogger(&buf)
		defer restore()

		warnCleartextEndpoint("mcp", "acme", ConfigKeyTokenURL, "http://idp.internal:8080/token")
		warnCleartextEndpoint("mcp", "acme", ConfigKeyTokenURL, "http://idp.internal:8080/token")

		out := buf.String()
		assert.Equal(t, 1, strings.Count(out, "cleartext http"),
			"the warning must be deduped; ParseConfig runs on every token request")
		assert.Contains(t, out, "idp.internal:8080")
		assert.Contains(t, out, ConfigKeyTokenURL)
	})

	t.Run("https is silent", func(t *testing.T) {
		cleartextWarned.Clear()
		var buf bytes.Buffer
		restore := swapDefaultLogger(&buf)
		defer restore()

		warnCleartextEndpoint("mcp", "acme", ConfigKeyTokenURL, "https://idp.example.com/token")
		assert.Empty(t, buf.String())
	})

	t.Run("unparseable is silent", func(t *testing.T) {
		cleartextWarned.Clear()
		var buf bytes.Buffer
		restore := swapDefaultLogger(&buf)
		defer restore()

		warnCleartextEndpoint("mcp", "acme", ConfigKeyTokenURL, "://nope")
		assert.Empty(t, buf.String())
	})

	t.Run("distinct keys each warn", func(t *testing.T) {
		cleartextWarned.Clear()
		var buf bytes.Buffer
		restore := swapDefaultLogger(&buf)
		defer restore()

		warnCleartextEndpoint("mcp", "acme", ConfigKeyTokenURL, "http://idp.internal/token")
		warnCleartextEndpoint("mcp", "acme", ConfigKeyAuthorizationURL, "http://idp.internal/auth")
		assert.Equal(t, 2, strings.Count(buf.String(), "cleartext http"))
	})
}

// swapDefaultLogger points slog's default logger at buf and returns a
// function restoring the previous default.
func swapDefaultLogger(buf *bytes.Buffer) func() {
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	return func() { slog.SetDefault(prev) }
}
