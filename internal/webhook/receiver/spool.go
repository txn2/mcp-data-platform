package receiver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/txn2/mcp-data-platform/internal/bgloop"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/webhook/whevent"
	"github.com/txn2/mcp-data-platform/internal/webhook/whlayout"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/internal/webhook/whspool"
	"github.com/txn2/mcp-data-platform/internal/webhook/whstore"
)

// A receiver with a spool (#2121) answers 202 once a request's events are
// synced to its own disk, and a drainer per source writes them to object
// storage behind the answer, through the same windows and layout a request
// without a spool is written through. A file is drained under its own name,
// so draining it again after a crash overwrites what the first drain wrote.

// Spool is the receiver's local spool. whspool.Spool satisfies it.
type Spool interface {
	Dir() string
	Append(source string, rec whspool.Record) (took time.Duration, err error)
	Active(source string) (whspool.Stats, bool)
	Seal(source string) error
	Sealed(source string) []whspool.Segment
	Sources() []string
	Read(seg whspool.Segment) ([]whspool.Record, error)
	Remove(seg whspool.Segment) error
	Stats() whspool.Stats
}

// SpoolLimits are the bounds a spool was opened with, for its reported state.
type SpoolLimits struct {
	MaxBytes int64
	MaxAge   time.Duration
}

// OutcomeSpoolFull is a request refused because the spool is at its bound.
const OutcomeSpoolFull = "spool_full"

// retrySpoolFull is the Retry-After a request refused by a full spool gets.
const retrySpoolFull = 5

// Drain pacing after a failed drain: one second, doubling, at most a minute.
const (
	drainRetryFirst = time.Second
	drainRetryMax   = time.Minute
)

// spoolTally is how the spool's drain has gone since the receiver started.
type spoolTally struct {
	drained  atomic.Int64
	failures atomic.Int64

	mu        sync.Mutex
	lastDrain time.Time
	lastError string
}

func (t *spoolTally) drainedAt(at time.Time, events int) {
	t.drained.Add(int64(events))
	t.mu.Lock()
	t.lastDrain, t.lastError = at, ""
	t.mu.Unlock()
}

func (t *spoolTally) failed(err error) {
	t.failures.Add(1)
	t.mu.Lock()
	t.lastError = logsan.SanitizeForLog(err.Error())
	t.mu.Unlock()
}

func (t *spoolTally) last() (drainedAt time.Time, lastError string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lastDrain, t.lastError
}

// acceptSpooled appends a delivery's events to the spool and answers 202 once
// they are synced; the drain writes them to object storage later.
func (r *Receiver) acceptSpooled(ctx context.Context, w http.ResponseWriter, src whsource.Source, d delivery) {
	if len(d.events) > 0 {
		took, err := r.cfg.Spool.Append(src.Name, whspool.Record{Hash: d.hash, Events: d.events})
		switch {
		case errors.Is(err, whspool.ErrFull):
			r.refuse(w, d, refusal{
				outcome: OutcomeSpoolFull, reason: "the receiver's spool is full", status: http.StatusServiceUnavailable,
				retryAfter: retrySpoolFull, detail: err.Error(),
			}, nil)
			return
		case errors.Is(err, whspool.ErrClosed):
			r.refuse(w, d, refusal{outcome: OutcomeWriteFailed, reason: "the platform is shutting down", status: http.StatusServiceUnavailable, retryAfter: retryStopping}, nil)
			return
		case err != nil:
			r.refuse(w, d, refusal{
				outcome: OutcomeWriteFailed, reason: "the events could not be written to the receiver's spool",
				status: http.StatusServiceUnavailable, retryAfter: retryWriteFailed, detail: err.Error(),
			}, nil)
			return
		}
		r.observeAccepted(ctx, src.Name, d, stages{put: took})
		if dr := r.drainerFor(src.Name); dr != nil {
			dr.wake()
		}
	} else {
		r.observeAccepted(ctx, src.Name, d, stages{})
	}
	writeJSON(w, http.StatusAccepted, `{"accepted":`+strconv.Itoa(len(d.events))+`}`)
}

// observeAccepted counts and times a delivery answered 202.
func (r *Receiver) observeAccepted(ctx context.Context, source string, d delivery, st stages) {
	total := r.now().Sub(d.started)
	r.count(source, OutcomeAccepted)
	r.observeAck(source, total, st)
	if r.cfg.Metrics != nil {
		r.cfg.Metrics.WebhookEvents(ctx, source, len(d.events))
		r.cfg.Metrics.WebhookAck(ctx, source, total)
	}
}

// drainer writes one source's spooled events to object storage.
type drainer struct {
	source string
	r      *Receiver
	kick   chan struct{}
	// failures is how many drains in a row have failed, which paces the
	// next.
	failures int
}

// drainerFor returns a source's drainer, starting it the first time, and nil
// once the receiver is stopping.
func (r *Receiver) drainerFor(name string) *drainer {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped || r.ctx == nil {
		return nil
	}
	if d := r.drainers[name]; d != nil {
		return d
	}
	d := &drainer{source: name, r: r, kick: make(chan struct{}, 1)}
	r.drainers[name] = d
	r.wg.Add(1)
	go d.run(r.ctx)
	return d
}

func (d *drainer) wake() {
	select {
	case d.kick <- struct{}{}:
	default:
	}
}

// run drains until ctx ends, then seals what is still being appended to and
// drains what it can before the receiver's write timeout; anything left is
// drained by the next process to open the spool.
func (d *drainer) run(ctx context.Context) {
	defer d.r.wg.Done()
	bgloop.Run(ctx, bgloop.Loop{
		Name: bgloop.NameWebhookSpoolDrain, Immediate: true, Wake: d.kick, SpanPerUnit: true,
		Next:  d.nextWait,
		Body:  d.pass,
		Final: func(context.Context) { d.final() },
	})
}

// nextWait is how long the drainer waits before looking again: backing off
// after a failed drain, not at all when a sealed file waits, until the active
// file is due otherwise, and an hour when the source spools nothing (an
// append wakes it sooner).
func (d *drainer) nextWait() time.Duration {
	if d.failures > 0 {
		return min(drainRetryFirst<<min(d.failures-1, 6), drainRetryMax)
	}
	if len(d.r.cfg.Spool.Sealed(d.source)) > 0 {
		return 0
	}
	wait, ready, empty := d.due()
	switch {
	case ready:
		return 0
	case empty:
		return time.Hour
	}
	return wait
}

// due reports how long until the active file must be sealed, by the same
// rules a buffer is flushed by, and whether it must be sealed now.
func (d *drainer) due() (wait time.Duration, ready, empty bool) {
	af, ok := d.r.cfg.Spool.Active(d.source)
	if !ok || af.Events == 0 {
		return 0, false, true
	}
	cfg, served := d.r.sourceConfig(d.source)
	if !served {
		return 0, true, false
	}
	if af.Events >= int64(cfg.FlushMaxEvents) || af.Bytes >= cfg.FlushMaxBytes {
		return 0, true, false
	}
	wait = cfg.FlushInterval() - d.r.now().Sub(af.Oldest)
	if wait <= 0 {
		return 0, true, false
	}
	return wait, false, false
}

// pass seals the active file when it is due and drains every sealed file,
// oldest first, stopping at the first that fails.
func (d *drainer) pass(ctx context.Context) error {
	if _, ready, _ := d.due(); ready {
		if err := d.r.cfg.Spool.Seal(d.source); err != nil {
			return d.fail(err)
		}
	}
	for _, seg := range d.r.cfg.Spool.Sealed(d.source) {
		err := bgloop.Unit(ctx, bgloop.NameWebhookSpoolSegment, func(ctx context.Context) error { return d.drain(ctx, seg) })
		if err != nil {
			return d.fail(err)
		}
	}
	d.failures = 0
	return nil
}

func (d *drainer) fail(err error) error {
	d.failures++
	d.r.spool.failed(err)
	if d.r.cfg.Metrics != nil {
		d.r.cfg.Metrics.WebhookSpoolDrainFailed(context.Background(), d.source)
	}
	d.r.cfg.Logger.Warn("webhooks: draining the spool failed; it is retried",
		"source", logsan.SanitizeForLog(d.source), "replica", logsan.SanitizeForLog(d.r.cfg.Replica),
		"failures", d.failures, logKeyError, logsan.SanitizeForLog(err.Error()))
	return err
}

// drain writes one sealed file's events to object storage, each window's
// under a key named for the file, then removes the file.
func (d *drainer) drain(ctx context.Context, seg whspool.Segment) error {
	recs, err := d.r.cfg.Spool.Read(seg)
	if err != nil {
		return err //nolint:wrapcheck // names the file
	}
	var events []whevent.Event
	for _, rec := range recs {
		events = append(events, rec.Events...)
	}
	if d.r.sourceDeleted(ctx, d.source) {
		return d.discard(seg, len(events))
	}
	wctx, cancel := context.WithTimeout(ctx, d.r.cfg.WriteTimeout)
	defer cancel()
	if _, err := d.r.writeEvents(wctx, d.source, events, func(start time.Time) string {
		return whlayout.SegmentKey(d.source, start, seg.Writer, seg.Seq)
	}); err != nil {
		return fmt.Errorf("draining %s: %w", whlayout.SegmentKey(d.source, seg.Oldest, seg.Writer, seg.Seq), err)
	}
	if err := d.r.cfg.Spool.Remove(seg); err != nil {
		return err //nolint:wrapcheck // names the file
	}
	d.r.spool.drainedAt(d.r.now(), len(events))
	if d.r.cfg.Metrics != nil {
		d.r.cfg.Metrics.WebhookSpoolDrained(context.Background(), d.source, len(events))
	}
	return nil
}

// sourceDeleted reports whether a source no longer exists in the store. A
// source deleted while its events waited here has had its tables and objects
// removed with it, and the window its events would be recorded against can no
// longer be, so draining it would fail on every attempt and hold the spool
// until it refused every source. It answers false whenever it cannot be sure:
// a source this replica serves, or a store it could not read.
func (r *Receiver) sourceDeleted(ctx context.Context, name string) bool {
	r.mu.RLock()
	_, known := r.sources[name]
	r.mu.RUnlock()
	if known {
		return false
	}
	list, err := r.cfg.Sources.List(ctx)
	if err != nil {
		return false
	}
	for _, s := range list {
		if s.Name == name {
			return false
		}
	}
	return true
}

// discard removes a spooled file of a deleted source, saying so: its events
// were acknowledged, and an administrator deleted the source they belong to.
func (d *drainer) discard(seg whspool.Segment, events int) error {
	if err := d.r.cfg.Spool.Remove(seg); err != nil {
		return err //nolint:wrapcheck // names the file
	}
	d.r.cfg.Logger.Warn("webhooks: spooled events of a deleted source discarded",
		"source", logsan.SanitizeForLog(d.source), "replica", logsan.SanitizeForLog(d.r.cfg.Replica), "events", events)
	return nil
}

// final seals the active file and drains what it can before the write
// timeout passes, so a stop with the object store answering leaves nothing
// behind; a stop without it leaves the files for the next start.
func (d *drainer) final() {
	if err := d.r.cfg.Spool.Seal(d.source); err != nil {
		d.r.spool.failed(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), d.r.cfg.WriteTimeout)
	defer cancel()
	for _, seg := range d.r.cfg.Spool.Sealed(d.source) {
		if ctx.Err() != nil {
			return
		}
		if err := d.drain(ctx, seg); err != nil {
			d.r.spool.failed(err)
			return
		}
	}
}

// startDrains starts a drainer for every source the spool holds files for:
// what an earlier process acknowledged and did not drain.
func (r *Receiver) startDrains() {
	if r.cfg.Spool == nil {
		return
	}
	for _, source := range r.cfg.Spool.Sources() {
		if d := r.drainerFor(source); d != nil {
			d.wake()
		}
	}
}

// spoolState is what the spool holds and how its drain is going, as the
// stats flush records it.
func (r *Receiver) spoolState() whstore.SpoolState {
	st := r.cfg.Spool.Stats()
	lastDrain, lastErr := r.spool.last()
	out := whstore.SpoolState{
		Replica: r.cfg.Replica, Dir: r.cfg.Spool.Dir(), Bytes: st.Bytes, Events: st.Events, Files: st.Files,
		MaxBytes: r.cfg.SpoolLimits.MaxBytes, MaxAge: r.cfg.SpoolLimits.MaxAge,
		Drained: r.spool.drained.Load(), DrainFailures: r.spool.failures.Load(), LastError: lastErr,
		StartedAt: r.started, UpdatedAt: r.now(),
	}
	if !st.Oldest.IsZero() {
		oldest := st.Oldest
		out.Oldest = &oldest
	}
	if !lastDrain.IsZero() {
		out.LastDrainAt = &lastDrain
	}
	return out
}

// flushSpool writes the spool's state and reports it as metrics.
func (r *Receiver) flushSpool(ctx context.Context) error {
	if r.cfg.Spool == nil {
		return nil
	}
	st := r.spoolState()
	if r.cfg.Metrics != nil {
		var age time.Duration
		if st.Oldest != nil {
			age = r.now().Sub(*st.Oldest)
		}
		r.cfg.Metrics.WebhookSpool(ctx, st.Bytes, st.Events, st.Files, age)
	}
	if err := r.cfg.Recorder.RecordSpool(ctx, st); err != nil {
		r.cfg.Logger.WarnContext(ctx, "webhooks: writing the spool state", logKeyError, logsan.SanitizeForLog(err.Error()))
		return err //nolint:wrapcheck // flushStats wraps
	}
	return nil
}
