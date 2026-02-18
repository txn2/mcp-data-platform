// Package trino provides a Trino toolkit adapter for the MCP data platform.
package trino

import (
	"context"
	"fmt"

	trinotools "github.com/txn2/mcp-trino/pkg/tools"
)

// ReadOnlyInterceptor blocks write operations when read_only mode is enabled.
// It delegates write detection to the upstream mcp-trino IsWriteSQL function.
type ReadOnlyInterceptor struct{}

// NewReadOnlyInterceptor creates a new read-only query interceptor.
func NewReadOnlyInterceptor() *ReadOnlyInterceptor {
	return &ReadOnlyInterceptor{}
}

// Intercept checks if the query is a write operation and blocks it in read-only mode.
func (*ReadOnlyInterceptor) Intercept(_ context.Context, sql string, _ trinotools.ToolName) (string, error) {
	if trinotools.IsWriteSQL(sql) {
		return "", fmt.Errorf("write operations not allowed in read-only mode")
	}
	return sql, nil
}

// Verify interface compliance.
var _ trinotools.QueryInterceptor = (*ReadOnlyInterceptor)(nil)
