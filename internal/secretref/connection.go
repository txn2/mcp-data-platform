package secretref

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
)

// ConnectionSource reads a stored secret on behalf of the connection whose own
// configuration names it (secretstore.Store.ConnectionValue): the value, or
// a refusal naming the secret and the connection.
type ConnectionSource func(ctx context.Context, name, connection string) (string, error)

// connectionSource is the installed source; nil until the platform has a
// database to read secrets from.
var connectionSource atomic.Pointer[ConnectionSource]

// SetConnectionSource installs the source a connection's configuration is
// filled from (#2066), and returns the one it replaces so a test can put it
// back. The platform installs its store once at startup; a nil src removes
// it, after which a configuration naming a secret is refused when it is used.
func SetConnectionSource(src ConnectionSource) ConnectionSource {
	var prev *ConnectionSource
	if src == nil {
		prev = connectionSource.Swap(nil)
	} else {
		prev = connectionSource.Swap(&src)
	}
	if prev == nil {
		return nil
	}
	return *prev
}

// ConnectionLookup is the lookup a connection's own configuration is filled
// through as a request is sent: a credential, a header the operator fixed, a
// sign-in body. Each value is read from the installed source as of this
// moment, so a rotated secret is used from the next request on, and is
// recorded on ctx's Redactor so a response that echoes it is redacted.
func ConnectionLookup(ctx context.Context, connection string) Lookup {
	return FromContext(ctx).Recording(func(name string) (string, error) {
		if secret, ok := IsTOTPName(name); ok {
			return "", fmt.Errorf("connection %q names {{totp:%s}}, and a one-time code is filled only in an api call's request, not in a connection's configuration", connection, secret)
		}
		src := connectionSource.Load()
		if src == nil {
			return "", fmt.Errorf("connection %q names secret %q, and stored secrets are not available here; a placeholder is never sent as written", connection, name)
		}
		return (*src)(ctx, name, connection)
	})
}

// FillConnection fills the placeholders in one value of connection's
// configuration, escaping each value with escape.
func FillConnection(ctx context.Context, connection, s string, escape func(string) string) (string, error) {
	if !HasPlaceholder(s) {
		return s, nil
	}
	return Fill(s, ConnectionLookup(ctx, connection), escape)
}

// HasPlaceholder reports whether s names a stored secret, or opens a
// placeholder that Fill would refuse as malformed.
func HasPlaceholder(s string) bool {
	return strings.Contains(s, opener) || strings.Contains(s, totpOpener)
}

// Names returns the names s's placeholders are looked up by, in order, each
// once: a secret's name, or TOTPName of one for {{totp:<name>}}.
func Names(s string) []string {
	if !HasPlaceholder(s) {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, m := range placeholderRE.FindAllStringSubmatch(s, -1) {
		name := lookupName(m[1], m[2])
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// Malformed reports whether s opens a placeholder it does not complete,
// which Fill refuses rather than sending as written.
func Malformed(s string) bool {
	rest := placeholderRE.ReplaceAllString(s, "")
	return strings.Contains(rest, opener) || strings.Contains(rest, totpOpener)
}

// WithoutPlaceholders is s with every placeholder removed, for a check on
// the text an operator wrote around one: a placeholder's own ':' is not a
// colon in a Basic auth userid.
func WithoutPlaceholders(s string) string {
	if !HasPlaceholder(s) {
		return s
	}
	return placeholderRE.ReplaceAllString(s, "")
}

// IsPlaceholder reports whether s is exactly one placeholder: a reference
// with nothing secret written beside it, which a connection's configuration
// can read back as written.
func IsPlaceholder(s string) bool {
	m := placeholderRE.FindStringIndex(s)
	return len(m) == 2 && m[0] == 0 && m[1] == len(s)
}
