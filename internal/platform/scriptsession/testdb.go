package scriptsession

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// TestDatabases answers one script test's application-database calls against
// throwaway databases built from each connection's migrations (#2407), in
// place of the recording: a constraint, a trigger or a policy is exercised
// rather than answered. A test opens one per connection with
// testing.database; every application-database call it makes after the first
// is answered here, and a call naming a connection the test did not open is
// refused rather than answered from the recording, so a test never mixes the
// two for one database.
type TestDatabases struct {
	ctx    context.Context
	open   func(ctx context.Context, connection string, schemas []string) (AppDBRunner, func(), error)
	caller *appdbCaller

	mu      sync.Mutex
	runners map[string]AppDBRunner
	scopes  map[string][]string
	closers []func()
}

// NewTestDatabases returns the throwaway databases of one test execution. ctx
// carries the identity the test runs as, which each database is authorized
// for as the connection itself is. It is nil on a deployment that serves no
// application database.
func NewTestDatabases(ctx context.Context) *TestDatabases {
	a := installed.Load()
	if a == nil || a.TestDatabase == nil {
		return nil
	}
	d := &TestDatabases{ctx: ctx, open: a.TestDatabase, runners: map[string]AppDBRunner{}, scopes: map[string][]string{}}
	db := &AppDB{Runner: func() AppDBRunner { return testRunner{d} }, Authorize: a.Authorize}
	d.caller = &appdbCaller{ctx: ctx, db: db, authorized: map[string]string{}, txs: map[string]AppDBTx{}}
	return d
}

// errNoTestDatabases refuses testing.database on a deployment with none.
var errNoTestDatabases = errors.New("no application database is served on this deployment, so a test has no database to build")

// Open builds connection's throwaway database for the test: its migrations
// in schemas (every schema when empty) applied from zero, cloned for this
// test alone. Opening it again with the same schemas is the same database.
func (d *TestDatabases) Open(connection string, schemas []string) error {
	if d == nil {
		return errNoTestDatabases
	}
	if err := d.caller.authorize(connection); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.runners[connection]; ok {
		if !slices.Equal(d.scopes[connection], schemas) {
			return fmt.Errorf("a test database for %q is open with schemas %v; a test builds one per connection", connection, d.scopes[connection])
		}
		return nil
	}
	r, closeDB, err := d.open(d.ctx, connection, schemas)
	if err != nil {
		return fmt.Errorf("building a test database for %q: %w", connection, err)
	}
	d.runners[connection], d.scopes[connection], d.closers = r, schemas, append(d.closers, closeDB)
	return nil
}

// Active reports whether the test opened a database, after which every
// application-database call is answered here.
func (d *TestDatabases) Active() bool {
	if d == nil {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.runners) > 0
}

// Call answers one application-database call.
func (d *TestDatabases) Call(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	return d.caller.call(ctx, name, args)
}

// Close rolls back the transactions the test left open and drops every
// database it opened.
func (d *TestDatabases) Close() {
	if d == nil {
		return
	}
	d.caller.close()
	d.mu.Lock()
	closers := d.closers
	d.closers, d.runners = nil, map[string]AppDBRunner{}
	d.mu.Unlock()
	for _, c := range closers {
		c()
	}
}

// runner is the database a test opened for connection.
func (d *TestDatabases) runner(connection string) (AppDBRunner, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if r, ok := d.runners[connection]; ok {
		return r, nil
	}
	msg := fmt.Sprintf("application database %q has no test database in this test; build one with testing.database(%q) "+
		"before the script reaches it", connection, connection)
	return nil, NewRefusal(msg, refusalEnv(postgres.CodeUnknownConnection, msg))
}

// testRunner dispatches each call to the database the test opened for its
// connection.
type testRunner struct{ d *TestDatabases }

// HasConnection implements AppDBRunner: the connections the test opened.
func (r testRunner) HasConnection(name string) bool {
	_, err := r.d.runner(name)
	return err == nil
}

// Query implements AppDBRunner.
func (r testRunner) Query(ctx context.Context, s postgres.Statement) (*postgres.Result, error) {
	db, err := r.d.runner(s.Connection)
	if err != nil {
		return nil, err
	}
	return db.Query(ctx, s) //nolint:wrapcheck // the database's typed failure, read by the caller
}

// Execute implements AppDBRunner.
func (r testRunner) Execute(ctx context.Context, s postgres.Statement) (*postgres.Result, error) {
	db, err := r.d.runner(s.Connection)
	if err != nil {
		return nil, err
	}
	return db.Execute(ctx, s) //nolint:wrapcheck // the database's typed failure, read by the caller
}

// Batch implements AppDBRunner.
func (r testRunner) Batch(ctx context.Context, connection string, stmts []postgres.Statement) ([]*postgres.Result, error) {
	db, err := r.d.runner(connection)
	if err != nil {
		return nil, err
	}
	return db.Batch(ctx, connection, stmts) //nolint:wrapcheck // the database's typed failure, read by the caller
}

// Begin implements AppDBRunner.
func (r testRunner) Begin(ctx context.Context, o postgres.TxOptions) (AppDBTx, error) {
	db, err := r.d.runner(o.Connection)
	if err != nil {
		return nil, err
	}
	return db.Begin(ctx, o) //nolint:wrapcheck // the database's typed failure, read by the caller
}
