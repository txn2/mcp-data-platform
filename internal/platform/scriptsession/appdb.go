package scriptsession

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/txn2/mcp-data-platform/internal/appdbops"
	"github.com/txn2/mcp-data-platform/internal/jsonwalk"
	"github.com/txn2/mcp-data-platform/internal/pglex"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
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
	// AppDBBatch is platform.batch: statements pipelined in one transaction
	// that commits when every one succeeded (#2402).
	AppDBBatch = "appdb.batch"
	// The calls platform.transaction makes (#2402): open the transaction,
	// run statements in it, and end it. Each statement goes through the same
	// call path as platform.query's, so a draft refuses a write inside a
	// transaction and a recording keeps each statement and how it ended.
	AppDBBegin     = "appdb.begin"
	AppDBTxQuery   = "appdb.tx_query"
	AppDBTxExecute = "appdb.tx_execute"
	AppDBTxBatch   = "appdb.tx_batch"
	AppDBCommit    = "appdb.commit"
	AppDBRollback  = "appdb.rollback"
	// The calls tx.savepoint makes (#2452): open a savepoint in the
	// transaction, then release it or roll back to it.
	AppDBSavepoint  = "appdb.savepoint"
	AppDBRelease    = "appdb.release"
	AppDBRollbackTo = "appdb.rollback_to"
	// The calls query_iter makes (#2454): declare a cursor over a query, in
	// the run's transaction or a READ ONLY one of its own, read it a page at
	// a time, and close it.
	AppDBCursorOpen  = "appdb.cursor_open"
	AppDBCursorFetch = "appdb.cursor_fetch"
	AppDBCursorClose = "appdb.cursor_close"
	// The calls copy_in and copy_out make (#2453). Their rows and copied
	// bytes never reach a recording (RecordedArgs, RecordedOut).
	AppDBCopyIn  = "appdb.copy_in"
	AppDBCopyOut = "appdb.copy_out"
	// AppDBMaintain is platform.maintain (#2410): ANALYZE, VACUUM or REINDEX
	// outside a transaction, for an administrator or a persona the
	// connection's schema_apply_personas names.
	AppDBMaintain = "appdb.maintain"
)

// IsAppDBCall reports whether name is one of the calls a session answers
// against an application database rather than the MCP server.
func IsAppDBCall(name string) bool {
	switch name {
	case AppDBQuery, AppDBExecute, AppDBBatch, AppDBBegin, AppDBTxQuery, AppDBTxExecute, AppDBTxBatch, AppDBCommit, AppDBRollback,
		AppDBSavepoint, AppDBRelease, AppDBRollbackTo, AppDBCursorOpen, AppDBCursorFetch, AppDBCursorClose,
		AppDBCopyIn, AppDBCopyOut, AppDBMaintain:
		return true
	}
	return false
}

// AppDBRunner runs statements on application databases: the postgres toolkit.
type AppDBRunner interface {
	HasConnection(name string) bool
	Query(ctx context.Context, s postgres.Statement) (*postgres.Result, error)
	Execute(ctx context.Context, s postgres.Statement) (*postgres.Result, error)
	Batch(ctx context.Context, connection string, stmts []postgres.Statement) ([]*postgres.Result, error)
	Begin(ctx context.Context, o postgres.TxOptions) (AppDBTx, error)
	OpenCursor(ctx context.Context, s postgres.Statement, pageRows int) (AppDBCursor, error)
	CopyIn(ctx context.Context, in postgres.CopyIn) (int64, error)
	CopyOut(ctx context.Context, s postgres.Statement, format string, w io.Writer, maxBytes int64) (*postgres.CopyOutResult, error)
	RunMaintenance(ctx context.Context, connection, stmt string, lockTimeout, timeout time.Duration) error
	ApplyPersonas(connection string) []string
}

// AppDBCursor is one query read in pages: a *postgres.Cursor.
type AppDBCursor interface {
	Next(ctx context.Context) (*postgres.Result, bool, error)
	Close()
}

// AppDBTx is one transaction a script holds: a *postgres.Tx.
type AppDBTx interface {
	Connection() string
	Query(ctx context.Context, s postgres.Statement) (*postgres.Result, error)
	Execute(ctx context.Context, s postgres.Statement) (*postgres.Result, error)
	Batch(ctx context.Context, stmts []postgres.Statement) ([]*postgres.Result, error)
	Commit(ctx context.Context) error
	Rollback()
	Savepoint(ctx context.Context) (string, error)
	Release(ctx context.Context, name string) error
	RollbackTo(ctx context.Context, name string) error
	OpenCursor(ctx context.Context, s postgres.Statement, pageRows int) (AppDBCursor, error)
	CopyIn(ctx context.Context, in postgres.CopyIn) (int64, error)
}

// AppDB is what the platform installs for the script sessions it opens.
type AppDB struct {
	// Runner returns the live runner, or nil on a deployment with none.
	Runner func() AppDBRunner
	// Authorize refuses a connection the identity on ctx may not use, and
	// names the persona it was allowed as, which chooses the database login
	// role its statements run as (#2403). ctx is the session's own, which
	// carries the run's identity.
	Authorize func(ctx context.Context, connection string) (persona string, err error)
	// TestDatabase builds a script test's throwaway database for a
	// connection from its migrations in schemas, every schema when empty
	// (#2407), and returns its runner and the function that drops it. Nil
	// on a deployment that keeps no migrations.
	TestDatabase func(ctx context.Context, connection string, schemas []string) (AppDBRunner, func(), error)
	// AdminPersona is the persona that is the platform's administrator,
	// which platform.maintain is allowed for on every connection.
	AdminPersona string
	// Applications answers which application's run role a script's
	// statements log in as, and which application holds a schema (#2539);
	// its zero value answers none.
	Applications Applications
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

	mu sync.Mutex
	// authorized are the connections the run may use, each with the persona
	// it was allowed as.
	authorized map[string]string
	// txs are the session's open transactions by id, and seq numbers them,
	// so a recording's ids are the same on every replay.
	txs map[string]AppDBTx
	seq int
	// cursors are the session's open cursors by id, numbered by cursorSeq.
	cursors   map[string]AppDBCursor
	cursorSeq int
	// apps is the application whose run role each connection's statements
	// log in as (#2539), asked once per connection.
	apps map[string]string
}

func newAppDBCaller(ctx context.Context) *appdbCaller {
	a := installed.Load()
	if a == nil {
		return nil
	}
	return &appdbCaller{
		ctx: ctx, db: a, authorized: make(map[string]string), txs: make(map[string]AppDBTx),
		cursors: make(map[string]AppDBCursor), apps: make(map[string]string),
	}
}

// errNoAppDB is the refusal of every call on a deployment with no runner.
const errNoAppDB = "no application database is served on this deployment"

// served reports whether this deployment serves an application database.
func (c *appdbCaller) served() bool {
	return c != nil && c.db.Runner != nil && c.db.Runner() != nil
}

// call runs one application-database call.
func (c *appdbCaller) call(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	if !c.served() {
		return nil, NewRefusal(errNoAppDB, refusalEnv(postgres.CodeUnknownConnection, errNoAppDB))
	}
	out, err := c.dispatch(ctx, name, args)
	if err != nil {
		return nil, c.reword(c.connectionOf(args), err)
	}
	return out, nil
}

// connectionOf is the connection a call acts on: the one it names, or the
// one of the transaction or cursor it names.
func (c *appdbCaller) connectionOf(args map[string]any) string {
	if conn, _ := args[argConnection].(string); conn != "" {
		return conn
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if id, _ := args[argTransaction].(string); id != "" {
		if tx, ok := c.txs[id]; ok {
			return tx.Connection()
		}
	}
	return ""
}

// dispatch runs one call by its name.
func (c *appdbCaller) dispatch(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	switch name {
	case AppDBQuery, AppDBExecute:
		return c.statement(ctx, name, args)
	case AppDBBatch:
		return c.batch(ctx, args)
	case AppDBBegin:
		return c.begin(args)
	case AppDBCursorOpen, AppDBCursorFetch, AppDBCursorClose:
		return c.cursor(ctx, name, args)
	case AppDBCopyIn:
		return c.copyIn(ctx, args)
	case AppDBCopyOut:
		return c.copyOut(ctx, args)
	case AppDBMaintain:
		return c.maintain(ctx, args)
	default:
		return c.inTx(ctx, name, args)
	}
}

// statement runs one statement in a transaction of its own.
func (c *appdbCaller) statement(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	st, err := statementOf(args)
	if err != nil {
		return nil, invalid(err)
	}
	if err := c.admit(st.Connection); err != nil {
		return nil, err
	}
	ctx = c.identity(ctx, st.Connection)
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

// batch runs platform.batch.
func (c *appdbCaller) batch(ctx context.Context, args map[string]any) (map[string]any, error) {
	connection, _ := args[argConnection].(string)
	if connection == "" {
		return nil, invalid(errors.New("a batch names its connection"))
	}
	stmts, err := statementsOf(args)
	if err != nil {
		return nil, invalid(err)
	}
	if err := c.admit(connection); err != nil {
		return nil, err
	}
	res, err := c.db.Runner().Batch(c.identity(ctx, connection), connection, stmts)
	if err != nil {
		return nil, appdbRefusal(err)
	}
	return resultsMap(res), nil
}

// admit authorizes a connection and refuses a statement outside the
// transaction this session holds open on it: it would run on another pooled
// connection and wait on the transaction's own locks.
func (c *appdbCaller) admit(connection string) error {
	if err := c.authorize(connection); err != nil {
		return NewRefusal(err.Error(), refusalEnv(codeNotAllowed, err.Error()))
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, tx := range c.txs {
		if tx.Connection() == connection {
			msg := fmt.Sprintf("a transaction is open on application database %q in this run; "+
				"run the statement in it with tx.query, tx.execute or tx.batch", connection)
			return NewRefusal(msg, refusalEnv(postgres.CodeInvalidStatement, msg))
		}
	}
	return nil
}

// codeNotAllowed is the refusal of a connection the run may not use.
const codeNotAllowed = "connection_not_allowed"

// begin opens a transaction for platform.transaction. It is opened under the
// session's own context, so it is rolled back when the run ends however it
// ends, never by the end of the call that opened it.
func (c *appdbCaller) begin(args map[string]any) (map[string]any, error) {
	o := postgres.TxOptions{}
	o.Connection, _ = args[argConnection].(string)
	o.Isolation, _ = args["isolation"].(string)
	o.ReadOnly, _ = args["read_only"].(bool)
	o.Attempt = intArg(args["attempt"])
	if o.Connection == "" {
		return nil, invalid(errors.New("a transaction names its connection"))
	}
	if err := c.admit(o.Connection); err != nil {
		return nil, err
	}
	tx, err := c.db.Runner().Begin(c.identity(c.ctx, o.Connection), o)
	if err != nil {
		return nil, appdbRefusal(err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seq++
	id := fmt.Sprintf("tx%d", c.seq)
	c.txs[id] = tx
	return map[string]any{argTransaction: id, argConnection: o.Connection}, nil
}

// inTx runs a call on an open transaction.
func (c *appdbCaller) inTx(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	id, _ := args[argTransaction].(string)
	tx, err := c.take(id, name == AppDBCommit || name == AppDBRollback)
	if err != nil {
		return nil, err
	}
	switch name {
	case AppDBCommit:
		if err := tx.Commit(ctx); err != nil {
			return nil, appdbRefusal(err)
		}
		return map[string]any{argTransaction: id, "committed": true}, nil
	case AppDBRollback:
		tx.Rollback()
		return map[string]any{argTransaction: id, "committed": false}, nil
	case AppDBTxBatch:
		return txBatch(ctx, tx, args)
	case AppDBSavepoint, AppDBRelease, AppDBRollbackTo:
		return txSavepoint(ctx, tx, name, id, args)
	}
	return txStatement(ctx, tx, name == AppDBTxExecute, args)
}

// argSavepoint names the savepoint a release or rollback_to ends.
const argSavepoint = "savepoint"

// txSavepoint opens a savepoint, or releases or rolls back to the one named.
func txSavepoint(ctx context.Context, tx AppDBTx, name, id string, args map[string]any) (map[string]any, error) {
	if name == AppDBSavepoint {
		sp, err := tx.Savepoint(ctx)
		if err != nil {
			return nil, appdbRefusal(err)
		}
		return map[string]any{argTransaction: id, argSavepoint: sp}, nil
	}
	sp, _ := args[argSavepoint].(string)
	end := tx.Release
	if name == AppDBRollbackTo {
		end = tx.RollbackTo
	}
	if err := end(ctx, sp); err != nil {
		return nil, appdbRefusal(err)
	}
	return map[string]any{argTransaction: id, argSavepoint: sp, "released": name == AppDBRelease}, nil
}

// take finds an open transaction, and forgets it when the call ends it.
func (c *appdbCaller) take(id string, ending bool) (AppDBTx, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	tx, ok := c.txs[id]
	if !ok {
		msg := fmt.Sprintf("no transaction %q is open in this run", id)
		return nil, NewRefusal(msg, refusalEnv(postgres.CodeTransactionEnded, msg))
	}
	if ending {
		delete(c.txs, id)
	}
	return tx, nil
}

// txBatch runs tx.batch.
func txBatch(ctx context.Context, tx AppDBTx, args map[string]any) (map[string]any, error) {
	stmts, err := statementsOf(args)
	if err != nil {
		return nil, invalid(err)
	}
	res, err := tx.Batch(ctx, stmts)
	if err != nil {
		return nil, appdbRefusal(err)
	}
	return resultsMap(res), nil
}

// txStatement runs tx.query, or tx.execute when write.
func txStatement(ctx context.Context, tx AppDBTx, write bool, args map[string]any) (map[string]any, error) {
	st, err := txStatementOf(args, tx.Connection())
	if err != nil {
		return nil, invalid(err)
	}
	run := tx.Query
	if write {
		run = tx.Execute
	}
	res, err := run(ctx, st)
	if err != nil {
		return nil, appdbRefusal(err)
	}
	return resultMap(res), nil
}

// The argument keys the application-database calls carry.
const (
	argTransaction = "transaction"
	argConnection  = "connection"
	argTable       = "table"
	// argRows is a result's rows, and a copy_in's.
	argRows = "rows"
)

// close closes every cursor and rolls back every transaction the session
// still holds: the run ended.
func (c *appdbCaller) close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	txs, cursors := c.txs, c.cursors
	c.txs, c.cursors = make(map[string]AppDBTx), make(map[string]AppDBCursor)
	c.mu.Unlock()
	for _, k := range cursors {
		k.Close()
	}
	for _, tx := range txs {
		tx.Rollback()
	}
}

// txCursor declares a cursor in the open transaction id.
func (c *appdbCaller) txCursor(ctx context.Context, id string, args map[string]any, pageRows int) (AppDBCursor, error) {
	tx, err := c.take(id, false)
	if err != nil {
		return nil, err
	}
	st, err := txStatementOf(args, tx.Connection())
	if err != nil {
		return nil, invalid(err)
	}
	k, err := tx.OpenCursor(ctx, st, pageRows)
	if err != nil {
		return nil, appdbRefusal(err)
	}
	return k, nil
}

// ownCursor declares a cursor in a READ ONLY transaction of its own, opened
// under the session's context, so the run's end closes it rather than the end
// of the call that opened it.
func (c *appdbCaller) ownCursor(args map[string]any, pageRows int) (AppDBCursor, error) {
	st, err := statementOf(args)
	if err != nil {
		return nil, invalid(err)
	}
	if err := c.admit(st.Connection); err != nil {
		return nil, err
	}
	k, err := c.db.Runner().OpenCursor(c.identity(c.ctx, st.Connection), st, pageRows)
	if err != nil {
		return nil, appdbRefusal(err)
	}
	return k, nil
}

// argCursor names the cursor a fetch or close reads.
const argCursor = "cursor"

// cursor opens, reads or closes a cursor for query_iter.
func (c *appdbCaller) cursor(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	if name == AppDBCursorOpen {
		return c.openCursor(ctx, args)
	}
	id, _ := args[argCursor].(string)
	c.mu.Lock()
	k, ok := c.cursors[id]
	if ok && name == AppDBCursorClose {
		delete(c.cursors, id)
	}
	c.mu.Unlock()
	if !ok {
		msg := fmt.Sprintf("no cursor %q is open in this run", id)
		return nil, NewRefusal(msg, refusalEnv(postgres.CodeTransactionEnded, msg))
	}
	if name == AppDBCursorClose {
		k.Close()
		return map[string]any{argCursor: id, "closed": true}, nil
	}
	page, done, err := k.Next(ctx)
	if err != nil {
		return nil, appdbRefusal(err)
	}
	out := resultMap(page)
	out["done"] = done
	return out, nil
}

// openCursor declares a cursor in the transaction args name, or in a READ
// ONLY transaction of its own on the connection args name.
func (c *appdbCaller) openCursor(ctx context.Context, args map[string]any) (map[string]any, error) {
	pageRows := intArg(args["page_rows"])
	var (
		k   AppDBCursor
		err error
	)
	if id, _ := args[argTransaction].(string); id != "" {
		k, err = c.txCursor(ctx, id, args, pageRows)
	} else {
		k, err = c.ownCursor(args, pageRows)
	}
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cursorSeq++
	id := fmt.Sprintf("cursor%d", c.cursorSeq)
	c.cursors[id] = k
	return map[string]any{argCursor: id}, nil
}

// invalid is the refusal of a call's malformed arguments.
func invalid(err error) error {
	return NewRefusal(err.Error(), refusalEnv(postgres.CodeInvalidStatement, err.Error()))
}

// txStatementOf reads a statement inside a transaction, which names no
// connection of its own.
func txStatementOf(args map[string]any, connection string) (postgres.Statement, error) {
	withConn := maps.Clone(args)
	if withConn == nil {
		withConn = map[string]any{}
	}
	withConn[argConnection] = connection
	return statementOf(withConn)
}

// statementsOf reads a batch's statements.
func statementsOf(args map[string]any) ([]postgres.Statement, error) {
	raw, _ := args["statements"].([]any)
	if len(raw) == 0 {
		return nil, errors.New("a batch holds at least one statement")
	}
	out := make([]postgres.Statement, len(raw))
	for i, r := range raw {
		m, ok := r.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("statement %d of the batch is not an object with sql and params", i+1)
		}
		out[i].SQL, _ = m["sql"].(string)
		if p, ok := m["params"]; ok && p != nil {
			out[i].Params = p
		}
	}
	return out, nil
}

// resultsMap is a batch's results, each in resultMap's shape.
func resultsMap(res []*postgres.Result) map[string]any {
	out := make([]any, len(res))
	for i, r := range res {
		out[i] = resultMap(r)
	}
	return map[string]any{"results": out}
}

// authorize checks a connection once per session.
func (c *appdbCaller) authorize(connection string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.authorized[connection]; ok {
		return nil
	}
	persona := ""
	if c.db.Authorize != nil {
		p, err := c.db.Authorize(c.ctx, connection)
		if err != nil {
			return err
		}
		persona = p
	}
	c.authorized[connection] = persona
	return nil
}

// identity returns ctx carrying who the session's statements on connection
// run for (#2403): the persona the run was allowed the connection as, and the
// claims of the endpoint request the session serves, or of the script's
// author when it serves none.
func (c *appdbCaller) identity(ctx context.Context, connection string) context.Context {
	c.mu.Lock()
	id := postgres.Identity{Persona: c.authorized[connection]}
	c.mu.Unlock()
	id.Application = c.runAs(connection)
	if ep, ok := endpointOf(c.ctx); ok {
		id.Source, id.UserID, id.Email = postgres.SourceEndpoint, ep.UserID, ep.Email
	} else if u := middleware.GetPreAuthenticatedUser(c.ctx); u != nil {
		// A saved run acts for its version's author; a draft acts as the
		// person running it, who is its author.
		id.Source, id.UserID, id.Email = postgres.SourceScript, u.OnBehalfOfSub, u.OnBehalfOf
		if u.OnBehalfOf == "" {
			id.UserID, id.Email = u.UserID, u.Email
		}
	} else if pc := middleware.GetPlatformContext(c.ctx); pc != nil {
		// A script's test runs in the session of the person saving or testing
		// it, and acts for them as a draft acts for its runner: a test of a
		// policy keyed on mcp.email reads the address it writes (#2413).
		id.Source, id.UserID, id.Email = postgres.SourceScript, pc.UserID, pc.UserEmail
	}
	return postgres.WithIdentity(ctx, id)
}

// EndpointCaller is who sent the endpoint request a session serves, as its
// statements' claims carry it (#2403). Both fields are empty on a public
// endpoint.
type EndpointCaller struct {
	UserID string
	Email  string
}

type endpointKey struct{}

// WithEndpoint marks ctx as an endpoint request's session, carrying its
// caller: a session opened on it sets mcp.user_id and mcp.email from caller,
// never from the script's author. A public endpoint's caller is the zero
// value.
func WithEndpoint(ctx context.Context, caller EndpointCaller) context.Context {
	return context.WithValue(ctx, endpointKey{}, caller)
}

// endpointOf is the endpoint caller on ctx, and whether ctx is an endpoint
// request's.
func endpointOf(ctx context.Context) (EndpointCaller, bool) {
	ep, ok := ctx.Value(endpointKey{}).(EndpointCaller)
	return ep, ok
}

// statementOf reads a call's arguments.
func statementOf(args map[string]any) (postgres.Statement, error) {
	st := postgres.Statement{}
	st.Connection, _ = args[argConnection].(string)
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
		argConnection:   res.Connection,
		"command":       res.Command,
		"rows_affected": res.RowsAffected,
		"columns":       columns,
		argRows:         rows,
		"row_count":     len(rows),
		"stats":         map[string]any{"truncated": res.Truncated, "limit": res.Limit},
	}
}

// appdbRefusal is a failed statement as the host reads a refused call. The
// text is the value-free summary, which a run's error and its audit record
// may carry; what the server said, DETAIL included, is in the envelope's
// postgres field, which only the script reads (on_error = "return").
func appdbRefusal(err error) error {
	if refusal, ok := errors.AsType[*RefusalError](err); ok {
		return refusal
	}
	if refused, ok := errors.AsType[*postgres.RefusedError](err); ok {
		return NewRefusal(refused.Error(), refusalEnv(refused.Code, refused.Error()))
	}
	if failed, ok := errors.AsType[*postgres.FailedError](err); ok {
		env := refusalEnv(failed.Code, failed.Error())
		if failed.Statement != nil {
			env["postgres"] = map[string]any{
				"sqlstate": failed.Statement.SQLState, "condition": failed.Statement.Condition,
				"message": failed.Statement.Message, "detail": failed.Statement.Detail, "hint": failed.Statement.Hint,
				"schema": failed.Statement.Schema, argTable: failed.Statement.Table, "column": failed.Statement.Column,
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

// copyIn writes copy_in's rows: in the transaction args name, or in one of
// its own on the connection args name.
func (c *appdbCaller) copyIn(ctx context.Context, args map[string]any) (map[string]any, error) {
	in := postgres.CopyIn{}
	in.Table, _ = args[argTable].(string)
	for _, col := range anyList(args["columns"]) {
		name, _ := col.(string)
		in.Columns = append(in.Columns, name)
	}
	for _, row := range anyList(args[argRows]) {
		in.Rows = append(in.Rows, anyList(row))
	}
	var (
		n   int64
		err error
	)
	if id, _ := args[argTransaction].(string); id != "" {
		tx, terr := c.take(id, false)
		if terr != nil {
			return nil, terr
		}
		in.Connection = tx.Connection()
		n, err = tx.CopyIn(ctx, in)
	} else {
		in.Connection, _ = args[argConnection].(string)
		if aerr := c.admit(in.Connection); aerr != nil {
			return nil, aerr
		}
		n, err = c.db.Runner().CopyIn(c.identity(ctx, in.Connection), in)
	}
	if err != nil {
		return nil, appdbRefusal(err)
	}
	return map[string]any{argConnection: in.Connection, argTable: in.Table, "columns": args["columns"], "rows_written": n}, nil
}

// copyOut writes copy_out's query into a buffer and answers with its bytes,
// which the binding hands to the output writer; RecordedOut keeps them out of
// a recording.
func (c *appdbCaller) copyOut(ctx context.Context, args map[string]any) (map[string]any, error) {
	st, err := statementOf(args)
	if err != nil {
		return nil, invalid(err)
	}
	if err := c.admit(st.Connection); err != nil {
		return nil, err
	}
	format, _ := args["format"].(string)
	var buf bytes.Buffer
	res, err := c.db.Runner().CopyOut(c.identity(ctx, st.Connection), st, format, &buf, int64(intArg(args["max_bytes"])))
	if err != nil {
		return nil, appdbRefusal(err)
	}
	return map[string]any{argConnection: st.Connection, "format": format, argRows: res.Rows, "bytes": res.Bytes, "data": buf.Bytes()}, nil
}

func anyList(v any) []any {
	l, _ := v.([]any)
	return l
}

// RecordedArgs is a call's arguments as a recording keeps and matches them: a
// copy_in's rows are bound values, kept nowhere but the database, so they are
// replaced by their count (#2453), and a password a SQL string gives a role
// is replaced by a placeholder (#2475).
func RecordedArgs(tool string, args map[string]any) map[string]any {
	args = redactedPasswords(args)
	if _, held := args[argRows]; tool != AppDBCopyIn || !held {
		return args
	}
	out := maps.Clone(args)
	delete(out, argRows)
	out["row_count"] = len(anyList(args[argRows]))
	return out
}

// redactedPasswords is args with the password literal of every SQL string
// that gives a role, user or group a password replaced (#2475): a statement a
// script executes and a migration it adds are recorded without the
// credential. A replay matches a call by these same arguments, so it still
// finds the call. args is returned as it is when nothing in it changes.
func redactedPasswords(args map[string]any) map[string]any {
	out, changed := jsonwalk.Strings(args, pglex.RedactPasswords)
	if !changed {
		return args
	}
	m, _ := out.(map[string]any)
	return m
}

// RecordedOut is a call's answer as a recording keeps it: a copy_out's bytes
// are an output, written where outputs go, never into a recording.
func RecordedOut(tool string, out map[string]any) map[string]any {
	if tool != AppDBCopyOut || out == nil {
		return out
	}
	kept := maps.Clone(out)
	delete(kept, "data")
	return kept
}

// maintain runs platform.maintain: the run's persona must be the
// administrator or one the connection's schema_apply_personas names. A draft
// never reaches it; the write barrier refuses appdb.maintain.
func (c *appdbCaller) maintain(ctx context.Context, args map[string]any) (map[string]any, error) {
	connection, _ := args[argConnection].(string)
	command, _ := args["command"].(string)
	table, _ := args[argTable].(string)
	if err := c.admit(connection); err != nil {
		return nil, err
	}
	c.mu.Lock()
	persona := c.authorized[connection]
	c.mu.Unlock()
	runner := c.db.Runner()
	if persona == "" || (persona != c.db.AdminPersona && !slices.Contains(runner.ApplyPersonas(connection), persona)) {
		msg := fmt.Sprintf("platform.maintain on application database %q is for the administrator and the personas its "+
			"schema_apply_personas names; this run's persona %q is neither", connection, persona)
		return nil, NewRefusal(msg, refusalEnv(postgres.CodeApplyNotAllowed, msg))
	}
	timeout := time.Duration(0)
	if deadline, ok := ctx.Deadline(); ok {
		timeout = time.Until(deadline)
	}
	out, err := appdbops.Maintain(ctx, runner, appdbops.Request{Connection: connection, Command: command, Table: table, Timeout: timeout})
	if err != nil {
		var refused *postgres.RefusedError
		var failed *postgres.FailedError
		if errors.As(err, &refused) || errors.As(err, &failed) {
			return nil, appdbRefusal(err)
		}
		return nil, invalid(err)
	}
	return map[string]any{"statement": out.Statement, "duration_ms": out.DurationMS}, nil
}
