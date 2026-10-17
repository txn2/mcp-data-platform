package compactor

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/txn2/mcp-data-platform/internal/logsan"
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
// stops part-way is finished by the next. Each failure is logged, does not
// stop the steps after it, and is returned joined with the others.
//
// The raw table's partitions are kept by name from the window records, never
// by listing the table's location (#2133): a window whose segments go has its
// raw partition unregistered, and a window a segment landed in since the
// previous pass is registered, which covers one the receiver could not
// register and one a late segment reopened. What a pass costs is the windows
// that changed, not everything raw retention keeps.
func (w *Worker) Retention(ctx context.Context) error {
	sources, err := w.deps.Sources.List(ctx)
	if err != nil {
		w.warn(ctx, "listing sources for retention", "", err)
		return fmt.Errorf("webhooks: listing sources for retention: %w", err)
	}
	var errs []error
	for _, src := range sources {
		src = src.WithDefaults()
		if err := w.retainSource(ctx, src); err != nil {
			w.warn(ctx, "applying retention", src.Name, err)
			errs = append(errs, err)
		}
	}
	if err := w.deps.Windows.RollupTelemetry(ctx, w.deps.Now().Add(-minutesKept)); err != nil {
		w.warn(ctx, "rolling up the webhook history", "", err)
		errs = append(errs, err)
	}
	if err := w.deps.Windows.PruneCounts(ctx, w.deps.Now().Add(-w.tuning.TelemetryKept)); err != nil {
		w.warn(ctx, "pruning the webhook history", "", err)
		errs = append(errs, err)
	}
	// An expired window's record outlives everything it described, and
	// one is added per source per window; past this age it is removed
	// (#1904). A window is chosen by when its events were received, so
	// no segment can land in one this old.
	if keep := w.tuning.ExpiredWindowsKept; keep > 0 {
		if _, err := w.deps.Windows.DeleteExpired(ctx, w.deps.Now().Add(-keep)); err != nil {
			w.warn(ctx, "deleting expired window records", "", err)
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("webhooks: retention: %w", err)
	}
	return nil
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
	if err := w.releaseRawBefore(ctx, tg, src, now.Add(-src.Config.RawRetention())); err != nil {
		return err
	}
	return w.registerRawSince(ctx, tg, src, now.Add(-w.rawLookback()))
}

// rawLookback is how far back a pass registers the raw partitions of windows
// a segment landed in: two retention intervals, so a pass that failed or was
// skipped is covered by the next. Each window in it costs a call on the
// catalog's metastore, so it is no longer than that.
func (w *Worker) rawLookback() time.Duration {
	return 2 * w.tuning.RetentionEvery
}

// registerRawSince registers the raw partition of every window of src that
// holds segments written since since. Registering one already registered is
// not an error.
func (w *Worker) registerRawSince(ctx context.Context, tg whtable.Target, src whsource.Source, since time.Time) error {
	windows, err := w.deps.Windows.RawRegistrable(ctx, src.Name, since)
	if err != nil {
		return fmt.Errorf("reading the windows to register: %w", err)
	}
	for _, h := range windows {
		if err := w.deps.Tables.RegisterRawWindow(ctx, tg, src, h.Start); err != nil {
			return fmt.Errorf("registering the raw window: %w", err)
		}
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
// whose last segment was written before cutoff. A window whose segments are no
// longer the set its compaction read is left for the compaction it is now owed.
func (w *Worker) releaseRawBefore(ctx context.Context, tg whtable.Target, src whsource.Source, cutoff time.Time) error {
	raw, err := w.deps.Windows.RawDeletable(ctx, src.Name, cutoff)
	if err != nil {
		return fmt.Errorf("reading the windows whose segments may go: %w", err)
	}
	for _, h := range raw {
		released, err := w.releaseCompactedRaw(ctx, src, h)
		if err != nil {
			return err
		}
		if !released {
			continue
		}
		if err := w.deps.Tables.UnregisterRawWindow(ctx, tg, src, h.Start); err != nil {
			return fmt.Errorf("unregistering the raw window: %w", err)
		}
		if err := w.deps.Windows.MarkRawDeleted(ctx, h); err != nil {
			return fmt.Errorf("recording the segments deleted: %w", err)
		}
	}
	return nil
}

// releaseCompactedRaw deletes a compacted window's raw segments when the
// segments listed now are the ones its current compaction read, and reports
// whether it did (#2151). The window was chosen before the listing, so a
// segment written in between is in the listing and in no Parquet file: its
// writer records it against the window after the put, which may not have
// happened yet, so the window's generation cannot be relied on to show it.
// The digest can: a listing holding any segment the compaction did not read
// does not match, and the window is left until it is compacted again. A
// segment written after the listing is not deleted; it records itself
// against the window, which makes the window owed another compaction.
func (w *Worker) releaseCompactedRaw(ctx context.Context, src whsource.Source, h whstore.Window) (bool, error) {
	keys, err := w.deps.Objects.ListKeys(ctx, w.deps.Bucket, whlayout.WindowPrefix(src.Name, h.Start))
	if err != nil {
		return false, fmt.Errorf("listing the window's segments: %w", err)
	}
	read, current, err := w.deps.Windows.CompactedDigest(ctx, h)
	if err != nil {
		return false, fmt.Errorf("reading the window's compaction: %w", err)
	}
	if !current || read != digest(eventKeys(keys)) {
		w.deps.Logger.InfoContext(ctx, "webhooks: raw segments kept, the window holds segments its compaction did not read",
			"source", logsan.SanitizeForLog(src.Name), "window", h.Start)
		return false, nil
	}
	return true, w.deleteKeys(ctx, keys)
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
	if err := w.deps.Tables.UnregisterRawWindow(ctx, tg, src, h.Start); err != nil {
		return fmt.Errorf("unregistering the raw window: %w", err)
	}
	if err := w.deps.Windows.MarkExpired(ctx, h); err != nil {
		return fmt.Errorf("recording the window expired: %w", err)
	}
	return nil
}

// deleteRaw deletes the raw segments of one window that exist now: an expired
// window's, every one of which goes whether or not it was compacted.
func (w *Worker) deleteRaw(ctx context.Context, src whsource.Source, h whstore.Window) error {
	keys, err := w.deps.Objects.ListKeys(ctx, w.deps.Bucket, whlayout.WindowPrefix(src.Name, h.Start))
	if err != nil {
		return fmt.Errorf("listing the window's segments: %w", err)
	}
	return w.deleteKeys(ctx, keys)
}

// deleteKeys deletes the listed objects.
func (w *Worker) deleteKeys(ctx context.Context, keys []string) error {
	for _, key := range keys {
		if err := w.deps.Objects.DeleteObject(ctx, w.deps.Bucket, key); err != nil {
			return fmt.Errorf("deleting segment %s: %w", key, err)
		}
	}
	return nil
}
