package trino

import (
	"context"
	"errors"

	trinotools "github.com/txn2/mcp-trino/pkg/tools"

	"github.com/txn2/mcp-data-platform/internal/sqltables"
)

// ReadToolInterceptor holds trino_query to statements that only read, on every
// connection whatever its read_only setting: it runs a statement
// sqltables.IsRead lists as a read, EXPLAIN without ANALYZE among them, which
// is how a plan is read since trino_explain was retired (#2808). Writing is
// trino_execute's, which a persona is granted separately; a statement kind the
// read list does not name is refused here rather than run by a tool a persona
// was given to read.
type ReadToolInterceptor struct{}

var errQueryNotRead = errors.New("trino_query runs only statements that read (SELECT, WITH, VALUES, TABLE, SHOW, " +
	"DESCRIBE, or EXPLAIN without ANALYZE); use trino_execute for any other statement")

// Intercept refuses a statement the read tool may not run.
func (ReadToolInterceptor) Intercept(_ context.Context, sql string, tool trinotools.ToolName) (string, error) {
	if tool == trinotools.ToolQuery && !sqltables.IsRead(sql) {
		return "", errQueryNotRead
	}
	return sql, nil
}
