package scriptsession

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// The calls a script's platform.query and platform.execute make on an
// application database (#2428). They are answered by the session caller
// itself, against the connection's pool, and never reach the MCP server: an
// application's request path runs many statements and cannot pay a tool call,
// the middleware chain and an audit row for each. They go through the host's
// call path all the same, so the draft write barrier classifies them, a
// recording keeps them and a script test answers them from the recording.
const (
	AppDBQuery   = "appdb.query"
	AppDBExecute = "appdb.execute"
)

// AppDBRunner runs statements on application databases: the postgres toolkit.
type AppDBRunner interface {
	HasConnection(name string) bool
	Query(ctx context.Context, s postgres.Statement) (*postgres.Result, error)
	Execute(ctx context.Context, s postgres.Statement) (*postgres.Result, error)
}

// AppDB is what the platform installs for the script sessions it opens.
type AppDB struct {
	// Runner returns the live runner, or nil on a deployment with none.
	Runner func() AppDBRunner
	// Authorize refuses a connection the identity on ctx may not use. ctx is
	// the session's own, which carries the run's identity.
	Authorize func(ctx context.Context, connection string) error
}

//nolint:gochecknoglobals // installed once by the platform, read by every script session it opens
var installed atomic.Pointer[AppDB]

// InstallAppDB sets the application-database runner every script session
// opened after it uses.
func InstallAppDB(a AppDB) { installed.Store(&a) }

// IsAppDB reports whether connection names an application database, which is
// how platform.query and platform.execute choose between the direct path and
// the query engine's tools.
func IsAppDB(connection string) bool {
	a := installed.Load()
	if a == nil || a.Runner == nil || connection == "" {
		return false
	}
	r := a.Runner()
	return r != nil && r.HasConnection(connection)
}

// appdbCaller answers the application-database calls of one session.
type appdbCaller struct {
	ctx context.Context
	db  *AppDB

	mu         sync.Mutex
	authorized map[string]bool
}

func newAppDBCaller(ctx context.Context) *appdbCaller {
	a := installed.Load()
	if a == nil {
		return nil
	}
	return &appdbCaller{ctx: ctx, db: a, authorized: make(map[string]bool)}
}

// call runs one application-database statement.
func (c *appdbCaller) call(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	if c == nil || c.db.Runner == nil || c.db.Runner() == nil {
		return nil, NewRefusal("no application database is served on this deployment",
			refusalEnv(postgres.CodeUnknownConnection, "no application database is served on this deployment"))
	}
	st, err := statementOf(args)
	if err != nil {
		return nil, NewRefusal(err.Error(), refusalEnv(postgres.CodeInvalidStatement, err.Error()))
	}
	if err := c.authorize(st.Connection); err != nil {
		return nil, NewRefusal(err.Error(), refusalEnv("connection_not_allowed", err.Error()))
	}
	run := c.db.Runner().Query
	if name == AppDBExecute {
		run = c.db.Runner().Execute
	}
	res, err := run(ctx, st)
	if err != nil {
		return nil, appdbRefusal(err)
	}
	return resultMap(res), nil
}

// authorize checks a connection once per session.
func (c *appdbCaller) authorize(connection string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.authorized[connection] {
		return nil
	}
	if c.db.Authorize != nil {
		if err := c.db.Authorize(c.ctx, connection); err != nil {
			return err
		}
	}
	c.authorized[connection] = true
	return nil
}

// statementOf reads a call's arguments.
func statementOf(args map[string]any) (postgres.Statement, error) {
	st := postgres.Statement{}
	st.Connection, _ = args["connection"].(string)
	st.SQL, _ = args["sql"].(string)
	if st.Connection == "" {
		return st, errors.New("an application database statement names its connection")
	}
	if p, ok := args["params"]; ok && p != nil {
		st.Params = p
	}
	st.Limit = intArg(args["limit"])
	st.TimeoutSeconds = intArg(args["timeout_seconds"])
	return st, nil
}

func intArg(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}

// resultMap is a statement's result in the shape platform.query reads from
// the query engine's tools: columns, rows as objects keyed by column, and the
// truncation flag under stats.
func resultMap(res *postgres.Result) map[string]any {
	columns := make([]any, len(res.Columns))
	for i, c := range res.Columns {
		columns[i] = map[string]any{"name": c.Name, "type": c.Type}
	}
	rows := make([]any, len(res.Rows))
	for i, r := range res.Rows {
		row := make(map[string]any, len(r))
		for j, v := range r {
			if j < len(res.Columns) {
				row[res.Columns[j].Name] = v
			}
		}
		rows[i] = row
	}
	return map[string]any{
		"connection":    res.Connection,
		"command":       res.Command,
		"rows_affected": res.RowsAffected,
		"columns":       columns,
		"rows":          rows,
		"row_count":     len(rows),
		"stats":         map[string]any{"truncated": res.Truncated, "limit": res.Limit},
	}
}

// appdbRefusal is a failed statement as the host reads a refused call. The
// text is the value-free summary, which a run's error and its audit record
// may carry; what the server said, DETAIL included, is in the envelope's
// postgres field, which only the script reads (on_error = "return").
func appdbRefusal(err error) error {
	if refused, ok := errors.AsType[*postgres.RefusedError](err); ok {
		return NewRefusal(refused.Error(), refusalEnv(refused.Code, refused.Error()))
	}
	if failed, ok := errors.AsType[*postgres.FailedError](err); ok {
		env := refusalEnv(failed.Code, failed.Error())
		if failed.Statement != nil {
			env["postgres"] = map[string]any{
				"sqlstate": failed.Statement.SQLState, "condition": failed.Statement.Condition,
				"message": failed.Statement.Message, "detail": failed.Statement.Detail, "hint": failed.Statement.Hint,
				"schema": failed.Statement.Schema, "table": failed.Statement.Table, "column": failed.Statement.Column,
				"constraint": failed.Statement.Constraint,
			}
		}
		return NewRefusal(failed.Error(), env)
	}
	return NewRefusal(fmt.Sprintf("application database: %v", err), refusalEnv(postgres.CodeUnavailable, err.Error()))
}

func refusalEnv(code, message string) map[string]any {
	return map[string]any{"code": code, "category": "tool_error", "message": message}
}
