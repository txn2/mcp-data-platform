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
