package middleware

import (
	"context"
	"maps"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// Upstream tool names referenced across multiple middleware files. These are
// fully-qualified MCP tool names exposed by the Trino and DataHub toolkits.
const (
	toolNameTrinoQuery       = "trino_query"
	toolNameTrinoExecute     = "trino_execute"
	toolNameSearch           = "search"
	toolNameDatahubGetEntity = "datahub_get_entity"
)

// defaultDescriptionOverrides contains built-in description overrides that
// guide agents toward discovery before running queries, and toward recording
// the queries that worked afterwards.
var defaultDescriptionOverrides = map[string]string{
	toolNameTrinoQuery: "Execute a read-only SQL query against Trino and return results. " +
		"IMPORTANT: Before writing SQL, call search to discover the table and " +
		"understand its business context (descriptions, owners, tags, glossary terms, prior insights). " +
		"Only SELECT, SHOW, DESCRIBE, EXPLAIN, and WITH statements are allowed. " +
		toolkit.CaptureRoute,
	toolNameTrinoExecute: "Execute a SQL statement against Trino, including write operations. " +
		"IMPORTANT: Before writing SQL, call search to discover the table and " +
		"understand its business context (descriptions, owners, tags, glossary terms, prior insights). " +
		"Use trino_query for read-only SELECT queries. This tool should be used when " +
		"you need to modify data or schema. " +
		toolkit.CaptureRoute,
}

// MergedDescriptionOverrides merges the built-in default overrides with
// user-provided config overrides. Config overrides take precedence.
func MergedDescriptionOverrides(configOverrides map[string]string) map[string]string {
	merged := make(map[string]string, len(defaultDescriptionOverrides)+len(configOverrides))
	maps.Copy(merged, defaultDescriptionOverrides)
	maps.Copy(merged, configOverrides)
	return merged
}

// MCPDescriptionOverrideMiddlewareDynamic creates MCP protocol-level middleware
// that replaces tool descriptions in tools/list responses. This is used to
// inject workflow guidance (e.g., "call search first") into tool descriptions
// that agents see when discovering available tools.
//
// The override map is re-resolved on every tools/list call, against that
// call's context, so per-tool descriptions authored from the admin portal take
// effect on the next listing in every replica rather than at the next restart
// of the one that served the edit. The getter is expected to merge built-in
// defaults with the stored overrides.
func MCPDescriptionOverrideMiddlewareDynamic(getOverrides func(ctx context.Context) map[string]string) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			result, err := next(ctx, method, req)
			if err != nil {
				return result, err
			}
			if method != methodToolsList {
				return result, nil
			}
			return applyDescriptionOverrides(getOverrides(ctx), result), nil
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
