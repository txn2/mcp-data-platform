package graphql

import (
	"encoding/json"

	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// The JSON Schemas the three tools advertise. They are written by hand
// rather than derived from the input structs because the descriptions
// are the tool's user interface: they are what an agent reads to decide
// what to send.

// discoverSchema is the JSON Schema for the graphql_discover tool
// input.
//
//nolint:gochecknoglobals // MCP tool schema must be a package-level var
var discoverSchema = json.RawMessage(`{
  "type": "object",
  "required": ["connection"],
  "additionalProperties": false,
  "properties": {
    "connection": {
      "type": "string",
      "description": "Name of the registered GraphQL connection (kind=graphql). Required. Use list_connections to discover available connections."
    },
    "query": {
      "type": "string",
      "description": "Optional case-insensitive search across the operation's dotted id, its description, the type it returns and its argument names. Under \"lexical\" ranking it is an AND filter: every whitespace-separated token has to appear. Under \"hybrid\" and \"semantic\" those matches come first and up to five near neighbors by intent follow them, each row carrying a score and lexical_match, with matched_lexical and shown_semantic saying where the matches ended. Empty returns the connection's operations (capped by limit), unscored. Ignored with operation_id."
    },
    "operation_id": {
      "type": "string",
      "description": "Optional. Return this one operation's arguments with their types and defaults, the input-object types they reference, the return type's field tree, and a skeleton document that already validates. Values come from the operation_id field of a returned operation; the kind prefix may be omitted when only one kind defines that dotted id."
    },
    "limit": {
      "type": "integer",
      "minimum": 1,
      "maximum": 500,
      "description": "Optional cap on the number of operations returned. Defaults to 50."
    },
    "ranking": {
      "type": "string",
      "enum": ["lexical", "semantic", "hybrid"],
      "description": "Optional ranking algorithm for query. Defaults to \"hybrid\" whenever this connection's schema has been indexed, otherwise \"lexical\". \"hybrid\" blends embedding cosine similarity with per-token substring match and is the recommended choice. \"semantic\" ranks by cosine alone, which finds an operation by intent even when no words overlap. \"lexical\" is a deterministic per-token substring match with no embedding dependency. semantic and hybrid need an embedding provider and an indexed schema; without either they fall back to lexical and the note says why."
    },
    "depth": {
      "type": "integer",
      "minimum": 1,
      "maximum": 10,
      "description": "Optional depth for the return-type field tree and the skeleton's selection set, with operation_id. Defaults to 4, which is what a paged connection needs to reach data (connection, edges, node, the node's own fields). Raise it for a deeply nested return type; every level costs context."
    }
  }
}`)

// paginateSchemaProperty is the `paginate` block both graphql_query and
// graphql_export take. One fragment spliced into both schemas, so the
// two tools cannot drift on the block they share.
const paginateSchemaProperty = `
    "paginate": {
      "type": "object",
      "required": ["items", "cursor_variable"],
      "additionalProperties": false,
      "description": "Walk every page of a paged connection inside this one call and merge the array that items names from each page, returning one page's shape holding every row. The cursor from each page is bound to the variable cursor_variable names, so the document itself is unchanged between pages. By default the cursor is read from the Relay pageInfo beside the items array (pageInfo.endCursor, pageInfo.hasNextPage); an upstream that pages some other way names next_cursor_path itself. The walk stops when the upstream reports no next page, at max_pages, or on the first page the upstream refuses, and reports pages_fetched, items_merged, stopped_by (end, max_pages, error) and the cursor the next page would have used. Omit to fetch one page.",
      "properties": {
        "items": {
          "type": "string",
          "description": "Dotted path, inside the response's data object, of the array merged across pages (\"masterData.product.query.edges\", \"searchAcrossEntities.searchResults\"). Required."
        },
        "cursor_variable": {
          "type": "string",
          "description": "Name of the document variable the next page's cursor is bound to (\"after\", \"scrollId\"). The document must declare it. Required."
        },
        "next_cursor_path": {
          "type": "string",
          "description": "Dotted path, inside data, holding the next page's cursor (\"searchAcrossEntities.nextScrollId\"). Defaults to the Relay pageInfo.endCursor beside the items array. A cursor that is absent, null or empty ends the walk."
        },
        "has_next_path": {
          "type": "string",
          "description": "Dotted path, inside data, of a boolean saying whether another page exists. Defaults to the Relay pageInfo.hasNextPage beside the items array when next_cursor_path is also left out. An upstream that signals the end with a null cursor needs no such field."
        },
        "max_pages": {
          "type": "integer",
          "minimum": 1,
          "maximum": 1000,
          "description": "Upper bound on pages walked. Defaults to 10. Reaching it is reported as stopped_by max_pages with next_cursor set, so the walk can be continued."
        }
      }
    }`

// variablesSchemaProperty is the `variables` block both executing tools
// take. Its two admitted forms are the point: a client that sends
// structured arguments sends the object, and a client that stringifies
// them sends the string, and both must reach the same request.
const variablesSchemaProperty = `
    "variables": {
      "type": ["object", "string"],
      "description": "Values for the document's variables, as a JSON object ({\"first\": 10}) or as a string holding that object's JSON (\"{\\\"first\\\": 10}\"). Both forms are accepted and behave identically. A variable the document declares but this object omits is absent from the call, so the schema's own default applies — which is why graphql_discover's stub carries only the required arguments."
    }`

// querySchema is the JSON Schema for the graphql_query tool input.
//
//nolint:gochecknoglobals // MCP tool schema must be a package-level var
var querySchema = json.RawMessage(`{
  "type": "object",
  "required": ["connection", "query"],
  "additionalProperties": false,
  "properties": {
    "connection": {
      "type": "string",
      "description": "Name of the registered GraphQL connection (kind=graphql). Required."
    },
    "query": {
      "type": "string",
      "description": "The GraphQL document to execute. Required. graphql_discover renders one for any operation, already carrying a valid selection set. Subscriptions are refused, and so are __schema / __type selections: call graphql_discover for the schema instead."
    },` + variablesSchemaProperty + `,
    "operation_name": {
      "type": "string",
      "description": "Which operation of the document to run. Required when the document defines more than one; a document with a single operation needs no name."
    },
    "timeout_seconds": {
      "type": "integer",
      "minimum": 1,
      "maximum": 600,
      "description": "Optional per-call timeout. The connection's own call_timeout still applies and the lower of the two wins."
    },` + paginateSchemaProperty + `
  }
}`)

// exportSchema is the JSON Schema for the graphql_export tool input.
//
//nolint:gochecknoglobals // MCP tool schema must be a package-level var
var exportSchema = json.RawMessage(`{
  "type": "object",
  "required": ["connection", "query", "name"],
  "additionalProperties": false,
  "properties": {
    "connection": {
      "type": "string",
      "description": "Name of the registered GraphQL connection (kind=graphql). Required."
    },
    "query": {
      "type": "string",
      "description": "The GraphQL document to execute. Required. Validated, authorized and depth-checked exactly as graphql_query validates it."
    },` + variablesSchemaProperty + `,
    "operation_name": {
      "type": "string",
      "description": "Which operation of the document to run. Required when the document defines more than one."
    },
    "name": {
      "type": "string",
      "description": "Name for the created asset. Required — it is what a person will look for later."
    },
    "description": {
      "type": "string",
      "description": "Optional description stored on the asset: what this result answers, and any caveat a reader needs."
    },
    "timeout_seconds": {
      "type": "integer",
      "minimum": 1,
      "maximum": 600,
      "description": "Optional per-call timeout. The connection's own call_timeout still applies and the lower of the two wins."
    },
    "tags": {
      "type": "array",
      "items": {"type": "string"},
      "description": "Optional tags for categorization, carried by the asset or the managed resource this call writes."
    },
    "idempotency_key": {
      "type": "string",
      "description": "Optional idempotency key. When supplied, a prior export by this caller with the same key returns the existing asset's metadata without re-running the document. Not valid with a resource destination, which already makes a repeat call the next version of one file."
    },
    "create_public_link": {
      "type": "boolean",
      "description": "When true, also create a public share link for the resulting asset. Returns share_url alongside the asset metadata."
    },
    "resource": ` + toolkit.ResourceDestinationSchema + `,` + paginateSchemaProperty + `
  }
}`)
