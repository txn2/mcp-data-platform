package receiver

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/webhook/whevent"
	"github.com/txn2/mcp-data-platform/internal/webhook/whstore"
)

// maxNamedEvents is how many of a refused delivery's event ids its log line
// and its rejection name.
const maxNamedEvents = 20

// delivery is one request as the receiver answers it: its source, when it
// arrived, and once its body has been read into events, the SHA-256 of the
// body and the events.
type delivery struct {
	source  string
	started time.Time
	hash    string
	events  []whevent.Event
	// window is the source's compaction window length, which files the
	// events.
	window time.Duration
}

// identify records the body's hash and the events built from it.
func (d *delivery) identify(body []byte, events []whevent.Event, window time.Duration) {
	sum := sha256.Sum256(body)
	d.hash, d.events, d.window = hex.EncodeToString(sum[:]), events, window
}

// eventIDs are the ids of the delivery's first maxNamedEvents events.
func (d delivery) eventIDs() []string {
	n := min(len(d.events), maxNamedEvents)
	if n == 0 {
		return nil
	}
	ids := make([]string, 0, n)
	for _, e := range d.events[:n] {
		ids = append(ids, e.EventID)
	}
	return ids
}

// windows are the starts of the windows the delivery's events fall in, in
// the order first seen, as RFC 3339.
func (d delivery) windows() []string {
	if d.window <= 0 {
		return nil
	}
	seen := map[time.Time]bool{}
	out := make([]string, 0, 1)
	for _, e := range d.events {
		w := e.Window(d.window)
		if !seen[w] {
			seen[w] = true
			out = append(out, w.Format(time.RFC3339))
		}
	}
	return out
}

// stages is how long a delivery's events spent in each step of their write:
// waiting in the buffer for the segment to start, recording its window, and
// writing it to object storage.
type stages struct {
	buffer, mark, put time.Duration
}

// slow is the stages as the slowest acknowledgement of a minute records them.
func (s stages) slow(at time.Time, total time.Duration) whstore.SlowAck {
	return whstore.SlowAck{
		At: at.UTC(), TotalMS: total.Milliseconds(),
		BufferMS: s.buffer.Milliseconds(), MarkMS: s.mark.Milliseconds(), PutMS: s.put.Milliseconds(),
	}
}

// attrs are the log attributes that say what a delivery was and how long it
// had been waiting: its source, this replica, the time since it arrived, the
// events it held, the windows they fall in, and the body's hash and event
// ids when it was read, with the stages when its write ran.
func (r *Receiver) attrs(d delivery, st *stages) []any {
	out := []any{
		"source", logsan.SanitizeForLog(d.source),
		"replica", logsan.SanitizeForLog(r.cfg.Replica),
		"elapsed_ms", r.now().Sub(d.started).Milliseconds(),
		"events", len(d.events),
	}
	if d.hash != "" {
		out = append(out, "windows", sanitizeAll(d.windows()),
			"request_hash", logsan.SanitizeForLog(d.hash), "event_ids", sanitizeAll(d.eventIDs()))
	}
	if st != nil {
		out = append(out, "buffer_ms", st.buffer.Milliseconds(), "mark_ms", st.mark.Milliseconds(),
			"put_ms", st.put.Milliseconds())
	}
	return out
}

// sanitizeAll is a new slice holding each of in made safe for a log line.
func sanitizeAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, logsan.SanitizeForLog(s))
	}
	return out
}

// logRefused writes the WARN line every request not answered 202 gets.
func (r *Receiver) logRefused(d delivery, outcome, reason string, st *stages) {
	attrs := append([]any{"outcome", outcome, "reason", logsan.SanitizeForLog(reason)}, r.attrs(d, st)...)
	r.cfg.Logger.Warn("webhooks: request not accepted", attrs...)
}

// senderGone records a delivery whose sender went away before its segment
// was written. The sender saw no answer, so it may retry; the write goes on,
// and a second line says how it ended once it has, which is what tells an
// operator whether these events were stored.
func (r *Receiver) senderGone(d delivery, bt *batch) {
	reason := fmt.Sprintf("the sender disconnected after %s, before its events were written",
		r.now().Sub(d.started).Round(time.Millisecond))
	r.count(d.source, OutcomeSenderGone)
	r.reject(d, OutcomeSenderGone, reason)
	r.logRefused(d, OutcomeSenderGone, reason, nil)
	go func() {
		res := <-bt.done
		attrs := append([]any{"stored", res.err == nil}, r.attrs(d, &res.stages)...)
		if res.err != nil {
			attrs = append(attrs, logKeyError, logsan.SanitizeForLog(res.err.Error()))
		}
		r.cfg.Logger.Warn("webhooks: write for a disconnected sender finished", attrs...)
	}()
}

// observeAck adds one delivery answered 202 to this minute's latency.
func (r *Receiver) observeAck(source string, total time.Duration, st stages) {
	now := r.now()
	k := latencyKey{source: source, minute: now.UTC().Truncate(time.Minute)}
	r.statsMu.Lock()
	defer r.statsMu.Unlock()
	l := r.latency[k]
	if l == nil {
		l = &whstore.Latency{Source: source, Minute: k.minute}
		r.latency[k] = l
	}
	l.Observe(st.slow(now, total))
}

// latencyKey is one source's minute of acknowledgement latency.
type latencyKey struct {
	source string
	minute time.Time
}
