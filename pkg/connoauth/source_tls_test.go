package connoauth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestNewTokenExchangeClient_EmptyBundleLeavesTransportNil keeps the
// default behavior intact for the common case (public IdP, no custom
// CA needed). A regression that attached an empty-pool tls.Config
// would silently break trust against system CAs.
func TestNewTokenExchangeClient_EmptyBundleLeavesTransportNil(t *testing.T) {
	t.Parallel()
	client, err := newTokenExchangeClient(Config{})
	if err != nil {
		t.Fatalf("unexpected error with empty bundle: %v", err)
	}
	if client == nil {
		t.Fatal("client must not be nil")
	}
	if client.Transport != nil {
		t.Fatalf("Transport must stay nil when no bundle is set (got %T)", client.Transport)
	}
	if client.Timeout != tokenFetchTimeout {
		t.Fatalf("Timeout=%v, want %v", client.Timeout, tokenFetchTimeout)
	}
}

// TestNewTokenExchangeClient_ValidBundleAttachesRootCAs proves the
// happy-path wiring: a parseable bundle produces a transport whose
// tls.Config carries the operator's RootCAs and meets the project's
// TLS 1.2 floor. The deeper end-to-end test below also exercises
// this against a real httptest TLS server.
func TestNewTokenExchangeClient_ValidBundleAttachesRootCAs(t *testing.T) {
	t.Parallel()
	caPEM := mustMintCAForTest(t)
	client, err := newTokenExchangeClient(Config{CABundlePEM: caPEM})
	if err != nil {
		t.Fatalf("newTokenExchangeClient: %v", err)
	}
	tr, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport must be *http.Transport, got %T", client.Transport)
	}
	if tr.TLSClientConfig == nil {
		t.Fatal("TLSClientConfig must be populated when a bundle is set")
	}
	if tr.TLSClientConfig.RootCAs == nil {
		t.Fatal("RootCAs must be populated")
	}
	if tr.TLSClientConfig.MinVersion < tls.VersionTLS12 {
		t.Fatalf("MinVersion below TLS 1.2: %d", tr.TLSClientConfig.MinVersion)
	}
}

// TestNewTokenExchangeClient_BadBundleReturnsError surfaces operator
// misconfiguration explicitly rather than silently degrading to
// system trust. The Exchange and Refresh callers propagate this so
// the admin layer can render a clear failure at Connect time.
func TestNewTokenExchangeClient_BadBundleReturnsError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		bundle string
	}{
		{"garbage", "not pem"},
		{"key blocks only", "-----BEGIN PRIVATE KEY-----\nMIIBVQIBA\n-----END PRIVATE KEY-----\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := newTokenExchangeClient(Config{CABundlePEM: tc.bundle})
			if err == nil {
				t.Fatal("expected error for invalid bundle")
			}
			if !contains(err.Error(), "ca_bundle_pem contained no valid certificates") {
				t.Fatalf("wrong error message: %v", err)
			}
		})
	}
}

// TestExchange_BadCABundleSurfacesErrorBeforeNetwork is the
// caller-side proof for Fix 3. A connection with an invalid bundle
// must fail at the gate, not silently fall back to system trust and
// then time out or hand a misleading TLS error to the operator.
func TestExchange_BadCABundleSurfacesErrorBeforeNetwork(t *testing.T) {
	t.Parallel()
	_, err := Exchange(context.Background(), ExchangeInput{
		Config: Config{
			TokenURL:     "https://idp.example/token",
			ClientID:     "c",
			ClientSecret: "s",
			CABundlePEM:  "not a pem block",
		},
		Code:         "code",
		CodeVerifier: "v",
		RedirectURI:  "https://platform.example/cb",
	})
	if err == nil {
		t.Fatal("Exchange must surface bundle parse failure")
	}
	if !contains(err.Error(), "ca_bundle_pem contained no valid certificates") {
		t.Fatalf("wrong error: %v", err)
	}
}

// TestRefresh_BadCABundleSurfacesErrorBeforeNetwork mirrors the
// Exchange check for the silent-refresh path. Without this guard,
// every background refresh tick against a misconfigured connection
// would spend the full tokenFetchTimeout dialing before failing.
func TestRefresh_BadCABundleSurfacesErrorBeforeNetwork(t *testing.T) {
	t.Parallel()
	_, err := Refresh(context.Background(), RefreshInput{
		Config: Config{
			TokenURL:     "https://idp.example/token",
			ClientID:     "c",
			ClientSecret: "s",
			CABundlePEM:  "not a pem block",
		},
		RefreshToken: "rt",
	})
	if err == nil {
		t.Fatal("Refresh must surface bundle parse failure")
	}
	if !contains(err.Error(), "ca_bundle_pem contained no valid certificates") {
		t.Fatalf("wrong error: %v", err)
	}
}

// TestExchange_HonorsCABundleAgainstPrivateCA is the end-to-end
// proof: an IdP signed by a CA outside system trust succeeds when
// the bundle is set, fails without it. Without this test, a future
// regression that wired the bundle into Transport but forgot to
// populate RootCAs (or accidentally substituted instead of
// appending) would still pass the assemble-shape tests above.
func TestExchange_HonorsCABundleAgainstPrivateCA(t *testing.T) {
	t.Parallel()
	caPEM, caCert, caKey := mintCAReturningParts(t)
	srvCertPEM, srvKeyPEM := signServerCert(t, caCert, caKey, "127.0.0.1")
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"a","refresh_token":"r","expires_in":3600,"token_type":"Bearer"}`))
	}))
	pair, err := tls.X509KeyPair([]byte(srvCertPEM), []byte(srvKeyPEM))
	if err != nil {
		t.Fatalf("X509KeyPair: %v", err)
	}
	srv.TLS = &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{pair},
	}
	srv.StartTLS()
	defer srv.Close()

	// Subtests run sequentially so the deferred srv.Close above does
	// not race the dial. httptest.Server.URL plus t.Parallel on a
	// subtest is the classic "server closed before child runs" bug;
	// keep the outer test parallel, keep the subtests serial.

	t.Run("with bundle: succeeds", func(t *testing.T) {
		_, err := Exchange(context.Background(), ExchangeInput{
			Config: Config{
				TokenURL: srv.URL + "/token", ClientID: "c", ClientSecret: "s",
				CABundlePEM: caPEM,
			},
			Code: "code", CodeVerifier: "v", RedirectURI: "https://x/cb",
		})
		if err != nil {
			t.Fatalf("with valid bundle, exchange must succeed: %v", err)
		}
	})

	t.Run("without bundle: handshake fails", func(t *testing.T) {
		_, err := Exchange(context.Background(), ExchangeInput{
			Config: Config{
				TokenURL: srv.URL + "/token", ClientID: "c", ClientSecret: "s",
			},
			Code: "code", CodeVerifier: "v", RedirectURI: "https://x/cb",
		})
		if err == nil {
			t.Fatal("without bundle, the private-CA server must fail verification")
		}
	})
}

// --- test CA helpers (kept local to avoid leaking a generic helper) ---

// contains is a local strings.Contains so this file's import set
// stays minimal; strings is already imported by source_test.go but
// importing it here would be redundant given the single call site.
func contains(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func mustMintCAForTest(t *testing.T) string {
	t.Helper()
	pemStr, _, _ := mintCAReturningParts(t)
	return pemStr
}

// mintCAReturningParts mints a self-signed CA usable both as a
// CABundlePEM input and as the issuer for downstream leaf certs in
// the end-to-end test.
func mintCAReturningParts(t *testing.T) (string, *x509.Certificate, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: "connoauth-tls-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("ParseCertificate: %v", err)
	}
	buf := bytes.NewBuffer(nil)
	if err := pem.Encode(buf, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		t.Fatalf("pem.Encode: %v", err)
	}
	return buf.String(), cert, key
}

// signServerCert issues a server cert signed by the given CA for the
// given IP SAN (the loopback address httptest uses). Returns cert
// and key PEM strings.
func signServerCert(t *testing.T, ca *x509.Certificate, caKey *rsa.PrivateKey, ipSAN string) (string, string) {
	t.Helper()
	leafKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("leaf key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "connoauth-tls-test-server"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP(ipSAN)},
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("leaf CreateCertificate: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalPKCS8PrivateKey(leafKey)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return string(certPEM), string(keyPEM)
}
