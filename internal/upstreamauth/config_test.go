package upstreamauth

import (
	"maps"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/connoauth"
)

// TestParse_AppliesDefaults pins what a connection that configures
// nothing gets: no credential, a header-placed API key name, and the
// three transport bounds. An operator who saves an empty form must land
// on these, not on zero values that would mean "no timeout" to
// net/http and "read nothing" to the body reader.
func TestParse_AppliesDefaults(t *testing.T) {
	c, err := Parse(connoauth.KindAPI, "apigateway", "https://upstream.example", map[string]any{})
	require.NoError(t, err)
	assert.Equal(t, AuthModeNone, c.AuthMode)
	assert.Equal(t, CredentialPlacementHeader, c.CredentialPlacement)
	assert.Equal(t, DefaultAPIKeyHeader, c.APIKeyHeader)
	assert.Equal(t, DefaultConnectTimeout, c.ConnectTimeout)
	assert.Equal(t, DefaultCallTimeout, c.CallTimeout)
	assert.Equal(t, DefaultMaxResponseBytes, c.MaxResponseBytes)
	assert.Equal(t, connoauth.KindAPI, c.Kind)
	assert.Equal(t, "apigateway", c.ErrPrefix)
}

// TestParse_ReadsEveryKey walks one connection carrying every key this
// package owns, in the shapes a saved connection presents them in.
func TestParse_ReadsEveryKey(t *testing.T) {
	c, err := Parse(connoauth.KindAPI, "apigateway", "https://upstream.example", map[string]any{
		"auth_mode":            AuthModeAPIKey,
		"credential":           "k3y",
		"api_key_placement":    CredentialPlacementQuery,
		"api_key_header":       "X-Custom",
		"api_key_param":        "apikey",
		"username":             "alice",
		"password":             "s3cret",
		"connect_timeout":      "3s",
		"call_timeout":         float64(90),
		"max_response_bytes":   float64(2048),
		"static_headers":       map[string]any{"X-Goog-User-Project": "proj"},
		"mtls_client_cert_pem": "cert",
		"mtls_client_key_pem":  "key",
		"tls_ca_bundle_pem":    "bundle",
		"identity_passthrough": true,
	})
	require.NoError(t, err)
	assert.Equal(t, AuthModeAPIKey, c.AuthMode)
	assert.Equal(t, "k3y", c.Credential)
	assert.Equal(t, CredentialPlacementQuery, c.CredentialPlacement)
	assert.Equal(t, "X-Custom", c.APIKeyHeader)
	assert.Equal(t, "apikey", c.APIKeyParam)
	assert.Equal(t, "alice", c.Username)
	assert.Equal(t, "s3cret", c.Password)
	assert.Equal(t, 3*time.Second, c.ConnectTimeout)
	assert.Equal(t, 90*time.Second, c.CallTimeout)
	assert.Equal(t, int64(2048), c.MaxResponseBytes)
	assert.Equal(t, map[string]string{"X-Goog-User-Project": "proj"}, c.StaticHeaders)
	assert.Equal(t, "cert", c.MTLSClientCertPEM)
	assert.Equal(t, "key", c.MTLSClientKeyPEM)
	assert.Equal(t, "bundle", c.TLSCABundlePEM)
	assert.True(t, c.IdentityPassthrough)
}

// TestParse_NormalizesEveryOAuthModeSpelling proves the two legacy
// auth_mode strings that encoded the grant collapse to the canonical
// mode with the grant carried separately, so the authenticator and the
// validators dispatch on one value rather than three.
func TestParse_NormalizesEveryOAuthModeSpelling(t *testing.T) {
	base := map[string]any{
		"oauth_token_url":         "https://idp.example/token",
		"oauth_authorization_url": "https://idp.example/authorize",
		"oauth_client_id":         "id",
		"oauth_client_secret":     "secret",
	}
	cases := []struct {
		mode      string
		wantGrant string
	}{
		{AuthModeOAuth2ClientCredentials, connoauth.GrantClientCredentials},
		{AuthModeOAuth2AuthorizationCode, connoauth.GrantAuthorizationCode},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			cfg := map[string]any{"auth_mode": tc.mode}
			maps.Copy(cfg, base)
			c, err := Parse(connoauth.KindAPI, "apigateway", "https://upstream.example", cfg)
			require.NoError(t, err)
			assert.Equal(t, AuthModeOAuth, c.AuthMode, "legacy mode must normalize to the canonical one")
			assert.Equal(t, tc.wantGrant, c.OAuth2.Grant)
			assert.Equal(t, OAuth2AuthStyleHeader, c.OAuth2.EndpointAuthStyle)
		})
	}
}

// TestParse_OAuthConfigErrorCarriesTheKindPrefix covers the one path
// Parse can fail on: connoauth refuses a malformed endpoint URL, and
// the refusal must reach the operator in the calling kind's voice.
func TestParse_OAuthConfigErrorCarriesTheKindPrefix(t *testing.T) {
	_, err := Parse(connoauth.KindAPI, "apigateway", "https://upstream.example", map[string]any{
		"auth_mode":       AuthModeOAuth,
		"oauth_token_url": "://not a url",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "apigateway: ")
}

// TestValidate_RunsEveryCheck exercises the composite entry point a
// kind with no keys of its own uses: a sound config passes, and a
// failure in any arm surfaces.
func TestValidate_RunsEveryCheck(t *testing.T) {
	sound := Config{
		ErrPrefix:        "graphql",
		AuthMode:         AuthModeBearer,
		Credential:       "tok",
		ConnectTimeout:   DefaultConnectTimeout,
		CallTimeout:      DefaultCallTimeout,
		MaxResponseBytes: DefaultMaxResponseBytes,
	}
	require.NoError(t, sound.Validate())

	cases := []struct {
		name    string
		mutate  func(c Config) Config
		wantMsg string
	}{
		{"auth", func(c Config) Config { c.Credential = ""; return c }, "credential is required"},
		{"transport", func(c Config) Config { c.CallTimeout = 0; return c }, "call_timeout must be positive"},
		{"static headers", func(c Config) Config {
			c.StaticHeaders = map[string]string{"Authorization": "x"}
			return c
		}, "static_headers must not set Authorization"},
		{"identity passthrough", func(c Config) Config { c.IdentityPassthrough = true; return c }, "identity_passthrough requires auth_mode=none"},
		{"tls material", func(c Config) Config { c.MTLSClientCertPEM = "cert-only"; return c }, "must both be set or both be empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.mutate(sound).Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantMsg)
			assert.Contains(t, err.Error(), "graphql: ", "the message must be in the calling kind's voice")
		})
	}
}

// TestValidateAuth_PerMode walks every credential rule the package
// enforces. The messages are the ones an operator reads when a
// connection save is refused, so each case pins the wording that names
// the offending key.
func TestValidateAuth_PerMode(t *testing.T) {
	cases := []struct {
		name    string
		cfg     Config
		wantMsg string // empty = must pass
	}{
		{name: "none", cfg: Config{AuthMode: AuthModeNone}},
		{name: "bearer ok", cfg: Config{AuthMode: AuthModeBearer, Credential: "t"}},
		{name: "bearer without credential", cfg: Config{AuthMode: AuthModeBearer}, wantMsg: "credential is required"},
		{
			name: "api_key header ok",
			cfg:  Config{AuthMode: AuthModeAPIKey, Credential: "k", CredentialPlacement: CredentialPlacementHeader, APIKeyHeader: DefaultAPIKeyHeader},
		},
		{
			name:    "api_key without credential",
			cfg:     Config{AuthMode: AuthModeAPIKey, CredentialPlacement: CredentialPlacementHeader, APIKeyHeader: DefaultAPIKeyHeader},
			wantMsg: "credential is required",
		},
		{
			name:    "api_key header empty",
			cfg:     Config{AuthMode: AuthModeAPIKey, Credential: "k", CredentialPlacement: CredentialPlacementHeader},
			wantMsg: "api_key_header must not be empty",
		},
		{
			name: "api_key query ok",
			cfg:  Config{AuthMode: AuthModeAPIKey, Credential: "k", CredentialPlacement: CredentialPlacementQuery, APIKeyParam: "key"},
		},
		{
			name:    "api_key query without param",
			cfg:     Config{AuthMode: AuthModeAPIKey, Credential: "k", CredentialPlacement: CredentialPlacementQuery},
			wantMsg: "api_key_param is required",
		},
		{
			name:    "api_key bad placement",
			cfg:     Config{AuthMode: AuthModeAPIKey, Credential: "k", CredentialPlacement: "body"},
			wantMsg: "invalid api_key_placement",
		},
		{name: "basic ok", cfg: Config{AuthMode: AuthModeBasic, Username: "alice", Password: "p"}},
		{name: "basic empty password ok", cfg: Config{AuthMode: AuthModeBasic, Username: "token"}},
		{name: "basic without username", cfg: Config{AuthMode: AuthModeBasic}, wantMsg: "username is required"},
		{
			name:    "basic username with colon",
			cfg:     Config{AuthMode: AuthModeBasic, Username: "a:b"},
			wantMsg: "must not contain",
		},
		{
			name:    "basic username with CRLF",
			cfg:     Config{AuthMode: AuthModeBasic, Username: "a\r\nX-Smuggled: 1"},
			wantMsg: "username contains CR/LF/NUL",
		},
		{
			name:    "basic password with NUL",
			cfg:     Config{AuthMode: AuthModeBasic, Username: "alice", Password: "p\x00"},
			wantMsg: "password contains CR/LF/NUL",
		},
		{name: "mtls defers to the TLS validator", cfg: Config{AuthMode: AuthModeMTLS}},
		{name: "unknown mode", cfg: Config{AuthMode: "future"}, wantMsg: "invalid auth_mode"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.ValidateAuth()
			if tc.wantMsg == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantMsg)
		})
	}
}

// TestValidateAuth_OAuthGrants covers the OAuth arm across the
// canonical mode and both legacy spellings, since each reaches the
// grant-specific validator by a different route.
func TestValidateAuth_OAuthGrants(t *testing.T) {
	full := OAuth2Config{
		Grant:             connoauth.GrantAuthorizationCode,
		TokenURL:          "https://idp/token",
		AuthorizationURL:  "https://idp/authorize",
		ClientID:          "id",
		ClientSecret:      "secret",
		EndpointAuthStyle: OAuth2AuthStyleHeader,
	}
	cases := []struct {
		name    string
		cfg     Config
		wantMsg string
	}{
		{name: "canonical authorization_code", cfg: Config{AuthMode: AuthModeOAuth, OAuth2: full}},
		{name: "legacy authorization_code", cfg: Config{AuthMode: AuthModeOAuth2AuthorizationCode, OAuth2: full}},
		{
			name: "canonical client_credentials",
			cfg: Config{AuthMode: AuthModeOAuth, OAuth2: OAuth2Config{
				Grant: connoauth.GrantClientCredentials, TokenURL: "https://idp/token",
				ClientID: "id", ClientSecret: "s", EndpointAuthStyle: OAuth2AuthStyleParams,
			}},
		},
		{
			name: "legacy client_credentials",
			cfg: Config{AuthMode: AuthModeOAuth2ClientCredentials, OAuth2: OAuth2Config{
				TokenURL: "https://idp/token", ClientID: "id", ClientSecret: "s",
				EndpointAuthStyle: OAuth2AuthStyleHeader,
			}},
		},
		{
			name:    "authorization_code without authorization_url",
			cfg:     Config{AuthMode: AuthModeOAuth, OAuth2: withField(full, func(o *OAuth2Config) { o.AuthorizationURL = "" })},
			wantMsg: "oauth_authorization_url is required",
		},
		{
			name:    "missing token_url",
			cfg:     Config{AuthMode: AuthModeOAuth, OAuth2: withField(full, func(o *OAuth2Config) { o.TokenURL = "" })},
			wantMsg: "oauth_token_url is required",
		},
		{
			name:    "missing client_id",
			cfg:     Config{AuthMode: AuthModeOAuth, OAuth2: withField(full, func(o *OAuth2Config) { o.ClientID = "" })},
			wantMsg: "oauth_client_id is required",
		},
		{
			name:    "missing client_secret",
			cfg:     Config{AuthMode: AuthModeOAuth, OAuth2: withField(full, func(o *OAuth2Config) { o.ClientSecret = "" })},
			wantMsg: "oauth_client_secret is required",
		},
		{
			name:    "invalid endpoint auth style",
			cfg:     Config{AuthMode: AuthModeOAuth, OAuth2: withField(full, func(o *OAuth2Config) { o.EndpointAuthStyle = "cookie" })},
			wantMsg: "invalid oauth_endpoint_auth_style",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.ValidateAuth()
			if tc.wantMsg == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantMsg)
		})
	}
}

// withField returns a copy of o with one field changed, so the table
// above can express "the full config, minus this" without restating
// every field per case.
func withField(o OAuth2Config, mutate func(*OAuth2Config)) OAuth2Config {
	mutate(&o)
	return o
}

func TestValidateTransport(t *testing.T) {
	sound := Config{ConnectTimeout: time.Second, CallTimeout: time.Second, MaxResponseBytes: 1}
	require.NoError(t, sound.ValidateTransport())

	cases := []struct {
		name    string
		mutate  func(c Config) Config
		wantMsg string
	}{
		{"connect timeout", func(c Config) Config { c.ConnectTimeout = 0; return c }, "connect_timeout must be positive"},
		{"call timeout", func(c Config) Config { c.CallTimeout = -time.Second; return c }, "call_timeout must be positive"},
		{"read cap", func(c Config) Config { c.MaxResponseBytes = 0; return c }, "max_response_bytes must be positive"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.mutate(sound).ValidateTransport()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantMsg)
		})
	}
}

// TestValidateIdentityPassthrough enforces the single-credential-source
// invariant: passthrough forwards the caller's own token, so a
// configured auth mode would be a second, contradictory source for the
// same header.
func TestValidateIdentityPassthrough(t *testing.T) {
	require.NoError(t, Config{IdentityPassthrough: true, AuthMode: AuthModeNone}.ValidateIdentityPassthrough())
	require.NoError(t, Config{IdentityPassthrough: false, AuthMode: AuthModeBearer}.ValidateIdentityPassthrough())
	err := Config{IdentityPassthrough: true, AuthMode: AuthModeBearer}.ValidateIdentityPassthrough()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "identity_passthrough requires auth_mode=none")
}

func TestIsOAuthAuthorizationCode(t *testing.T) {
	assert.True(t, Config{
		AuthMode: AuthModeOAuth,
		OAuth2:   OAuth2Config{Grant: connoauth.GrantAuthorizationCode},
	}.IsOAuthAuthorizationCode())
	assert.False(t, Config{
		AuthMode: AuthModeOAuth,
		OAuth2:   OAuth2Config{Grant: connoauth.GrantClientCredentials},
	}.IsOAuthAuthorizationCode())
	assert.False(t, Config{AuthMode: AuthModeBearer}.IsOAuthAuthorizationCode())
}

// TestErrPrefix_FallsBackToThePackageName covers the unset case. No
// kind should reach it, but a Config built by hand must still produce a
// readable error rather than one that starts with ": ".
func TestErrPrefix_FallsBackToThePackageName(t *testing.T) {
	err := Config{AuthMode: AuthModeBearer}.ValidateAuth()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "upstreamauth: ")
}

// --- authenticator behavior -----------------------------------------

// TestAuthenticatorApply_PerMode asserts what each mode actually puts
// on the wire. The header values are pinned literally so a refactor
// that changes an encoding fails here rather than against an upstream.
func TestAuthenticatorApply_PerMode(t *testing.T) {
	cases := []struct {
		name       string
		cfg        Config
		wantHeader string
		wantValue  string
		wantQuery  string
	}{
		{
			name:       "bearer",
			cfg:        Config{AuthMode: AuthModeBearer, Credential: "T0K3N"},
			wantHeader: "Authorization", wantValue: "Bearer T0K3N",
		},
		{
			name: "api_key in a header",
			cfg: Config{
				AuthMode: AuthModeAPIKey, Credential: "k3y",
				CredentialPlacement: CredentialPlacementHeader, APIKeyHeader: "X-Custom-Key",
			},
			wantHeader: "X-Custom-Key", wantValue: "k3y",
		},
		{
			name: "api_key in the query",
			cfg: Config{
				AuthMode: AuthModeAPIKey, Credential: "k3y",
				CredentialPlacement: CredentialPlacementQuery, APIKeyParam: "apikey",
			},
			wantQuery: "apikey=k3y",
		},
		{
			name:       "basic",
			cfg:        Config{AuthMode: AuthModeBasic, Username: "alice", Password: "s3cret"},
			wantHeader: "Authorization", wantValue: "Basic YWxpY2U6czNjcmV0",
		},
		{name: "none touches nothing", cfg: Config{AuthMode: AuthModeNone}},
		{name: "mtls touches nothing", cfg: Config{AuthMode: AuthModeMTLS}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			auth, err := NewAuthenticator(tc.cfg)
			require.NoError(t, err)
			req, err := http.NewRequest(http.MethodGet, "https://upstream.example/x", http.NoBody) //nolint:noctx // no request is sent
			require.NoError(t, err)
			require.NoError(t, auth.Apply(req))
			if tc.wantHeader != "" {
				assert.Equal(t, tc.wantValue, req.Header.Get(tc.wantHeader))
			} else {
				assert.Empty(t, req.Header.Get("Authorization"))
			}
			if tc.wantQuery != "" {
				assert.Equal(t, tc.wantQuery, req.URL.RawQuery)
			}
		})
	}
}

// TestNewAuthenticator_RejectsIncompleteCredentials covers the
// authenticator's own defense-in-depth guards, which run even when a
// caller constructed the Config without going through validation.
func TestNewAuthenticator_RejectsIncompleteCredentials(t *testing.T) {
	cases := []struct {
		name    string
		cfg     Config
		wantMsg string
	}{
		{"api_key empty credential", Config{AuthMode: AuthModeAPIKey, CredentialPlacement: CredentialPlacementHeader, APIKeyHeader: "X"}, "api_key credential is empty"},
		{"api_key empty header", Config{AuthMode: AuthModeAPIKey, Credential: "k", CredentialPlacement: CredentialPlacementHeader}, "api_key_header is empty"},
		{"api_key empty param", Config{AuthMode: AuthModeAPIKey, Credential: "k", CredentialPlacement: CredentialPlacementQuery}, "api_key_param is empty"},
		{"api_key bad placement", Config{AuthMode: AuthModeAPIKey, Credential: "k", CredentialPlacement: "body"}, "invalid api_key_placement"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewAuthenticator(tc.cfg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantMsg)
		})
	}
}

// TestBearerAuth_ApplyRefusesAnEmptyCredential covers the runtime guard
// on a Config that reached the authenticator without validation: an
// empty token must be refused rather than sent as a bare "Bearer ".
func TestBearerAuth_ApplyRefusesAnEmptyCredential(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://upstream.example/x", http.NoBody) //nolint:noctx // no request is sent
	require.NoError(t, err)
	err = bearerAuth{cfg: Config{ErrPrefix: "apigateway"}}.Apply(req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bearer credential is empty")
}

// TestAPIKeyAuth_ApplyRefusesAnInvalidPlacement covers the switch's
// default arm, reachable only by mutating a constructed authenticator —
// the guard that keeps a future placement value from silently sending
// no credential at all.
func TestAPIKeyAuth_ApplyRefusesAnInvalidPlacement(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://upstream.example/x", http.NoBody) //nolint:noctx // no request is sent
	require.NoError(t, err)
	a := apiKeyAuth{cfg: Config{ErrPrefix: "apigateway"}, credential: "k", placement: "body"}
	err = a.Apply(req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid api_key_placement")
}

// TestSetConnOAuthStore_OnlyWiresTheGrantThatNeedsIt lets a kind call
// the wiring helpers over every connection without type-switching:
// they report whether the authenticator took the value.
func TestSetConnOAuthStore_OnlyWiresTheGrantThatNeedsIt(t *testing.T) {
	authCode, err := NewAuthenticator(Config{
		AuthMode: AuthModeOAuth,
		OAuth2:   OAuth2Config{Grant: connoauth.GrantAuthorizationCode, TokenURL: "https://idp/token", ClientID: "id"},
	})
	require.NoError(t, err)
	assert.True(t, SetConnOAuthStore(authCode, connoauth.NewMemoryStore()))
	assert.True(t, SetAuthEvents(authCode, nil))

	bearer, err := NewAuthenticator(Config{AuthMode: AuthModeBearer, Credential: "t"})
	require.NoError(t, err)
	assert.False(t, SetConnOAuthStore(bearer, connoauth.NewMemoryStore()))
	assert.False(t, SetAuthEvents(bearer, nil))
}

// TestValidateOAuthAuth_LegacyModeOutranksTheGrantField pins the one
// case where the mode string and the grant field can disagree: a
// hand-built Config that bypassed Parse. Parse never produces one — it
// normalizes the legacy spellings to the canonical mode and carries the
// grant separately — so the rule exists to keep a Config assembled in
// code validating the way its mode says, as it did before the two
// dispatch paths collapsed into one.
func TestValidateOAuthAuth_LegacyModeOutranksTheGrantField(t *testing.T) {
	// client_credentials mode, authorization_code grant: the mode wins,
	// so the missing authorization_url is not required.
	err := Config{
		AuthMode: AuthModeOAuth2ClientCredentials,
		OAuth2: OAuth2Config{
			Grant: connoauth.GrantAuthorizationCode, TokenURL: "https://idp/token",
			ClientID: "id", ClientSecret: "s", EndpointAuthStyle: OAuth2AuthStyleHeader,
		},
	}.ValidateAuth()
	require.NoError(t, err)

	// authorization_code mode, client_credentials grant: the mode wins
	// the other way, and the missing authorization_url is refused.
	err = Config{
		AuthMode: AuthModeOAuth2AuthorizationCode,
		OAuth2: OAuth2Config{
			Grant: connoauth.GrantClientCredentials, TokenURL: "https://idp/token",
			ClientID: "id", ClientSecret: "s", EndpointAuthStyle: OAuth2AuthStyleHeader,
		},
	}.ValidateAuth()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "oauth_authorization_url is required")
}

// A refusal names the mode and grant the connection carries, so an operator
// reading it looks at the configuration they wrote rather than at the
// client_credentials mode the message used to hard-code (#1681).
func TestValidateAuth_RefusalNamesTheModeAndGrantInScope(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want string
	}{
		{
			name: "canonical authorization_code",
			cfg: Config{AuthMode: AuthModeOAuth, OAuth2: OAuth2Config{
				Grant: connoauth.GrantAuthorizationCode, EndpointAuthStyle: OAuth2AuthStyleHeader,
			}},
			want: `oauth_token_url is required when auth_mode is "oauth" and oauth_grant is "authorization_code"`,
		},
		{
			// A Config parsed from a canonical connection that names no
			// grant defaults to client_credentials, and the message says so
			// rather than leaving the grant blank.
			name: "canonical with no grant stated",
			cfg: Config{AuthMode: AuthModeOAuth, OAuth2: OAuth2Config{
				EndpointAuthStyle: OAuth2AuthStyleHeader,
			}},
			want: `oauth_token_url is required when auth_mode is "oauth" and oauth_grant is "client_credentials"`,
		},
		{
			// A Config built from a connection still stored in a legacy mode
			// keeps naming that mode: it is what the operator will find in
			// the configuration file they are editing.
			name: "legacy mode",
			cfg: Config{AuthMode: AuthModeOAuth2AuthorizationCode, OAuth2: OAuth2Config{
				EndpointAuthStyle: OAuth2AuthStyleHeader,
			}},
			want: `oauth_token_url is required when auth_mode is "oauth2_authorization_code"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.ValidateAuth()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}
