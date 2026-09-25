package compactor

import (
	"context"
	"fmt"
	"time"

	"github.com/txn2/mcp-data-platform/internal/webhook/whlayout"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/internal/webhook/whstore"
	"github.com/txn2/mcp-data-platform/internal/webhook/whtable"
)

// Retention applies every source's retention, and prunes request counts no
// page reads any more.
//
// Retention has two settings. A compacted window's raw segments are deleted once
// the last of them is older than raw retention; a window owed a compaction is
// never touched, so no event is deleted before it is in a Parquet file. A
// whole window is removed once it is older than compacted retention: its
// partition is unregistered first, so the view stops serving it, then its
// resource and any raw segments are deleted, and the window is recorded as
// expired. Each step is recorded in the platform database, so a pass that
// stops part-way is finished by the next.
func (w *Worker) Retention(ctx context.Context) {
	sources, err := w.deps.Sources.List(ctx)
	if err != nil {
		w.warn("listing sources for retention", "", err)
		return
	}
	for _, src := range sources {
		src = src.WithDefaults()
		if err := w.retainSource(ctx, src); err != nil {
			w.warn("applying retention", src.Name, err)
		}
	}
	if err := w.deps.Windows.PruneCounts(ctx, w.deps.Now().Add(-countsKept)); err != nil {
		w.warn("pruning request counts", "", err)
	}
}

// retainSource applies one source's retention.
func (w *Worker) retainSource(ctx context.Context, src whsource.Source) error {
	tg, err := w.deps.Tables.TargetFor(src.Connection)
	if err != nil {
		return fmt.Errorf("resolving the source's connection: %w", err)
	}
	now := w.deps.Now()
	if keep := src.Config.CompactedRetention(); keep > 0 {
		if err := w.expireBefore(ctx, tg, src, now.Add(-keep)); err != nil {
			return err
		}
	}
	if err := w.releaseRawBefore(ctx, src, now.Add(-src.Config.RawRetention())); err != nil {
		return err
	}
	// The raw table's partitions are made to match the directories that
	// exist: a window whose segments are gone is dropped, and a window the
	// receiver could not register when it wrote its first segment is added.
	if err := w.deps.Tables.SyncRaw(ctx, tg, src); err != nil {
		return fmt.Errorf("syncing the raw partitions: %w", err)
	}
	return nil
}

// expireBefore removes every window of src that ended before cutoff.
func (w *Worker) expireBefore(ctx context.Context, tg whtable.Target, src whsource.Source, cutoff time.Time) error {
	expired, err := w.deps.Windows.Expirable(ctx, src.Name, cutoff)
	if err != nil {
		return fmt.Errorf("reading the windows to expire: %w", err)
	}
	for _, h := range expired {
		if err := w.expire(ctx, tg, src, h); err != nil {
			return err
		}
	}
	return nil
}

// releaseRawBefore deletes the raw segments of every compacted window of src
// whose last segment was written before cutoff.
func (w *Worker) releaseRawBefore(ctx context.Context, src whsource.Source, cutoff time.Time) error {
	raw, err := w.deps.Windows.RawDeletable(ctx, src.Name, cutoff)
	if err != nil {
		return fmt.Errorf("reading the windows whose segments may go: %w", err)
	}
	for _, h := range raw {
		if err := w.deleteRaw(ctx, src, h); err != nil {
			return err
		}
		if err := w.deps.Windows.MarkRawDeleted(ctx, h); err != nil {
			return fmt.Errorf("recording the segments deleted: %w", err)
		}
	}
	return nil
}

// expire removes one window: its partition first, then its objects.
func (w *Worker) expire(ctx context.Context, tg whtable.Target, src whsource.Source, h whstore.Window) error {
	if err := w.deps.Tables.UnregisterWindow(ctx, tg, src, h.Start); err != nil {
		return fmt.Errorf("unregistering the window: %w", err)
	}
	if err := w.deps.Windows.MarkUnregistered(ctx, h); err != nil {
		return fmt.Errorf("recording the window unregistered: %w", err)
	}
	if h.ResourceID != "" {
		if err := w.deps.Resources.Delete(ctx, h.ResourceID); err != nil {
			return fmt.Errorf("deleting the window's resource: %w", err)
		}
	}
	if err := w.deleteRaw(ctx, src, h); err != nil {
		return err
	}
	if err := w.deps.Windows.MarkExpired(ctx, h); err != nil {
		return fmt.Errorf("recording the window expired: %w", err)
	}
	return nil
}

// deleteRaw deletes the raw segments of one window that exist now. A segment
// written after the listing is not deleted; it records itself against the
// window, which makes the window owed another compaction before its raw segments
// can be released again.
func (w *Worker) deleteRaw(ctx context.Context, src whsource.Source, h whstore.Window) error {
	keys, err := w.deps.Objects.ListKeys(ctx, w.deps.Bucket, whlayout.WindowPrefix(src.Name, h.Start))
	if err != nil {
		return fmt.Errorf("listing the window's segments: %w", err)
	}
	for _, key := range keys {
		if err := w.deps.Objects.DeleteObject(ctx, w.deps.Bucket, key); err != nil {
			return fmt.Errorf("deleting segment %s: %w", key, err)
		}
	}
	return nil
}
