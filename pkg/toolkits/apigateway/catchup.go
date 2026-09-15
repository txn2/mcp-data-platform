package apigateway

import (
	"context"

	"github.com/txn2/mcp-data-platform/internal/conncatchup"
)

// A connection saved through the admin API is applied on the replica that
// served the request and reaches the others over the reload bus. Until
// that announcement is delivered, a call landing on another replica was
// told the connection does not exist — a wrong answer about a connection
// that is saved, and one an operator cannot tell from a misconfiguration
// (#1746). The resolution is internal/conncatchup, shared with the
// graphql kind, which had the same window closed in #1714: on a name this
// instance does not hold, the connection store is read before "not found"
// is the answer.
//
// Nothing about a connection is derived per replica here. A connection's
// specs and operation vectors are keyed on its catalog, not on the
// connection, so a replica catching up reads the same catalog rows the
// saving replica read and has nothing to write back.

// ConnectionStore reads the connections an operator saved. The platform
// wires its database-backed store; a deployment that keeps connections in
// its configuration file alone wires none and serves what it was handed.
type ConnectionStore interface {
	// GetConnection returns the stored configuration of one connection,
	// reporting a name it does not hold with an error satisfying
	// ErrConnectionNotFound.
	GetConnection(ctx context.Context, name string) (map[string]any, error)
}

// SetConnectionStore wires the store connections are saved in, which a
// request for a connection this instance does not serve is answered from.
// Passing nil leaves this instance serving only the connections it was
// handed.
func (t *Toolkit) SetConnectionStore(s ConnectionStore) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.connStore = s
}

// AdoptConnection makes config the live configuration of a connection
// another replica saved, applying that replica's announcement. Satisfies
// toolkit.ConnectionAdopter.
//
// It replaces what this instance served under the name rather than
// refusing a duplicate, so an announcement and a catch-up that raced can
// both be applied: the connection either kind installs is built from the
// same stored configuration.
func (t *Toolkit) AdoptConnection(name string, config map[string]any) error {
	cfg, err := ParseConfig(config)
	if err != nil {
		return err
	}
	if cfg.ConnectionName == "" {
		cfg.ConnectionName = name
	}
	unlock := t.changes.Lock(name)
	defer unlock()
	return t.installConnection(name, cfg)
}

// ServesConnection reports whether this toolkit serves the named
// connection, taking on one another replica saved when the announcement
// of that save has not reached this instance yet.
func (t *Toolkit) ServesConnection(ctx context.Context, name string) bool {
	_, ok := t.serving(ctx, name)
	return ok
}

// serving returns the connection served under name. When this instance
// serves none, it reads the connection store and takes on a connection
// another replica saved: the save is in the store before it returns, and
// its announcement reaches this instance some time after, so a request
// arriving in between is answered as the saving replica answers it
// (#1746). Requests for one name share one read.
func (t *Toolkit) serving(ctx context.Context, name string) (*conn, bool) {
	if c, ok := t.lookup(name); ok {
		return c, true
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

// lookup returns the connection served under name.
func (t *Toolkit) lookup(name string) (*conn, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	c, ok := t.connections[name]
	return c, ok
}

// catchUpServer is this kind's connection map as the catch-up changes it.
type catchUpServer struct{ t *Toolkit }

// Compile-time interface check.
var _ conncatchup.Server = catchUpServer{}

// HasConnection reports whether the toolkit serves name.
func (s catchUpServer) HasConnection(name string) bool { return s.t.HasConnection(name) }

// Install serves the connection the store holds under name. The caller
// holds the name's lock.
func (s catchUpServer) Install(_ context.Context, name string, config map[string]any) error {
	cfg, err := ParseConfig(config)
	if err != nil {
		return err
	}
	if cfg.ConnectionName == "" {
		cfg.ConnectionName = name
	}
	return s.t.installConnection(name, cfg)
}

// Withdraw takes a connection out of service, as RemoveConnection does
// for the replica an operator deleted it on. It does the removal itself
// rather than calling that method, which takes the name's lock this
// caller already holds.
func (s catchUpServer) Withdraw(name string) { s.t.withdraw(name) }

// installConnection builds cfg and puts it in service under name,
// replacing whatever this instance served there. The caller holds the
// name's lock.
func (t *Toolkit) installConnection(name string, cfg Config) error {
	c, err := t.buildConn(name, cfg)
	if err != nil {
		return err
	}
	t.serve(name, c)
	return nil
}

// serve puts c in service under name, with the dependencies wired after
// construction threaded through it, and releases the idle transports of
// the connection it replaces. The caller holds the name's lock and has
// decided under it that c is what the name serves.
func (t *Toolkit) serve(name string, c *conn) {
	t.mu.Lock()
	previous, held := t.connections[name]
	t.wireConnLocked(name, c)
	t.connections[name] = c
	t.mu.Unlock()
	if held {
		closeIdle(previous)
	}
}

// closeIdle releases a connection's idle transports. Calls in flight keep
// the ones they hold.
func closeIdle(c *conn) {
	if c != nil && c.client != nil {
		c.client.CloseIdleConnections()
	}
}
