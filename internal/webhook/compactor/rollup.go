package compactor

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/webhook/whlayout"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
)

// The open window's rollup (#2132).
//
// A source's open window is read from its raw segments, one object each, and
// a busy source writes thousands an hour, so every reader of recent events
// pays a request per segment. A rollup merges the open window's segments into
// one object in the same directory and format, which the raw table reads like
// any segment, and deletes the segments it merged.
//
// The deletion waits. A query lists a directory and then reads what it
// listed, so a segment deleted in between fails that query; the segments a
// rollup merged are deleted by a later pass, at least rollupDeleteDelay after
// the rollup. Until then an event is in a segment and in the rollup, which a
// reader of the raw side counts twice. The raw side already holds a sender's
// retries until compaction drops them by event id, which drops these too.
// A hidden manifest beside the rollup lists what it merged; the query engine
// skips a name beginning with ".", and compaction skips it by name.
//
// Only the open window is rolled up. Compaction takes a window once it has
// ended and the grace has passed, so the two never act on one window; a
// rollup's claim of the window's row keeps two replicas from merging the same
// segments, and holds back a compaction no longer than its lease.

// The rollup's defaults and fixed bounds.
const (
	// DefaultRollupCheckEvery is how often the rollup pass runs.
	DefaultRollupCheckEvery = time.Minute
	// DefaultRollupDeleteDelay is the least time between a rollup and the
	// deletion of the segments it merged: longer than a query reading the
	// open window is expected to run.
	DefaultRollupDeleteDelay = 2 * time.Minute
	// rollupLease bounds one replica's claim of a window for a rollup.
	rollupLease = time.Minute
	// minRollupSegments is the fewest segments worth merging.
	minRollupSegments = 2
	// segmentContentType is what a rollup is stored as, the segment's own.
	segmentContentType = "application/gzip"
)

// rollupDue reports whether the rollup pass is due, and marks it run.
func (w *Worker) rollupDue(now time.Time) bool {
	if now.Sub(w.lastRollup) < w.tuning.RollupCheckEvery {
		return false
	}
	w.lastRollup = now
	return true
}

// Rollup rolls up the open window of every source. A failure is logged, does
// not stop the other sources, and is returned joined with the others.
func (w *Worker) Rollup(ctx context.Context) error {
	sources, err := w.deps.Sources.List(ctx)
	if err != nil {
		w.warn(ctx, "listing sources for rollup", "", err)
		return fmt.Errorf("webhooks: listing sources for rollup: %w", err)
	}
	var errs []error
	for _, src := range sources {
		if err := w.rollupSource(ctx, src.WithDefaults()); err != nil {
			w.warn(ctx, "rolling up the open window", src.Name, err)
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("webhooks: rollup: %w", err)
	}
	return nil
}

// rollupSource deletes what earlier rollups of the source's open window
// merged once it is safe to, and merges the segments written since the last
// rollup when one is due.
func (w *Worker) rollupSource(ctx context.Context, src whsource.Source) error {
	now := w.deps.Now().UTC()
	start := now.Truncate(src.Config.Window())
	keys, err := w.deps.Objects.ListKeys(ctx, w.deps.Bucket, whlayout.WindowPrefix(src.Name, start))
	if err != nil {
		return fmt.Errorf("listing the open window: %w", err)
	}
	if len(keys) < minRollupSegments {
		return nil
	}
	claimed, err := w.deps.Windows.ClaimRollup(ctx, src.Name, start, rollupLease)
	if err != nil || !claimed {
		return err //nolint:wrapcheck // the store names the claim
	}
	merged, err := w.settleManifests(ctx, keys, now)
	if err != nil {
		return err
	}
	// A rollup the window closes on before its segments could be deleted
	// would add an object and a second copy of every event, not remove any.
	if start.Add(src.Config.Window()).Sub(now) <= w.tuning.RollupDeleteDelay {
		return nil
	}
	in := rollupInputs(keys, merged, now, src.Config.RollupEvery())
	if len(in) < minRollupSegments {
		return nil
	}
	return w.writeRollup(ctx, src, start, now, in)
}

// settleManifests deletes what each manifest at least rollupDeleteDelay old
// lists, then the manifest, and returns every key a manifest named: merged
// into a rollup, or deleted just now, and in either case not to be merged.
// The delay is the worker's RollupDeleteDelay.
func (w *Worker) settleManifests(ctx context.Context, keys []string, now time.Time) (map[string]bool, error) {
	merged := map[string]bool{}
	for _, key := range keys {
		kind, at := whlayout.Classify(key)
		if kind != whlayout.KindManifest {
			continue
		}
		data, err := w.deps.Objects.GetObject(ctx, w.deps.Bucket, key)
		if err != nil {
			return nil, fmt.Errorf("reading rollup manifest %s: %w", key, err)
		}
		listed := strings.Fields(string(data))
		for _, k := range listed {
			merged[k] = true
		}
		if now.Sub(at) < w.tuning.RollupDeleteDelay {
			continue
		}
		for _, k := range append(listed, key) {
			if err := w.deps.Objects.DeleteObject(ctx, w.deps.Bucket, k); err != nil {
				return nil, fmt.Errorf("deleting merged segment %s: %w", k, err)
			}
		}
	}
	return merged, nil
}

// rollupInputs returns the segments a rollup at now merges: every segment no
// earlier rollup merged, when the newest rollup is at least every old, and
// none otherwise. A rollup is never merged again, so each one costs the
// segments written since the last, not the window so far.
func rollupInputs(keys []string, merged map[string]bool, now time.Time, every time.Duration) []string {
	var segments []string
	for _, key := range keys {
		kind, at := whlayout.Classify(key)
		switch {
		case kind == whlayout.KindRollup && now.Sub(at) < every:
			return nil
		case kind == whlayout.KindSegment && !merged[key]:
			segments = append(segments, key)
		}
	}
	return segments
}

// writeRollup merges the segments into one rollup of the window, then writes
// the manifest naming them. A pass that stops between the two leaves a
// rollup no manifest names: its segments are merged again by the next, and
// the events counted twice until compaction.
func (w *Worker) writeRollup(ctx context.Context, src whsource.Source, start, now time.Time, segments []string) error {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	for _, key := range segments {
		data, err := w.deps.Objects.GetObject(ctx, w.deps.Bucket, key)
		if err != nil {
			return fmt.Errorf("reading segment %s: %w", key, err)
		}
		if err := appendLines(zw, data); err != nil {
			return fmt.Errorf("segment %s: %w", key, err)
		}
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("closing the rollup: %w", err)
	}
	if err := w.deps.Objects.PutObject(ctx, w.deps.Bucket, whlayout.RollupKey(src.Name, start, now), buf.Bytes(), segmentContentType); err != nil {
		return fmt.Errorf("writing the rollup: %w", err)
	}
	manifest := []byte(strings.Join(segments, "\n") + "\n")
	if err := w.deps.Objects.PutObject(ctx, w.deps.Bucket, whlayout.ManifestKey(src.Name, start, now), manifest, "text/plain"); err != nil {
		return fmt.Errorf("writing the rollup manifest: %w", err)
	}
	return nil
}

// appendLines copies a gzipped segment's lines to w, each ending in a newline.
func appendLines(w io.Writer, segment []byte) error {
	zr, err := gzip.NewReader(bytes.NewReader(segment))
	if err != nil {
		return fmt.Errorf("opening segment: %w", err)
	}
	defer func() { _ = zr.Close() }()
	br := bufio.NewReader(zr)
	for {
		line, err := br.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			if line[len(line)-1] != '\n' {
				line = append(line, '\n')
			}
			if _, werr := w.Write(line); werr != nil {
				return fmt.Errorf("writing the rollup: %w", werr)
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reading segment: %w", err)
		}
	}
}
