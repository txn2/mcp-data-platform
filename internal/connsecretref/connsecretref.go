// Package connsecretref is the save-time half of a connection configuration
// that names a stored secret as {{secret:<name>}} (#2066): which fields the
// platform fills as the connection sends a request, and the check the admin
// API runs before it stores such a configuration. The send-time half, the
// fill itself, is internal/upstreamauth's and pkg/toolkits/gateway's; the
// configuration keys named here are theirs, and a test holds the two lists
// to the keys those packages read.
package connsecretref

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/secretref"
	"github.com/txn2/mcp-data-platform/internal/secretstore"
	"github.com/txn2/mcp-data-platform/internal/upstreamauth/sessionlogin"
	"github.com/txn2/mcp-data-platform/pkg/connoauth"
)

// The connection kinds whose own configuration may name a stored secret.
const (
	kindAPI     = "api"
	kindGraphQL = "graphql"
	kindMCP     = "mcp"
)

// The configuration keys read here, as the kinds spell them.
const (
	keyCredential     = "credential"
	keyUsername       = "username"
	keyPassword       = "password"
	keyPathSecret     = "path_secret" // #nosec G101 -- a config key, not a credential
	keyStaticHeaders  = "static_headers"
	keyAuthMode       = "auth_mode"
	keyConnectionName = "connection_name"

	authModeOAuth             = "oauth"
	authModeClientCredentials = "oauth2_client_credentials" // #nosec G101 -- an auth mode's name, not a credential

	// listSep joins the names in a refusal.
	listSep = ", "
)

// referenceKeys are the top-level keys of an api or graphql connection whose
// value may name a stored secret: the values a request carries or a sign-in
// sends.
var referenceKeys = map[string]bool{
	keyCredential:                     true,
	keyUsername:                       true,
	keyPassword:                       true,
	keyPathSecret:                     true,
	sessionlogin.ConfigKeyLoginBody:   true,
	sessionlogin.ConfigKeyLoginSecret: true,
}

// referenceMapKeys are the keys whose value is a map of header values, each
// of which may name a stored secret.
var referenceMapKeys = map[string]bool{
	keyStaticHeaders:                   true,
	sessionlogin.ConfigKeyLoginHeaders: true,
}

// Allowed reports whether a value at path (a top-level key, or "<map
// key>.<entry>" for a map of headers) in a connection of kind may name a
// stored secret: whether the platform fills it as the connection sends a
// request. A reference anywhere else would be sent as written. The OAuth
// client's id and secret are filled for the client_credentials grant, whose
// token exchange the platform makes on every refresh; the
// authorization_code grant's exchange is made by the admin flow, which reads
// them as stored.
func Allowed(kind string, cfg map[string]any, path string) bool {
	key, entry, nested := strings.Cut(path, ".")
	switch {
	case !fillsConfig[kind]:
		return false
	case oauthClientKeys[key]:
		return !nested && clientCredentialsGrant(cfg)
	case kind == kindMCP:
		return !nested && key == keyCredential
	case nested:
		return referenceMapKeys[key] && validEntry(entry)
	}
	return referenceKeys[key]
}

// fillsConfig are the kinds that fill a reference in their own configuration.
var fillsConfig = map[string]bool{kindAPI: true, kindGraphQL: true, kindMCP: true}

// oauthClientKeys are the OAuth client's id and secret.
var oauthClientKeys = map[string]bool{connoauth.ConfigKeyClientID: true, connoauth.ConfigKeyClientSecret: true}

// validEntry reports whether a map entry's name is one header, not a path
// deeper into the configuration.
func validEntry(entry string) bool {
	return entry != "" && !strings.Contains(entry, ".")
}

// clientCredentialsGrant reports whether cfg is an OAuth connection on the
// client_credentials grant.
func clientCredentialsGrant(cfg map[string]any) bool {
	mode, _ := cfg[keyAuthMode].(string)
	if mode == authModeClientCredentials {
		return true
	}
	if mode != authModeOAuth {
		return false
	}
	grant, _ := cfg[connoauth.ConfigKeyGrant].(string)
	return grant == "" || grant == connoauth.GrantClientCredentials
}

// Fields names, for an operator reading a refusal, where a connection of
// kind may name a stored secret.
func Fields(kind string) string {
	const oauth = connoauth.ConfigKeyClientID + " and " + connoauth.ConfigKeyClientSecret + " under the client_credentials grant"
	switch kind {
	case kindAPI, kindGraphQL:
		return strings.Join([]string{
			keyCredential, keyUsername, keyPassword, keyPathSecret,
			sessionlogin.ConfigKeyLoginBody, sessionlogin.ConfigKeyLoginSecret,
			"the values of " + keyStaticHeaders + " and " + sessionlogin.ConfigKeyLoginHeaders, "and " + oauth,
		}, listSep)
	case kindMCP:
		return keyCredential + listSep + "and " + oauth
	}
	return "none of a " + kind + " connection's fields"
}

// Scopes reads a stored secret's scope, never its value.
// *secretstore.Store satisfies it.
type Scopes interface {
	Get(ctx context.Context, name string) (secretstore.Secret, error)
}

// reference is one configuration value naming a stored secret.
type reference struct {
	path  string
	value string
}

// Check refuses a connection configuration that names a stored secret as
// {{secret:<name>}} where the platform would not fill it, in a malformed
// placeholder, as a one-time code, or naming a secret that does not exist or
// that the connection may not use. The configuration is stored with the
// placeholder as written and the value read each time the connection sends
// a request, so this is the refusal an administrator sees at save rather
// than at the connection's first call. scopes nil refuses any reference.
func Check(ctx context.Context, scopes Scopes, kind, name string, cfg map[string]any) error {
	refs := references(cfg)
	if len(refs) == 0 {
		return nil
	}
	connection := name
	if cn, ok := cfg[keyConnectionName].(string); ok && kind == kindMCP && cn != "" {
		connection = cn
	}
	target := saveTarget{kind: kind, connection: connection, cfg: cfg}
	for _, ref := range refs {
		if err := checkReference(ctx, scopes, target, ref); err != nil {
			return err
		}
	}
	return nil
}

// saveTarget is the connection a configuration is being saved for.
type saveTarget struct {
	kind       string
	connection string
	cfg        map[string]any
}

// checkReference refuses one configuration value naming a stored secret.
func checkReference(ctx context.Context, scopes Scopes, target saveTarget, ref reference) error {
	kind, connection, cfg := target.kind, target.connection, target.cfg
	if secretref.Malformed(ref.value) {
		return fmt.Errorf("field %s names a stored secret in a form that is not {{secret:<name>}}, where a name is lower case letters, digits, '.', '_' and '-' only", ref.path)
	}
	if !Allowed(kind, cfg, ref.path) {
		return fmt.Errorf("field %s names a stored secret, which a %s connection does not fill there, so it would be sent as written; a stored secret may be named in %s",
			ref.path, kind, Fields(kind))
	}
	for _, secret := range secretref.Names(ref.value) {
		if code, ok := secretref.IsTOTPName(secret); ok {
			return fmt.Errorf("field %s names {{totp:%s}}, and a one-time code is filled only in an api call's request, not in a connection's configuration", ref.path, code)
		}
		if err := checkScope(ctx, scopes, ref.path, secret, connection); err != nil {
			return err
		}
	}
	return nil
}

// checkScope refuses one named secret that is missing or out of scope for
// connection.
func checkScope(ctx context.Context, scopes Scopes, path, secret, connection string) error {
	if scopes == nil {
		return fmt.Errorf("field %s names secret %q, and stored secrets are not available on this deployment", path, secret)
	}
	sec, err := scopes.Get(ctx, secret)
	if errors.Is(err, secretstore.ErrNotFound) {
		return fmt.Errorf("field %s names secret %q, which does not exist; store it under Admin > Secrets with %q in its allowed connections first", path, secret, connection)
	}
	if err != nil {
		slog.WarnContext(ctx, "connsecretref: reading the secret failed", "error", err)
		return fmt.Errorf("field %s names secret %q, which could not be read; see the server log", path, secret)
	}
	if err := secretstore.ConnectionAllowed(sec, connection); err != nil {
		return fmt.Errorf("field %s: %w", path, err)
	}
	return nil
}

// references is every string in cfg that names a stored secret, in path
// order, each at its dotted path.
func references(cfg map[string]any) []reference {
	var refs []reference
	walk(cfg, "", func(path, value string) {
		if secretref.HasPlaceholder(value) {
			refs = append(refs, reference{path: path, value: value})
		}
	})
	sort.Slice(refs, func(i, j int) bool { return refs[i].path < refs[j].path })
	return refs
}

// walk visits every string in a configuration value at its dotted path; a
// list element's path carries its index.
func walk(value any, path string, visit func(path, value string)) {
	join := func(key string) string {
		if path == "" {
			return key
		}
		return path + "." + key
	}
	switch v := value.(type) {
	case string:
		visit(path, v)
	case map[string]any:
		for key, nested := range v {
			walk(nested, join(key), visit)
		}
	case map[string]string:
		for key, nested := range v {
			visit(join(key), nested)
		}
	case []any:
		for i, nested := range v {
			walk(nested, fmt.Sprintf("%s[%d]", path, i), visit)
		}
	}
}

// DropCarriedSessionSecret removes a session_login_secret the portal sent
// back as redacted from a sign-in body that no longer has anywhere to write
// it, because the body now names a stored secret as {{secret:<name>}}.
// Kept, the stored value would be merged back in and the save refused for a
// secret the body does not use; a new value the operator typed is left for
// validation to refuse.
func DropCarriedSessionSecret(cfg map[string]any, redacted string) {
	if s, ok := cfg[sessionlogin.ConfigKeyLoginSecret].(string); !ok || s != redacted {
		return
	}
	body, _ := cfg[sessionlogin.ConfigKeyLoginBody].(string)
	if !strings.Contains(body, sessionlogin.SessionSecretPlaceholder) {
		delete(cfg, sessionlogin.ConfigKeyLoginSecret)
	}
}

// NamesStoredSecret reports whether a sensitive value is exactly one
// {{secret:<name>}} reference. It holds no secret, only the name of one, so
// the admin API reads it back as written rather than redacted: an
// administrator sees which secret a credential comes from, and a portal that
// sends it back saves it unchanged.
func NamesStoredSecret(v any) bool {
	s, ok := v.(string)
	return ok && secretref.IsPlaceholder(s)
}
