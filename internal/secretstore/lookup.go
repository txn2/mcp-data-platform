package secretstore

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/secretref"
)

// listSep joins the names a refusal lists.
const listSep = ", "

// Source is what the gateway fills placeholders from: the store, or a fake
// in a test.
type Source interface {
	Lookup(ctx context.Context, connection, persona string) secretref.Lookup
}

// Lookup returns the lookup one call fills its placeholders through: a
// secret is answered only when connection is in its allow_connections and,
// when it names personas, persona is one of them or the administrator
// persona. allow_connections holds for every caller: it is where the value
// may go, not who may ask. Every refusal names the
// secret and says what would allow it. A name is read once per call however
// many placeholders name it.
//
// A {{totp:<name>}} placeholder is asked for as secretref.TOTPName(name) and
// answered with a one-time code (#2065): the seed is recorded on ctx's
// Redactor so a response that echoes it is redacted, and the code is issued
// once per period (issueCode). Every placeholder naming the same secret in
// one call gets the same code.
func (s *Store) Lookup(ctx context.Context, connection, persona string) secretref.Lookup {
	seen := map[string]string{}
	return func(qualified string) (string, error) {
		if v, ok := seen[qualified]; ok {
			return v, nil
		}
		name, wantCode := secretref.IsTOTPName(qualified)
		sec, value, err := s.withValue(ctx, name)
		if errors.Is(err, ErrNotFound) {
			return "", fmt.Errorf("secret %q does not exist; an administrator stores secrets under Admin > Secrets, and a placeholder is never sent as written", name)
		}
		if err != nil {
			return "", err
		}
		if err := kindMatches(sec, wantCode); err != nil {
			return "", err
		}
		if persona != "" && persona == s.admin {
			sec.AllowPersonas = nil
		}
		if err := Allowed(sec, connection, persona); err != nil {
			return "", err
		}
		if wantCode {
			secretref.FromContext(ctx).Add(name, value)
			if value, err = s.issueCode(ctx, sec, value); err != nil {
				return "", err
			}
		}
		seen[qualified] = value
		return value, nil
	}
}

// kindMatches refuses a placeholder of the other kind: {{secret:<name>}}
// naming an authenticator seed, which would send the seed itself, and
// {{totp:<name>}} naming a value, which has no code.
func kindMatches(sec Secret, wantCode bool) error {
	switch {
	case wantCode && sec.Kind != KindTOTP:
		return fmt.Errorf("secret %q holds a value, not an authenticator seed, so it has no one-time code; write {{secret:%s}}", sec.Name, sec.Name)
	case !wantCode && sec.Kind == KindTOTP:
		return fmt.Errorf("secret %q is an authenticator seed, which is never sent; write {{totp:%s}} for its current one-time code", sec.Name, sec.Name)
	}
	return nil
}

// Allowed refuses a use of sec outside its scope.
func Allowed(sec Secret, connection, persona string) error {
	if !slices.Contains(sec.AllowConnections, connection) {
		return fmt.Errorf("secret %q may not be sent through connection %q; it is allowed on %s", sec.Name, connection, strings.Join(sec.AllowConnections, listSep))
	}
	if len(sec.AllowPersonas) > 0 && !slices.Contains(sec.AllowPersonas, persona) {
		who := persona
		if who == "" {
			who = "(none)"
		}
		return fmt.Errorf("secret %q may not be used by persona %s; it is allowed for %s", sec.Name, who, strings.Join(sec.AllowPersonas, listSep))
	}
	return nil
}

// ConnectionValue reads a secret a connection's own configuration names,
// rather than a call's placeholder: the key file of a Google service account
// (#2061), or a {{secret:<name>}} in a credential field (#2066). The value is
// the connection's credential for every caller, so
// allow_connections must list the connection, and allow_personas must be
// empty: the token one key mints is cached and served to every persona the
// connection serves, so a key cannot be limited to some of them.
func (s *Store) ConnectionValue(ctx context.Context, name, connection string) (string, error) {
	sec, value, err := s.withValue(ctx, name)
	if errors.Is(err, ErrNotFound) {
		return "", fmt.Errorf("secret %q does not exist; an administrator stores secrets under Admin > Secrets", name)
	}
	if err != nil {
		return "", err
	}
	if sec.Kind == KindTOTP {
		return "", fmt.Errorf("secret %q is an authenticator seed, which is never sent; a connection's configuration names a value secret", name)
	}
	if err := ConnectionAllowed(sec, connection); err != nil {
		return "", err
	}
	return value, nil
}

// ConnectionAllowed refuses sec to the configuration of a connection it may
// not be used by: one its allow_connections does not list, or any connection
// at all while it names personas, since a connection's credential serves
// every persona that uses the connection. The admin API asks it when a
// connection configuration naming the secret is saved (#2066), and the read
// at send time asks it again, so a secret rescoped afterwards is refused
// from the next request on.
func ConnectionAllowed(sec Secret, connection string) error {
	if !slices.Contains(sec.AllowConnections, connection) {
		return fmt.Errorf("secret %q may not be used by connection %q; it is allowed on %s", sec.Name, connection, strings.Join(sec.AllowConnections, listSep))
	}
	if len(sec.AllowPersonas) > 0 {
		return fmt.Errorf("secret %q is limited to personas %s, and a connection's credential serves every persona that uses the connection; clear its allow_personas",
			sec.Name, strings.Join(sec.AllowPersonas, listSep))
	}
	return nil
}
