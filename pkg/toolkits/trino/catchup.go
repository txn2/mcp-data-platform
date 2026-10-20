package trino

import (
	"context"
	"errors"
	"time"

	"github.com/txn2/mcp-data-platform/internal/conncatchup"
)

// A connection saved through the admin API is applied on the replica that
// served the request and reaches the others over the reload bus. Until that
// announcement arrives, a replica asked to create a webhook source's table or
// register a table on the connection answered that it had no scratch catalog,
// a wrong answer about a connection that is saved (#2218). The resolution is
// internal/conncatchup, shared with the api and graphql kinds (#1714, #1746):
// on a name this instance does not hold, the connection store is read before
// the answer is given.

// ErrConnectionNotFound is what a ConnectionStore reports for a name it does
// not hold.
var ErrConnectionNotFound = errors.New("trino connection not found")

// ConnectionStore reads the connections an operator saved. The platform wires
// its database-backed store; a deployment that keeps connections in its
// configuration file alone wires none and serves what it was handed.
type ConnectionStore interface {
	// GetConnection returns the stored configuration of one connection,
	// reporting a name it does not hold with an error satisfying
	// ErrConnectionNotFound.
	GetConnection(ctx context.Context, name string) (map[string]any, error)
}

// SetConnectionStore wires the store a request for a connection this instance
// does not serve is answered from. Passing nil leaves this instance serving
// only the connections it was handed.
func (t *Toolkit) SetConnectionStore(s ConnectionStore) {
	t.connMu.Lock()
	defer t.connMu.Unlock()
	t.connStore = s
}

// ServesConnection reports whether this toolkit serves the named connection,
// taking on one another replica saved when the announcement of that save has
// not reached this instance yet. An empty name is the default connection,
// which is always served.
func (t *Toolkit) ServesConnection(ctx context.Context, name string) bool {
	if name == "" || t.HasConnection(name) {
		return true
	}
	if t.catchUp == nil {
		return false
	}
	t.connMu.RLock()
	store := t.connStore
	t.connMu.RUnlock()
	// A nil store travels as a nil interface rather than an adapter wrapping
	// nothing, which is how the catch-up recognizes a deployment that keeps
	// connections in memory only.
	var reads conncatchup.Store
	if store != nil {
		reads = store
	}
	return t.catchUp.Resolve(ctx, catchUpServer{t: t}, reads, name)
}

// catchUpReadTimeout bounds the store read a catch-up makes for a caller that
// passes no context (ScratchTarget, AcceptsWrites).
const catchUpReadTimeout = 10 * time.Second

// catchUpUnbound is ServesConnection for a caller with no context of its own:
// a webhook source or table registration reading the connection's scratch
// target or write setting, which every replica must answer alike once a save
// has returned.
func (t *Toolkit) catchUpUnbound(name string) {
	if name == "" || t.HasConnection(name) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), catchUpReadTimeout)
	defer cancel()
	t.ServesConnection(ctx, name)
}

// catchUpServer is this kind's connection map as the catch-up changes it.
type catchUpServer struct{ t *Toolkit }

// Compile-time interface check.
var _ conncatchup.Server = catchUpServer{}

// HasConnection reports whether the toolkit serves name.
func (s catchUpServer) HasConnection(name string) bool { return s.t.HasConnection(name) }

// Install serves the connection the store holds under name, as a save on
// this replica would.
func (s catchUpServer) Install(_ context.Context, name string, config map[string]any) error {
	return s.t.AddConnection(name, config)
}

// Withdraw takes a connection a deletion removed back out of service.
func (s catchUpServer) Withdraw(name string) { _ = s.t.RemoveConnection(name) }
