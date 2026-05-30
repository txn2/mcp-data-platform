package indexjobs

import (
	"fmt"
	"sort"
	"sync"
)

// Registry maps a source_kind to its Source + Sink pair. The
// worker looks the pair up by the source_kind on each claimed job;
// the reconciler iterates every registered Sink to detect gaps.
// One Registry is shared across the worker pool, reaper, and
// reconciler so all three agree on the set of live kinds.
//
// Registration happens at platform wiring time (before Start), so
// the map is effectively read-only once the queue is running. The
// mutex guards the wiring window and any future hot-registration
// path without forcing callers to reason about ordering.
type Registry struct {
	mu    sync.RWMutex
	pairs map[string]pair
}

type pair struct {
	source Source
	sink   Sink
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{pairs: make(map[string]pair)}
}

// Register binds a Source and Sink to their shared source kind.
// Returns an error when source.Kind() and sink.Kind() disagree, or
// when the kind is already registered, so a wiring mistake fails
// loudly at startup rather than silently routing jobs to the wrong
// storage.
func (r *Registry) Register(source Source, sink Sink) error {
	if source == nil || sink == nil {
		return fmt.Errorf("indexjobs: register: source and sink must both be non-nil")
	}
	if source.Kind() != sink.Kind() {
		return fmt.Errorf("indexjobs: register: source kind %q != sink kind %q", source.Kind(), sink.Kind())
	}
	kind := source.Kind()
	if kind == "" {
		return fmt.Errorf("indexjobs: register: empty source kind")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.pairs[kind]; ok {
		return fmt.Errorf("indexjobs: register: kind %q already registered", kind)
	}
	r.pairs[kind] = pair{source: source, sink: sink}
	return nil
}

// Lookup returns the Source and Sink for a source kind. ok is
// false when the kind is not registered (a job for an
// unregistered kind, e.g. a leftover row from a removed consumer);
// the worker terminates such a job rather than spinning on it.
func (r *Registry) Lookup(kind string) (source Source, sink Sink, ok bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.pairs[kind]
	if !ok {
		return nil, nil, false
	}
	return p.source, p.sink, true
}

// Sinks returns every registered Sink, ordered by kind for stable
// iteration. The reconciler walks this set calling FindGaps on
// each.
func (r *Registry) Sinks() []Sink {
	r.mu.RLock()
	defer r.mu.RUnlock()
	kinds := make([]string, 0, len(r.pairs))
	for k := range r.pairs {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	out := make([]Sink, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, r.pairs[k].sink)
	}
	return out
}

// Kinds returns every registered source kind, sorted. Exposed for
// the admin surface and for wiring tests that assert the expected
// set of consumers registered.
func (r *Registry) Kinds() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	kinds := make([]string, 0, len(r.pairs))
	for k := range r.pairs {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	return kinds
}
