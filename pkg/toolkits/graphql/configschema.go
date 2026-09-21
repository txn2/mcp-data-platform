package graphql

import (
	"fmt"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/upstreamauth"
	"github.com/txn2/mcp-data-platform/pkg/connoauth"
)

// ownConfigSchemaProperties are the keys this kind reads itself; the
// credential, transport and OAuth keys are spliced from the packages that
// actually read them.
const ownConfigSchemaProperties = `
    "endpoint_url": {"type": "string", "description": "The GraphQL endpoint this connection posts documents to. Required."},
    "catalog_id": {"type": "string", "description": "Take this connection's schema from the named catalog instead of introspecting the endpoint. That is the path for an endpoint that disables introspection, and for one schema serving several connections."},
    "schema_validation": {"type": "string", "description": "How strictly a document is checked against the connection's schema before it is sent. Defaults to strict."},
    "max_query_depth": {"type": "integer", "description": "Deepest selection a document may nest."},
    "namespace_depth": {"type": "integer", "description": "How far the operation index descends a namespaced schema when building dotted operation ids."},
    "max_inline_bytes": {"type": "integer", "description": "How much of a response is returned through the model before the call reports it truncated."},
    "read_only": {"type": "boolean", "description": "Refuse every mutation document on this connection, for every persona."},
    "description": {"type": "string", "description": "What this connection is, as list_connections and search report it."}`

// ConfigSchemaJSON declares what a graphql connection's config takes. See the
// same constant on the trino kind for why a kind declares this at all (#1805).
var ConfigSchemaJSON = fmt.Sprintf(`{
  "type": "object",
  "required": ["endpoint_url"],
  "properties": {%s,
    %s,
    %s
  }
}`, ownConfigSchemaProperties,
	strings.TrimSuffix(strings.TrimPrefix(upstreamauth.ConfigSchemaPropertiesJSON, "{"), "}"),
	strings.TrimSuffix(strings.TrimPrefix(connoauth.ConfigSchemaPropertiesJSON, "{"), "}"))
