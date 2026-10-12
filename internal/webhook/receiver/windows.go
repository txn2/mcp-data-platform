package receiver

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"time"

	"github.com/txn2/mcp-data-platform/internal/bgloop"
	"github.com/txn2/mcp-data-platform/internal/logsan"
)

// DefaultRegisterRetry is how often a window whose raw partition could not be
// registered is tried again.
const DefaultRegisterRetry = 15 * time.Second

// registerTimeout bounds one registration of a window's raw partition.
const registerTimeout = time.Minute

// maxRegisterAhead is the most ahead of a window's start the registration
// loop registers it for a source writing into the window before it. A short
// window is registered half its length ahead.
const maxRegisterAhead = time.Minute

// windowsKept is how long this process remembers a window it recorded or
// registered. Past it a late segment records and registers its window again,
// which both allow.
const windowsKept = 48 * time.Hour

// rawWindow is a window whose raw partition is owed a registration.
type rawWindow struct {
	source string
	start  time.Time
}

// windowKey names one window of one source.
func windowKey(source string, start time.Time) string {
	return source + "@" + start.UTC().Format(time.RFC3339)
}

// needsMark reports whether this process has yet to record the window before
// writing a segment into it. The first segment records the window before its
// write, so a window whose segment is written has a row even when the
// database is unreachable once the write finishes; after that the record
// made after each write is enough.
func (r *Receiver) needsMark(source string, start time.Time) bool {
	_, done := r.marked.Load(windowKey(source, start))
	return !done
}

// markedWindow records that this process recorded the window before a write.
func (r *Receiver) markedWindow(source string, start time.Time) {
	r.marked.Store(windowKey(source, start), start)
}

// queueRawWindow asks the registration loop to register a window's raw
// partition, unless this process has, and remembers it as the source's
// latest window so the loop registers the one after it ahead of time. It
// never waits: registration runs after the segment's requests are answered,
// so a slow query engine at a window boundary never delays an
// acknowledgement.
func (r *Receiver) queueRawWindow(source string, start time.Time) {
	if r.cfg.RawWindows == nil {
		return
	}
	r.regMu.Lock()
	if cur, ok := r.latest[source]; !ok || start.After(cur) {
		r.latest[source] = start
	}
	r.regMu.Unlock()
	r.queue(source, start)
}

// queue adds a window to those owed a registration and wakes the loop,
// unless this process has registered it.
func (r *Receiver) queue(source string, start time.Time) {
	key := windowKey(source, start)
	if _, done := r.rawWindows.Load(key); done {
		return
	}
	r.regMu.Lock()
	r.regPending[key] = rawWindow{source: source, start: start}
	r.regMu.Unlock()
	select {
	case r.regKick <- struct{}{}:
	default:
	}
}

// registerLoop registers queued windows when a write queues one, and retries
// those that failed every RegisterRetry, until Stop. A window still owed a
// registration at Stop is left to the compactor's sync.
func (r *Receiver) registerLoop() {
	defer r.wg.Done()
	bgloop.Run(r.ctx, bgloop.Loop{
		Name: bgloop.NameWebhookRawWindows, Every: r.cfg.RegisterRetry, Wake: r.regKick,
		Body: r.registerPending,
	})
}

// registerPending registers every queued window, with the next window of
// each source that wrote into the current one once it is near. A window whose
// source is no longer served is dropped; one that fails stays queued for the
// next pass.
func (r *Receiver) registerPending(ctx context.Context) error {
	r.forgetOld()
	r.queueNext()
	r.regMu.Lock()
	queued := make(map[string]rawWindow, len(r.regPending))
	maps.Copy(queued, r.regPending)
	r.regMu.Unlock()
	var errs []error
	for key, w := range queued {
		err := r.registerOne(ctx, w)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		r.regMu.Lock()
		delete(r.regPending, key)
		r.regMu.Unlock()
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("webhooks: registering raw windows: %w", err)
	}
	return nil
}

// queueNext queues the window after each source's latest one when that
// window starts within the lead: a source receiving steadily has its next
// window registered before the first event lands in it, so the event is
// queryable as soon as it is acknowledged. A source whose latest window is
// not the current one has gone quiet, and its next window waits for an event.
func (r *Receiver) queueNext() {
	now := r.now()
	r.regMu.Lock()
	latest := make(map[string]time.Time, len(r.latest))
	maps.Copy(latest, r.latest)
	r.regMu.Unlock()
	for name, start := range latest {
		cfg, ok := r.sourceConfig(name)
		if !ok {
			r.regMu.Lock()
			delete(r.latest, name)
			r.regMu.Unlock()
			continue
		}
		length := cfg.Window()
		next := start.Add(length)
		if now.Before(start) || !now.Before(next) {
			continue
		}
		if next.Sub(now) <= min(maxRegisterAhead, length/2) {
			r.queue(name, next)
		}
	}
}

// registerOne registers one window's raw partition, logging a failure with
// how long it took.
func (r *Receiver) registerOne(ctx context.Context, w rawWindow) error {
	src, ok := r.lookup(w.source)
	if !ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, registerTimeout)
	defer cancel()
	began := r.now()
	if err := r.cfg.RawWindows.EnsureRawWindow(ctx, src.Source, w.start); err != nil {
		r.cfg.Logger.Warn("webhooks: registering a new window", "source", logsan.SanitizeForLog(w.source),
			"replica", logsan.SanitizeForLog(r.cfg.Replica), "window", w.start.UTC().Format(time.RFC3339),
			"elapsed_ms", r.now().Sub(began).Milliseconds(), logKeyError, logsan.SanitizeForLog(err.Error()))
		return fmt.Errorf("window %s of %s: %w", w.start.UTC().Format(time.RFC3339), w.source, err)
	}
	r.rawWindows.Store(windowKey(w.source, w.start), w.start)
	return nil
}

// forgetOld drops the windows this process recorded or registered that
// started more than windowsKept ago, so neither set grows for as long as the
// process runs.
func (r *Receiver) forgetOld() {
	cutoff := r.now().Add(-windowsKept)
	for _, m := range []interface {
		Range(func(k, v any) bool)
		Delete(k any)
	}{&r.marked, &r.rawWindows} {
		m.Range(func(k, v any) bool {
			if start, ok := v.(time.Time); ok && start.Before(cutoff) {
				m.Delete(k)
			}
			return true
		})
	}
}
