package receiver

import (
	"context"
	"time"

	"github.com/txn2/mcp-data-platform/internal/logsan"
)

// DefaultMissRefresh is the least time between two re-reads of the sources
// made for requests naming a source this replica does not serve. A caller
// inventing names costs the database at most one read per interval.
const DefaultMissRefresh = time.Second

// missReadTimeout bounds one re-read of the sources made for a miss.
const missReadTimeout = 10 * time.Second

// missRead is one re-read of the sources that the requests naming a source
// this replica does not serve wait on. done is closed when it has finished.
type missRead struct {
	done chan struct{}
}

// lookupFresh is lookup, and for a name not served, lookup again after a
// re-read of the sources that started after the request arrived (#2108). A
// source created on another replica is therefore served here from its first
// request, whatever other names were asked for in the same second.
//
// The re-reads are shared and spaced: every miss that arrives before the
// next re-read starts waits for that one, and a re-read starts no sooner than
// MissRefresh after the last, so a burst of invented names is one read per
// interval and a wait of at most an interval for each of them.
func (r *Receiver) lookupFresh(ctx context.Context, name string) (served, bool) {
	if s, ok := r.lookup(name); ok {
		return s, true
	}
	read := r.joinMissRead()
	select {
	case <-read.done:
	case <-ctx.Done():
		return served{}, false
	}
	return r.lookup(name)
}

// joinMissRead returns the re-read not yet started, starting one when there
// is none.
func (r *Receiver) joinMissRead() *missRead {
	r.missMu.Lock()
	defer r.missMu.Unlock()
	if r.nextMiss != nil {
		return r.nextMiss
	}
	read := &missRead{done: make(chan struct{})}
	r.nextMiss = read
	go r.runMissRead(read, r.cfg.MissRefresh-r.now().Sub(r.lastMiss))
	return read
}

// runMissRead waits out the interval since the last re-read, then re-reads
// the sources and releases the requests waiting on it. Once it starts, a new
// miss waits for the next re-read, since this one may have read the sources
// before that request's source was created.
func (r *Receiver) runMissRead(read *missRead, wait time.Duration) {
	defer close(read.done)
	if wait > 0 {
		timer := time.NewTimer(wait)
		<-timer.C
	}
	r.missMu.Lock()
	r.nextMiss = nil
	r.lastMiss = r.now()
	r.missMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), missReadTimeout)
	defer cancel()
	if err := r.refresh(ctx); err != nil {
		r.cfg.Logger.Warn("webhooks: refreshing sources", logKeyError, logsan.SanitizeForLog(err.Error()))
	}
}
