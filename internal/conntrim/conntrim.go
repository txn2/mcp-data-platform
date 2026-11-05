// Package conntrim removes the whitespace a paste brings from the connection
// configuration fields where it can never be part of the value (#2467): the
// admin API's connection save, which the portal's editor saves through, runs
// every configuration through it before the save is judged.
package conntrim

import (
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/connoauth"
)

// trimmedKeys are the configuration keys whose leading and trailing
// whitespace can never be part of the value: addresses, identifiers and
// claims, the OAuth scope, and durations (#2467). A value pasted from a
// provider's console or a document often brings a space or a line break with
// it, and a URL with one fails the save as unparseable without saying why.
//
// A secret is never trimmed (credential, password, oauth_client_secret,
// jwt_client_secret, session_login_secret, path_secret): a credential may in
// principle begin or end with a space, and changing one silently is worse
// than refusing it. Neither is a body or header value sent as written
// (session_login_body, static_headers), nor a PEM block.
var trimmedKeys = []string{
	"base_url", connoauth.ConfigKeyTokenURL, connoauth.ConfigKeyAuthorizationURL,
	"session_login_url", "session_logout_url",
	connoauth.ConfigKeyClientID, "jwt_issuer", "jwt_subject", "jwt_audience", "jwt_key_id",
	"catalog_id", "api_key_header", "api_key_param",
	connoauth.ConfigKeyScope, "call_timeout", "connect_timeout", "jwt_token_lifetime", "oauth_token_lifetime",
}

// Values removes leading and trailing whitespace, line breaks included,
// from the string value of each key whose whitespace is never meaningful,
// in place, and returns cfg. Every other value is left as written.
func Values(cfg map[string]any) map[string]any {
	for _, key := range trimmedKeys {
		if s, ok := cfg[key].(string); ok {
			cfg[key] = strings.TrimSpace(s)
		}
	}
	return cfg
}
