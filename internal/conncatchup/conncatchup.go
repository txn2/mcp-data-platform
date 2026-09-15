// Package conncatchup is what a toolkit does when it is asked about a
// connection it does not serve.
//
// A deployment runs several replicas over one database, and a connection
// saved through one of them reaches the others twice: in the connection
// store before the save returns, and as an announcement on the reload bus
// some time after. A replica that answers only from what the bus has
// delivered tells a caller the connection does not exist for as long as
// the announcement is in flight — a wrong answer about a connection that
// is saved, and one nothing distinguishes from a genuine misconfiguration
// (#1714 for the graphql kind, #1746 for api).
//
// What is here is the one resolution of that miss: on a name the toolkit
// does not hold, read the connection store, put what it holds in service,
// and answer as the saving replica answers. A connection the toolkit
// already holds never reaches this package, so the store is touched only
// on the miss that is otherwise a wrong answer.
//
// It holds no toolkit types. A kind supplies a Server over its own
// connection map and a Store over its own records, which is what keeps
// two kinds from growing two of these. It sits beside pkg/connreconcile,
// which owns the other half of the same job — applying a connection
// change to the live toolkits — rather than inside it: that package
// reaches the toolkits through pkg/registry, which every toolkit is
// registered in, so a toolkit cannot import it.
package conncatchup

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"golang.org/x/sync/singleflight"

	"github.com/txn2/mcp-data-platform/internal/logsan"
)

// Store is the connection store a miss is resolved against: the records
// the admin write path commits before it returns. A name the store does
// not hold is reported with an error satisfying the kind's not-found
// sentinel, which is what separates "no such connection" from "the store
// could not answer".
type Store interface {
	GetConnection(ctx context.Context, name string) (map[string]any, error)
}

// Server is the toolkit's own view of what it serves, as the catch-up
// needs it: whether a name is held, how a stored connection is put in
// service, and how one is taken back out. Install is called under the
// name's lock with the toolkit known not to hold it, so it materializes
// and serves rather than refusing a duplicate.
type Server interface {
	HasConnection(name string) bool
	Install(ctx context.Context, name string, config map[string]any) error
	Withdraw(name string)
}

// Locks serializes, per connection name, everything that replaces a served
// connection or changes what it holds: a save, a peer's announcement of
// one, a connection taken on from the store, a deletion, a re-read, an
// upload. Without it a connection prepared for service could replace one
// an upload had just changed, and serve the version the upload replaced.
// Distinct connections do not wait on each other.
//
// The zero value is ready to use.
type Locks struct {
	mu    sync.Mutex
	names map[string]*nameLock
}

// nameLock is one name's lock, with the count of holders and waiters that
// keeps it in the map.
type nameLock struct {
	mu   sync.Mutex
	refs int
}

// Lock takes name's lock and returns what releases it.
func (l *Locks) Lock(name string) (unlock func()) {
	l.mu.Lock()
	if l.names == nil {
		l.names = make(map[string]*nameLock)
	}
	nl := l.names[name]
	if nl == nil {
		nl = &nameLock{}
		l.names[name] = nl
	}
	nl.refs++
	l.mu.Unlock()
	nl.mu.Lock()
	return func() {
		nl.mu.Unlock()
		l.mu.Lock()
		nl.refs--
		if nl.refs == 0 {
			delete(l.names, name)
		}
		l.mu.Unlock()
	}
}

// Resolver resolves a miss against the connection store. One belongs to
// one toolkit, beside the Locks that toolkit serializes its connection
// changes with: the catch-up takes the same lock a save takes, so a
// connection taken on from the store cannot land on top of one a save is
// installing.
type Resolver struct {
	locks    *Locks
	kind     string
	notFound error
	shared   singleflight.Group
}

// New returns a Resolver for one kind. kind names the toolkit in the log
// messages a failed catch-up writes, and notFound is the sentinel its
// Store reports an absent connection with.
func New(locks *Locks, kind string, notFound error) *Resolver {
	return &Resolver{locks: locks, kind: kind, notFound: notFound}
}

// Resolve takes on the connection the store holds under name, reporting
// whether the toolkit serves it afterwards. It is called on the miss
// alone: a connection the toolkit holds is answered without it.
//
// Requests that arrive for one name while a read is in flight share that
// read, because the read parses whatever the connection is built from and
// a burst of calls for a connection this instance does not serve would
// otherwise each pay for it.
//
// A nil store is a deployment that keeps connections in memory only,
// where there is nothing to catch up to.
func (r *Resolver) Resolve(ctx context.Context, s Server, store Store, name string) bool {
	if store == nil || name == "" {
		return false
	}
	// The read is shared, so it must not end with whichever request
	// happened to start it.
	shared := context.WithoutCancel(ctx)
	_, _, _ = r.shared.Do(name, func() (any, error) {
		if r.takeOn(shared, s, store, name) {
			r.withdrawIfDeleted(shared, s, store, name)
		}
		return struct{}{}, nil
	})
	return s.HasConnection(name)
}

// takeOn puts the stored connection in service, reporting whether it did.
// A connection the store does not hold is not served, and one a save put
// in service before the lock was taken is left as it is.
func (r *Resolver) takeOn(ctx context.Context, s Server, store Store, name string) bool {
	unlock := r.locks.Lock(name)
	defer unlock()
	if s.HasConnection(name) {
		return false
	}
	config, err := store.GetConnection(ctx, name)
	if err != nil {
		if !errors.Is(err, r.notFound) {
			r.warn("reading a connection from the connection store failed", name, err)
		}
		return false
	}
	if err := s.Install(ctx, name, config); err != nil {
		r.warn("a stored connection could not be served", name, err)
		return false
	}
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
func (r *Resolver) withdrawIfDeleted(ctx context.Context, s Server, store Store, name string) {
	unlock := r.locks.Lock(name)
	defer unlock()
	if _, err := store.GetConnection(ctx, name); !errors.Is(err, r.notFound) {
		return
	}
	s.Withdraw(name)
}

// warn reports a catch-up that could not serve a connection, in the
// kind's voice.
func (r *Resolver) warn(msg, name string, err error) {
	slog.Warn(r.kind+": "+msg,
		"connection", logsan.SanitizeForLog(name),
		"error", logsan.SanitizeForLog(err.Error()))
}
