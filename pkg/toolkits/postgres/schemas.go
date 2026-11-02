package postgres

import (
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// The JSON Schemas the two tools advertise, written by hand because their
// descriptions are what an agent reads to decide what to send.

// paramsSchemaProperty is the params argument both tools take, and each item
// of a statements list.
const paramsSchemaProperty = `"params": {
      "type": ["array", "object"],
      "description": "Values bound to the statement's parameters, never written into its text. A list binds $1, $2, ... in order; an object binds @name placeholders by name, and a placeholder the object does not name, or a name no placeholder uses, is refused. Each value is read by the type PostgreSQL gives the parameter: a number or string is parsed as that type (send a numeric as a string to keep every digit), json and jsonb take any value, an array type takes a list, bytea takes base64, null is NULL."
    }`

//nolint:gochecknoglobals // MCP tool schema must be a package-level var
var querySchema = json.RawMessage(`{
  "type": "object",
  "required": ["connection", "sql"],
  "additionalProperties": false,
  "properties": {
    "connection": {
      "type": "string",
      "description": "Name of the PostgreSQL connection (kind=postgres). Use list_connections to find one."
    },
    "sql": {
      "type": "string",
      "description": "One statement, run in a READ ONLY transaction. A statement that writes fails there; use postgres_execute. A string holding two statements is refused."
    },
    ` + paramsSchemaProperty + `,
    "limit": {
      "type": "integer",
      "minimum": 1,
      "description": "Most rows to return. Defaults to, and is capped at, the connection's max_rows. truncated says whether more rows were there."
    },
    "timeout_seconds": {
      "type": "integer",
      "minimum": 1,
      "description": "Statement timeout for this call, lower than the connection's statement_timeout. Defaults to that timeout."
    }
  }
}`)

//nolint:gochecknoglobals // MCP tool schema must be a package-level var
var executeSchema = json.RawMessage(`{
  "type": "object",
  "required": ["connection"],
  "additionalProperties": false,
  "properties": {
    "connection": {
      "type": "string",
      "description": "Name of the PostgreSQL connection (kind=postgres). Refused on a read_only connection."
    },
    "sql": {
      "type": "string",
      "description": "One statement to run (INSERT, UPDATE, DELETE, MERGE, DDL), in a transaction of its own. Give sql or statements, not both."
    },
    ` + paramsSchemaProperty + `,
    "statements": {
      "type": "array",
      "minItems": 1,
      "maxItems": 100,
      "description": "Statements run in order in ONE transaction, which commits only when every statement succeeds: a failure in any of them leaves no change from the others, and the result names the statement that failed. Each item is {sql, params}. BEGIN, COMMIT, ROLLBACK and SAVEPOINT are refused: the tool holds the transaction.",
      "items": {
        "type": "object",
        "required": ["sql"],
        "additionalProperties": false,
        "properties": {
          "sql": {"type": "string", "description": "One statement."},
          ` + paramsSchemaProperty + `
        }
      }
    },
    "timeout_seconds": {
      "type": "integer",
      "minimum": 1,
      "description": "Statement timeout for each statement of this call, lower than the connection's statement_timeout. Defaults to that timeout."
    }
  }
}`)

func queryTool() *mcp.Tool {
	return &mcp.Tool{
		Name:        ToolQuery,
		Title:       "Query PostgreSQL",
		Description: "Run one read-only SQL statement on a PostgreSQL connection and return its columns (name and PostgreSQL type) and rows. Values are bound as parameters ($1 from a params list, @name from a params object), never written into the SQL. numeric is returned as a decimal string, timestamptz as RFC 3339, json/jsonb decoded, uuid as a string, arrays as lists, bytea as base64.",
		InputSchema: querySchema,
		Annotations: toolkit.ReadOnlyAnnotations(),
	}
}

func executeTool() *mcp.Tool {
	return &mcp.Tool{
		Name:        ToolExecute,
		Title:       "Execute on PostgreSQL",
		Description: "Run one writing SQL statement, or a statements list in one transaction that commits only if every statement succeeds, on a PostgreSQL connection. Returns each statement's command, rows affected and any RETURNING rows. Values are bound as parameters, never written into the SQL. Refused on a read_only connection.",
		InputSchema: executeSchema,
		Annotations: toolkit.WriteAnnotations(true),
	}
}
