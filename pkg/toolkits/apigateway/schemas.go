package apigateway

import "encoding/json"

// listEndpointsSchema is the JSON Schema for the api_list_endpoints tool input.
//
//nolint:gochecknoglobals // MCP tool schema must be a package-level var
var listEndpointsSchema = json.RawMessage(`{
  "type": "object",
  "required": ["connection"],
  "properties": {
    "connection": {
      "type": "string",
      "description": "Name of the registered API connection (kind=api). Required."
    },
    "query": {
      "type": "string",
      "description": "Optional case-insensitive search across operation_id, path, summary, spec name, and tags. Multiple whitespace-separated tokens combine with AND, so \"gift list\" matches operations containing both \"gift\" and \"list\" in any of those fields. Empty returns the full list (capped by limit)."
    },
    "limit": {
      "type": "integer",
      "minimum": 1,
      "maximum": 500,
      "description": "Optional cap on the number of operations returned. Defaults to 50. Pass a higher value when exploring large APIs."
    },
    "spec": {
      "type": "string",
      "description": "Optional component-spec filter for multi-spec catalogs. Restricts results to operations whose spec field matches this value exactly. Pair with query to narrow to a section of a large catalog (e.g. spec=\"orders\" + query=\"refund\"). Values come from the spec field on operations in a prior api_list_endpoints response."
    },
    "ranking": {
      "type": "string",
      "enum": ["lexical", "semantic", "hybrid"],
      "description": "Optional ranking algorithm. \"lexical\" (default) is per-token case-insensitive substring match: fast, deterministic, but misses when your phrasing differs from the spec author's. \"semantic\" ranks by embedding cosine similarity, which finds endpoints by intent (\"create order\" finds POST /v1/orders) even when no words overlap. \"hybrid\" blends both: best for free-form intent queries that may also share path/tag vocabulary. semantic and hybrid require an embedding provider; if unavailable they fall back to lexical and a note explains the reason."
    }
  }
}`)

// getEndpointSchemaInputSchema is the JSON Schema for the
// api_get_endpoint_schema tool input.
//
//nolint:gochecknoglobals // MCP tool schema must be a package-level var
var getEndpointSchemaInputSchema = json.RawMessage(`{
  "type": "object",
  "required": ["connection", "operation_id"],
  "properties": {
    "connection": {
      "type": "string",
      "description": "Name of the registered API connection (kind=api). Required."
    },
    "operation_id": {
      "type": "string",
      "description": "The operation_id returned by api_list_endpoints. Required."
    },
    "spec": {
      "type": "string",
      "description": "Optional component spec name within the connection's catalog. Only needed when an operation_id is defined by more than one component spec. api_list_endpoints surfaces the spec field for each operation so the disambiguation is local — pass the same value back."
    }
  }
}`)

// apiExportInputSchema is the JSON Schema for the api_export tool
// input. Mirrors invokeEndpointSchema for connection/method/path/
// query/headers/body and adds the portal-asset metadata fields
// (name, description, tags, idempotency_key, create_public_link)
// matched to trino_export's surface.
//
//nolint:gochecknoglobals // MCP tool schema must be a package-level var
var apiExportInputSchema = json.RawMessage(`{
  "type": "object",
  "required": ["connection", "method", "path", "name"],
  "properties": {
    "connection": {
      "type": "string",
      "description": "Name of the registered API connection (kind=api). Required."
    },
    "method": {
      "type": "string",
      "enum": ["GET", "POST", "PUT", "DELETE", "PATCH", "HEAD"],
      "description": "HTTP method. Required."
    },
    "path": {
      "type": "string",
      "description": "Request path joined to the connection's base URL. Required. Must start with \"/\"."
    },
    "query_params": {
      "type": "object",
      "description": "Optional HTTP query-string parameters sent to the upstream. Distinct from api_list_endpoints's \"query\" field (which is search text).",
      "additionalProperties": true
    },
    "headers": {
      "type": "object",
      "description": "Optional custom request headers. Sending Authorization or the connection's api_key header is rejected.",
      "additionalProperties": {"type": "string"}
    },
    "body": {
      "description": "Optional request body. Same encoding rules as api_invoke_endpoint."
    },
    "timeout_seconds": {
      "type": "integer",
      "minimum": 1,
      "maximum": 1800,
      "description": "Optional per-call timeout override. Capped at 30 minutes."
    },
    "name": {
      "type": "string",
      "description": "Asset display name; doubles as download filename. Keep short and ASCII-only (letters, digits, spaces, hyphens, dots). Required."
    },
    "description": {
      "type": "string",
      "description": "Optional asset description shown in the portal."
    },
    "tags": {
      "type": "array",
      "items": {"type": "string"},
      "description": "Optional asset tags."
    },
    "idempotency_key": {
      "type": "string",
      "description": "Optional idempotency key. When supplied, a prior export by this user with the same key returns the existing asset's metadata without re-running the upstream call."
    },
    "create_public_link": {
      "type": "boolean",
      "description": "When true, also create a public share link for the resulting asset. Returns share_url alongside the asset metadata."
    }
  }
}`)

// invokeEndpointSchema is the JSON Schema for the api_invoke_endpoint tool input.
//
//nolint:gochecknoglobals // MCP tool schema must be a package-level var
var invokeEndpointSchema = json.RawMessage(`{
  "type": "object",
  "required": ["connection", "method", "path"],
  "properties": {
    "connection": {
      "type": "string",
      "description": "Name of the registered API connection (kind=api). Required. Use list_connections to discover available connections."
    },
    "method": {
      "type": "string",
      "enum": ["GET", "POST", "PUT", "DELETE", "PATCH", "HEAD"],
      "description": "HTTP method. Required."
    },
    "path": {
      "type": "string",
      "description": "Request path joined to the connection's base URL. Examples: \"/v1/users/123\", \"/api/items\". Required. Must start with \"/\"."
    },
    "query_params": {
      "type": "object",
      "description": "Optional HTTP query-string parameters sent to the upstream. Values may be strings, numbers, or booleans; arrays send the parameter once per value. Distinct from api_list_endpoints's \"query\" field (which is search text).",
      "additionalProperties": true
    },
    "headers": {
      "type": "object",
      "description": "Optional custom request headers. Sending Authorization or the connection's api_key header is rejected.",
      "additionalProperties": {"type": "string"}
    },
    "body": {
      "description": "Optional request body. When the connection's OpenAPI catalog declares application/json on the resolved operation, objects/arrays are JSON-encoded and strings that parse as JSON pass through verbatim, both with Content-Type: application/json. Strings that do not parse as JSON, and bodies on operations the catalog does not declare, fall back to: objects/arrays as application/json, strings as text/plain. An explicit Content-Type in headers always wins. Ignored for GET and HEAD."
    },
    "timeout_seconds": {
      "type": "integer",
      "minimum": 1,
      "maximum": 600,
      "description": "Optional per-call timeout override in seconds. Capped to 600 (10 minutes). Defaults to the connection's call_timeout."
    }
  }
}`)
