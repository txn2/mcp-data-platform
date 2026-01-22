// Package trino provides a Trino toolkit adapter for the MCP data platform.
package trino

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	trinotools "github.com/txn2/mcp-trino/pkg/tools"
)

// ReadOnlyInterceptor blocks write operations when read_only mode is enabled.
// This interceptor detects SQL statements that modify data or schema.
type ReadOnlyInterceptor struct{}

// NewReadOnlyInterceptor creates a new read-only query interceptor.
func NewReadOnlyInterceptor() *ReadOnlyInterceptor {
	return &ReadOnlyInterceptor{}
}

// writeKeywords are SQL keywords that indicate write operations.
// These are matched at the beginning of SQL statements (after stripping comments/whitespace).
var writeKeywords = []string{
	"INSERT",
	"UPDATE",
	"DELETE",
	"DROP",
	"CREATE",
	"ALTER",
	"TRUNCATE",
	"GRANT",
	"REVOKE",
	"MERGE",
	"CALL",
	"EXECUTE",
}

// writePattern matches SQL statements that start with write keywords.
// Handles optional leading whitespace and common comment styles.
var writePattern = regexp.MustCompile(
	`(?i)^\s*(?:--[^\n]*\n\s*|/\*[\s\S]*?\*/\s*)*\s*(` +
		strings.Join(writeKeywords, "|") +
		`)(?:\s|$|;|\()`,
)

// Intercept checks if the query is a write operation and blocks it in read-only mode.
func (r *ReadOnlyInterceptor) Intercept(_ context.Context, sql string, _ trinotools.ToolName) (string, error) {
	if isWriteQuery(sql) {
		return "", fmt.Errorf("write operations not allowed in read-only mode")
	}
	return sql, nil
}

// isWriteQuery checks if the SQL query is a write operation.
func isWriteQuery(sql string) bool {
	// Normalize whitespace and check against pattern
	normalized := strings.TrimSpace(sql)
	return writePattern.MatchString(normalized)
}

// Verify interface compliance.
var _ trinotools.QueryInterceptor = (*ReadOnlyInterceptor)(nil)
