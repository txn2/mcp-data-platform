// Package oidcdiscovery fetches and parses an OpenID Connect provider's
// discovery document (<issuer>/.well-known/openid-configuration, per OpenID
// Connect Discovery 1.0 / RFC 8414).
//
// It is a dependency-root leaf so both pkg/auth (which needs jwks_uri for JWKS
// validation) and pkg/oauth (which needs authorization_endpoint/token_endpoint
// for the brokered login flow) can share ONE fetch/parse implementation rather
// than each carrying its own copy that could drift.
package oidcdiscovery

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// WellKnownPath is the discovery path appended to an issuer URL.
const WellKnownPath = "/.well-known/openid-configuration"

// maxDocumentBytes caps the discovery response body read into memory. Real
// discovery documents are a few KB; this bounds a misconfigured, compromised, or
// MITM'd issuer from returning a huge body and exhausting memory.
const maxDocumentBytes = 1 << 20 // 1 MiB

// Document is the subset of an OIDC provider's discovery metadata the platform
// consumes. Fields absent from the provider's document decode to the empty
// string; callers validate the ones they require.
type Document struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
}

// Fetch retrieves and parses <issuer>/.well-known/openid-configuration using the
// provided HTTP client. It returns an error on a non-200 response or an
// undecodable body; it does NOT validate that any particular endpoint is
// present, leaving that to the caller.
func Fetch(ctx context.Context, client *http.Client, issuer string) (*Document, error) {
	if client == nil {
		client = http.DefaultClient
	}

	// Trim whitespace/newlines that commonly ride along from YAML block scalars
	// or copy-paste, which would otherwise produce a malformed discovery URL.
	discoveryURL := strings.TrimSuffix(strings.TrimSpace(issuer), "/") + WellKnownPath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("creating discovery request: %w", err)
	}

	resp, err := client.Do(req) // #nosec G704 -- URL from admin-controlled OIDC issuer config
	if err != nil {
		return nil, fmt.Errorf("fetching discovery document: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("discovery request failed: %d", resp.StatusCode)
	}

	var doc Document
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxDocumentBytes)).Decode(&doc); err != nil {
		return nil, fmt.Errorf("parsing discovery document: %w", err)
	}
	return &doc, nil
}
