package graphql

import (
	"context"

	"github.com/txn2/mcp-data-platform/internal/conncatchup"
)

// A deployment runs several replicas over one database, and a connection
// saved through one of them reaches the others twice: in the store before
// the save returns, and as an announcement some time after. How a replica
// serves such a connection from the first of those rather than the second
// is internal/conncatchup, shared with every other kind that has the same
// window (#1714, #1746). What is here is this kind's half: what it means
// to install one, and the schema state a change to a served connection is
// committed against.

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
	unlock := t.changes.Lock(c.name)
	defer unlock()
	if c.phase.Load() == phaseRetired {
		return false
	}
	fn()
	return true
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
	unlock := t.changes.Lock(name)
	defer unlock()
	return t.installConnection(context.Background(), name, config)
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

// serving returns the connection served under name, taking on one another
// replica saved when the announcement of that save has not reached this
// instance yet.
func (t *Toolkit) serving(ctx context.Context, name string) (*conn, RoutePolicy, bool) {
	if c, policy, ok := t.lookup(name); ok {
		return c, policy, true
	}
	t.mu.RLock()
	store := t.connStore
	t.mu.RUnlock()
	// A nil store travels as a nil interface rather than an adapter
	// wrapping nothing, which is how the catch-up recognizes a
	// deployment that keeps connections in memory only.
	var reads conncatchup.Store
	if store != nil {
		reads = store
	}
	t.catchUp.Resolve(ctx, catchUpServer{t: t}, reads, name)
	return t.lookup(name)
}

// catchUpServer is this kind's connection map as the catch-up changes it.
type catchUpServer struct{ t *Toolkit }

// Compile-time interface check.
var _ conncatchup.Server = catchUpServer{}

// HasConnection reports whether the toolkit serves name.
func (s catchUpServer) HasConnection(name string) bool { return s.t.HasConnection(name) }

// Install serves the connection the store holds under name. The caller
// holds the name's lock.
func (s catchUpServer) Install(ctx context.Context, name string, config map[string]any) error {
	return s.t.installConnection(ctx, name, config)
}

// Withdraw takes a connection out of service without touching the schema
// the store holds for it: the replica that deleted the connection dropped
// that, and this instance is only catching up with the removal.
func (s catchUpServer) Withdraw(name string) {
	s.t.mu.Lock()
	c, served := s.t.connections[name]
	if served {
		delete(s.t.connections, name)
	}
	s.t.mu.Unlock()
	if served {
		retire(c)
	}
}

// installConnection materializes config and puts it in service under
// name, replacing whatever this instance served there. The schema comes
// from the store, and the endpoint is read only when the store holds
// none; a schema the replaced connection held stands in when neither
// answers. The caller holds the name's lock.
func (t *Toolkit) installConnection(ctx context.Context, name string, config map[string]any) error {
	cfg, err := ParseConfig(config)
	if err != nil {
		return err
	}
	cfg.ConnectionName = name
	c, err := newConn(cfg)
	if err != nil {
		return err
	}
	if existing, _, ok := t.lookup(name); ok {
		t.carrySchema(ctx, existing, c)
	}
	t.loadOrRead(ctx, c)
	t.serve(name, c)
	return nil
}
