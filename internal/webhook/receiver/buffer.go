package receiver

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/txn2/mcp-data-platform/internal/webhook/whevent"
	"github.com/txn2/mcp-data-platform/internal/webhook/whlayout"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
)

// segmentContentType is the media type a raw segment is written with.
const segmentContentType = "application/gzip"

// errBufferFull means admitting a request would take the source past its
// buffer limit.
var errBufferFull = errors.New("the source's buffer is full")

// errClosed means the buffer's writer has stopped.
var errClosed = errors.New("the receiver is shutting down")

// batch is one request's events and the channel its answer arrives on.
type batch struct {
	events []whevent.Event
	bytes  int64
	done   chan error
}

// buffer holds one source's events until they are written. Writes are one at
// a time: while one is under way the next request's events wait in pending,
// and both count against the limit, so a stalled object store stops admitting
// events at the limit rather than growing without bound.
type buffer struct {
	source string
	r      *Receiver

	mu            sync.Mutex
	pending       []*batch
	pendingEvents int
	pendingBytes  int64
	inflight      int
	oldest        time.Time
	// closed is set once the writer has stopped. A buffer that is closed
	// admits nothing, because nothing would write it.
	closed bool

	kick chan struct{}
}

func newBuffer(r *Receiver, source string) *buffer {
	return &buffer{source: source, r: r, kick: make(chan struct{}, 1)}
}

// admit queues events for the next segment, or refuses them when the source
// holds limit events already.
func (b *buffer) admit(events []whevent.Event, limit int) (*batch, error) {
	var size int64
	for _, e := range events {
		size += int64(len(e.Payload))
	}
	bt := &batch{events: events, bytes: size, done: make(chan error, 1)}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil, errClosed
	}
	if b.pendingEvents+b.inflight+len(events) > limit {
		b.mu.Unlock()
		return nil, errBufferFull
	}
	if len(b.pending) == 0 {
		b.oldest = b.r.now()
	}
	b.pending = append(b.pending, bt)
	b.pendingEvents += len(events)
	b.pendingBytes += size
	held := b.pendingEvents + b.inflight
	b.mu.Unlock()
	b.r.metricBuffer(b.source, held)
	select {
	case b.kick <- struct{}{}:
	default:
	}
	return bt, nil
}

// run writes segments until ctx ends, then writes what is still pending so
// every waiting request is answered.
func (b *buffer) run(ctx context.Context) {
	defer b.r.wg.Done()
	for {
		wait, ready, empty := b.due()
		switch {
		case ready:
			b.flush()
			continue
		case empty:
			wait = time.Hour
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			b.drain()
			return
		case <-b.kick:
		case <-timer.C:
		}
		timer.Stop()
	}
}

// drain writes what is pending until nothing is, then closes the buffer in
// the same critical section that found it empty, so no request is admitted
// after the last write.
func (b *buffer) drain() {
	for {
		b.mu.Lock()
		if len(b.pending) == 0 {
			b.closed = true
			b.mu.Unlock()
			return
		}
		b.mu.Unlock()
		b.flush()
	}
}

// due reports how long until the pending events must be written, and whether
// they must be written now.
func (b *buffer) due() (wait time.Duration, ready, empty bool) {
	cfg, ok := b.r.sourceConfig(b.source)
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.pending) == 0 {
		return 0, false, true
	}
	if !ok {
		return 0, true, false
	}
	if b.pendingEvents >= cfg.FlushMaxEvents || b.pendingBytes >= cfg.FlushMaxBytes {
		return 0, true, false
	}
	wait = cfg.FlushInterval() - b.r.now().Sub(b.oldest)
	if wait <= 0 {
		return 0, true, false
	}
	return wait, false, false
}

// flush takes everything pending, writes one segment per window its events
// fall in, and answers every request in it.
func (b *buffer) flush() {
	b.mu.Lock()
	taken := b.pending
	b.pending = nil
	b.inflight += b.pendingEvents
	count := b.pendingEvents
	b.pendingEvents, b.pendingBytes = 0, 0
	b.mu.Unlock()
	if len(taken) == 0 {
		return
	}

	err := b.write(taken)
	for _, bt := range taken {
		bt.done <- err
	}

	b.mu.Lock()
	b.inflight -= count
	held := b.pendingEvents + b.inflight
	b.mu.Unlock()
	b.r.metricBuffer(b.source, held)
}

// write renders the batches' events as segments, one per window of the
// source's compact_every length, and records each window. Any failure fails
// every batch: a request is answered 202 only when all of its events are in
// object storage.
func (b *buffer) write(batches []*batch) error {
	length := b.r.window(b.source)
	byWindow := map[time.Time][]whevent.Event{}
	for _, bt := range batches {
		for _, e := range bt.events {
			w := e.Window(length)
			byWindow[w] = append(byWindow[w], e)
		}
	}
	starts := make([]time.Time, 0, len(byWindow))
	for w := range byWindow {
		starts = append(starts, w)
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i].Before(starts[j]) })

	ctx, cancel := context.WithTimeout(context.Background(), b.r.cfg.WriteTimeout)
	defer cancel()
	for _, w := range starts {
		if err := b.writeWindow(ctx, w, length, byWindow[w]); err != nil {
			return err
		}
	}
	return nil
}

// writeWindow writes one segment and records it against its window.
//
// The window is recorded before the write as well as after it. Before, so a
// window whose segment is written always has a row, and is compacted and
// expired like any other even if the database is unreachable when the write
// finishes. After, so a compaction that listed the window's segments while
// this one was being written is followed by another that includes it.
func (b *buffer) writeWindow(ctx context.Context, start time.Time, length time.Duration, events []whevent.Event) error {
	data, err := whevent.EncodeSegment(events, b.r.now())
	if err != nil {
		return fmt.Errorf("encoding the segment: %w", err)
	}
	if err := b.r.cfg.Recorder.MarkSegment(ctx, b.source, start, length); err != nil {
		return fmt.Errorf("recording segment: %w", err)
	}
	key := whlayout.SegmentKey(b.source, start, b.r.writer, b.r.seq.Add(1))
	if err := b.r.cfg.Objects.PutObject(ctx, b.r.cfg.Bucket, key, data, segmentContentType); err != nil {
		return fmt.Errorf("writing segment: %w", err)
	}
	b.r.metricSegment(b.source)
	if err := b.r.cfg.Recorder.MarkSegment(ctx, b.source, start, length); err != nil {
		return fmt.Errorf("recording segment: %w", err)
	}
	b.r.ensureRawWindow(ctx, b.source, start)
	return nil
}

// window is the length of a source's compaction window at the moment its
// segment is written. A source removed while its events were buffered keeps
// the default, so the events already acknowledged to nobody are still filed.
func (r *Receiver) window(name string) time.Duration {
	cfg, _ := r.sourceConfig(name)
	return cfg.Window()
}

// sourceConfig returns a source's current settings, or false when it is no
// longer served.
func (r *Receiver) sourceConfig(name string) (whsource.Config, bool) {
	s, ok := r.lookup(name)
	return s.Config, ok
}
