package connoauth

import (
	"fmt"
	"log/slog"
	"net/url"
	"sync"

	"github.com/txn2/mcp-data-platform/internal/logsan"
)

// cleartextWarned dedups the cleartext-endpoint warning to one line per
// (kind, name, config key). ParseConfig runs on the connection-load and
// token-exchange paths, so an undeduped warning would repeat for the
// life of the process.
var cleartextWarned sync.Map // map[string]struct{} keyed by kind + "/" + name + "/" + configKey

// validateEndpointURL rejects an operator-supplied OAuth endpoint that
// cannot address an IdP. Both endpoints carry credentials — the token
// endpoint receives the client secret, the authorization code and the
// PKCE verifier; the authorization endpoint is where the operator's
// browser is sent — so a value that is merely "a string" is not good
// enough to build a request from.
//
// The host is deliberately NOT constrained. Self-hosted IdPs on
// loopback and RFC1918 addresses (Keycloak in-cluster, a dev realm on
// localhost) are supported deployments, so a private-address or
// allowlist rule here would refuse legitimate connections. What is
// constrained is the shape: an absolute http/https URL with a host,
// no embedded credentials, and no fragment. An empty value is the
// caller's concern (the per-grant required-field checks) and passes.
//
// configKey names the offending connection-config key so the admin API
// surfaces which field to fix. No part of the raw value is ever echoed:
// the error text reaches the admin HTTP response and the log, and a
// value typed into the wrong field can be credential material. That is
// also why the url.Parse error is dropped rather than wrapped — it
// folds the raw input into its own message.
func validateEndpointURL(configKey, raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("config key %s is not a parseable URL: %w", configKey, ErrInvalidConfig)
	}
	switch {
	case u.Scheme == "":
		return fmt.Errorf("config key %s must be an absolute URL (scheme://host/path): %w",
			configKey, ErrInvalidConfig)
	case u.Scheme != "http" && u.Scheme != "https":
		// The scheme is not echoed. A value pasted into the wrong field
		// ("acctname:s3cr3t") parses as scheme "acctname", so echoing it
		// would publish an account name into the admin API response and
		// the log — the same exposure the userinfo branch below avoids.
		return fmt.Errorf("config key %s has an unsupported scheme (want http or https): %w",
			configKey, ErrInvalidConfig)
	case u.Host == "":
		return fmt.Errorf("config key %s is missing a host: %w", configKey, ErrInvalidConfig)
	case u.User != nil:
		// Not echoed even redacted: the username half of a userinfo
		// section is often an account name worth keeping out of the
		// admin API response and the log.
		return fmt.Errorf("config key %s must not embed credentials in the URL "+
			"(use %s and %s): %w", configKey, ConfigKeyClientID, ConfigKeyClientSecret, ErrInvalidConfig)
	case u.Fragment != "":
		return fmt.Errorf("config key %s must not contain a fragment: %w", configKey, ErrInvalidConfig)
	}
	return nil
}

// warnCleartextEndpoint emits one warning per (kind, name, configKey)
// when an OAuth endpoint is plain http, so a connection with both
// endpoints on http is told about each one. Cleartext is allowed — an
// in-cluster IdP reached over http on a trusted network is a real
// deployment — but the client secret and the authorization code cross
// the wire unprotected, so the operator is told. The dedup keeps that
// to one line per endpoint for the life of the process; ParseConfig
// runs on every token request.
//
// raw is assumed to have passed validateEndpointURL; a parse failure
// here is silently skipped because the caller has already rejected it.
func warnCleartextEndpoint(kind, name, configKey, raw string) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" {
		return
	}
	dedupKey := kind + "/" + name + "/" + configKey
	if _, loaded := cleartextWarned.LoadOrStore(dedupKey, struct{}{}); loaded {
		return
	}
	slog.Warn("connoauth: oauth endpoint uses cleartext http; "+
		"the client secret and authorization code are sent unencrypted",
		"kind", logsan.SanitizeForLog(kind),
		"name", logsan.SanitizeForLog(name),
		"config_key", configKey,
		"endpoint_host", logsan.SanitizeForLog(u.Host))
}

// validateEndpoints checks both OAuth endpoint URLs on a parsed Config
// and warns about each cleartext endpoint it carries. Called by
// ParseConfig so every kind that routes its OAuth config through the
// shared door gets the same treatment, at connection-save time (via
// registry.ValidateConnectionConfig) and again on the runtime paths
// that rebuild the Config before a token request.
func validateEndpoints(kind, name string, c Config) error {
	endpoints := [...]struct {
		configKey string
		raw       string
	}{
		{ConfigKeyTokenURL, c.TokenURL},
		{ConfigKeyAuthorizationURL, c.AuthorizationURL},
	}
	for _, e := range endpoints {
		if err := validateEndpointURL(e.configKey, e.raw); err != nil {
			return err
		}
	}
	for _, e := range endpoints {
		warnCleartextEndpoint(kind, name, e.configKey, e.raw)
	}
	return nil
}
