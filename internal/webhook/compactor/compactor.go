// Package compactor turns each window of a webhook source's raw segments into
// one Parquet file (#1870), and applies the source's retention.
//
// A window is compacted once it has ended and a grace period for segments
// still being written has passed. The compactor reads every raw segment of the
// window, and the window's previous Parquet file when there is one, removes
// duplicate event ids keeping the earliest received, writes the result as the
// window's managed resource, and registers the window's partition at that
// resource's directory. A segment written for a window after it was compacted
// makes it owed another compaction, which starts again from everything the
// window holds, so rewriting a window is idempotent.
//
// The worker claims windows under a Postgres lease, so any number of replicas
// run it and each window is compacted by one of them at a time.
package compactor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/webhook/whevent"
	"github.com/txn2/mcp-data-platform/internal/webhook/whlayout"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/internal/webhook/whstore"
)

// Results a compaction is counted under.
const (
	ResultCompacted = "compacted"
	ResultFailed    = "failed"
)

// Defaults for the worker's pacing.
const (
	DefaultPoll           = 30 * time.Second
	DefaultLease          = 10 * time.Minute
	DefaultBatch          = 4
	DefaultGrace          = 2 * time.Minute
	DefaultRetryBackoff   = time.Minute
	DefaultRetentionEvery = 10 * time.Minute
	// countsKept is how long per-minute request counts are kept: the source's
	// page reads the last day of them.
	countsKept = 48 * time.Hour
	// maxBackoff caps how long a failing window is held back.
	maxBackoff = time.Hour
)

// Tuning paces the worker. A zero field takes its default.
type Tuning struct {
	Poll           time.Duration
	Lease          time.Duration
	Batch          int
	Grace          time.Duration
	RetryBackoff   time.Duration
	RetentionEvery time.Duration
}

func (t Tuning) withDefaults() Tuning {
	if t.Poll <= 0 {
		t.Poll = DefaultPoll
	}
	if t.Lease <= 0 {
		t.Lease = DefaultLease
	}
	if t.Batch <= 0 {
		t.Batch = DefaultBatch
	}
	if t.Grace <= 0 {
		t.Grace = DefaultGrace
	}
	if t.RetryBackoff <= 0 {
		t.RetryBackoff = DefaultRetryBackoff
	}
	if t.RetentionEvery <= 0 {
		t.RetentionEvery = DefaultRetentionEvery
	}
	return t
}

// Deps are what the worker acts through.
type Deps struct {
	Windows   WindowStore
	Sources   Sources
	Objects   Objects
	Bucket    string
	Tables    Tables
	Resources WindowResources
	Metrics   Metrics
	Logger    *slog.Logger
	Now       func() time.Time
}

// Worker compacts windows and applies retention until stopped.
type Worker struct {
	tuning Tuning
	deps   Deps

	lastRetention time.Time

	stop     chan struct{}
	stopOnce sync.Once
	done     chan struct{}
}

// New builds a worker. It returns nil when a dependency is missing, and a nil
// worker's Start and Stop do nothing.
func New(t Tuning, d Deps) *Worker {
	if d.Windows == nil || d.Sources == nil || d.Objects == nil || d.Tables == nil || d.Resources == nil {
		return nil
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Worker{tuning: t.withDefaults(), deps: d, stop: make(chan struct{}), done: make(chan struct{})}
}

// Start runs the worker until Stop.
func (w *Worker) Start(ctx context.Context) {
	if w == nil {
		return
	}
	go w.run(ctx)
}

// Stop ends the worker and waits for the pass under way to finish.
func (w *Worker) Stop() {
	if w == nil {
		return
	}
	w.stopOnce.Do(func() { close(w.stop) })
	<-w.done
}

func (w *Worker) run(ctx context.Context) {
	defer close(w.done)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-w.stop:
			cancel()
		case <-ctx.Done():
		}
	}()
	for {
		busy := w.Pass(ctx)
		wait := w.tuning.Poll
		if busy {
			wait = 0
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// Pass compacts one batch of owed windows, applies retention when it is due, and
// reports whether it found windows to compact, which is the caller's cue to run
// again without waiting.
func (w *Worker) Pass(ctx context.Context) bool {
	now := w.deps.Now()
	windows, err := w.deps.Windows.ClaimOwed(ctx, now.Add(-w.tuning.Grace), w.tuning.Lease, w.tuning.Batch)
	if err != nil {
		w.warn("claiming windows", "", err)
		return false
	}
	for _, h := range windows {
		w.compactOne(ctx, h)
	}
	if now.Sub(w.lastRetention) >= w.tuning.RetentionEvery {
		w.lastRetention = now
		w.Retention(ctx)
	}
	return len(windows) > 0
}

// compactOne compacts one claimed window and records the outcome.
func (w *Worker) compactOne(ctx context.Context, h whstore.Window) {
	src, err := w.deps.Sources.Get(ctx, h.Source)
	if errors.Is(err, whsource.ErrNotFound) {
		return
	}
	if err == nil {
		err = w.compact(ctx, src.WithDefaults(), h)
	}
	if err != nil {
		w.warn("compacting a window", h.Source, err)
		w.metric(ctx, h.Source, ResultFailed, 0)
		hold := min(w.tuning.RetryBackoff*time.Duration(max(h.Attempts, 1)), maxBackoff)
		if rerr := w.deps.Windows.RecordFailure(ctx, h, logsan.SanitizeForLog(err.Error()), hold); rerr != nil {
			w.warn("recording a compaction failure", h.Source, rerr)
		}
	}
}

// compact rewrites one window from everything it holds.
func (w *Worker) compact(ctx context.Context, src whsource.Source, h whstore.Window) error {
	tg, err := w.deps.Tables.TargetFor(src.Connection)
	if err != nil {
		return fmt.Errorf("resolving the source's connection: %w", err)
	}
	keys, err := w.deps.Objects.ListKeys(ctx, w.deps.Bucket, whlayout.WindowPrefix(src.Name, h.Start))
	if err != nil {
		return fmt.Errorf("listing the window's segments: %w", err)
	}
	events, err := w.gather(ctx, h, keys)
	if err != nil {
		return err
	}
	kept, dropped := dedup(events)
	data, err := encodeWindow(kept)
	if err != nil {
		return err
	}
	stored, err := w.deps.Resources.Put(ctx, src, h.Start, h.ResourceID, data)
	if err != nil {
		return fmt.Errorf("writing the window's resource: %w", err)
	}
	location := w.deps.Tables.S3Location(dirOf(stored.Key))
	if err := w.deps.Windows.RecordLocation(ctx, h, stored.ResourceID, location); err != nil {
		return fmt.Errorf("recording the window's resource: %w", err)
	}
	if err := w.deps.Tables.RegisterWindow(ctx, tg, src, h.Start, location); err != nil {
		return fmt.Errorf("registering the window's partition: %w", err)
	}
	if err := w.deps.Windows.RecordCompacted(ctx, h, whstore.Compaction{
		Segments: len(keys), Events: int64(len(kept)), Duplicates: dropped,
		Digest: digest(keys), ResourceID: stored.ResourceID, Location: location,
	}); err != nil {
		return fmt.Errorf("recording the compaction: %w", err)
	}
	w.metric(ctx, src.Name, ResultCompacted, dropped)
	return nil
}

// gather reads the window's previous Parquet file, when it has one, and every
// raw segment.
func (w *Worker) gather(ctx context.Context, h whstore.Window, keys []string) ([]whevent.Stored, error) {
	events, err := w.previous(ctx, h)
	if err != nil {
		return nil, err
	}
	for _, key := range keys {
		data, err := w.deps.Objects.GetObject(ctx, w.deps.Bucket, key)
		if err != nil {
			return nil, fmt.Errorf("reading segment %s: %w", key, err)
		}
		seg, err := whevent.DecodeSegment(data)
		if err != nil {
			return nil, fmt.Errorf("segment %s: %w", key, err)
		}
		events = append(events, seg...)
	}
	return events, nil
}

// previous reads the events of the window's last compaction, which is what
// keeps the events of segments retention has since deleted.
func (w *Worker) previous(ctx context.Context, h whstore.Window) ([]whevent.Stored, error) {
	if h.ResourceID == "" {
		return nil, nil
	}
	key, ok, err := w.deps.Resources.Key(ctx, h.ResourceID)
	if err != nil {
		return nil, fmt.Errorf("finding the window's previous file: %w", err)
	}
	if !ok {
		return nil, nil
	}
	data, err := w.deps.Objects.GetObject(ctx, w.deps.Bucket, key)
	if err != nil {
		return nil, fmt.Errorf("reading the window's previous file: %w", err)
	}
	return decodeWindow(data)
}

// dedup keeps one event per event id, the earliest received (then the earliest
// written), and returns them in the order they were received.
func dedup(events []whevent.Stored) (kept []whevent.Stored, dropped int64) {
	sort.SliceStable(events, func(i, j int) bool {
		a, b := events[i], events[j]
		if !a.ReceivedAt.Equal(b.ReceivedAt) {
			return a.ReceivedAt.Before(b.ReceivedAt)
		}
		return a.LandedAt.Before(b.LandedAt)
	})
	seen := make(map[string]struct{}, len(events))
	kept = make([]whevent.Stored, 0, len(events))
	for _, e := range events {
		if _, dup := seen[e.EventID]; dup {
			dropped++
			continue
		}
		seen[e.EventID] = struct{}{}
		kept = append(kept, e)
	}
	return kept, dropped
}

// digest identifies the set of segments a compaction read.
func digest(keys []string) string {
	sorted := append([]string(nil), keys...)
	sort.Strings(sorted)
	sum := sha256.Sum256([]byte(strings.Join(sorted, "\n")))
	return hex.EncodeToString(sum[:])
}

// dirOf is the directory of an object key, with its trailing slash.
func dirOf(key string) string {
	if i := strings.LastIndexByte(key, '/'); i >= 0 {
		return key[:i+1]
	}
	return ""
}

func (w *Worker) metric(ctx context.Context, source, result string, dropped int64) {
	if w.deps.Metrics == nil {
		return
	}
	w.deps.Metrics.WebhookCompaction(ctx, source, result)
	if dropped > 0 {
		w.deps.Metrics.WebhookDuplicatesDropped(ctx, source, dropped)
	}
}

func (w *Worker) warn(what, source string, err error) {
	w.deps.Logger.Warn("webhooks: "+what, "source", logsan.SanitizeForLog(source),
		"error", logsan.SanitizeForLog(err.Error()))
}
