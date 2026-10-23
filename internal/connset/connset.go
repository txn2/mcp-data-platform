// Package connset is the connection registry a toolkit of a connection kind
// keeps: the connections it serves by name, added, replaced and removed as an
// operator saves them, and taken on from the connection store when a call
// names one another replica saved and this one has not heard of yet
// (internal/conncatchup).
//
// It holds no kind's types. A kind supplies how a stored configuration becomes
// the value it serves (build) and what to release when one is retired, which
// is what keeps the caldav and imap kinds (#2246, #2247) from each growing a
// copy of the same map, lock and catch-up the graphql kind carries inline.
package connset

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/txn2/mcp-data-platform/internal/conncatchup"
)

// ErrExists is the refusal of an Add for a name already served.
var ErrExists = errors.New("already exists")

// Set is the connections of one kind, by name.
type Set[C any] struct {
	kind     string
	notFound error
	build    func(name string, cfg map[string]any) (C, error)
	release  func(C)

	mu    sync.RWMutex
	conns map[string]C
	store conncatchup.Store

	changes conncatchup.Locks
	catchUp *conncatchup.Resolver
}

// New returns an empty set. notFound is the kind's not-found sentinel, which
// the connection store reports a missing name with; release may be nil.
func New[C any](kind string, notFound error, build func(string, map[string]any) (C, error), release func(C)) *Set[C] {
	s := &Set[C]{kind: kind, notFound: notFound, build: build, release: release, conns: map[string]C{}}
	s.catchUp = conncatchup.New(&s.changes, kind, notFound)
	return s
}

// SetStore attaches the connection store a miss is resolved against.
func (s *Set[C]) SetStore(store conncatchup.Store) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.store = store
}

// Add builds and serves a connection the set does not hold.
func (s *Set[C]) Add(name string, cfg map[string]any) error {
	c, err := s.build(name, cfg)
	if err != nil {
		return err
	}
	unlock := s.changes.Lock(name)
	defer unlock()
	if s.Has(name) {
		s.retire(c)
		return fmt.Errorf("connection %q: %w", name, ErrExists)
	}
	s.serve(name, c)
	return nil
}

// Put serves an already-built connection under name, replacing any served
// there: the toolkit's constructor, from configuration it parsed itself.
func (s *Set[C]) Put(name string, c C) {
	unlock := s.changes.Lock(name)
	defer unlock()
	s.serve(name, c)
}

// Update replaces a served connection with one built from cfg.
func (s *Set[C]) Update(name string, cfg map[string]any) error {
	c, err := s.build(name, cfg)
	if err != nil {
		return err
	}
	unlock := s.changes.Lock(name)
	defer unlock()
	if !s.Has(name) {
		s.retire(c)
		return s.missing(name)
	}
	s.serve(name, c)
	return nil
}

// Adopt serves cfg under name whether or not the set holds it: a peer's
// announcement of a save.
func (s *Set[C]) Adopt(name string, cfg map[string]any) error {
	unlock := s.changes.Lock(name)
	defer unlock()
	return s.install(name, cfg)
}

// Remove stops serving name.
func (s *Set[C]) Remove(name string) error {
	unlock := s.changes.Lock(name)
	defer unlock()
	if !s.withdraw(name) {
		return s.missing(name)
	}
	return nil
}

// Has reports whether name is served.
func (s *Set[C]) Has(name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.conns[name]
	return ok
}

// Names lists the served connections, sorted.
func (s *Set[C]) Names() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.conns))
	for n := range s.conns {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Each calls f for every served connection, in name order.
func (s *Set[C]) Each(f func(name string, c C)) {
	for _, n := range s.Names() {
		s.mu.RLock()
		c, ok := s.conns[n]
		s.mu.RUnlock()
		if ok {
			f(n, c)
		}
	}
}

// Get returns the connection a call names, taking it on from the store when
// another replica saved it. An empty name means the only connection when
// there is exactly one; otherwise the error names the choices.
func (s *Set[C]) Get(ctx context.Context, name string) (C, error) {
	var zero C
	if name == "" {
		names := s.Names()
		switch len(names) {
		case 0:
			return zero, fmt.Errorf("no %s connection is configured", s.kind)
		case 1:
			name = names[0]
		default:
			return zero, fmt.Errorf("name a %s connection; this deployment has %s", s.kind, strings.Join(names, ", "))
		}
	}
	if c, ok := s.lookup(name); ok {
		return c, nil
	}
	s.mu.RLock()
	store := s.store
	s.mu.RUnlock()
	s.catchUp.Resolve(ctx, server[C]{s}, store, name)
	if c, ok := s.lookup(name); ok {
		return c, nil
	}
	return zero, s.missing(name)
}

// Close retires every connection.
func (s *Set[C]) Close() {
	s.mu.Lock()
	conns := s.conns
	s.conns = map[string]C{}
	s.mu.Unlock()
	for _, c := range conns {
		s.retire(c)
	}
}

func (s *Set[C]) lookup(name string) (C, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.conns[name]
	return c, ok
}

func (s *Set[C]) install(name string, cfg map[string]any) error {
	c, err := s.build(name, cfg)
	if err != nil {
		return err
	}
	s.serve(name, c)
	return nil
}

func (s *Set[C]) serve(name string, c C) {
	s.mu.Lock()
	prev, held := s.conns[name]
	s.conns[name] = c
	s.mu.Unlock()
	if held {
		s.retire(prev)
	}
}

func (s *Set[C]) withdraw(name string) bool {
	s.mu.Lock()
	c, ok := s.conns[name]
	delete(s.conns, name)
	s.mu.Unlock()
	if ok {
		s.retire(c)
	}
	return ok
}

func (s *Set[C]) retire(c C) {
	if s.release != nil {
		s.release(c)
	}
}

func (s *Set[C]) missing(name string) error {
	return fmt.Errorf("connection %q: %w", name, s.notFound)
}

// server is the set as conncatchup sees it.
type server[C any] struct{ s *Set[C] }

// HasConnection reports whether the set serves name.
func (v server[C]) HasConnection(name string) bool { return v.s.Has(name) }

// Install serves a stored connection; the catch-up holds name's lock.
func (v server[C]) Install(_ context.Context, name string, cfg map[string]any) error {
	return v.s.install(name, cfg)
}

// Withdraw stops serving name.
func (v server[C]) Withdraw(name string) { v.s.withdraw(name) }
