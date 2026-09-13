package graphql

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/txn2/mcp-data-platform/internal/logsan"
)

// A deployment runs several replicas over one database, and a connection
// saved through one of them reaches the others twice: in the store before
// the save returns, and as an announcement some time after. What is here
// is how a replica serves such a connection as the saving replica does
// from the first of those rather than the second (#1714).

// connLocks serializes, per connection name, everything that replaces a
// served connection or changes the schema it holds: a save, a peer's
// announcement of one, a connection taken on from the store, a deletion, a
// re-read, an upload. Without it a connection prepared for service could
// replace one an upload had just changed, and serve the schema the upload
// replaced. Distinct connections do not wait on each other.
type connLocks struct {
	mu    sync.Mutex
	names map[string]*connLock
}

// A connection's phases. A connection being prepared is reachable only
// by the goroutine preparing it; from the moment it is served, what
// changes its schema does so under its name's lock, and a retired one is
// changed no more.
const (
	phasePreparing int32 = iota
	phaseServed
	phaseRetired
)

// commit runs fn, a change to c's schema state, where the change belongs:
// directly on a connection still being prepared, and under the name's lock
// on a served one, unless a replacement or a deletion retired it while the
// change was being made, in which case it reports false and fn does not
// run. What replaced a connection brought its own schema up, and a read
// made through a connection that is gone is not a fact about the one that
// replaced it.
func (t *Toolkit) commit(c *conn, fn func()) bool {
	if c.phase.Load() == phasePreparing {
		fn()
		return true
	}
	unlock := t.changes.lock(c.name)
	defer unlock()
	if c.phase.Load() == phaseRetired {
		return false
	}
	fn()
	return true
}

// connLock is one name's lock, with the count of holders and waiters that
// keeps it in the map.
type connLock struct {
	mu   sync.Mutex
	refs int
}

// lock takes name's lock and returns what releases it.
func (l *connLocks) lock(name string) (unlock func()) {
	l.mu.Lock()
	if l.names == nil {
		l.names = make(map[string]*connLock)
	}
	cl := l.names[name]
	if cl == nil {
		cl = &connLock{}
		l.names[name] = cl
	}
	cl.refs++
	l.mu.Unlock()
	cl.mu.Lock()
	return func() {
		cl.mu.Unlock()
		l.mu.Lock()
		cl.refs--
		if cl.refs == 0 {
			delete(l.names, name)
		}
		l.mu.Unlock()
	}
}

// AdoptConnection makes config the live configuration of a connection
// another replica saved, applying that replica's announcement. Satisfies
// toolkit.ConnectionAdopter.
//
// The schema comes from the store, where the saving replica wrote what its
// read found before announcing the save; the endpoint is read only when the
// store holds none. Reading it again on every replica for every save put
// each replica's announcements behind the slowest endpoint, and served the
// connection with no schema until the read was over (#1714). The connection
// is served complete, replacing the one this instance held, whose schema
// stands in when the store has none and the endpoint refuses.
func (t *Toolkit) AdoptConnection(name string, config map[string]any) error {
	cfg, err := ParseConfig(config)
	if err != nil {
		return err
	}
	cfg.ConnectionName = name
	c, err := newConn(cfg)
	if err != nil {
		return err
	}
	ctx := context.Background()
	unlock := t.changes.lock(name)
	defer unlock()
	if existing, _, ok := t.lookup(name); ok {
		t.carrySchema(ctx, existing, c)
	}
	t.loadOrRead(ctx, c)
	t.serve(name, c)
	return nil
}

// ServesConnection reports whether this toolkit serves the named
// connection, taking on one another replica saved when the announcement
// of that save has not reached this instance yet. The admin schema routes
// find a connection through it, so a Schema card opened on any replica
// once a save has returned reports the connection (#1714).
func (t *Toolkit) ServesConnection(ctx context.Context, name string) bool {
	_, _, ok := t.serving(ctx, name)
	return ok
}

// serving returns the connection served under name. When this instance
// serves none, it reads the connection store and takes on a connection
// another replica saved: the save is in the store before it returns, and
// its announcement reaches this instance some time after, so a request
// arriving in between is answered as the saving replica answers it
// (#1714). Requests for one name share one read.
func (t *Toolkit) serving(ctx context.Context, name string) (*conn, RoutePolicy, bool) {
	if c, policy, ok := t.lookup(name); ok {
		return c, policy, true
	}
	t.mu.RLock()
	store := t.connStore
	t.mu.RUnlock()
	if store == nil || name == "" {
		return nil, nil, false
	}
	// The read is shared, so it must not end with whichever request
	// happened to start it.
	shared := context.WithoutCancel(ctx)
	_, _, _ = t.catchUp.Do(name, func() (any, error) {
		t.takeOn(shared, store, name)
		return struct{}{}, nil
	})
	return t.lookup(name)
}

// takeOn serves a connection the connection store holds and this instance
// does not, with the schema brought up the way AdoptConnection brings it
// up. A connection the store does not hold is not served, and one a save
// put in service before the lock was taken is left as it is.
func (t *Toolkit) takeOn(ctx context.Context, store ConnectionStore, name string) {
	if t.takeOnLocked(ctx, store, name) {
		t.withdrawIfDeleted(ctx, store, name)
	}
}

// takeOnLocked is takeOn's part under the connection's lock, reporting
// whether it put a connection in service.
func (t *Toolkit) takeOnLocked(ctx context.Context, store ConnectionStore, name string) bool {
	unlock := t.changes.lock(name)
	defer unlock()
	if t.HasConnection(name) {
		return false
	}
	config, err := store.GetConnection(ctx, name)
	if err != nil {
		if !errors.Is(err, ErrConnectionNotFound) {
			slog.Warn("graphql: reading a connection from the connection store failed",
				logKeyConnection, logsan.SanitizeForLog(name), logKeyError, logsan.SanitizeForLog(err.Error()))
		}
		return false
	}
	cfg, err := ParseConfig(config)
	if err != nil {
		slog.Warn("graphql: a stored connection does not parse",
			logKeyConnection, logsan.SanitizeForLog(name), logKeyError, logsan.SanitizeForLog(err.Error()))
		return false
	}
	cfg.ConnectionName = name
	c, err := newConn(cfg)
	if err != nil {
		slog.Warn("graphql: a stored connection could not be materialized",
			logKeyConnection, logsan.SanitizeForLog(name), logKeyError, logsan.SanitizeForLog(err.Error()))
		return false
	}
	t.loadOrRead(ctx, c)
	t.serve(name, c)
	return true
}

// withdrawIfDeleted takes a connection takeOn put in service back out when
// the connection store no longer holds it. A deletion that committed while
// takeOn read the connection was announced while this instance did not
// serve it yet, so the announcement removed nothing here, and without this
// the deleted connection would be served until a restart. A deletion that
// commits after this read is announced after the connection was served,
// and removes it. A store that cannot answer leaves the connection served:
// the read that put it in service found it.
func (t *Toolkit) withdrawIfDeleted(ctx context.Context, store ConnectionStore, name string) {
	unlock := t.changes.lock(name)
	defer unlock()
	if _, err := store.GetConnection(ctx, name); !errors.Is(err, ErrConnectionNotFound) {
		return
	}
	t.mu.Lock()
	c, served := t.connections[name]
	if served {
		delete(t.connections, name)
	}
	t.mu.Unlock()
	if served {
		retire(c)
	}
}
