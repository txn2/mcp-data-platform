package apigateway

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
)

// minRSABits is the smallest RSA key the toolkit will accept for an
// outbound client certificate. NIST SP 800-131A retires < 2048-bit
// RSA for signature generation in transitional use; 2048 is the
// public-CA baseline today. Operators with a hard requirement for
// 1024-bit keys on a legacy upstream should rotate the upstream, not
// the gateway's policy.
const minRSABits = 2048

// validateTLSMaterial enforces the per-connection mTLS and CA-trust
// rules. Three independent checks:
//
//  1. Cert + key are mutually required when either is set. A connection
//     with only a cert (and no key) cannot complete a TLS handshake;
//     refusing here surfaces the misconfiguration at admin write time
//     instead of as a runtime error on the first outbound call.
//
//  2. When mTLS material is present, the cert and key must parse as PEM,
//     the key must match the cert (tls.X509KeyPair checks via signature),
//     and the key algorithm must clear the minimum-strength bar
//     (RSA >= minRSABits, ECDSA P-256/P-384/P-521, or Ed25519). Weaker
//     keys are rejected.
//
//  3. The CA bundle must be a parseable PEM bundle with at least one
//     certificate when set. Empty string means "no extra CAs", which is
//     the existing default.
//
// auth_mode=mtls additionally requires the cert + key (the cert IS the
// credential). Layered modes (bearer/oauth2/etc.) may carry mTLS
// material without requiring it.
func (c Config) validateTLSMaterial() error {
	if c.AuthMode == AuthModeMTLS {
		if c.MTLSClientCertPEM == "" || c.MTLSClientKeyPEM == "" {
			return errors.New("apigateway: mtls_client_cert_pem and mtls_client_key_pem are required when auth_mode is \"mtls\"")
		}
	}
	if (c.MTLSClientCertPEM == "") != (c.MTLSClientKeyPEM == "") {
		return errors.New("apigateway: mtls_client_cert_pem and mtls_client_key_pem must both be set or both be empty")
	}
	if c.MTLSClientCertPEM != "" {
		if err := validateClientKeyPair(c.MTLSClientCertPEM, c.MTLSClientKeyPEM); err != nil {
			return err
		}
	}
	if c.TLSCABundlePEM != "" {
		if err := validateCABundle(c.TLSCABundlePEM); err != nil {
			return err
		}
	}
	return nil
}

// validateClientKeyPair returns an error if the cert/key pair would not
// load as a usable tls.Certificate. tls.X509KeyPair runs PEM decode,
// x509.ParseCertificate, and the key-matches-cert signature check; the
// extra leaf-cert parse here gives a clean place to enforce minimum
// key strength without re-deriving the key from raw bytes.
func validateClientKeyPair(certPEM, keyPEM string) error {
	pair, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		return fmt.Errorf("apigateway: mtls cert/key invalid: %s", sanitizeKeyPairError(err))
	}
	if len(pair.Certificate) == 0 {
		return errors.New("apigateway: mtls_client_cert_pem contained no certificates")
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return fmt.Errorf("apigateway: mtls leaf certificate unreadable: %s", err.Error())
	}
	return checkKeyStrength(leaf.PublicKey)
}

// sanitizeKeyPairError strips any PEM content from tls.X509KeyPair's
// error message before it reaches an operator-visible error path. The
// stdlib wraps the offending block in its error in some cases (e.g.,
// "tls: failed to parse private key: asn1: structure error: ..."), and
// while the private-key bytes are not directly included, the wrapping
// has historically included the encoded block on parse failures. The
// safe default is to surface the high-level reason without echoing the
// underlying string.
func sanitizeKeyPairError(err error) string {
	msg := err.Error()
	if strings.Contains(msg, "-----BEGIN") || strings.Contains(msg, "-----END") {
		return "cert or key contained unexpected PEM content (details redacted)"
	}
	return msg
}

// checkKeyStrength enforces minimum acceptable strength for the
// connection's mTLS private key. RSA below minRSABits is rejected;
// EC curves other than P-256 / P-384 / P-521 are rejected (P-224
// is not interoperable with most peers and stronger curves are not
// supported by Go's TLS stack as of this writing); Ed25519 is
// always accepted. Unknown key algorithms are rejected loudly.
func checkKeyStrength(pub any) error {
	switch k := pub.(type) {
	case *rsa.PublicKey:
		if k.N == nil || k.N.BitLen() < minRSABits {
			return fmt.Errorf("apigateway: mtls private key RSA-%d is below the minimum %d bits", k.N.BitLen(), minRSABits)
		}
		return nil
	case *ecdsa.PublicKey:
		switch k.Curve {
		case elliptic.P256(), elliptic.P384(), elliptic.P521():
			return nil
		}
		return errors.New("apigateway: mtls private key uses an unsupported ECDSA curve (want P-256, P-384, or P-521)")
	case ed25519.PublicKey:
		return nil
	default:
		return fmt.Errorf("apigateway: mtls private key uses an unsupported algorithm %T", pub)
	}
}

// validateCABundle parses every PEM block in the bundle as an
// x509.Certificate. An empty bundle returns an error (the caller has
// already filtered out the no-bundle case); a bundle with zero
// CERTIFICATE blocks (e.g., one that contains only PRIVATE KEY blocks)
// is rejected as misconfigured.
func validateCABundle(bundle string) error {
	rest := []byte(bundle)
	count := 0
	for len(rest) > 0 {
		block, remainder := pem.Decode(rest)
		if block == nil {
			break
		}
		rest = remainder
		if block.Type != "CERTIFICATE" {
			continue
		}
		if _, err := x509.ParseCertificate(block.Bytes); err != nil {
			return fmt.Errorf("apigateway: tls_ca_bundle_pem contains an unparseable certificate: %s", err.Error())
		}
		count++
	}
	if count == 0 {
		return errors.New("apigateway: tls_ca_bundle_pem must contain at least one CERTIFICATE block")
	}
	return nil
}

// buildTLSConfig returns a *tls.Config wired with this connection's
// client certificate (when configured) and an extended root CA pool
// (when a bundle is configured). Returns nil when neither is set so
// the http.Transport falls back to its default tls.Config behavior
// (system roots, no client cert).
//
// The returned config is safe for concurrent use across all
// outbound requests on this connection's transport. A connection
// reload rebuilds the transport in place (existing
// addParsedConnection pattern), so cert rotation does not require a
// process restart.
//
// Validation has already run via Config.Validate; this function does
// not re-check key strength or bundle parseability. It does still
// surface tls.X509KeyPair errors so a programmatic caller that
// bypassed Validate gets a clear failure instead of a nil-deref.
func buildTLSConfig(c Config) (*tls.Config, error) {
	hasClient := c.MTLSClientCertPEM != "" && c.MTLSClientKeyPEM != ""
	hasCABundle := c.TLSCABundlePEM != ""
	if !hasClient && !hasCABundle {
		return nil, nil //nolint:nilnil // nil config = use http.Transport defaults
	}
	out := &tls.Config{MinVersion: tls.VersionTLS12}
	if hasClient {
		pair, err := tls.X509KeyPair([]byte(c.MTLSClientCertPEM), []byte(c.MTLSClientKeyPEM))
		if err != nil {
			return nil, fmt.Errorf("apigateway: building mtls keypair: %s", sanitizeKeyPairError(err))
		}
		out.Certificates = []tls.Certificate{pair}
	}
	if hasCABundle {
		pool, err := rootPoolWithBundle(c.TLSCABundlePEM)
		if err != nil {
			return nil, err
		}
		out.RootCAs = pool
	}
	return out, nil
}

// rootPoolWithBundle appends the operator's CA bundle to the system
// root pool. Substituting (rather than appending) would silently
// break upstreams that legitimately use public CAs alongside the
// private one; that is a footgun we do not give the operator. When
// the system pool cannot be loaded (rare, e.g., a minimal
// distroless base without a CA cert bundle) we fall back to an
// empty pool plus the operator's bundle, which preserves the
// operator's intent without crashing.
func rootPoolWithBundle(bundle string) (*x509.CertPool, error) {
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if ok := pool.AppendCertsFromPEM([]byte(bundle)); !ok {
		return nil, errors.New("apigateway: tls_ca_bundle_pem contained no valid certificates")
	}
	return pool, nil
}
