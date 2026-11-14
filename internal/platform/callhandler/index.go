package callhandler

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/txn2/mcp-data-platform/internal/logsan"
)

// Channel is the NOTIFY channel migration 000248's trigger announces a
// changed handler on.
const Channel = "call_handlers"

// DefaultMaxAge is the longest the Index answers from one read with no change
// heard: a NOTIFY sent while a replica's LISTEN connection was down is lost,
// and this bounds how long it can miss a changed handler.
const DefaultMaxAge = 10 * time.Second

// refreshTimeout bounds one read of the enabled handlers.
const refreshTimeout = 10 * time.Second

// Reader reads every enabled handler (Store).
type Reader interface {
	Enabled(ctx context.Context) ([]Handler, error)
}

// Index is every enabled handler, kept in memory so a call no handler
// matches costs the match and nothing else. It never makes a call wait for a
// read once it has one: a stale snapshot answers while a fresh one is read in
// the background, after a change is heard (Notify) or the snapshot ages past
// MaxAge.
type Index struct {
	Reader Reader
	MaxAge time.Duration

	snap atomic.Pointer[snapshot]
	gen  atomic.Uint64

	mu      sync.Mutex
	reading bool
	// lastTry is when the last read started, so a store that is down is
	// asked at most once per retryEvery rather than on every call.
	lastTry time.Time
	now     func() time.Time
}

// retryEvery is the least time between two reads.
const retryEvery = time.Second

// snapshot is one read of the enabled handlers, with when and at which change
// generation it was read.
type snapshot struct {
	handlers []Handler
	readAt   time.Time
	gen      uint64
}

// Notify marks the snapshot stale: the next lookup reads it again in the
// background. It is the pglisten.Notifier a LISTEN on Channel wakes.
func (x *Index) Notify() { x.gen.Add(1) }

// Load reads the handlers once, before the first call, so a replica that
// just started does not answer its first calls as if there were none.
func (x *Index) Load(ctx context.Context) error {
	list, err := x.Reader.Enabled(ctx)
	if err != nil {
		return err //nolint:wrapcheck // the store's own words
	}
	x.snap.Store(&snapshot{handlers: list, readAt: x.clock(), gen: x.gen.Load()})
	return nil
}

// Match is every enabled handler of kind that applies to c, in the order they
// run. A call nothing applies to gets nil.
func (x *Index) Match(c *Call, kind string) []Handler {
	s := x.current()
	if s == nil {
		return nil
	}
	var out []Handler
	for i := range s.handlers {
		h := &s.handlers[i]
		if h.Kind == kind && h.Matches(c) {
			out = append(out, *h)
		}
	}
	return out
}

// On is every enabled handler on connection, whatever it matches: what the
// test panel names as not applying to a call it does not match.
func (x *Index) On(connection string) []Handler {
	s := x.current()
	if s == nil {
		return nil
	}
	var out []Handler
	for i := range s.handlers {
		if ConnectionMatches(s.handlers[i].Connection, connection) {
			out = append(out, s.handlers[i])
		}
	}
	return out
}

// Any reports whether any enabled handler could apply to a call on
// connection, whatever its operation and caller: the first, cheapest check.
func (x *Index) Any(connection string) bool {
	s := x.current()
	if s == nil {
		return false
	}
	for i := range s.handlers {
		if ConnectionMatches(s.handlers[i].Connection, connection) {
			return true
		}
	}
	return false
}

// changeWait bounds how long a call waits for the read a heard change
// starts.
const changeWait = 2 * time.Second

// current is the snapshot to answer from. After a change is heard, a call
// waits for the read it starts, at most changeWait, so a handler added or
// changed applies to the next call; a snapshot that has only aged answers
// while a fresh one is read in the background.
func (x *Index) current() *snapshot {
	s := x.snap.Load()
	switch {
	case s != nil && s.gen != x.gen.Load():
		return x.fresh(s)
	case s == nil || x.clock().Sub(s.readAt) >= x.maxAge():
		x.refresh()
	}
	return s
}

// fresh reads the handlers now, bounded by changeWait, answering with stale
// when the read fails or takes longer.
func (x *Index) fresh(stale *snapshot) *snapshot {
	gen := x.gen.Load()
	ctx, cancel := context.WithTimeout(context.Background(), changeWait)
	defer cancel()
	list, err := x.Reader.Enabled(ctx)
	if err != nil {
		x.refresh()
		return stale
	}
	s := &snapshot{handlers: list, readAt: x.clock(), gen: gen}
	x.snap.Store(s)
	return s
}

// refresh reads the handlers in the background unless a read is under way.
func (x *Index) refresh() {
	x.mu.Lock()
	now := x.clock()
	if x.reading || x.Reader == nil || now.Sub(x.lastTry) < retryEvery {
		x.mu.Unlock()
		return
	}
	x.reading, x.lastTry = true, now
	x.mu.Unlock()
	gen := x.gen.Load()
	go func() {
		defer func() {
			x.mu.Lock()
			x.reading = false
			x.mu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), refreshTimeout)
		defer cancel()
		list, err := x.Reader.Enabled(ctx)
		if err != nil {
			slog.Warn("call handlers: reading the enabled handlers failed; the last read keeps answering",
				"error", logsan.SanitizeForLog(err.Error()))
			return
		}
		x.snap.Store(&snapshot{handlers: list, readAt: x.clock(), gen: gen})
	}()
}

func (x *Index) maxAge() time.Duration {
	if x.MaxAge > 0 {
		return x.MaxAge
	}
	return DefaultMaxAge
}

func (x *Index) clock() time.Time {
	if x.now != nil {
		return x.now()
	}
	return time.Now()
}
