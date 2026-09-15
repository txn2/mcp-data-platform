// Package callcatchup puts the connection a tool call names into service when
// this process does not serve it.
//
// A connection exists because the connection store holds a row for it. Several
// replicas run over one database, and a connection saved through one of them is
// a row before it is anything in the others' memory: it reaches them as an
// announcement on the reload bus some time after the save returns. A replica
// that answers only from what the bus has delivered refuses a call against a
// connection that is saved, with a message an operator cannot tell from a
// genuine misconfiguration.
//
// Two kinds have had that window closed one at a time, by reading the store on
// a name their own connection map does not hold (#1714 for graphql, #1746 for
// api). The same window is open on every other kind, and their tool handlers
// are not all in this repository to reach into — trino's and s3's are the
// upstream toolkits'. So it is closed here instead, in the one place every tool
// call already passes through with the kind and connection name resolved: a
// call naming a connection this process does not serve takes it on from the
// store before the handler runs, whatever kind it is (#1757).
//
// Nothing about the resolution is new. The store read, the per-name lock, the
// shared read and the withdrawal of a connection deleted while it was being
// taken on are internal/conncatchup's, and putting the connection in service is
// pkg/connreconcile's — the same call the reload bus makes when the
// announcement does arrive.
package callcatchup

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/txn2/mcp-data-platform/internal/conncatchup"
	"github.com/txn2/mcp-data-platform/pkg/connreconcile"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// ErrNotFound is how a store reports a connection it does not hold. It is what
// separates "no such connection" from "the store could not answer": the first
// leaves the process serving what it serves, the second is logged.
var ErrNotFound = errors.New("the connection store holds no such connection")

// Store reads the connections an operator saved, of any kind. A deployment
// that keeps its connections in its configuration file alone wires none.
type Store interface {
	// ConnectionConfig returns one saved connection's configuration, reporting
	// a connection it does not hold with an error satisfying ErrNotFound.
	ConnectionConfig(ctx context.Context, kind, name string) (map[string]any, error)
}

// RecordStore is the platform's connection store, as far as this package reads
// it: one saved connection of a kind, as the record type the store keeps, and
// whether the store outlives the process.
type RecordStore[R any] interface {
	Get(ctx context.Context, kind, name string) (R, error)
	Persistent() bool
}

// Reader adapts the platform's connection store to the Store a catch-up reads,
// translating the store's absent-record error into ErrNotFound and handing back
// the configuration map a toolkit installs.
//
// A nil store, or one that does not outlive the process and so holds nothing
// another replica wrote, adapts to nil: there is nothing to catch up to where
// every replica knows only what it was handed.
func Reader[R any](store RecordStore[R], notFound error, config func(R) map[string]any) Store {
	if store == nil || !store.Persistent() || config == nil {
		return nil
	}
	return reader[R]{store: store, notFound: notFound, config: config}
}

// reader is Reader's adapter.
type reader[R any] struct {
	store    RecordStore[R]
	notFound error
	config   func(R) map[string]any
}

// ConnectionConfig returns one saved connection's configuration.
func (r reader[R]) ConnectionConfig(ctx context.Context, kind, name string) (map[string]any, error) {
	record, err := r.store.Get(ctx, kind, name)
	if errors.Is(err, r.notFound) {
		return nil, fmt.Errorf("no %s connection %s is saved: %w", kind, name, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s connection %s: %w", kind, name, err)
	}
	return r.config(record), nil
}

// ToolkitSource is the live toolkit registry.
type ToolkitSource interface {
	All() []registry.Toolkit
}

// Resolver takes on the connection a call names. One belongs to a process.
type Resolver struct {
	source     ToolkitSource
	store      Store
	reconciler *connreconcile.Reconciler
	locks      conncatchup.Locks

	mu     sync.Mutex
	byKind map[string]*conncatchup.Resolver
}

// New returns a Resolver, or nil where there is nothing to catch up to: no
// registry to put a connection in service on, or no store holding what another
// replica saved.
func New(source ToolkitSource, store Store) *Resolver {
	if source == nil || store == nil {
		return nil
	}
	return &Resolver{
		source:     source,
		store:      store,
		reconciler: connreconcile.New(source),
		byKind:     make(map[string]*conncatchup.Resolver),
	}
}

// TakeOn puts the named connection of kind into service when this process does
// not serve it, so the call that named it is answered as the saving replica
// answers it. A connection this process already serves costs one map lookup.
//
// It reports nothing. A connection the store does not hold is a call against a
// connection that does not exist, and the handler's own refusal names it far
// better than this layer could; a store that cannot answer leaves the handler
// to refuse as it did before.
func (r *Resolver) TakeOn(ctx context.Context, kind, name string) {
	if r == nil || kind == "" || name == "" {
		return
	}
	s := server{source: r.source, reconciler: r.reconciler, kind: kind}
	// A kind with no toolkit that manages connections has nothing to take a
	// connection on: the knowledge, portal and memory toolkits answer for
	// themselves and hold none. Asking first is what keeps every call to one
	// of their tools from reading the connection store for a name it will
	// never find.
	if !s.manages() || s.HasConnection(name) {
		return
	}
	r.forKind(kind).Resolve(ctx, s, kindStore{store: r.store, kind: kind}, name)
}

// forKind returns the kind's resolver, which holds the shared read that keeps a
// burst of calls for one connection to one store read. Every kind shares the
// process's name locks: two kinds do not hold connections of the same name, and
// where a kind has its own catch-up the toolkit takes its own lock underneath
// this one.
func (r *Resolver) forKind(kind string) *conncatchup.Resolver {
	r.mu.Lock()
	defer r.mu.Unlock()
	resolver, ok := r.byKind[kind]
	if !ok {
		resolver = conncatchup.New(&r.locks, kind, ErrNotFound)
		r.byKind[kind] = resolver
	}
	return resolver
}

// kindStore is the connection store narrowed to one kind, which is the shape
// the shared catch-up reads.
type kindStore struct {
	store Store
	kind  string
}

// GetConnection returns one connection of this kind.
func (s kindStore) GetConnection(ctx context.Context, name string) (map[string]any, error) {
	config, err := s.store.ConnectionConfig(ctx, s.kind, name)
	if err != nil {
		return nil, fmt.Errorf("reading the %s connection store: %w", s.kind, err)
	}
	return config, nil
}

// server is the live toolkits of one kind, as the catch-up changes them.
type server struct {
	source     ToolkitSource
	reconciler *connreconcile.Reconciler
	kind       string
}

// Compile-time interface check.
var _ conncatchup.Server = server{}

// HasConnection reports whether any toolkit of this kind serves name.
func (s server) HasConnection(name string) bool {
	for _, manager := range s.managers() {
		if manager.HasConnection(name) {
			return true
		}
	}
	return false
}

// manages reports whether this process holds a toolkit of this kind that can
// take a connection on.
func (s server) manages() bool { return len(s.managers()) > 0 }

// managers returns the live toolkits of this kind that manage connections.
func (s server) managers() []toolkit.ConnectionManager {
	var out []toolkit.ConnectionManager
	for _, tk := range s.source.All() {
		if tk.Kind() != s.kind {
			continue
		}
		if manager, ok := tk.(toolkit.ConnectionManager); ok {
			out = append(out, manager)
		}
	}
	return out
}

// Install puts the stored connection in service, by the same call the reload
// bus makes when a peer announces the save. A toolkit that took nothing on
// reports so, rather than a connection installed nowhere being reported as
// served.
func (s server) Install(_ context.Context, name string, config map[string]any) error {
	failures := s.reconciler.Adopt(s.kind, name, config)
	if len(failures) > 0 {
		return fmt.Errorf("the %s %s failed: %w", s.kind, failures[0].Phase, failures[0].Err)
	}
	if !s.HasConnection(name) {
		return fmt.Errorf("no %s toolkit took the connection on", s.kind)
	}
	return nil
}

// Withdraw takes a connection back out, for one deleted while it was being
// taken on.
func (s server) Withdraw(name string) {
	s.reconciler.Remove(s.kind, name)
}
