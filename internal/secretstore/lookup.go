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
func (s *Store) Lookup(ctx context.Context, connection, persona string) secretref.Lookup {
	seen := map[string]string{}
	return func(name string) (string, error) {
		if v, ok := seen[name]; ok {
			return v, nil
		}
		sec, value, err := s.withValue(ctx, name)
		if errors.Is(err, ErrNotFound) {
			return "", fmt.Errorf("secret %q does not exist; an administrator stores secrets under Admin > Secrets, and a placeholder is never sent as written", name)
		}
		if err != nil {
			return "", err
		}
		if persona != "" && persona == s.admin {
			sec.AllowPersonas = nil
		}
		if err := Allowed(sec, connection, persona); err != nil {
			return "", err
		}
		seen[name] = value
		return value, nil
	}
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
// (#2061). The value is the connection's credential for every caller, so
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
	if !slices.Contains(sec.AllowConnections, connection) {
		return "", fmt.Errorf("secret %q may not be used by connection %q; it is allowed on %s", name, connection, strings.Join(sec.AllowConnections, listSep))
	}
	if len(sec.AllowPersonas) > 0 {
		return "", fmt.Errorf("secret %q is limited to personas %s, and a connection's key serves every persona that uses the connection; clear its allow_personas",
			name, strings.Join(sec.AllowPersonas, listSep))
	}
	return value, nil
}
