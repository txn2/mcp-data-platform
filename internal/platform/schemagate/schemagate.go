// Package schemagate keeps SQL from being written against a table whose
// columns the agent has not read.
//
// A search hit names a table and describes it. It does not list the table's
// columns, so an agent that goes from a search hit straight to SQL writes the
// column names it expects. A wrong guess fails ("Column cannot be resolved").
// A guess that happens to name a real column, such as filtering on id when the
// record id is p_id, returns rows that look right and are wrong, and nothing
// reports the mistake.
//
// The gate does two things on the SQL tools (trino_query, trino_execute,
// trino_export), and on no other tool:
//
//   - The first statement a session runs against a table whose schema it has
//     not read is refused before it runs (#2386). The refusal carries every such
//     table's columns, types and catalog descriptions, and delivering them counts
//     as the read, so the same statement resent runs.
//   - A statement that fails because a column or a type is wrong comes back with
//     the columns of each table it names (#2387).
//
// A read is trino_describe_table of the table, fetch of its mcp:dataset
// reference, a DESCRIBE or SHOW COLUMNS statement, or either of the deliveries
// above. Reads are kept in the search gate's shared store, under the same
// discovery scope, for as long as a search is remembered.
package schemagate

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/bgloop"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/sqltables"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/searchgate"
	searchgatepostgres "github.com/txn2/mcp-data-platform/pkg/searchgate/postgres"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// Tool names the gate reads.
const (
	toolQuery    = "trino_query"
	toolExecute  = "trino_execute"
	toolExport   = "trino_export"
	toolDescribe = "trino_describe_table"
	toolFetch    = "fetch"
)

// sqlTools are the tools whose statements the gate checks. API, GraphQL, S3
// and every other tool are out of its scope.
var sqlTools = map[string]bool{toolQuery: true, toolExecute: true, toolExport: true}

// Namespace is a connection and the catalog and schema an unqualified table
// name resolves in on it.
type Namespace struct {
	Connection string
	Catalog    string
	Schema     string
}

// Describer reads table schemas from the query engine.
type Describer interface {
	// Namespace resolves a connection name (empty: the default connection)
	// to the connection and its default catalog and schema.
	Namespace(connection string) (Namespace, bool)
	// Describe returns a table's columns on a connection.
	Describe(ctx context.Context, connection, catalog, schema, table string) ([]Column, error)
}

// Config assembles a Gate.
type Config struct {
	// Store keeps which tables a scope has read. Required.
	Store searchgate.Store
	// Describer reads a table's columns. Required.
	Describer Describer
	// Semantic adds the catalog's column descriptions and glossary terms.
	// Optional.
	Semantic semantic.Provider
	// Require refuses a statement against an unread table. When false, the
	// gate still adds columns to a failed statement's error.
	Require bool
}

// Gate is the schema gate.
type Gate struct {
	cfg Config
}

// New builds a Gate.
func New(cfg Config) *Gate {
	return &Gate{cfg: cfg}
}

// call is the part of a tools/call request the gate reads.
type call struct {
	name string
	args map[string]any
}

func (c call) str(key string) string {
	s, _ := c.args[key].(string)
	return strings.TrimSpace(s)
}

// Middleware returns the receiving middleware. It reads PlatformContext, so it
// is registered inner to the tool-call middleware (which has authenticated the
// caller and authorized the tool and the connection), and outer to the error
// contract, so the error it reads is the normalized one.
func (g *Gate) Middleware() mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method != "tools/call" {
				return next(ctx, method, req)
			}
			return g.dispatch(ctx, req, func() (mcp.Result, error) { return next(ctx, method, req) })
		}
	}
}

// dispatch routes one tools/call to the check its tool needs.
func (g *Gate) dispatch(ctx context.Context, req mcp.Request, run func() (mcp.Result, error)) (mcp.Result, error) {
	pc := middleware.GetPlatformContext(ctx)
	c, ok := parseCall(req)
	if pc == nil || !ok || exemptSource(pc.Source) {
		return run()
	}
	scope := pc.DiscoveryScopeKey()
	switch {
	case scope == "":
		return run()
	case sqlTools[c.name]:
		return g.handleSQL(ctx, scope, c, run)
	case c.name == toolDescribe || c.name == toolFetch:
		res, err := run()
		g.recordRead(ctx, scope, c, res, err)
		return res, err
	}
	return run()
}

// exemptSource reports a call the gate leaves alone: a managed script's run
// (its statements are saved code, and refusing them would fail every
// scheduled run) and the portal and REST shims the search gate exempts too.
func exemptSource(source string) bool {
	return source == middleware.SourceScript || source == middleware.SourceAdmin || source == middleware.SourceREST
}

func parseCall(req mcp.Request) (call, bool) {
	if req == nil {
		return call{}, false
	}
	params, ok := req.GetParams().(*mcp.CallToolParamsRaw)
	if !ok || params == nil {
		return call{}, false
	}
	c := call{name: params.Name}
	if len(params.Arguments) > 0 {
		_ = json.Unmarshal(params.Arguments, &c.args) // malformed arguments: the handler reports them
	}
	return c, true
}

// handleSQL runs the checks on one SQL tool call.
func (g *Gate) handleSQL(ctx context.Context, scope string, c call, run func() (mcp.Result, error)) (mcp.Result, error) {
	stmt := c.str("sql")
	if stmt == "" {
		return run()
	}
	ns, ok := g.cfg.Describer.Namespace(c.str("connection"))
	if !ok {
		return run()
	}
	if ref, isSchemaRead := schemaStatementTable(stmt); isSchemaRead {
		return g.runSchemaRead(ctx, scope, resolve(ns, ref), run)
	}
	tables := statementTables(ns, stmt)
	// SHOW TABLES/SCHEMAS/CATALOGS name no table whose columns a statement uses.
	if len(tables) == 0 || sqltables.StatementKind(stmt) == "show" {
		return run()
	}
	if g.cfg.Require {
		if refusal := g.refuseUnread(ctx, scope, c.name, tables); refusal != nil {
			return refusal, nil
		}
	}
	res, err := run()
	if err == nil && isSchemaError(res) {
		g.addColumns(ctx, scope, res, tables)
	}
	return res, err
}

// runSchemaRead runs a DESCRIBE or SHOW COLUMNS statement and records the
// read when it succeeds.
func (g *Gate) runSchemaRead(ctx context.Context, scope string, t tableRef, run func() (mcp.Result, error)) (mcp.Result, error) {
	res, err := run()
	if succeeded(res, err) {
		g.markRead(ctx, scope, t)
	}
	return res, err
}

// refuseUnread returns the refusal for a statement naming tables the scope has
// not read, or nil when it may run. A table whose schema cannot be read (it
// does not exist, the engine refused) does not hold the statement back: the
// engine's own error for it is the better answer.
func (g *Gate) refuseUnread(ctx context.Context, scope, tool string, tables []tableRef) mcp.Result {
	unread := make([]tableRef, 0, len(tables))
	for _, t := range tables {
		if !g.hasRead(ctx, scope, t) {
			unread = append(unread, t)
		}
	}
	if len(unread) == 0 {
		return nil
	}
	described := g.describeAll(ctx, unread)
	if len(described) == 0 {
		return nil
	}
	for _, t := range described {
		g.markRead(ctx, scope, t.ref)
	}
	slog.InfoContext(ctx, "schema gate: statement refused until the table schema is read",
		"tool", tool, "tables", logsan.SanitizeForLog(joinNames(described)))
	return refusalResult(tool, described)
}

// addColumns appends the columns of the statement's tables to a failed
// statement's error result.
func (g *Gate) addColumns(ctx context.Context, scope string, res mcp.Result, tables []tableRef) {
	cr, ok := res.(*mcp.CallToolResult)
	if !ok {
		return
	}
	described := g.describeAll(ctx, tables)
	if len(described) == 0 {
		return
	}
	for _, t := range described {
		g.markRead(ctx, scope, t.ref)
	}
	cr.Content = append(cr.Content, &mcp.TextContent{Text: failureColumnsText(described)})
}

// recordRead marks the table a successful trino_describe_table or fetch read.
func (g *Gate) recordRead(ctx context.Context, scope string, c call, res mcp.Result, err error) {
	if !succeeded(res, err) {
		return
	}
	switch c.name {
	case toolDescribe:
		ns, ok := g.cfg.Describer.Namespace(c.str("connection"))
		if !ok || c.str("table") == "" {
			return
		}
		g.markRead(ctx, scope, resolve(ns, tableRef{catalog: c.str("catalog"), schema: c.str("schema"), table: c.str("table")}))
	case toolFetch:
		if !fetchFound(res) {
			return
		}
		if t, ok := datasetTable(c.str("reference")); ok {
			g.markRead(ctx, scope, t)
		}
	}
}

// succeeded reports a call that returned a result that is not an error.
func succeeded(res mcp.Result, err error) bool {
	if err != nil {
		return false
	}
	cr, ok := res.(*mcp.CallToolResult)
	return ok && cr != nil && !cr.IsError
}

// readKey is the store key for one scope's read of one table. The unit
// separator keeps it apart from the scope's own search record.
func readKey(scope string, t tableRef) string {
	return scope + "\x1fschema\x1f" + t.connection + "\x1f" + t.fullName()
}

// maxReadKeyBytes bounds a read key. Table names come from the caller's
// statement; a key past what the store's primary-key index holds is not
// stored, and its table is not held back.
const maxReadKeyBytes = 1024

func (g *Gate) hasRead(ctx context.Context, scope string, t tableRef) bool {
	key := readKey(scope, t)
	if len(key) > maxReadKeyBytes {
		return true
	}
	ok, err := g.cfg.Store.HasDiscovered(ctx, key)
	if err != nil {
		// The store cannot answer: let the statement run rather than refuse
		// every query during a database outage, as the search gate does.
		slog.WarnContext(ctx, "schema gate: read check failed; allowing the statement", "error", logsan.SanitizeForLog(err.Error()))
		return true
	}
	return ok
}

func (g *Gate) markRead(ctx context.Context, scope string, t tableRef) {
	key := readKey(scope, t)
	if t.table == "" || len(key) > maxReadKeyBytes {
		return
	}
	if err := g.cfg.Store.MarkDiscovered(ctx, key); err != nil {
		slog.WarnContext(ctx, "schema gate: failed to record a schema read", "error", logsan.SanitizeForLog(err.Error()))
	}
}

// StartCleanup evicts expired reads every interval until stop closes. In a
// deployment that runs the search gate too, its cleanup clears the same table;
// this one keeps a deployment with the search gate off from accumulating them.
func (g *Gate) StartCleanup(interval time.Duration, stop <-chan struct{}) {
	go bgloop.Run(context.Background(), bgloop.Loop{
		Name: bgloop.NameSchemaGateCleanup, Every: interval, Stop: stop,
		Body: func(ctx context.Context) error {
			if err := g.cfg.Store.Cleanup(ctx); err != nil {
				return fmt.Errorf("schema gate cleanup: %w", err)
			}
			return nil
		},
	})
}

// Wire assembles the gate the platform installs: reads kept for ttl in the
// search gate's store, schemas read through the live Trino toolkits, curation from semantic.
// It returns the middleware and the function that stops its cleanup loop.
func Wire(db *sql.DB, ttl time.Duration, toolkits ToolkitLister, sem semantic.Provider, refuse bool) (mw mcp.Middleware, stop func()) {
	g := New(Config{Store: searchgatepostgres.Shared(db, ttl), Describer: TrinoDescriber{Toolkits: toolkits}, Semantic: sem, Require: refuse})
	done := make(chan struct{})
	g.StartCleanup(time.Minute, done)
	return g.Middleware(), func() { close(done) }
}
