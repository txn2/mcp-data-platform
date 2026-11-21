package trino

import (
	"context"
	"errors"

	trinotools "github.com/txn2/mcp-trino/pkg/tools"

	"github.com/txn2/mcp-data-platform/internal/sqltables"
)

// ReadToolInterceptor holds the tools that only read to statements that only
// read, on every connection whatever its read_only setting: trino_query runs
// a statement sqltables.IsRead lists as a read, and trino_explain explains
// one without running it. Writing is trino_execute's, which a persona is
// granted separately; a statement kind the read list does not name is
// refused here rather than run by a tool a persona was given to read.
type ReadToolInterceptor struct{}

var (
	errQueryNotRead = errors.New("trino_query runs only statements that read (SELECT, WITH, VALUES, TABLE, SHOW, " +
		"DESCRIBE, or EXPLAIN without ANALYZE); use trino_execute for any other statement")
	errExplainRuns = errors.New("trino_explain explains a statement without running it; " +
		"EXPLAIN ANALYZE and ANALYZE run it, so they are not explained here")
)

// Intercept refuses a statement the read tool may not run.
func (ReadToolInterceptor) Intercept(_ context.Context, sql string, tool trinotools.ToolName) (string, error) {
	switch tool {
	case trinotools.ToolQuery:
		if !sqltables.IsRead(sql) {
			return "", errQueryNotRead
		}
	case trinotools.ToolExplain:
		// The tool sends "EXPLAIN (TYPE ...) <sql>"; that is a read only
		// when what it explains is not run.
		if !sqltables.IsRead("EXPLAIN " + sql) {
			return "", errExplainRuns
		}
	}
	return sql, nil
}
