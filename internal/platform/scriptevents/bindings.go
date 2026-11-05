package scriptevents

import (
	"context"
	"sync"
	"time"
)

// BindingsChannel is the NOTIFY channel a change to any script's webhook
// source is announced on (migration 000230's trigger), which drops every
// replica's Bindings.
const BindingsChannel = "script_event_sources"

// DefaultBindingsMaxAge is the longest Bindings answers from one read when no
// change is heard: a NOTIFY sent while a replica's LISTEN connection was down
// is lost, and this is how long such a replica can miss a new binding. It is
// the receiver's own source refresh interval.
const DefaultBindingsMaxAge = 5 * time.Second

// BindingReader reads which scripts run for every webhook source
// (PostgresStore).
type BindingReader interface {
	EventBindings(ctx context.Context) (map[string][]string, error)
}

// Bindings is which scripts run for each webhook source, kept in memory so a
// stored delivery costs no query (#2480). The webhook receiver hands every
// delivery to the enqueuer inside the sender's request; asking the database
// each time held the shared pool through a sender's burst even for a source
// no script names.
//
// The map is read whole, at most once per MaxAge, and again on the next
// delivery after Notify, which a LISTEN on BindingsChannel calls when any
// replica changes a script's source. Callers arriving while a read is under
// way share it, so a burst after a change is one query.
type Bindings struct {
	Reader BindingReader
	// MaxAge bounds how long one read is used with no change heard; zero is
	// DefaultBindingsMaxAge.
	MaxAge time.Duration

	now func() time.Time

	mu       sync.Mutex
	bySource map[string][]string
	readAt   time.Time
	// gen counts Notify calls; the map is current only when it was read at
	// the generation it is now, so a change heard during a read is not lost.
	gen, readGen uint64
	loaded       bool
	inflight     *bindingRead
}

// bindingRead is one read callers share.
type bindingRead struct {
	done     chan struct{}
	bySource map[string][]string
	err      error
}

// Notify drops the map: the next lookup reads it again. It is the
// pglisten.Notifier a LISTEN on BindingsChannel wakes.
func (b *Bindings) Notify() {
	b.mu.Lock()
	b.gen++
	b.mu.Unlock()
}

// For returns the ids of the scripts that run for source, from the map while
// it is current and from a fresh read otherwise. A source no script names is
// an empty list.
func (b *Bindings) For(ctx context.Context, source string) ([]string, error) {
	b.mu.Lock()
	if b.current() {
		ids := b.bySource[source]
		b.mu.Unlock()
		return orEmpty(ids), nil
	}
	read := b.inflight
	if read == nil {
		read = &bindingRead{done: make(chan struct{})}
		b.inflight = read
		go b.read(context.WithoutCancel(ctx), read, b.gen)
	}
	b.mu.Unlock()

	select {
	case <-read.done:
	case <-ctx.Done():
		return nil, ctx.Err() //nolint:wrapcheck // the caller's own context
	}
	if read.err != nil {
		return nil, read.err
	}
	return orEmpty(read.bySource[source]), nil
}

// read loads the map at generation gen and hands it to every waiting caller.
// It keeps the first caller's values (its trace) but not its cancellation, so
// a sender that hung up does not fail the read the others wait on; the
// reader's own bound is the timeout.
func (b *Bindings) read(ctx context.Context, r *bindingRead, gen uint64) {
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()
	r.bySource, r.err = b.Reader.EventBindings(ctx)

	b.mu.Lock()
	if r.err == nil {
		b.bySource, b.readAt, b.readGen, b.loaded = r.bySource, b.clock(), gen, true
	}
	b.inflight = nil
	b.mu.Unlock()
	close(r.done)
}

// current reports whether the map can answer: read, no change heard since,
// and younger than MaxAge. Called with mu held.
func (b *Bindings) current() bool {
	return b.loaded && b.readGen == b.gen && b.clock().Sub(b.readAt) < b.maxAge()
}

func (b *Bindings) maxAge() time.Duration {
	if b.MaxAge > 0 {
		return b.MaxAge
	}
	return DefaultBindingsMaxAge
}

func (b *Bindings) clock() time.Time {
	if b.now != nil {
		return b.now()
	}
	return time.Now()
}

func orEmpty(ids []string) []string {
	if ids == nil {
		return []string{}
	}
	return ids
}
