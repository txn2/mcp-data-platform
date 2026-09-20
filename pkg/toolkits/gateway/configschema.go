package gateway

import (
	"fmt"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/connoauth"
)

// ownConfigSchemaProperties are the keys this kind reads itself; the oauth_*
// keys are spliced from pkg/connoauth, which parses them.
const ownConfigSchemaProperties = `
    "endpoint": {"type": "string", "description": "The upstream MCP server's URL. Required."},
    "auth_mode": {"type": "string", "enum": ["none", "bearer", "api_key", "oauth"], "description": "How the platform authenticates to the upstream server. Defaults to none."},
    "credential": {"type": "string", "description": "The bearer token or API key, required when auth_mode is bearer or api_key. Encrypted at rest; read back as \"[REDACTED]\", and sending that placeholder keeps the stored value."},
    "trust_level": {"type": "string", "enum": ["untrusted", "trusted"], "description": "Whether this upstream's tool results are treated as data only. Defaults to untrusted."},
    "connect_timeout": {"type": ["string", "integer"], "description": "How long to wait for the upstream session to open, as a duration string or seconds."},
    "call_timeout": {"type": ["string", "integer"], "description": "How long one proxied tool call may take, as a duration string or seconds."},
    "connection_name": {"type": "string", "description": "The name calls bind this connection by, when it differs from the instance name it is stored under. It also prefixes the proxied tool names."}`

// ConfigSchemaJSON declares what an mcp connection's config takes. See the same
// constant on the trino kind for why a kind declares this at all (#1805).
var ConfigSchemaJSON = fmt.Sprintf(`{
  "type": "object",
  "required": ["endpoint"],
  "properties": {%s,
    %s
  }
}`, ownConfigSchemaProperties,
	strings.TrimSuffix(strings.TrimPrefix(connoauth.ConfigSchemaPropertiesJSON, "{"), "}"))
