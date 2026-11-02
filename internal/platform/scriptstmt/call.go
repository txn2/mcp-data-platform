package scriptstmt

import (
	"go.starlark.net/starlark"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptsession"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsql"
)

// argSQL is the statement argument of every call Call builds.
const argSQL = "sql"

// Call is the call platform.query (write false) or platform.execute (write
// true) makes for one statement.
//
// On an application database (#2428) it is the session's direct path, which
// never reaches the MCP server, with each :name placeholder rewritten to $n and
// the values bound natively. On the query engine it is trino_query or
// trino_execute with the values written into the SQL as literals.
func Call(write bool, connection, sql string, params *starlark.Dict, timeout int) (tool string, call map[string]any, err error) {
	if scriptsession.IsAppDB(connection) {
		tool, call, err = appdbCall(write, sql, params)
	} else {
		tool, call, err = engineCall(write, sql, params)
	}
	if err != nil {
		return "", nil, err
	}
	if connection != "" {
		call["connection"] = connection
	}
	if timeout != 0 {
		call[ArgTimeout] = timeout
	}
	return tool, call, nil
}

// appdbCall is the session's direct path, the values bound natively.
func appdbCall(write bool, sql string, params *starlark.Dict) (tool string, call map[string]any, err error) {
	positional, args, err := scriptsql.Positional(sql, params)
	if err != nil {
		return "", nil, err //nolint:wrapcheck // names the placeholder; the binding names itself around it
	}
	tool, call = scriptsession.AppDBQuery, map[string]any{argSQL: positional}
	if write {
		tool = scriptsession.AppDBExecute
	}
	if len(args) > 0 {
		call["params"] = args
	}
	return tool, call, nil
}

// engineCall is the query engine's tool, the values written as literals.
func engineCall(write bool, sql string, params *starlark.Dict) (tool string, call map[string]any, err error) {
	bound, err := scriptsql.Bind(sql, params)
	if err != nil {
		return "", nil, err //nolint:wrapcheck // names the placeholder; the binding names itself around it
	}
	tool = ToolQuery
	if write {
		tool = ToolExecute
	}
	return tool, map[string]any{argSQL: bound}, nil
}
