// This file adds the gate that keeps a connection kind's declared config
// schema complete (issue #1805).
//
// A connection's config is a freeform object on the wire: the admin API takes
// {"config": {...}} and nothing describes what belongs inside it, so the only
// way to learn a key's name was to read the deployment's own configuration file
// out of band — which an operator or agent without cluster access cannot do.
// Each kind therefore declares a ConfigSchemaJSON, served by
// GET /admin/connection-kinds.
//
// A declaration is only worth reading if it is complete, and a hand-written one
// drifts the moment a key is added to a parser. So the keys each parser reads
// are recovered from its source and every one of them must appear in that
// kind's schema. A schema that silently stopped covering its kind would be
// worse than none: it would answer "what does this take?" with a subset, and
// the caller would have no way to tell.
//
// Run: go test -run TestConnectionConfigSchemasAreComplete .
package structure_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// configKeyPatterns recover a config key from the forms the parsers read one
// in: a named constant, a typed getter on the config map, and a direct index.
var configKeyPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?:cfgKey|ConfigKey)[A-Za-z0-9]*\s*=\s*"([a-z_0-9]+)"`),
	regexp.MustCompile(`get(?:String|StringDefault|Int|Int64|Bool|BoolPtr|Duration)\(cfg, "([a-z_0-9]+)"`),
	regexp.MustCompile(`cfgmap\.[A-Za-z0-9]+\(cfg, "([a-z_0-9]+)"`),
	regexp.MustCompile(`cfg\["([a-z_0-9]+)"\]`),
}

// kindSources names, per connection kind, the source files whose config keys
// that kind's schema must declare. A kind that delegates its credential and
// transport keys to a shared package lists that package's parser too, because
// those keys arrive in the same flat config object an operator writes.
var kindSources = map[string][]string{
	"trino": {"pkg/toolkits/trino/config.go"},
	"s3":    {"pkg/toolkits/s3/config.go"},
	"mcp": {
		"pkg/toolkits/gateway/toolkit.go",
		"pkg/connoauth/parse.go",
	},
	"api": {
		"pkg/toolkits/apigateway/config.go",
		"internal/upstreamauth/config.go",
		"internal/upstreamauth/signedjwt.go",
		"pkg/connoauth/parse.go",
	},
	"graphql": {
		"pkg/toolkits/graphql/config.go",
		"internal/upstreamauth/config.go",
		"internal/upstreamauth/signedjwt.go",
		"pkg/connoauth/parse.go",
	},
}

// notConfigKeys are strings the patterns recover that are not connection-config
// keys: log-field names, and the two vocabularies a kind's own parser reads off
// something other than the connection config.
var notConfigKeys = map[string]bool{
	"kind": true, "name": true, "error": true, "token_url_host": true,
	// Legacy oauth2_* spellings are folded onto their canonical oauth_*
	// siblings by the admin write path before a config is ever stored, so the
	// schema documents the canonical set alone (#1682).
	"oauth2_grant": true, "oauth2_token_url": true, "oauth2_authorization_url": true,
	"oauth2_client_id": true, "oauth2_client_secret": true, "oauth2_scope": true,
	"oauth2_prompt": true, "oauth2_endpoint_auth_style": true,
}

// TestConnectionConfigSchemasAreComplete fails when a kind's parser reads a
// config key its declared schema does not name.
//
// The remedy is to add the key to that kind's ConfigSchemaJSON with a
// description an operator can act on — or, when the string is not a connection
// config key at all, to name it in notConfigKeys above.
func TestConnectionConfigSchemasAreComplete(t *testing.T) {
	root := moduleRoot(t)

	for kind, sources := range kindSources {
		t.Run(kind, func(t *testing.T) {
			declared := schemaProperties(t, kind)
			require.NotEmpty(t, declared, "kind %q declares no config schema", kind)

			var missing []string
			for _, key := range readConfigKeys(t, root, sources) {
				if !declared[key] {
					missing = append(missing, key)
				}
			}
			sort.Strings(missing)
			require.Empty(t, missing,
				"the %s kind's parser reads these config keys, and its ConfigSchemaJSON does not declare them: %v\n"+
					"A schema that covers only some of a kind's keys answers \"what does this connection take?\" "+
					"with a subset, and the caller cannot tell (#1805).", kind, missing)
		})
	}
}

// schemaProperties returns the property names a kind's declared schema names.
func schemaProperties(t *testing.T, kind string) map[string]bool {
	t.Helper()
	raw := registry.ConnectionConfigSchema(kind)
	require.NotNil(t, raw, "kind %q has no registered config schema", kind)

	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(raw, &schema), "the %s config schema is not valid JSON", kind)

	names := make(map[string]bool, len(schema.Properties))
	for name := range schema.Properties {
		names[name] = true
	}
	return names
}

// readConfigKeys recovers the distinct config keys the named sources read.
func readConfigKeys(t *testing.T, root string, sources []string) []string {
	t.Helper()
	seen := map[string]bool{}
	var keys []string
	for _, source := range sources {
		body, err := os.ReadFile(filepath.Join(root, source)) //nolint:gosec // reading the repository's own source
		require.NoError(t, err, "reading %s", source)
		for _, pattern := range configKeyPatterns {
			for _, match := range pattern.FindAllStringSubmatch(string(body), -1) {
				key := match[1]
				if seen[key] || notConfigKeys[key] {
					continue
				}
				seen[key] = true
				keys = append(keys, key)
			}
		}
	}
	return keys
}
