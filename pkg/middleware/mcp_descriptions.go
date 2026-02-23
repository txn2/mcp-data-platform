package middleware

import (
	"context"
	"maps"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// defaultDescriptionOverrides contains built-in description overrides that
// guide agents toward DataHub discovery before running queries.
var defaultDescriptionOverrides = map[string]string{
	"trino_query": "Execute a read-only SQL query against Trino and return results. " +
		"IMPORTANT: Before writing SQL, call datahub_search to discover the table and " +
		"understand its business context (descriptions, owners, tags, glossary terms). " +
		"The search results include schema previews with column names and types. " +
		"Only SELECT, SHOW, DESCRIBE, EXPLAIN, and WITH statements are allowed.",
	"trino_execute": "Execute a SQL statement against Trino, including write operations. " +
		"IMPORTANT: Before writing SQL, call datahub_search to discover the table and " +
		"understand its business context (descriptions, owners, tags, glossary terms). " +
		"Use trino_query for read-only SELECT queries. This tool should be used when " +
		"you need to modify data or schema.",
}

// MergedDescriptionOverrides merges the built-in default overrides with
// user-provided config overrides. Config overrides take precedence.
func MergedDescriptionOverrides(configOverrides map[string]string) map[string]string {
	merged := make(map[string]string, len(defaultDescriptionOverrides)+len(configOverrides))
	maps.Copy(merged, defaultDescriptionOverrides)
	maps.Copy(merged, configOverrides)
	return merged
}

// MCPDescriptionOverrideMiddleware creates MCP protocol-level middleware that
// replaces tool descriptions in tools/list responses. This is used to inject
// workflow guidance (e.g., "call datahub_search first") into tool descriptions
// that agents see when discovering available tools.
func MCPDescriptionOverrideMiddleware(overrides map[string]string) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			result, err := next(ctx, method, req)
			if err != nil {
				return result, err
			}
			if method != methodToolsList {
				return result, nil
			}
			return applyDescriptionOverrides(overrides, result), nil
		}
	}
}

// applyDescriptionOverrides replaces tool descriptions for matching names.
func applyDescriptionOverrides(overrides map[string]string, result mcp.Result) mcp.Result {
	listResult, ok := result.(*mcp.ListToolsResult)
	if !ok || listResult == nil || len(overrides) == 0 {
		return result
	}

	for _, tool := range listResult.Tools {
		if desc, found := overrides[tool.Name]; found {
			tool.Description = desc
		}
	}

	return listResult
}
