package receiver

import (
	"context"
	"time"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/webhook/whstore"
)

// minuteLocked is this minute's tally, made on first use. statsMu is held.
func (r *Receiver) minuteLocked(at time.Time) *whstore.ReplicaMinute {
	minute := at.UTC().Truncate(time.Minute)
	m := r.minutes[minute]
	if m == nil {
		m = &whstore.ReplicaMinute{Replica: r.cfg.Replica, Minute: minute}
		r.minutes[minute] = m
	}
	return m
}

// tallyReceived adds a delivery's events accepted this minute.
func (r *Receiver) tallyReceived(events int) {
	r.statsMu.Lock()
	defer r.statsMu.Unlock()
	r.minuteLocked(r.now()).ReceivedEvents += int64(events)
}

// tallyWritten adds a segment written to object storage this minute: drained
// from the spool, or written from the buffer.
func (r *Receiver) tallyWritten(events, bytes int) {
	r.statsMu.Lock()
	defer r.statsMu.Unlock()
	m := r.minuteLocked(r.now())
	m.DrainedEvents += int64(events)
	m.DrainedBytes += int64(bytes)
}

// flushMinutes writes the minutes tallied since the last flush, the current
// one carrying what the spool holds now. A failed write puts them back.
func (r *Receiver) flushMinutes(ctx context.Context) error {
	r.statsMu.Lock()
	current := r.minuteLocked(r.now())
	if r.cfg.Spool != nil {
		st := r.cfg.Spool.Stats()
		current.SpoolBytes, current.SpoolEvents = st.Bytes, st.Events
		if !st.Oldest.IsZero() {
			current.OldestAgeSeconds = int64(r.now().Sub(st.Oldest).Seconds())
		}
	}
	minutes := make([]whstore.ReplicaMinute, 0, len(r.minutes))
	for _, m := range r.minutes {
		minutes = append(minutes, *m)
	}
	r.minutes = map[time.Time]*whstore.ReplicaMinute{}
	r.statsMu.Unlock()

	if err := r.cfg.Recorder.RecordReplicaMinutes(ctx, minutes); err != nil {
		r.cfg.Logger.WarnContext(ctx, "webhooks: writing receiver minutes", logKeyError, logsan.SanitizeForLog(err.Error()))
		r.statsMu.Lock()
		for _, m := range minutes {
			cur := r.minuteLocked(m.Minute)
			cur.ReceivedEvents += m.ReceivedEvents
			cur.DrainedEvents += m.DrainedEvents
			cur.DrainedBytes += m.DrainedBytes
			cur.SpoolBytes = max(cur.SpoolBytes, m.SpoolBytes)
			cur.SpoolEvents = max(cur.SpoolEvents, m.SpoolEvents)
			cur.OldestAgeSeconds = max(cur.OldestAgeSeconds, m.OldestAgeSeconds)
		}
		r.statsMu.Unlock()
		return err //nolint:wrapcheck // flushStats wraps
	}
	return nil
}
