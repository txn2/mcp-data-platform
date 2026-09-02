package apigateway

import "encoding/json"

// Every api_* input schema below is closed at the top level
// ("additionalProperties": false), so a misnamed argument is refused by the
// tool boundary with an error naming the offending property instead of being
// dropped by the struct unmarshal. Nested maps stay open on purpose:
// query_params, headers, and body carry the UPSTREAM API's names, not ours.
//
// The platform-injected session_id argument is stripped from the arguments by
// the session resolver before the SDK validates them
// (middleware.SessionResolver, pkg/middleware/mcp_session_handle.go), so a
// closed schema does not conflict with it.

// discoverSchema is the JSON Schema for the api_discover tool input.
//
//nolint:gochecknoglobals // MCP tool schema must be a package-level var
var discoverSchema = json.RawMessage(`{
  "type": "object",
  "required": ["connection"],
  "additionalProperties": false,
  "properties": {
    "connection": {
      "type": "string",
      "description": "Name of the registered API connection (kind=api). Required. Use list_connections to discover available connections."
    },
    "spec": {
      "type": "string",
      "description": "Optional component spec within the connection's catalog. Without operation_id, restricts the operations returned to this spec; with operation_id, disambiguates an id that more than one component spec defines. Values come from the specs level (the name field) or from the spec field on a returned operation. Pair with query to narrow to a section of a large catalog (e.g. spec=\"orders\" + query=\"refund\")."
    },
    "operation_id": {
      "type": "string",
      "description": "Optional. Return this one operation's parameters, request body, and per-status responses, ready for api_invoke_endpoint. Values come from the operation_id field on a returned operation."
    },
    "query": {
      "type": "string",
      "description": "Optional case-insensitive search across operation_id, path, summary, spec name, and tags. Multiple whitespace-separated tokens combine with AND, so \"gift list\" matches operations containing both \"gift\" and \"list\" in any of those fields. On a multi-spec catalog a query with no spec ranks operations across every spec. Empty returns the full list (capped by limit). Ignored with operation_id."
    },
    "limit": {
      "type": "integer",
      "minimum": 1,
      "maximum": 500,
      "description": "Optional cap on the number of operations returned. Defaults to 50. Pass a higher value when exploring large APIs."
    },
    "ranking": {
      "type": "string",
      "enum": ["lexical", "semantic", "hybrid"],
      "description": "Optional ranking algorithm for query. Defaults to \"hybrid\" whenever this connection has an embedding index available (the platform default), otherwise \"lexical\". \"hybrid\" blends embedding cosine similarity with per-token substring match: best for free-form intent queries that may also share path/tag vocabulary, and the recommended choice. \"semantic\" ranks by embedding cosine similarity only, which finds endpoints by intent (\"create order\" finds POST /v1/orders) even when no words overlap. \"lexical\" is a fast, deterministic per-token substring match with no embedding dependency; pass it explicitly to opt out of semantic ranking. semantic and hybrid require an embedding provider; if unavailable they fall back to lexical and a note explains the reason."
    }
  }
}`)

// paginateSchemaProperty is the `paginate` block both api_invoke_endpoint
// and api_export take (issue #1535). One fragment spliced into both
// schemas, so the two tools cannot drift on the block they share.
const paginateSchemaProperty = `
    "paginate": {
      "type": "object",
      "required": ["items"],
      "additionalProperties": false,
      "description": "Walk every page of a paginated collection inside this one call and merge the array that items names from each page. How the next page is reached is decided per page from the response: an RFC 5988 Link rel=\"next\" header, @odata.nextLink, or a URL-valued next field is followed (pinned to the connection's host and re-checked against the route policy on every page); a next_cursor / nextCursor / next_page_token / nextPageToken / next value is sent back as the query parameter cursor_param names; with neither signal, page_param is advanced by page_step from its value in query_params until a page has no items. The walk stops at the first page with no next signal or no items, at max_pages, or at the byte cap, and reports pages_fetched, items_merged, and stopped_by (end, max_pages, max_bytes). A 429 or 503 with Retry-After pauses the walk for that interval and retries the page; any other failed page fails the call, naming the page. Omit to fetch one page and have its pagination signal reported without being followed.",
      "properties": {
        "items": {
          "type": "string",
          "description": "Key of the array merged across pages (data, items, results, value), a dotted path to a nested one (result.items), or \"$\" when the page body itself is the array. Required."
        },
        "cursor_param": {
          "type": "string",
          "description": "Query parameter a body cursor is sent back as (cursor, page_token, starting_after). Required when the API pages by cursor and page_param is not named."
        },
        "page_param": {
          "type": "string",
          "description": "Query parameter advanced when a page carries no next signal (page, offset). Its starting value must be in query_params; the first page is requested exactly as given."
        },
        "page_step": {
          "type": "integer",
          "minimum": 1,
          "description": "What page_param is advanced by per page. Defaults to 1 (page numbers); set the page size for an offset parameter."
        },
        "max_pages": {
          "type": "integer",
          "minimum": 1,
          "maximum": 10000,
          "description": "Upper bound on pages walked. Defaults to 100. Reaching it is reported as stopped_by max_pages with the signal for the next page in pagination."
        }
      }
    }`

// apiExportInputSchema is the JSON Schema for the api_export tool
// input. Mirrors invokeEndpointSchema for connection/method/path/
// query/headers/body and adds the portal-asset metadata fields
// (name, description, tags, idempotency_key, create_public_link)
// matched to trino_export's surface.
//
//nolint:gochecknoglobals // MCP tool schema must be a package-level var
var apiExportInputSchema = json.RawMessage(`{
  "type": "object",
  "required": ["connection", "name"],
  "additionalProperties": false,
  "properties": {
    "connection": {
      "type": "string",
      "description": "Name of the registered API connection (kind=api). Required."
    },
    "operation_id": {
      "type": "string",
      "description": "The operation_id returned by api_discover. Address the operation by this stable identifier instead of method+path; the platform resolves it to method and path from the connection's catalog. Supply either operation_id or method+path, not both. For a templated path, pass the placeholder values in path_params."
    },
    "path_params": {
      "type": "object",
      "description": "Values for the {placeholder} holes in the resolved operation's path template, e.g. {\"id\": \"123\"} for /v1/users/{id}. One segment may carry more than one placeholder or mix a placeholder with literal text (/points/{latitude},{longitude}, /files/{name}.json); name each placeholder separately. Only valid with operation_id. Every template placeholder must have a value; each value is URL-escaped in place.",
      "additionalProperties": {"type": "string"}
    },
    "spec": {
      "type": "string",
      "description": "Optional component spec name, used only with operation_id to disambiguate when the same operation_id is defined by more than one spec in the connection's catalog."
    },
    "method": {
      "type": "string",
      "enum": ["GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "PROPFIND", "MKCOL", "MOVE", "COPY"],
      "description": "HTTP method. Required unless operation_id is supplied."
    },
    "path": {
      "type": "string",
      "description": "Request path joined to the connection's base URL. Required unless operation_id is supplied. Must start with \"/\"."
    },
    "query_params": {
      "type": "object",
      "description": "Optional HTTP query-string parameters sent to the upstream. Distinct from api_discover's \"query\" field (which is search text).",
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
    },` + paginateSchemaProperty + `
  }
}`)

// invokeEndpointSchema is the JSON Schema for the api_invoke_endpoint tool input.
//
//nolint:gochecknoglobals // MCP tool schema must be a package-level var
var invokeEndpointSchema = json.RawMessage(`{
  "type": "object",
  "required": ["connection"],
  "additionalProperties": false,
  "properties": {
    "connection": {
      "type": "string",
      "description": "Name of the registered API connection (kind=api). Required. Use list_connections to discover available connections."
    },
    "operation_id": {
      "type": "string",
      "description": "The operation_id returned by api_discover. Address the operation by this stable identifier instead of method+path; the platform resolves it to the method and path template from the connection's catalog. Supply either operation_id or method+path, not both. For a templated path (e.g. /v1/users/{id}), pass the placeholder values in path_params rather than substituting them by hand."
    },
    "path_params": {
      "type": "object",
      "description": "Values for the {placeholder} holes in the resolved operation's path template, e.g. {\"id\": \"123\"} for /v1/users/{id}. One segment may carry more than one placeholder or mix a placeholder with literal text (/points/{latitude},{longitude}, /files/{name}.json); name each placeholder separately. Only valid with operation_id. Every template placeholder must have a value; each value is URL-escaped in place.",
      "additionalProperties": {"type": "string"}
    },
    "spec": {
      "type": "string",
      "description": "Optional component spec name, used only with operation_id to disambiguate when the same operation_id is defined by more than one spec in the connection's catalog. The ambiguity error names the candidate specs."
    },
    "method": {
      "type": "string",
      "enum": ["GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "PROPFIND", "MKCOL", "MOVE", "COPY"],
      "description": "HTTP method. Required unless operation_id is supplied. Use for raw or uncataloged calls."
    },
    "path": {
      "type": "string",
      "description": "Request path joined to the connection's base URL. Examples: \"/v1/users/123\", \"/api/items\". Required unless operation_id is supplied. Must start with \"/\". When you use method+path you substitute any path parameters yourself."
    },
    "query_params": {
      "type": "object",
      "description": "Optional HTTP query-string parameters sent to the upstream. Values may be strings, numbers, or booleans; arrays send the parameter once per value. Distinct from api_discover's \"query\" field (which is search text).",
      "additionalProperties": true
    },
    "headers": {
      "type": "object",
      "description": "Optional custom request headers. Sending Authorization or the connection's api_key header is rejected.",
      "additionalProperties": {"type": "string"}
    },
    "body": {
      "description": "Optional request body. When the connection's OpenAPI catalog declares application/json on the resolved operation, objects/arrays are JSON-encoded and strings that parse as JSON pass through verbatim, both with Content-Type: application/json. Strings that do not parse as JSON, and bodies on operations the catalog does not declare, fall back to: objects/arrays as application/json, strings as text/plain. When the catalog declares multipart/form-data, pass an object of form fields: a scalar becomes a text field, an array becomes one part per element, and a file part is {\"filename\": \"data.csv\", \"content_type\": \"text/csv\", \"content\": \"...\"} — use \"content_base64\" instead of \"content\" for binary. The platform generates the multipart boundary, so never assemble a multipart body by hand or set its Content-Type. An explicit Content-Type in headers otherwise wins. Ignored for GET, HEAD, and MKCOL."
    },
    "timeout_seconds": {
      "type": "integer",
      "minimum": 1,
      "maximum": 600,
      "description": "Optional per-call timeout override in seconds. Capped to 600 (10 minutes). Defaults to the connection's call_timeout."
    },` + paginateSchemaProperty + `
  }
}`)
