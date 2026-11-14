package scriptwiring

import (
	"context"
	"fmt"
	"strings"

	trinoclient "github.com/txn2/mcp-trino/pkg/client"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epwire"
	"github.com/txn2/mcp-data-platform/internal/platform/callhandler"
	"github.com/txn2/mcp-data-platform/internal/platform/callrun"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptlayer"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsession"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptstore"
	"github.com/txn2/mcp-data-platform/internal/sqlprotect"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// wireCallHandlers assembles the tool-call handlers (#2436, #2634, #2635):
// the evaluator the tools/call chain runs them in, the test panel handler_test
// asks, and the lifecycle that keeps the index current, writes watch runs and
// sweeps. Nil with no database or no approvals queue: a handler runs only a
// version an administrator approved.
func wireCallHandlers(deps Deps, endpoints *epwire.Endpoints, layer *scriptlayer.Handle) *callrun.Runtime {
	if deps.DB == nil || endpoints == nil || endpoints.Approvals == nil {
		return nil
	}
	store := callhandler.NewStore(deps.DB)
	e := &callrun.Evaluator{
		Index: &callhandler.Index{Reader: store}, Store: store, Scripts: scriptstore.New(deps.DB),
		Approvals: endpoints.Approvals, Limits: runLimits(deps.Worker, deps.SQL),
		Watches: callrun.NewWatchQueue(deps.DB, 0),
		Shapes:  shapesFrom(deps.Toolkits),
		Protect: protectWith(deps.Toolkits),
	}
	e.SetServer(deps.Server)
	layer.SetCallTester(e)
	if svc := layer.Handlers(); svc != nil {
		svc.OnCallChange = e.Index.Notify
	}
	rt := &callrun.Runtime{Evaluator: e, DSN: deps.DSN, Sweeper: store}
	layer.SetCallHandlers(rt)
	endpoints.CallHandlers = rt
	// A script's application database statements bind straight to a pool
	// and never cross the tools/call chain, so they are put through the
	// handlers there (#2436).
	scriptsession.InstallAppDBHandlers(e)
	return rt
}

// shapesFrom reads an api call's operation through the toolkit of that kind.
func shapesFrom(toolkits *registry.Registry) func(kind string) callrun.Shapes {
	return func(kind string) callrun.Shapes {
		if toolkits == nil {
			return nil
		}
		for _, tk := range toolkits.All() {
			if api, ok := tk.(callrun.APIOperations); ok && tk.Kind() == kind {
				return callrun.APIShapes{API: api}
			}
		}
		return nil
	}
}

// querier runs a read statement on a Trino connection (trino.Toolkit).
type querier interface {
	Query(ctx context.Context, connection, sql string, opts trinoclient.QueryOptions) (*trinoclient.QueryResult, error)
}

// protectWith checks a covered caller's SQL, following a Trino connection's
// views through their definitions.
func protectWith(toolkits *registry.Registry) func(ctx context.Context, connection, sql string, protected map[string]string) error {
	return func(ctx context.Context, connection, sql string, protected map[string]string) error {
		return sqlprotect.Check(ctx, sql, protected, viewsOn(toolkits, connection)) //nolint:wrapcheck // the refusal names the column and handler
	}
}

// appdbQuerier runs a read statement on an application database
// (postgres.Toolkit).
type appdbQuerier interface {
	HasConnection(name string) bool
	Query(ctx context.Context, s postgres.Statement) (*postgres.Result, error)
}

// viewsOn reads a view's definition on a connection, "" for a relation that
// is not a view or that cannot be read: from PostgreSQL's catalog on an
// application database, from the catalog's information schema on Trino.
func viewsOn(toolkits *registry.Registry, connection string) sqlprotect.ViewReader {
	if toolkits == nil {
		return nil
	}
	var q querier
	for _, tk := range toolkits.All() {
		if a, ok := tk.(appdbQuerier); ok && a.HasConnection(connection) {
			return pgViews(a, connection)
		}
		if v, ok := tk.(querier); ok && q == nil && tk.Kind() == "trino" {
			q = v
		}
	}
	if q == nil {
		return nil
	}
	return trinoViews(q, connection)
}

// trinoViews reads a view's definition on a Trino connection.
func trinoViews(q querier, connection string) sqlprotect.ViewReader {
	return func(ctx context.Context, catalog, schema, name string) string {
		res, err := q.Query(ctx, connection, viewQuery(catalog, schema, name), trinoclient.QueryOptions{Limit: 1})
		if err != nil || res == nil || len(res.Rows) == 0 {
			return ""
		}
		def, _ := res.Rows[0]["view_definition"].(string)
		return def
	}
}

// pgViewQuery reads a view's or materialized view's definition from
// PostgreSQL's catalog, which shows it to every role that can see the view
// (information_schema.views shows it to the owner alone). An unqualified
// name is resolved on the search path, first schema first.
const pgViewQuery = `SELECT v.definition FROM (
	SELECT schemaname, viewname AS name, definition FROM pg_catalog.pg_views
	UNION ALL
	SELECT schemaname, matviewname, definition FROM pg_catalog.pg_matviews
) v
WHERE v.name = $2 AND (v.schemaname = $1 OR ($1 = '' AND v.schemaname = ANY (current_schemas(false))))
ORDER BY array_position(current_schemas(false), v.schemaname::text) NULLS LAST
LIMIT 1`

// pgViews reads a view's definition on an application database.
func pgViews(a appdbQuerier, connection string) sqlprotect.ViewReader {
	return func(ctx context.Context, _, schema, name string) string {
		res, err := a.Query(ctx, postgres.Statement{
			Connection: connection, SQL: pgViewQuery, Params: []any{identifier(schema), identifier(name)}, Limit: 1,
		})
		if err != nil || res == nil || len(res.Rows) == 0 || len(res.Rows[0]) == 0 {
			return ""
		}
		def, _ := res.Rows[0][0].(string)
		return def
	}
}

// dquote quotes an SQL identifier.
const dquote = `"`

// identifier is a name as PostgreSQL stores it: a quoted name exactly, an
// unquoted one folded to lower case.
func identifier(s string) string {
	if len(s) >= 2 && strings.HasPrefix(s, dquote) && strings.HasSuffix(s, dquote) {
		return strings.ReplaceAll(s[1:len(s)-1], dquote+dquote, dquote)
	}
	return strings.ToLower(s)
}

// viewQuery is the statement reading one view's definition from its
// catalog's information schema.
func viewQuery(catalog, schema, name string) string {
	from := "information_schema.views"
	if catalog != "" {
		from = dquote + strings.ReplaceAll(strings.Trim(catalog, dquote), dquote, dquote+dquote) + dquote + ".information_schema.views"
	}
	where := fmt.Sprintf("table_name = '%s'", literal(name))
	if schema != "" {
		where += fmt.Sprintf(" AND table_schema = '%s'", literal(schema))
	}
	return "SELECT view_definition FROM " + from + " WHERE " + where
}

func literal(s string) string {
	return strings.ReplaceAll(strings.ToLower(strings.Trim(s, dquote)), "'", "''")
}
