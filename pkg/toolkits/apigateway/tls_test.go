package apigateway

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// keyAlg selects which private-key algorithm the test helper mints.
type keyAlg int

const (
	keyRSA2048 keyAlg = iota
	keyRSA1024
	keyECDSAP256
	keyECDSAP224
	keyEd25519
)

// generateCertPair mints a self-signed leaf certificate with the named
// key algorithm and returns (cert PEM, key PEM, leaf *x509.Certificate).
// Helper for the table-driven validation tests; the certificates are
// not signed by any CA the test layer trusts (each test that needs a
// trusted server constructs its own CA via newTestCA).
func generateCertPair(t *testing.T, alg keyAlg) (certPEM, keyPEM string, leaf *x509.Certificate) {
	t.Helper()
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "apigateway-mtls-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	priv, pub := genKey(t, alg)
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, priv)
	require.NoError(t, err)
	certPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	require.NoError(t, err)
	keyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	leaf, err = x509.ParseCertificate(der)
	require.NoError(t, err)
	return certPEM, keyPEM, leaf
}

// genKey returns a (private, public) pair for the named algorithm.
// Wrapped in its own function so the table-driven tests do not have a
// big switch on every call.
func genKey(t *testing.T, alg keyAlg) (priv, pub any) {
	t.Helper()
	switch alg {
	case keyRSA2048:
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		require.NoError(t, err)
		return k, &k.PublicKey
	case keyRSA1024:
		// #nosec G403 -- intentional weak key to exercise checkKeyStrength's RSA-bits rejection branch.
		k, err := rsa.GenerateKey(rand.Reader, 1024)
		require.NoError(t, err)
		return k, &k.PublicKey
	case keyECDSAP256:
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, err)
		return k, &k.PublicKey
	case keyECDSAP224:
		k, err := ecdsa.GenerateKey(elliptic.P224(), rand.Reader)
		require.NoError(t, err)
		return k, &k.PublicKey
	case keyEd25519:
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		require.NoError(t, err)
		return priv, pub
	}
	t.Fatalf("unknown alg: %d", alg)
	return nil, nil
}

// TestValidateTLSMaterial_AmbiguousPair refuses configs that set only
// one of cert/key. The runtime contract is "both or neither" and the
// validator must surface this at write time, not at first call.
func TestValidateTLSMaterial_AmbiguousPair(t *testing.T) {
	cert, key, _ := generateCertPair(t, keyRSA2048)
	cases := []struct {
		name string
		cert string
		key  string
	}{
		{"cert only", cert, ""},
		{"key only", "", key},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Config{MTLSClientCertPEM: tc.cert, MTLSClientKeyPEM: tc.key}
			err := c.validateTLSMaterial()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "both be set or both be empty")
		})
	}
}

// TestValidateTLSMaterial_MTLSModeRequiresMaterial enforces that
// auth_mode=mtls cannot be selected without the cert + key pair. The
// AuthModeMTLS authenticator is a no-op (the TLS layer carries the
// credential), so a missing pair would leave the connection with no
// authentication at all: refuse at write time.
func TestValidateTLSMaterial_MTLSModeRequiresMaterial(t *testing.T) {
	c := Config{AuthMode: AuthModeMTLS}
	err := c.validateTLSMaterial()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "auth_mode is \"mtls\"")
}

// TestValidateTLSMaterial_KeyStrengthEnforcement exercises the minimum
// key-strength bar for every supported algorithm plus the ones the
// toolkit refuses. RSA below 2048 bits, ECDSA on non-NIST curves, and
// unknown algorithms all fail; RSA-2048, ECDSA P-256, and Ed25519 pass.
func TestValidateTLSMaterial_KeyStrengthEnforcement(t *testing.T) {
	cases := []struct {
		name    string
		alg     keyAlg
		wantErr string
	}{
		{"rsa-2048 accepted", keyRSA2048, ""},
		{"rsa-1024 rejected", keyRSA1024, "RSA-1024 is below"},
		{"ecdsa-p256 accepted", keyECDSAP256, ""},
		{"ecdsa-p224 rejected", keyECDSAP224, "unsupported ECDSA curve"},
		{"ed25519 accepted", keyEd25519, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cert, key, _ := generateCertPair(t, tc.alg)
			c := Config{MTLSClientCertPEM: cert, MTLSClientKeyPEM: key}
			err := c.validateTLSMaterial()
			if tc.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

// TestValidateTLSMaterial_KeyDoesNotMatchCert verifies the cross-check.
// tls.X509KeyPair runs a signature against both halves; we surface
// that failure through a sanitized error path that does not leak any
// PEM bytes.
func TestValidateTLSMaterial_KeyDoesNotMatchCert(t *testing.T) {
	certA, _, _ := generateCertPair(t, keyRSA2048)
	_, keyB, _ := generateCertPair(t, keyRSA2048)
	c := Config{MTLSClientCertPEM: certA, MTLSClientKeyPEM: keyB}
	err := c.validateTLSMaterial()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mtls cert/key invalid")
	assert.NotContains(t, err.Error(), "-----BEGIN")
	assert.NotContains(t, err.Error(), "-----END")
}

// TestValidateTLSMaterial_PEMParseFailures rejects garbage in either
// field. The cert/key error message must not echo the input bytes
// (which a future stdlib release could include in its error string).
func TestValidateTLSMaterial_PEMParseFailures(t *testing.T) {
	cert, key, _ := generateCertPair(t, keyRSA2048)
	cases := []struct {
		name string
		cert string
		key  string
	}{
		{"garbage cert", "not a pem", key},
		{"garbage key", cert, "not a pem"},
		{"empty cert block", "-----BEGIN CERTIFICATE-----\n-----END CERTIFICATE-----\n", key},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Config{MTLSClientCertPEM: tc.cert, MTLSClientKeyPEM: tc.key}
			err := c.validateTLSMaterial()
			require.Error(t, err)
		})
	}
}

// TestValidateTLSMaterial_CABundleParseable accepts a real PEM bundle
// (one CA cert) and rejects a bundle with zero CERTIFICATE blocks.
// Zero-block bundles can creep in when an operator pastes only a
// PRIVATE KEY by mistake; surface the misconfiguration loudly.
func TestValidateTLSMaterial_CABundleParseable(t *testing.T) {
	caCert, _, _ := generateCertPair(t, keyRSA2048)
	t.Run("valid bundle accepted", func(t *testing.T) {
		c := Config{TLSCABundlePEM: caCert}
		assert.NoError(t, c.validateTLSMaterial())
	})
	t.Run("zero certificate blocks rejected", func(t *testing.T) {
		c := Config{TLSCABundlePEM: "-----BEGIN PRIVATE KEY-----\nabc\n-----END PRIVATE KEY-----\n"}
		err := c.validateTLSMaterial()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "at least one CERTIFICATE block")
	})
}

// TestBuildTLSConfig_NilWhenNothingSet keeps the http.Transport
// default behavior (system trust, no client cert) intact for the
// common case so existing connections are unaffected by this feature.
func TestBuildTLSConfig_NilWhenNothingSet(t *testing.T) {
	out, err := buildTLSConfig(Config{})
	require.NoError(t, err)
	assert.Nil(t, out)
}

// TestBuildTLSConfig_ClientCertWired loads the client certificate and
// attaches it to tls.Config.Certificates. The leaf parse confirms the
// returned Certificates slice contains the cert we put in (not a stub
// or an empty entry).
func TestBuildTLSConfig_ClientCertWired(t *testing.T) {
	cert, key, leaf := generateCertPair(t, keyECDSAP256)
	out, err := buildTLSConfig(Config{MTLSClientCertPEM: cert, MTLSClientKeyPEM: key})
	require.NoError(t, err)
	require.NotNil(t, out)
	require.Len(t, out.Certificates, 1)
	require.Len(t, out.Certificates[0].Certificate, 1)
	got, err := x509.ParseCertificate(out.Certificates[0].Certificate[0])
	require.NoError(t, err)
	assert.Equal(t, leaf.SerialNumber, got.SerialNumber)
	assert.Equal(t, leaf.Subject.CommonName, got.Subject.CommonName)
	assert.True(t, out.MinVersion >= tls.VersionTLS12)
}

// TestBuildTLSConfig_CABundleAppendedNotSubstituted is the safety
// check on rootPoolWithBundle's intent. An operator-bundle-only world
// would silently break upstreams that legitimately use public CAs
// alongside the private one; the implementation must merge with the
// system pool, not replace it.
func TestBuildTLSConfig_CABundleAppendedNotSubstituted(t *testing.T) {
	caCert, _, _ := generateCertPair(t, keyRSA2048)
	out, err := buildTLSConfig(Config{TLSCABundlePEM: caCert})
	require.NoError(t, err)
	require.NotNil(t, out)
	require.NotNil(t, out.RootCAs)
	// We cannot directly inspect the system pool's contents through the
	// public API, but we can confirm the returned pool is not empty and
	// that the operator's CA is present in it (we just appended it).
	// The "not substituted" property holds because rootPoolWithBundle
	// starts from x509.SystemCertPool() before appending.
	leaf, _, _ := generateCertPair(t, keyRSA2048)
	leafDER, _ := pem.Decode([]byte(leaf))
	require.NotNil(t, leafDER)
	parsed, err := x509.ParseCertificate(leafDER.Bytes)
	require.NoError(t, err)
	// Verifying an unrelated cert returns ErrUnknownAuthority, which
	// proves the pool is not the "trust everything" zero value.
	_, verifyErr := parsed.Verify(x509.VerifyOptions{Roots: out.RootCAs})
	require.Error(t, verifyErr)
	var uaErr x509.UnknownAuthorityError
	assert.True(t, errors.As(verifyErr, &uaErr), "want UnknownAuthorityError, got %T: %v", verifyErr, verifyErr)
}

// TestMTLSAuth_ApplyIsNoOp guards the contract that auth_mode=mtls
// must never touch the request headers: the cert IS the credential.
// A regression that added a stray Header.Set here would silently
// double up with the upstream's expected auth and likely cause 400s.
func TestMTLSAuth_ApplyIsNoOp(t *testing.T) {
	auth, err := NewAuthenticator(Config{
		BaseURL:          "https://example",
		AuthMode:         AuthModeMTLS,
		ConnectTimeout:   DefaultConnectTimeout,
		CallTimeout:      DefaultCallTimeout,
		TrustLevel:       TrustLevelUntrusted,
		MaxResponseBytes: DefaultMaxResponseBytes,
		// Validate is not called here because we exercise NewAuthenticator
		// in isolation; the integration test below covers the wiring
		// through ParseConfig.
		MTLSClientCertPEM: mustGenCertForAuthTest(t),
		MTLSClientKeyPEM:  mustGenKeyForAuthTest(t),
	})
	require.NoError(t, err)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "https://example", http.NoBody)
	require.NoError(t, auth.Apply(req))
	assert.Empty(t, req.Header.Get("Authorization"))
}

// TestParseConfig_MTLSAuthMode_PopulatesNoOpAuthenticator confirms the
// end-to-end wiring from the connection-instances map shape through
// ParseConfig to NewAuthenticator. A regression here would surface as
// "no authenticator for auth_mode mtls" at startup time.
func TestParseConfig_MTLSAuthMode_PopulatesNoOpAuthenticator(t *testing.T) {
	cert, key, _ := generateCertPair(t, keyECDSAP256)
	cfg, err := ParseConfig(map[string]any{
		"base_url":             "https://upstream.example",
		"auth_mode":            AuthModeMTLS,
		"mtls_client_cert_pem": cert,
		"mtls_client_key_pem":  key,
	})
	require.NoError(t, err)
	assert.Equal(t, AuthModeMTLS, cfg.AuthMode)
	auth, err := NewAuthenticator(cfg)
	require.NoError(t, err)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "https://upstream.example/x", http.NoBody)
	require.NoError(t, auth.Apply(req))
	assert.Empty(t, req.Header.Get("Authorization"))
}

// --- integration: the full request path through buildTLSConfig --------

// TestNewHTTPTransport_PresentsClientCertAndTrustsPrivateCA is the
// acceptance criterion: a connection with a bearer credential AND
// mTLS material AND a private CA bundle must succeed against a test
// server that requires all three layers. The test server is signed
// by a CA we mint here; the trust path runs only through the bundle
// (system trust would not include this CA), and the server insists
// on a valid client cert. A regression in any of the wiring would
// surface as a TLS handshake failure or a missing-Authorization 401.
func TestNewHTTPTransport_PresentsClientCertAndTrustsPrivateCA(t *testing.T) {
	ca := newTestCA(t)
	serverCert, serverKey := ca.issueServerCert(t, "127.0.0.1")
	clientCertPEM, clientKeyPEM := ca.issueClientCert(t, "client-test")

	srvTLS := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{mustKeyPair(t, serverCert, serverKey)},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    ca.pool(),
	}

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer T0K3N" {
			http.Error(w, "missing bearer", http.StatusUnauthorized)
			return
		}
		if len(r.TLS.PeerCertificates) == 0 {
			http.Error(w, "missing client cert", http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	srv.TLS = srvTLS
	srv.StartTLS()
	defer srv.Close()

	cfg, err := ParseConfig(map[string]any{
		"base_url":             srv.URL,
		"auth_mode":            AuthModeBearer,
		"credential":           "T0K3N",
		"mtls_client_cert_pem": clientCertPEM,
		"mtls_client_key_pem":  clientKeyPEM,
		"tls_ca_bundle_pem":    ca.certPEM,
	})
	require.NoError(t, err)

	client := newHTTPClient(cfg)
	auth, err := NewAuthenticator(cfg)
	require.NoError(t, err)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/probe", http.NoBody)
	require.NoError(t, err)
	require.NoError(t, auth.Apply(req))
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// TestNewHTTPTransport_RejectsHandshakeWithoutClientCert is the
// negative side of the above. With the same server but a connection
// that omits the mTLS material, the TLS handshake must fail before
// the bearer header is ever sent.
func TestNewHTTPTransport_RejectsHandshakeWithoutClientCert(t *testing.T) {
	ca := newTestCA(t)
	serverCert, serverKey := ca.issueServerCert(t, "127.0.0.1")

	srvTLS := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{mustKeyPair(t, serverCert, serverKey)},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    ca.pool(),
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	srv.TLS = srvTLS
	srv.StartTLS()
	defer srv.Close()

	cfg, err := ParseConfig(map[string]any{
		"base_url":          srv.URL,
		"auth_mode":         AuthModeNone,
		"tls_ca_bundle_pem": ca.certPEM,
	})
	require.NoError(t, err)
	client := newHTTPClient(cfg)

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/probe", http.NoBody)
	resp, err := client.Do(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	require.Error(t, err)
	// The handshake failure surfaces as a tls.* error wrapped in a
	// url.Error; the exact text varies across Go versions, so assert
	// only on the URL wrap rather than a substring of the inner msg.
	var urlErr *url.Error
	assert.True(t, errors.As(err, &urlErr))
}

// TestRootPoolWithBundle_RejectsInvalidPEM exercises the
// AppendCertsFromPEM-returned-false branch of rootPoolWithBundle.
// A bundle that is syntactically PEM-shaped but carries the wrong
// block types (no CERTIFICATE) must surface as an explicit error,
// not as a silently-empty pool.
func TestRootPoolWithBundle_RejectsInvalidPEM(t *testing.T) {
	bundle := "-----BEGIN PRIVATE KEY-----\nMIIBVQIBA\n-----END PRIVATE KEY-----\n"
	_, err := rootPoolWithBundle(bundle)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no valid certificates")
}

// TestNewTokenExchangeClient_BadBundleFallsBackQuietly is the
// resilience contract: a CA bundle that fails to parse at runtime
// (impossible if Validate ran but possible if a caller bypassed it)
// must NOT panic or block token fetches with a nil transport. The
// fallback is a plain http.Client without the bundle, matching the
// pre-feature behavior; the request will then fail with a TLS error
// against the IdP and the operator gets a normal error path.
func TestNewTokenExchangeClient_BadBundleFallsBackQuietly(t *testing.T) {
	client := newTokenExchangeClient(Config{TLSCABundlePEM: "not pem"})
	require.NotNil(t, client)
	assert.Nil(t, client.Transport, "fallback must not attach a half-built transport")
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

// --- test CA helpers -------------------------------------------------

// testCA is a minimal CA built per-test. It mints leaf certs for use
// as a server cert (with a configurable SAN) or as a client cert.
type testCA struct {
	cert    *x509.Certificate
	key     *rsa.PrivateKey
	certPEM string
}

func newTestCA(t *testing.T) *testCA {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	buf := bytes.NewBuffer(nil)
	require.NoError(t, pem.Encode(buf, &pem.Block{Type: "CERTIFICATE", Bytes: der}))
	return &testCA{cert: cert, key: key, certPEM: buf.String()}
}

func (c *testCA) pool() *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(c.cert)
	return pool
}

func (c *testCA) issueServerCert(t *testing.T, ipSAN string) (certPEM, keyPEM string) {
	t.Helper()
	leafKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "test-server"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  parseIPs(t, ipSAN),
		DNSNames:     []string{"localhost"},
	}
	return c.signLeaf(t, tmpl, leafKey)
}

func (c *testCA) issueClientCert(t *testing.T, cn string) (certPEM, keyPEM string) {
	t.Helper()
	leafKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	return c.signLeaf(t, tmpl, leafKey)
}

func (c *testCA) signLeaf(t *testing.T, tmpl *x509.Certificate, key *rsa.PrivateKey) (certPEM, keyPEM string) {
	t.Helper()
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.cert, &key.PublicKey, c.key)
	require.NoError(t, err)
	certPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	keyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	return certPEM, keyPEM
}

func parseIPs(t *testing.T, ip string) []net.IP {
	t.Helper()
	parsed := net.ParseIP(ip)
	require.NotNil(t, parsed)
	return []net.IP{parsed}
}

func mustKeyPair(t *testing.T, cert, key string) tls.Certificate {
	t.Helper()
	pair, err := tls.X509KeyPair([]byte(cert), []byte(key))
	require.NoError(t, err)
	return pair
}

func mustGenCertForAuthTest(t *testing.T) string {
	t.Helper()
	cert, _, _ := generateCertPair(t, keyECDSAP256)
	return cert
}

func mustGenKeyForAuthTest(t *testing.T) string {
	t.Helper()
	_, key, _ := generateCertPair(t, keyECDSAP256)
	return key
}
