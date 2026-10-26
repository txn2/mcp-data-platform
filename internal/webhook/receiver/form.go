package receiver

import (
	"context"
	"errors"

	"github.com/txn2/mcp-data-platform/internal/webhook/whevent"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
)

// ErrNoFormSource is a form with no enabled source to store into.
var ErrNoFormSource = errors.New("this form has no enabled source to store its submissions in")

// ErrNotStored wraps the reason a form's submission was not stored, in the
// words a sender would have been answered with.
var ErrNotStored = errors.New("the submission could not be stored")

// notStoredError is a refusal of a form's submission as an error.
type notStoredError struct{ why refusal }

func (n notStoredError) Error() string { return ErrNotStored.Error() + ": " + n.why.reason }

func (notStoredError) Unwrap() error { return ErrNotStored }

// RetryAfter is how many seconds the submitter should wait before sending it
// again, zero when sending it again will not help.
func (n notStoredError) RetryAfter() int { return n.why.retryAfter }

// FormSource is the enabled source collecting for the form script, found on
// this replica's served sources and, when it is not among them, after a
// re-read of the sources, as a request naming a source this replica does not
// serve yet is (#2108).
func (r *Receiver) FormSource(ctx context.Context, scriptID string) (whsource.Source, bool) {
	if s, ok := r.servedFormSource(scriptID); ok {
		return s, true
	}
	read := r.joinMissRead()
	select {
	case <-read.done:
	case <-ctx.Done():
		return whsource.Source{}, false
	}
	return r.servedFormSource(scriptID)
}

func (r *Receiver) servedFormSource(scriptID string) (whsource.Source, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, s := range r.sources {
		if s.Enabled && s.IsForm() && s.Config.Form != nil && s.Config.Form.ScriptID == scriptID {
			return s.Source, true
		}
	}
	return whsource.Source{}, false
}

// Deliver stores one form submission's event in the form's source and
// returns once it is durable -- synced to the spool, or written to object
// storage -- the guarantee a 202 from /hooks/ carries (#2178). A refusal is
// ErrNotStored, counted and kept on the source's page as a sender's is.
func (r *Receiver) Deliver(ctx context.Context, src whsource.Source, event whevent.Event, body []byte) error {
	if !src.IsForm() {
		return ErrNoFormSource
	}
	d := delivery{source: src.Name, started: r.now()}
	event.Replica = r.cfg.Replica
	d.identify(body, []whevent.Event{event}, src.Config.Window())
	st, why, gone := r.store(ctx, src, d)
	switch {
	case gone:
		return ctx.Err() //nolint:wrapcheck // the submitter left; the write goes on and logs how it ended
	case why != nil:
		kept := why.reason
		if why.detail != "" {
			kept += ": " + why.detail
		}
		r.count(d.source, why.outcome, why.status)
		r.reject(d, why.outcome, kept)
		r.logRefused(d, why.outcome, kept, st)
		return notStoredError{why: *why}
	}
	return nil
}

// postedTo reports whether a source answers /hooks/: a form's source is
// written by its form alone.
func postedTo(s whsource.Source) bool { return !s.IsForm() }
