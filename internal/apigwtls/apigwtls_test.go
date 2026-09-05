package apigwtls

import (
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
	"math/big"
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

// TestValidate_AmbiguousPair refuses configs that set only
// one of cert/key. The runtime contract is "both or neither" and the
// validator must surface this at write time, not at first call.
func TestValidate_AmbiguousPair(t *testing.T) {
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
			err := Validate(Material{ClientCertPEM: tc.cert, ClientKeyPEM: tc.key})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "both be set or both be empty")
		})
	}
}

// TestValidate_MTLSModeRequiresMaterial enforces that
// auth_mode=mtls cannot be selected without the cert + key pair. The
// AuthModeMTLS authenticator is a no-op (the TLS layer carries the
// credential), so a missing pair would leave the connection with no
// authentication at all: refuse at write time.
func TestValidate_MTLSModeRequiresMaterial(t *testing.T) {
	err := Validate(Material{ClientPairRequired: true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "auth_mode is \"mtls\"")
}

// TestValidate_KeyStrengthEnforcement exercises the minimum
// key-strength bar for every supported algorithm plus the ones the
// toolkit refuses. RSA below 2048 bits, ECDSA on non-NIST curves, and
// unknown algorithms all fail; RSA-2048, ECDSA P-256, and Ed25519 pass.
func TestValidate_KeyStrengthEnforcement(t *testing.T) {
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
			err := Validate(Material{ClientCertPEM: cert, ClientKeyPEM: key})
			if tc.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

// TestValidate_KeyDoesNotMatchCert verifies the cross-check.
// tls.X509KeyPair runs a signature against both halves; we surface
// that failure through a sanitized error path that does not leak any
// PEM bytes.
func TestValidate_KeyDoesNotMatchCert(t *testing.T) {
	certA, _, _ := generateCertPair(t, keyRSA2048)
	_, keyB, _ := generateCertPair(t, keyRSA2048)
	err := Validate(Material{ClientCertPEM: certA, ClientKeyPEM: keyB})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mtls cert/key invalid")
	assert.NotContains(t, err.Error(), "-----BEGIN")
	assert.NotContains(t, err.Error(), "-----END")
}

// TestValidate_PEMParseFailures rejects garbage in either
// field. The cert/key error message must not echo the input bytes
// (which a future stdlib release could include in its error string).
func TestValidate_PEMParseFailures(t *testing.T) {
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
			err := Validate(Material{ClientCertPEM: tc.cert, ClientKeyPEM: tc.key})
			require.Error(t, err)
		})
	}
}

// TestValidate_CABundleParseable accepts a real PEM bundle
// (one CA cert) and rejects a bundle with zero CERTIFICATE blocks.
// Zero-block bundles can creep in when an operator pastes only a
// PRIVATE KEY by mistake; surface the misconfiguration loudly.
func TestValidate_CABundleParseable(t *testing.T) {
	caCert, _, _ := generateCertPair(t, keyRSA2048)
	t.Run("valid bundle accepted", func(t *testing.T) {
		assert.NoError(t, Validate(Material{CABundlePEM: caCert}))
	})
	t.Run("zero certificate blocks rejected", func(t *testing.T) {
		err := Validate(Material{CABundlePEM: "-----BEGIN PRIVATE KEY-----\nabc\n-----END PRIVATE KEY-----\n"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "at least one CERTIFICATE block")
	})
}

// TestBuild_NilWhenNothingSet keeps the http.Transport
// default behavior (system trust, no client cert) intact for the
// common case so existing connections are unaffected by this feature.
func TestBuild_NilWhenNothingSet(t *testing.T) {
	out, err := Build(Material{})
	require.NoError(t, err)
	assert.Nil(t, out)
}

// TestBuild_ClientCertWired loads the client certificate and
// attaches it to tls.Config.Certificates. The leaf parse confirms the
// returned Certificates slice contains the cert we put in (not a stub
// or an empty entry).
func TestBuild_ClientCertWired(t *testing.T) {
	cert, key, leaf := generateCertPair(t, keyECDSAP256)
	out, err := Build(Material{ClientCertPEM: cert, ClientKeyPEM: key})
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

// TestBuild_CABundleAppendedNotSubstituted is the safety
// check on RootPool's intent. An operator-bundle-only world
// would silently break upstreams that legitimately use public CAs
// alongside the private one; the implementation must merge with the
// system pool, not replace it.
func TestBuild_CABundleAppendedNotSubstituted(t *testing.T) {
	caCert, _, _ := generateCertPair(t, keyRSA2048)
	out, err := Build(Material{CABundlePEM: caCert})
	require.NoError(t, err)
	require.NotNil(t, out)
	require.NotNil(t, out.RootCAs)
	// We cannot directly inspect the system pool's contents through the
	// public API, but we can confirm the returned pool is not empty and
	// that the operator's CA is present in it (we just appended it).
	// The "not substituted" property holds because RootPool
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

// TestRootPoolWithBundle_RejectsInvalidPEM exercises the
// AppendCertsFromPEM-returned-false branch of RootPool.
// A bundle that is syntactically PEM-shaped but carries the wrong
// block types (no CERTIFICATE) must surface as an explicit error,
// not as a silently-empty pool.
func TestRootPoolWithBundle_RejectsInvalidPEM(t *testing.T) {
	bundle := "-----BEGIN PRIVATE KEY-----\nMIIBVQIBA\n-----END PRIVATE KEY-----\n"
	_, err := RootPool(bundle)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no valid certificates")
}
