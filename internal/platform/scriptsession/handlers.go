package scriptsession

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/txn2/mcp-data-platform/internal/calltransform"
	"github.com/txn2/mcp-data-platform/internal/platform/callhandler"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// AppDBHandlers puts a script's statement on an application database through
// the tool-call handlers (#2436, #2634): that path binds straight to a pool
// and never crosses the tools/call chain, so the handlers are asked here. A
// policy decides the statement; a transform covering the run changes the
// rows it returns, and refuses a statement using a column it protects.
type AppDBHandlers interface {
	// Statement is what the handlers on its connection make of a statement:
	// an error the run is refused with, or the transformer its rows pass
	// through (nil for none).
	Statement(ctx context.Context, st callhandler.Statement) (calltransform.Transformer, error)
}

//nolint:gochecknoglobals // installed once by the platform, read by every script session it opens, as the application database is
var handlers atomic.Pointer[AppDBHandlers]

// InstallAppDBHandlers sets the tool-call handlers every script session's
// application database statements are put through.
func InstallAppDBHandlers(h AppDBHandlers) {
	if h == nil {
		handlers.Store(nil)
		return
	}
	handlers.Store(&h)
}

// throughHandlers asks the installed handlers about one statement, for the
// run's identity, unless the run is a handler's own.
func (c *appdbCaller) throughHandlers(ctx context.Context, name, connection, sql string) (calltransform.Transformer, error) {
	h := handlers.Load()
	if h == nil || callhandler.Within(c.ctx) {
		return nil, nil //nolint:nilnil // no handlers to ask: no transformer, and nothing refused
	}
	c.mu.Lock()
	persona := c.authorized[connection]
	c.mu.Unlock()
	t, err := (*h).Statement(ctx, callhandler.Statement{User: runUser(c.ctx), Persona: persona, Connection: connection, Tool: name, SQL: sql})
	if err != nil {
		return nil, NewRefusal(err.Error(), refusalEnv(codeNotAllowed, err.Error()))
	}
	return t, nil
}

// runUser is who a run's statements act for, as the handlers match them: an
// endpoint's caller, a saved run's version author, a draft's or test's runner.
func runUser(ctx context.Context) string {
	if ep, ok := endpointOf(ctx); ok {
		return ep.Email
	}
	if u := middleware.GetPreAuthenticatedUser(ctx); u != nil {
		if u.OnBehalfOf != "" {
			return u.OnBehalfOf
		}
		return u.Email
	}
	if pc := middleware.GetPlatformContext(ctx); pc != nil {
		return pc.UserEmail
	}
	return ""
}

// transformRows passes a statement's rows through t.
func transformRows(ctx context.Context, t calltransform.Transformer, out map[string]any) (map[string]any, error) {
	if t == nil {
		return out, nil
	}
	list, _ := out[argRows].([]any)
	rows := make([]map[string]any, 0, len(list))
	for _, r := range list {
		if m, ok := r.(map[string]any); ok {
			rows = append(rows, m)
		}
	}
	got, err := t.Rows(ctx, columnNames(out), rows)
	if err != nil {
		return nil, NewRefusal(err.Error(), refusalEnv(codeNotAllowed, err.Error()))
	}
	back := make([]any, 0, len(got))
	for _, r := range got {
		back = append(back, r)
	}
	out[argRows], out["row_count"] = back, len(back)
	return out, nil
}

// columnNames are a result's column names, in order.
func columnNames(out map[string]any) []string {
	cols, _ := out["columns"].([]any)
	names := make([]string, 0, len(cols))
	for _, c := range cols {
		m, _ := c.(map[string]any)
		if n, ok := m["name"].(string); ok {
			names = append(names, n)
		}
	}
	return names
}

// operationOf is the operation a policy matches an application database call
// by: a statement in a transaction, and a query read a page at a time, are
// the operation the same statement is outside one, so a policy on
// appdb.query covers tx.query and query_iter too.
func operationOf(name string) string {
	switch name {
	case AppDBTxQuery, AppDBCursorOpen:
		return AppDBQuery
	case AppDBTxExecute:
		return AppDBExecute
	case AppDBTxBatch:
		return AppDBBatch
	}
	return name
}

// batchThroughHandlers asks the handlers about each statement of a batch, by
// its own SQL: one the policies refuse refuses the batch before any runs.
func (c *appdbCaller) batchThroughHandlers(ctx context.Context, connection string, stmts []postgres.Statement) ([]calltransform.Transformer, error) {
	out := make([]calltransform.Transformer, len(stmts))
	for i, st := range stmts {
		t, err := c.throughHandlers(ctx, AppDBBatch, connection, st.SQL)
		if err != nil {
			return nil, err
		}
		out[i] = t
	}
	return out, nil
}

// transformResults passes each of a batch's results through its statement's
// transformer.
func transformResults(ctx context.Context, transforms []calltransform.Transformer, out map[string]any) (map[string]any, error) {
	results, _ := out["results"].([]any)
	back := make([]any, len(results))
	copy(back, results)
	for i, t := range transforms {
		if i >= len(back) {
			break
		}
		m, ok := back[i].(map[string]any)
		if !ok {
			continue
		}
		changed, err := transformRows(ctx, t, m)
		if err != nil {
			return nil, err
		}
		back[i] = changed
	}
	if results != nil {
		out["results"] = back
	}
	return out, nil
}

// copyInThroughHandlers asks the policies about a copy_in, read as the COPY it
// runs.
func (c *appdbCaller) copyInThroughHandlers(ctx context.Context, in postgres.CopyIn) error {
	sql := "COPY " + in.Table
	if len(in.Columns) > 0 {
		sql += " (" + strings.Join(in.Columns, ", ") + ")"
	}
	_, err := c.throughHandlers(ctx, AppDBCopyIn, in.Connection, sql+" FROM STDIN")
	return err
}

// copyOutThroughHandlers asks the policies about a copy_out's query, and
// refuses it when a transform covers the run: COPY writes the database's own
// bytes, which no transform reads row by row.
func (c *appdbCaller) copyOutThroughHandlers(ctx context.Context, st postgres.Statement) error {
	t, err := c.throughHandlers(ctx, AppDBCopyOut, st.Connection, st.SQL)
	if err != nil || t == nil {
		return err
	}
	msg := fmt.Sprintf("copy_out on application database %q is refused: a transform changes what this run reads from it, "+
		"and COPY writes the database's own bytes; read the rows with platform.query or query_iter, which pass through it", st.Connection)
	return NewRefusal(msg, refusalEnv(codeNotAllowed, msg))
}
