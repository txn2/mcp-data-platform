package apigateway

import "encoding/json"

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
    "query": {
      "type": "object",
      "description": "Optional query string parameters. Values may be strings, numbers, or booleans; arrays send the parameter once per value.",
      "additionalProperties": true
    },
    "headers": {
      "type": "object",
      "description": "Optional custom request headers. Sending Authorization or the connection's api_key header is rejected.",
      "additionalProperties": {"type": "string"}
    },
    "body": {
      "description": "Optional request body. Objects/arrays are JSON-encoded with Content-Type application/json. Strings are sent verbatim with Content-Type text/plain unless an explicit Content-Type header is provided. Ignored for GET and HEAD."
    },
    "timeout_seconds": {
      "type": "integer",
      "minimum": 1,
      "maximum": 600,
      "description": "Optional per-call timeout override in seconds. Capped to 600 (10 minutes). Defaults to the connection's call_timeout."
    }
  }
}`)
