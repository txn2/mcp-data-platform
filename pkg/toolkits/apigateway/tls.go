package apigateway

import (
	"crypto/tls"

	"github.com/txn2/mcp-data-platform/internal/apigwtls"
)

// tlsMaterial is this connection's TLS material as the validator and the
// transport builder take it. auth_mode=mtls makes the client certificate the
// credential, so the pair is required there and optional under a layered mode.
func (c Config) tlsMaterial() apigwtls.Material {
	return apigwtls.Material{
		ClientCertPEM:      c.MTLSClientCertPEM,
		ClientKeyPEM:       c.MTLSClientKeyPEM,
		CABundlePEM:        c.TLSCABundlePEM,
		ClientPairRequired: c.AuthMode == AuthModeMTLS,
	}
}

// validateTLSMaterial enforces the per-connection mTLS and CA-trust rules, so
// a misconfiguration is refused at admin write time rather than on the first
// outbound call.
func (c Config) validateTLSMaterial() error {
	//nolint:wrapcheck // the message names the subsystem an operator sees refuse the write; wrapping would prefix it twice
	return apigwtls.Validate(c.tlsMaterial())
}

// buildTLSConfig returns the *tls.Config this connection's transport presents,
// or nil when it carries neither a client keypair nor a CA bundle.
func buildTLSConfig(c Config) (*tls.Config, error) {
	//nolint:wrapcheck // as above: the error is already an operator-facing "apigateway: ..." message
	return apigwtls.Build(c.tlsMaterial())
}
