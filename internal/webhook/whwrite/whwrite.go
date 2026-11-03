// Package whwrite files a webhook source's events as raw segments, the way
// the HTTP receiver writes a request's: one gzipped JSON-lines object per
// window, the window recorded before and after the write, its raw partition
// registered after, and the scripts naming the source told (#2332). The
// inputs the platform reads itself rather than being posted to, a mailbox
// (#2247) and an application database (#2408), write through it.
package whwrite

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/webhook/whevent"
	"github.com/txn2/mcp-data-platform/internal/webhook/whlayout"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
)

// segmentContentType is what a raw segment is stored as, the HTTP receiver's.
const segmentContentType = "application/gzip"

// Ports.
type (
	// Objects writes a segment to the managed-resources bucket.
	Objects interface {
		PutObject(ctx context.Context, bucket, key string, data []byte, contentType string) error
	}
	// Recorder records a window a segment was written to.
	Recorder interface {
		MarkSegment(ctx context.Context, source string, start time.Time, length time.Duration, bytes int64) error
	}
	// RawWindows registers a window's raw partition so its events are
	// queryable before compaction.
	RawWindows interface {
		EnsureRawWindow(ctx context.Context, src whsource.Source, start time.Time) error
	}
)

// Config assembles a writer.
type Config struct {
	Objects    Objects
	Bucket     string
	Recorder   Recorder
	RawWindows RawWindows
	// Writer names the writer in each segment's key: "imap-<replica>".
	Writer string
	Logger *slog.Logger
	Now    func() time.Time
}

// Writer files events as segments.
type Writer struct {
	cfg Config
	seq atomic.Uint64

	mu sync.Mutex
	// onStored is told each window's events once their segment is written;
	// nil tells nothing.
	onStored func(ctx context.Context, source string, events []whevent.Event)
}

// New returns a writer.
func New(cfg Config) *Writer {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	w := &Writer{cfg: cfg}
	w.seq.Store(uint64(cfg.Now().UnixNano())) //nolint:gosec // a clock reading seeds a counter
	return w
}

// SetStored is what is told each written segment's events from here on: the
// hook the runs of an automation naming the source are enqueued through.
func (w *Writer) SetStored(fn func(ctx context.Context, source string, events []whevent.Event)) {
	w.mu.Lock()
	w.onStored = fn
	w.mu.Unlock()
}

// Write files events as segments, one per window, oldest window first.
func (w *Writer) Write(ctx context.Context, src whsource.Source, events []whevent.Event) error {
	if len(events) == 0 {
		return nil
	}
	length := src.Config.Window()
	byWindow := map[time.Time][]whevent.Event{}
	for _, e := range events {
		start := e.Window(length)
		byWindow[start] = append(byWindow[start], e)
	}
	starts := make([]time.Time, 0, len(byWindow))
	for start := range byWindow {
		starts = append(starts, start)
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i].Before(starts[j]) })
	for _, start := range starts {
		if err := w.window(ctx, src, start, length, byWindow[start]); err != nil {
			return err
		}
	}
	return nil
}

func (w *Writer) window(ctx context.Context, src whsource.Source, start time.Time, length time.Duration, events []whevent.Event) error {
	data, err := whevent.EncodeSegment(events, w.cfg.Now())
	if err != nil {
		return fmt.Errorf("encoding the segment: %w", err)
	}
	if err := w.cfg.Recorder.MarkSegment(ctx, src.Name, start, length, 0); err != nil {
		return fmt.Errorf("recording segment: %w", err)
	}
	key := whlayout.SegmentKey(src.Name, start, w.cfg.Writer, w.seq.Add(1))
	if err := w.cfg.Objects.PutObject(ctx, w.cfg.Bucket, key, data, segmentContentType); err != nil {
		return fmt.Errorf("writing segment: %w", err)
	}
	if err := w.cfg.Recorder.MarkSegment(ctx, src.Name, start, length, int64(len(data))); err != nil {
		return fmt.Errorf("recording segment: %w", err)
	}
	w.mu.Lock()
	stored := w.onStored
	w.mu.Unlock()
	if stored != nil {
		stored(ctx, src.Name, events)
	}
	if w.cfg.RawWindows != nil {
		if err := w.cfg.RawWindows.EnsureRawWindow(ctx, src, start); err != nil {
			w.cfg.Logger.WarnContext(ctx, "webhooks: registering a window's raw partition",
				"source", logsan.SanitizeForLog(src.Name), "error", logsan.SanitizeForLog(err.Error()))
		}
	}
	return nil
}
