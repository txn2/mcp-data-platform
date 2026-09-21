package apigateway

import (
	"fmt"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/upstreamauth"
	"github.com/txn2/mcp-data-platform/pkg/connoauth"
)

// ownConfigSchemaProperties are the keys this kind reads itself. The
// credential, timeout, response-cap, static-header, TLS and OAuth keys are read
// from the same config map by internal/upstreamauth and pkg/connoauth, and are
// spliced from those packages rather than restated here — for the reason the
// parser does not restate them either: two packages must not come to disagree
// about a key's spelling.
const ownConfigSchemaProperties = `
    "base_url": {"type": "string", "description": "The upstream API root, for example https://api.example.com. Required unless handler is set."},
    "catalog_id": {"type": "string", "description": "The api_catalogs row supplying this connection's OpenAPI specs. A catalog is shared: several connections to one vendor reference the same catalog rather than each carrying a copy. Empty means the connection has no spec surface, so api_discover answers with a note and no operations."},
    "trust_level": {"type": "string", "enum": ["untrusted", "trusted"], "description": "Whether responses from this upstream are treated as data only. Defaults to untrusted."},
    "max_inline_bytes": {"type": "integer", "description": "How much of a response is returned through the model before the call reports it truncated."},
    "handler": {"type": "string", "description": "Resolve this connection's operations with an in-process handler instead of dialing an upstream. Set by the platform's built-in connections; leave unset."},
    "description": {"type": "string", "description": "What this connection is, as list_connections and search report it. Falls back to the base URL."}`

// ConfigSchemaJSON declares what an api connection's config takes. See the same
// constant on the trino kind for why a kind declares this at all (#1805).
var ConfigSchemaJSON = fmt.Sprintf(`{
  "type": "object",
  "required": ["base_url"],
  "properties": {%s,
    %s,
    %s
  }
}`, ownConfigSchemaProperties,
	strings.TrimSuffix(strings.TrimPrefix(upstreamauth.ConfigSchemaPropertiesJSON, "{"), "}"),
	strings.TrimSuffix(strings.TrimPrefix(connoauth.ConfigSchemaPropertiesJSON, "{"), "}"))
