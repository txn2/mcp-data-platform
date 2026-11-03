package scriptsession

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sync"
	"sync/atomic"

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
)

// IsAppDBCall reports whether name is one of the calls a session answers
// against an application database rather than the MCP server.
func IsAppDBCall(name string) bool {
	switch name {
	case AppDBQuery, AppDBExecute, AppDBBatch, AppDBBegin, AppDBTxQuery, AppDBTxExecute, AppDBTxBatch, AppDBCommit, AppDBRollback:
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
}

// AppDBTx is one transaction a script holds: a *postgres.Tx.
type AppDBTx interface {
	Connection() string
	Query(ctx context.Context, s postgres.Statement) (*postgres.Result, error)
	Execute(ctx context.Context, s postgres.Statement) (*postgres.Result, error)
	Batch(ctx context.Context, stmts []postgres.Statement) ([]*postgres.Result, error)
	Commit(ctx context.Context) error
	Rollback()
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
}

func newAppDBCaller(ctx context.Context) *appdbCaller {
	a := installed.Load()
	if a == nil {
		return nil
	}
	return &appdbCaller{ctx: ctx, db: a, authorized: make(map[string]string), txs: make(map[string]AppDBTx)}
}

// errNoAppDB is the refusal of every call on a deployment with no runner.
const errNoAppDB = "no application database is served on this deployment"

// call runs one application-database call.
func (c *appdbCaller) call(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	if c == nil || c.db.Runner == nil || c.db.Runner() == nil {
		return nil, NewRefusal(errNoAppDB, refusalEnv(postgres.CodeUnknownConnection, errNoAppDB))
	}
	switch name {
	case AppDBQuery, AppDBExecute:
		return c.statement(ctx, name, args)
	case AppDBBatch:
		return c.batch(ctx, args)
	case AppDBBegin:
		return c.begin(args)
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
	}
	return txStatement(ctx, tx, name == AppDBTxExecute, args)
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
)

// close rolls back every transaction the session still holds: the run ended.
func (c *appdbCaller) close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	txs := c.txs
	c.txs = make(map[string]AppDBTx)
	c.mu.Unlock()
	for _, tx := range txs {
		tx.Rollback()
	}
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
	if ep, ok := endpointOf(c.ctx); ok {
		id.Source, id.UserID, id.Email = postgres.SourceEndpoint, ep.UserID, ep.Email
	} else if u := middleware.GetPreAuthenticatedUser(c.ctx); u != nil {
		// A saved run acts for its version's author; a draft acts as the
		// person running it, who is its author.
		id.Source, id.UserID, id.Email = postgres.SourceScript, u.OnBehalfOfSub, u.OnBehalfOf
		if u.OnBehalfOf == "" {
			id.UserID, id.Email = u.UserID, u.Email
		}
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
