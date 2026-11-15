package sitetraffic

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/txn2/mcp-data-platform/internal/logsan"
)

// MaxHeld bounds the minutes a replica holds while the database will not take
// a flush: past it the oldest is dropped and counted, so memory stays bounded
// and a slow or unavailable database never reaches a visitor.
const MaxHeld = 60

// FlushEvery is how often a replica adds its counts into the shared rows.
const FlushEvery = time.Minute

// Adder writes a flush. *Store satisfies it.
type Adder interface {
	Add(ctx context.Context, rows []Row, replicas []ReplicaRow) (unknown bool, err error)
}

// Flusher adds a replica's counts into the shared rows once a minute. A minute
// is never counted twice: a flush that fails before it commits is held and
// tried again with the next minute, and one whose commit outcome is unknown is
// dropped, not retried, and counted.
type Flusher struct {
	Tally    *Tally
	Store    Adder
	Instance string
	// Enabled is sites.traffic.enabled as configured now; nil is on.
	Enabled func() bool
	Now     func() time.Time

	mu   sync.Mutex
	held []held

	// Dropped counts minutes dropped past MaxHeld, Unknown flushes whose
	// commit outcome could not be known, and Overflow requests past MaxKeys.
	Dropped, Unknown, Overflow atomic.Int64
}

// held is one minute's counts not yet written.
type held struct {
	at time.Time
	d  Drained
}

func (f *Flusher) now() time.Time {
	if f.Now != nil {
		return f.Now()
	}
	return time.Now()
}

// Flush takes the counts since the last flush and writes every minute held.
func (f *Flusher) Flush(ctx context.Context) error {
	on := f.Enabled == nil || f.Enabled()
	f.Tally.SetEnabled(on)
	d := f.Tally.Drain()
	f.Overflow.Add(d.Dropped)
	f.mu.Lock()
	defer f.mu.Unlock()
	if !on {
		f.held = nil
		return nil
	}
	if len(d.Keys) > 0 || d.Replica.Total() > 0 {
		f.held = append(f.held, held{at: f.now().UTC().Truncate(time.Minute), d: d})
	}
	if n := len(f.held) - MaxHeld; n > 0 {
		f.held = f.held[n:]
		f.Dropped.Add(int64(n))
		slog.WarnContext(ctx, "sites: traffic minutes dropped while the database would not take them", "minutes", n)
	}
	if len(f.held) == 0 {
		return nil
	}
	rows, replicas := f.rows()
	unknown, err := f.Store.Add(ctx, rows, replicas)
	switch {
	case err == nil:
		f.held = nil
	case unknown:
		f.held = nil
		f.Unknown.Add(1)
		slog.WarnContext(ctx, "sites: a traffic flush's outcome is unknown; its counts are dropped rather than counted twice",
			"error", logsan.SanitizeForLog(err.Error()))
	}
	if err != nil {
		return fmt.Errorf("flushing site traffic: %w", err)
	}
	return nil
}

// rowKey is one shared row.
type rowKey struct {
	grain  string
	bucket time.Time
	key    Key
}

// replicaKey is one replica row.
type replicaKey struct {
	grain  string
	bucket time.Time
}

// rows is every held minute as the rows it adds into, each row once: two
// minutes of one hour add into one hour row, which one statement may not touch
// twice.
func (f *Flusher) rows() ([]Row, []ReplicaRow) {
	sites := map[rowKey]Counts{}
	reps := map[replicaKey]Counts{}
	for _, h := range f.held {
		hour := h.at.Truncate(time.Hour)
		for k, c := range h.d.Keys {
			for _, rk := range []rowKey{{GrainMinute, h.at, k}, {GrainHour, hour, k}} {
				sum := sites[rk]
				sum.Add(c)
				sites[rk] = sum
			}
		}
		if h.d.Replica.Total() > 0 {
			for _, rk := range []replicaKey{{GrainMinute, h.at}, {GrainHour, hour}} {
				sum := reps[rk]
				sum.Add(h.d.Replica)
				reps[rk] = sum
			}
		}
	}
	rows := make([]Row, 0, len(sites))
	for k, c := range sites {
		rows = append(rows, Row{Grain: k.grain, Bucket: k.bucket, Key: k.key, Counts: c})
	}
	replicas := make([]ReplicaRow, 0, len(reps))
	for k, c := range reps {
		replicas = append(replicas, ReplicaRow{Grain: k.grain, Bucket: k.bucket, Instance: f.Instance, Counts: c})
	}
	return rows, replicas
}

// Held is how many minutes wait to be written.
func (f *Flusher) Held() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.held)
}
