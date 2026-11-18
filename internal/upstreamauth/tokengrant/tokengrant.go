// Package tokengrant is what every OAuth grant the platform makes without a
// browser shares when it calls a token endpoint: the bounded client the
// request is sent through, the scrub a failure is read through before anyone
// sees it, the reading of an RFC 6749 section 5.2 refusal, and the password
// grant's token source (#2689). internal/upstreamauth builds the
// authenticators from it; it knows nothing about connections beyond the
// values it is handed.
package tokengrant

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"golang.org/x/oauth2"

	"github.com/txn2/mcp-data-platform/internal/apigwtls"
	"github.com/txn2/mcp-data-platform/internal/outbound"
)

// FetchTimeout caps how long a single token-endpoint request can take.
// Without it the default Go http.Client has no timeout and an unreachable
// IdP would hang every call until the OS-level dial timeout fires (minutes).
// 30 seconds matches the order of magnitude of the upstream call_timeout
// while staying generous for IdPs with slow first-token issuance.
const FetchTimeout = 30 * time.Second

// MaxDescriptionRunes bounds how much of an upstream's error_description the
// platform repeats in a tool error and an alert.
const MaxDescriptionRunes = 300

// NewClient builds the http.Client a token request is sent through, on the
// outbound chain (the platform's User-Agent, its span and metrics), bounded
// by FetchTimeout.
//
// When caBundlePEM is set the bundle is appended to the system root pool and
// attached to a dedicated transport: public CAs remain trusted, and the
// bundle never substitutes for them. A bundle that does not parse leaves the
// default transport, and the request fails its TLS handshake on the normal
// error path. Client mTLS material is deliberately not plumbed here:
// presenting a client certificate to the IdP is a decision that should not
// piggy-back on the upstream's.
func NewClient(connection, caBundlePEM string) *http.Client {
	opts := outbound.Options{Kind: outbound.KindOAuth, Connection: connection, Timeout: FetchTimeout}
	if caBundlePEM != "" {
		if pool, err := apigwtls.RootPool(caBundlePEM); err == nil {
			opts.Base = &http.Transport{
				TLSClientConfig: &tls.Config{
					MinVersion: tls.VersionTLS12,
					RootCAs:    pool,
				},
			}
		}
	}
	return outbound.NewClient(opts)
}

// Scrub rebuilds a failed token request's error from its non-sensitive
// parts. The library's *oauth2.RetrieveError carries the IdP's response body,
// which can hold material from a partial grant exchange, and a *url.Error
// carries the token URL, whose query or userinfo can hold a credential an
// operator embedded in it. The result carries no prefix; the caller states
// it in its kind's voice.
func Scrub(err error) error {
	if re, ok := errors.AsType[*oauth2.RetrieveError](err); ok {
		return fmt.Errorf("oauth2 token fetch failed: status=%d", re.Response.StatusCode)
	}
	if ue, ok := errors.AsType[*url.Error](err); ok {
		parsed, perr := url.Parse(ue.URL)
		if perr != nil {
			return fmt.Errorf("oauth2 token fetch %s: %w", ue.Op, ue.Err)
		}
		parsed.RawQuery = ""
		parsed.User = nil
		return fmt.Errorf("oauth2 token fetch %s %q: %w", ue.Op, parsed.String(), ue.Err)
	}
	// Anything URL-shaped is redacted, in case a future library version
	// wraps the failure in a different error type.
	msg := err.Error()
	if strings.Contains(msg, "://") {
		return errors.New("oauth2 token fetch failed (details redacted)")
	}
	return fmt.Errorf("oauth2 token fetch failed: %s", msg)
}

// IsRefusal reports whether an RFC 6749 section 5.2 error code is a refusal
// an operator fixes at the upstream or in the connection: the grant or its
// user is not accepted (invalid_grant), the client is not recognized
// (invalid_client), the client may not use this grant (unauthorized_client),
// or a scope it names is not one the endpoint issues (invalid_scope). Other
// codes describe a malformed request, which is the platform's fault rather
// than the connection's.
func IsRefusal(code string) bool {
	switch code {
	case "invalid_grant", "invalid_client", "unauthorized_client", "invalid_scope":
		return true
	default:
		return false
	}
}

// Description makes an upstream's error_description safe to repeat in a tool
// error and an email: control characters become spaces, runs of whitespace
// collapse, and the text is cut at MaxDescriptionRunes.
func Description(raw string) string {
	cleaned := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, raw)
	out := strings.Join(strings.Fields(cleaned), " ")
	if runes := []rune(out); len(runes) > MaxDescriptionRunes {
		out = string(runes[:MaxDescriptionRunes]) + "..."
	}
	return out
}

// WithoutSecrets removes each secret from text. An upstream that echoes a
// submitted password in its error_description would otherwise put it in a
// tool error, an audit row and a connection test result. An empty secret is
// skipped.
func WithoutSecrets(text string, secrets ...string) string {
	for _, s := range secrets {
		if s != "" {
			text = strings.ReplaceAll(text, s, "[REDACTED]")
		}
	}
	return text
}
