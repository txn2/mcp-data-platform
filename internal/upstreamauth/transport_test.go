package upstreamauth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/membudget"
	"github.com/txn2/mcp-data-platform/internal/useragent"
)

func TestConfigAuthHeader(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want string
	}{
		{"none", Config{AuthMode: AuthModeNone}, ""},
		{"bearer", Config{AuthMode: AuthModeBearer}, "Authorization"},
		{"api_key header default", Config{AuthMode: AuthModeAPIKey, CredentialPlacement: CredentialPlacementHeader, APIKeyHeader: DefaultAPIKeyHeader}, DefaultAPIKeyHeader},
		{"api_key header custom", Config{AuthMode: AuthModeAPIKey, CredentialPlacement: CredentialPlacementHeader, APIKeyHeader: "X-My-Key"}, "X-My-Key"},
		{"api_key query", Config{AuthMode: AuthModeAPIKey, CredentialPlacement: CredentialPlacementQuery, APIKeyParam: "key"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.AuthHeader(); got != tc.want {
				t.Errorf("AuthHeader = %q; want %q", got, tc.want)
			}
		})
	}
}

func TestValidateCustomHeaders_RejectsAuthorization(t *testing.T) {
	err := Config{}.ValidateCustomHeaders(map[string]string{"AUTHORIZATION": "anything"})
	if err == nil {
		t.Error("Authorization header allowed")
	}
}

func TestValidateCustomHeaders_RejectsConfiguredAPIKeyHeader(t *testing.T) {
	cfg := Config{AuthMode: AuthModeAPIKey, CredentialPlacement: CredentialPlacementHeader, APIKeyHeader: "X-Custom-Key"}
	err := cfg.ValidateCustomHeaders(map[string]string{"x-custom-key": "spoof"})
	if err == nil {
		t.Error("configured api_key header allowed (case-insensitive check failed)")
	}
}

func TestValidateCustomHeaders_AllowsOtherHeaders(t *testing.T) {
	cfg := Config{AuthMode: AuthModeAPIKey, CredentialPlacement: CredentialPlacementHeader, APIKeyHeader: DefaultAPIKeyHeader}
	err := cfg.ValidateCustomHeaders(map[string]string{"Accept-Language": "en"})
	if err != nil {
		t.Errorf("unrelated header rejected: %v", err)
	}
}

func TestValidateCustomHeaders_RejectsStaticHeaderOverride(t *testing.T) {
	cfg := Config{StaticHeaders: map[string]string{"X-Goog-User-Project": "secret"}}
	err := cfg.ValidateCustomHeaders(map[string]string{"x-goog-user-project": "spoof"})
	if err == nil {
		t.Error("model attempt to override static header allowed (case-insensitive check failed)")
	}
}

func TestIsValidHeaderName(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"X-API-Key", true},
		{"X-Api-Version2", true},
		{"X-Subscription-Key", true},
		{"x-goog-user-project", true},
		{"Content-Type", true},
		{"", false},
		{"Bad Name", false},
		{"With\rCR", false},
		{"Colon:Inside", false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := isValidHeaderName(tc.in); got != tc.want {
				t.Errorf("isValidHeaderName(%q) = %v; want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestNewTokenExchangeClient_BadBundleFallsBackQuietly is the
// resilience contract: a CA bundle that fails to parse at runtime
// (impossible if Validate ran but possible if a caller bypassed it)
// must NOT panic or block token fetches with a nil transport. The
// fallback is the system default transport without the bundle, under
// the platform's User-Agent wrapper as every token client is (#1679);
// the request will then fail with a TLS error against the IdP and the
// operator gets a normal error path.
func TestNewTokenExchangeClient_BadBundleFallsBackQuietly(t *testing.T) {
	client := newTokenExchangeClient(Config{TLSCABundlePEM: "not pem"})
	require.NotNil(t, client)
	base, wrapped := useragent.Wraps(client.Transport)
	require.True(t, wrapped, "the token client must send the platform's User-Agent")
	assert.Same(t, http.DefaultTransport, base, "fallback must not attach a half-built transport")
}

// TestNewTokenExchangeClient_HonorsCABundle exercises the IdP-side CA
// trust plumbing for oauth2_client_credentials: when the IdP is
// signed by a private CA in tls_ca_bundle_pem, the token-fetch must
// succeed. The negative branch (no bundle) is implicit: without the
// trust the default RoundTripper would reject the IdP's cert.
func TestNewTokenExchangeClient_HonorsCABundle(t *testing.T) {
	ca := newTestCA(t)
	idpCert, idpKey := ca.issueServerCert(t, "127.0.0.1")
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"abc","token_type":"bearer","expires_in":3600}`)
	}))
	srv.TLS = &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{mustKeyPair(t, idpCert, idpKey)},
	}
	srv.StartTLS()
	defer srv.Close()

	cfg := Config{TLSCABundlePEM: ca.certPEM}
	client := newTokenExchangeClient(cfg)
	postReq, err := http.NewRequestWithContext(context.Background(),
		http.MethodPost, srv.URL+"/token",
		strings.NewReader(""))
	require.NoError(t, err)
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(postReq)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(body), "access_token")
}

// --- test CA helper --------------------------------------------------

// testCA is a minimal single-cert CA built per test. It exists so the
// token-exchange tests can stand up an IdP whose certificate chains to
// a bundle this package's config carries and to nothing the host
// trusts, which is the only way to prove the bundle is what made the
// handshake succeed.
type testCA struct {
	certPEM string
	cert    *x509.Certificate
	key     *rsa.PrivateKey
}

func newTestCA(t *testing.T) *testCA {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "upstreamauth-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return &testCA{
		certPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		cert:    cert,
		key:     key,
	}
}

// issueServerCert mints a leaf certificate for the given IP SAN,
// signed by this CA.
func (ca *testCA) issueServerCert(t *testing.T, ip string) (certPEM, keyPEM string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: ip},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP(ip)},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	require.NoError(t, err)
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
}

func mustKeyPair(t *testing.T, certPEM, keyPEM string) tls.Certificate {
	t.Helper()
	pair, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	require.NoError(t, err)
	return pair
}

// --- transport policy ------------------------------------------------

// TestValidateStaticHeaders walks the operator-config rules. Each
// refusal is a header that would either be silently overridden by the
// auth layer, be managed by net/http, or smuggle a second header
// through a CR/LF payload — all cases where accepting the config and
// failing later would be worse than refusing the save.
func TestValidateStaticHeaders(t *testing.T) {
	apiKeyCfg := func(headers map[string]string) Config {
		return Config{
			ErrPrefix:           "apigateway",
			AuthMode:            AuthModeAPIKey,
			Credential:          "k",
			CredentialPlacement: CredentialPlacementHeader,
			APIKeyHeader:        "X-Custom-Key",
			StaticHeaders:       headers,
		}
	}
	cases := []struct {
		name    string
		cfg     Config
		wantMsg string // empty = must pass
	}{
		{name: "no static headers", cfg: Config{}},
		{name: "ordinary header", cfg: apiKeyCfg(map[string]string{"X-Goog-User-Project": "proj"})},
		{name: "empty name", cfg: apiKeyCfg(map[string]string{"": "v"}), wantMsg: "empty header name"},
		{name: "invalid name", cfg: apiKeyCfg(map[string]string{"Bad Name": "v"}), wantMsg: "not permitted in an HTTP header name"},
		{name: "CRLF in value", cfg: apiKeyCfg(map[string]string{"X-Ok": "a\r\nX-Smuggled: 1"}), wantMsg: "header smuggling vector"},
		{name: "Authorization", cfg: apiKeyCfg(map[string]string{"authorization": "Bearer x"}), wantMsg: "must not set Authorization"},
		{name: "the auth mode's own header", cfg: apiKeyCfg(map[string]string{"x-custom-key": "spoof"}), wantMsg: "already managed by auth_mode"},
		{name: "hop-by-hop header", cfg: apiKeyCfg(map[string]string{"Connection": "close"}), wantMsg: "hop-by-hop"},
		{name: "net/http-managed header", cfg: apiKeyCfg(map[string]string{"Content-Length": "10"}), wantMsg: "net/http-managed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.ValidateStaticHeaders()
			if tc.wantMsg == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantMsg)
		})
	}
}

func TestIsReservedHopHeader(t *testing.T) {
	for _, name := range []string{
		"host", "Content-Length", "CONNECTION", "transfer-encoding", "Upgrade",
		"keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailer",
	} {
		if !isReservedHopHeader(name) {
			t.Errorf("isReservedHopHeader(%q) = false; want true", name)
		}
	}
	for _, name := range []string{"X-API-Key", "Accept", "x-goog-user-project"} {
		if isReservedHopHeader(name) {
			t.Errorf("isReservedHopHeader(%q) = true; want false", name)
		}
	}
}

func TestReadLimit(t *testing.T) {
	assert.Equal(t, int64(4096), ReadLimit(4096))
	assert.Equal(t, DefaultMaxResponseBytes, ReadLimit(0), "unset limit takes the default cap")
	assert.Equal(t, DefaultMaxResponseBytes, ReadLimit(-1), "a negative limit must not read nothing")
}

// TestReadBody covers the cap's three outcomes: a body under it comes
// back whole, a body over it comes back cut and flagged, and a reader
// that fails surfaces the error in the calling kind's voice.
func TestReadBody(t *testing.T) {
	t.Run("under the cap", func(t *testing.T) {
		body, truncated, err := ReadBody("apigateway", strings.NewReader("hello"), 1024)
		require.NoError(t, err)
		assert.False(t, truncated)
		assert.Equal(t, "hello", string(body))
	})
	t.Run("exactly at the cap is not truncated", func(t *testing.T) {
		body, truncated, err := ReadBody("apigateway", strings.NewReader("hello"), 5)
		require.NoError(t, err)
		assert.False(t, truncated)
		assert.Equal(t, "hello", string(body))
	})
	t.Run("over the cap", func(t *testing.T) {
		body, truncated, err := ReadBody("apigateway", strings.NewReader("hello world"), 5)
		require.NoError(t, err)
		assert.True(t, truncated)
		assert.Equal(t, "hello", string(body))
	})
	t.Run("zero cap takes the default", func(t *testing.T) {
		body, truncated, err := ReadBody("apigateway", strings.NewReader("hello"), 0)
		require.NoError(t, err)
		assert.False(t, truncated)
		assert.Equal(t, "hello", string(body))
	})
	t.Run("read error", func(t *testing.T) {
		_, _, err := ReadBody("apigateway", failingReader{}, 1024)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "apigateway: reading response body")
	})
}

// failingReader fails on the first Read so ReadBody's error arm is
// reachable without a live connection.
type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

// TestReserveBodyBudget pins the reservation rule that keeps a small
// response from tying up the full per-request cap: a declared
// Content-Length under the cap reserves only that much, while an
// unknown length reserves the whole cap because that is what the read
// may buffer.
func TestReserveBodyBudget(t *testing.T) {
	cases := []struct {
		name          string
		contentLength int64
		readCap       int64
		want          int64
	}{
		{"declared length under the cap", 100, 1024, 100},
		{"declared length over the cap", 4096, 1024, 1024},
		{"declared empty body", 0, 1024, 0},
		{"unknown length reserves the cap", -1, 1024, 1024},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			budget := membudget.New(1 << 20)
			reserved, ok := ReserveBodyBudget(budget, tc.contentLength, tc.readCap)
			assert.True(t, ok)
			assert.Equal(t, tc.want, reserved)
			budget.Release(reserved)
		})
	}
	t.Run("refused when the budget is exhausted", func(t *testing.T) {
		budget := membudget.New(512)
		_, ok := ReserveBodyBudget(budget, -1, 1024)
		assert.False(t, ok)
	})
	t.Run("a nil budget always grants", func(t *testing.T) {
		reserved, ok := ReserveBodyBudget(nil, -1, 1024)
		assert.True(t, ok)
		assert.Equal(t, int64(1024), reserved)
	})
}

// TestNewHTTPClient_WiresTimeoutsAndRefusesRedirects covers the
// transport policy a kind gets for free: the call timeout as the client
// deadline, the connect timeout on the dial and handshake, and a
// redirect that is returned rather than followed — the rule that stops
// a 302 from carrying the connection's credential to a host the
// operator never configured.
func TestNewHTTPClient_WiresTimeoutsAndRefusesRedirects(t *testing.T) {
	cfg := Config{ConnectTimeout: 1500 * time.Millisecond, CallTimeout: 30 * time.Second}
	client := NewHTTPClient(cfg)
	assert.Equal(t, cfg.CallTimeout, client.Timeout)
	require.NotNil(t, client.Transport, "a nil transport would silently fall back to http.DefaultTransport")

	base, wrapped := useragent.Wraps(client.Transport)
	require.True(t, wrapped, "the client must send the platform's User-Agent (#1679)")
	tr, ok := base.(*http.Transport)
	require.True(t, ok)
	assert.Equal(t, cfg.ConnectTimeout, tr.TLSHandshakeTimeout)
	require.NotNil(t, tr.DialContext, "DialContext is nil; ConnectTimeout cannot be enforced")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/from" {
			http.Redirect(w, r, "/to", http.StatusFound)
			return
		}
		_, _ = io.WriteString(w, "followed")
	}))
	defer srv.Close()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/from", http.NoBody)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusFound, resp.StatusCode, "the redirect must be returned, not followed")
}

// TestBuildTLSConfig covers the three shapes a connection's TLS
// material takes: none (so net/http keeps its defaults), a CA bundle
// alone, and a client keypair.
func TestBuildTLSConfig(t *testing.T) {
	ca := newTestCA(t)
	certPEM, keyPEM := ca.issueServerCert(t, "127.0.0.1")

	t.Run("no material yields no config", func(t *testing.T) {
		got, err := Config{}.BuildTLSConfig()
		require.NoError(t, err)
		assert.Nil(t, got, "nil lets http.Transport use its defaults")
	})
	t.Run("CA bundle only", func(t *testing.T) {
		got, err := Config{TLSCABundlePEM: ca.certPEM}.BuildTLSConfig()
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.NotNil(t, got.RootCAs)
		assert.Empty(t, got.Certificates)
	})
	t.Run("client keypair", func(t *testing.T) {
		got, err := Config{MTLSClientCertPEM: certPEM, MTLSClientKeyPEM: keyPEM}.BuildTLSConfig()
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Len(t, got.Certificates, 1)
	})
	t.Run("unusable keypair is refused in the kind's voice", func(t *testing.T) {
		_, err := Config{
			ErrPrefix:         "graphql",
			MTLSClientCertPEM: "not pem",
			MTLSClientKeyPEM:  "not pem",
		}.BuildTLSConfig()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "graphql: building mtls keypair")
	})
}

// TestValidateTLSMaterial_RequiresThePairUnderMTLS proves the auth mode
// reaches the TLS validator: under auth_mode=mtls the client keypair is
// the credential, so its absence is a refused connection rather than a
// handshake failure on the first call.
func TestValidateTLSMaterial_RequiresThePairUnderMTLS(t *testing.T) {
	err := Config{ErrPrefix: "apigateway", AuthMode: AuthModeMTLS}.ValidateTLSMaterial()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "apigateway: mtls_client_cert_pem and mtls_client_key_pem are required")

	require.NoError(t, Config{ErrPrefix: "apigateway", AuthMode: AuthModeNone}.ValidateTLSMaterial())
}

// TestNewHTTPTransport_AttachesTheConnectionsTLSConfig covers the arm
// that plumbs a connection's CA bundle into the transport. Without it
// an upstream behind a private CA would fail its handshake even though
// the operator configured the bundle.
func TestNewHTTPTransport_AttachesTheConnectionsTLSConfig(t *testing.T) {
	ca := newTestCA(t)
	tr := NewHTTPTransport(Config{TLSCABundlePEM: ca.certPEM})
	require.NotNil(t, tr.TLSClientConfig)
	assert.NotNil(t, tr.TLSClientConfig.RootCAs)

	bare := NewHTTPTransport(Config{})
	assert.Nil(t, bare.TLSClientConfig, "with no material net/http must keep its own defaults")
}
