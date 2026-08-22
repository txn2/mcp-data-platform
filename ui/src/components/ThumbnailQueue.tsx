import { Suspense, lazy, useState, useEffect, useCallback, useRef } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { apiFetchRaw } from "@/api/portal/client";
import type { Asset } from "@/api/portal/types";
import { useIdleGate } from "@/lib/idle";
import {
  isThumbnailSupported,
  isThemeable,
  isLegacyThumbnailKey,
  THUMBNAIL_SOURCE_LIMIT,
} from "@/lib/thumbnailSupport";

// The capturer pulls in html2canvas, the markdown renderer and the diagram
// engine — roughly 200 KB that the assets home has no use for until it finds
// an asset actually missing a thumbnail. Loading it on demand keeps it out of
// the landing chunk (#1351).
const ThumbnailGenerator = lazy(() =>
  import("./ThumbnailGenerator").then((m) => ({ default: m.ThumbnailGenerator })),
);

interface Props {
  assets: Asset[];
}

/**
 * How many assets one visit to the list will capture.
 *
 * Capture is a long main-thread task per asset, so an unbounded backfill of a
 * large library is a page that stalls repeatedly rather than once. The rest
 * are picked up by the next visit, which is what already happened for anything
 * below the fold, and the cap is high enough that a normal library fills in
 * over a few visits.
 */
const MAX_CAPTURES_PER_VISIT = 8;

/**
 * Background queue that fills in missing thumbnails from the asset list page.
 *
 * It runs one asset at a time, and only while the browser is idle and the tab
 * is visible: the reader's page is what the main thread is for. It renders
 * nothing visible.
 *
 * Tracks processed asset IDs to prevent duplicate captures when the asset
 * list is refetched after a successful upload.
 */
export function ThumbnailQueue({ assets }: Props) {
  const qc = useQueryClient();
  const [queue, setQueue] = useState<Asset[]>([]);
  const [current, setCurrent] = useState<{ asset: Asset; content: string } | null>(null);
  const processedRef = useRef(new Set<string>());
  const capturedThisVisit = useRef(0);
  // Set when a capture uploads during the current drain cycle. We refresh the
  // asset list exactly once when the queue goes idle, rather than after every
  // capture: a per-capture invalidation refetched the list and re-rendered the
  // grid on each thumbnail, which tore down and re-requested every <img> (and
  // aborted in-flight loads) so thumbnails never settled. Batching collapses a
  // backfill of N thumbnails into a single refetch.
  const dirtyRef = useRef(false);

  // Build the queue of assets needing thumbnails, excluding already-processed
  // ones. A themeable asset (markdown/CSV) needs capture until BOTH the light
  // and dark variants exist; single-theme types need only the light variant.
  useEffect(() => {
    const budget = Math.max(0, MAX_CAPTURES_PER_VISIT - capturedThisVisit.current);
    setQueue(assets.filter((a) => needsCapture(a, processedRef.current)).slice(0, budget));
    setCurrent(null);
  }, [assets]);

  const idle = useIdleGate(queue.length > 0 && !current);

  // Fetch the next item's content, but only once the browser has gone idle
  // with the tab in front. The fetch is deferred along with the capture
  // because it pulls the asset's whole body over the wire.
  useEffect(() => {
    if (!idle || current || queue.length === 0) return;
    if (capturedThisVisit.current >= MAX_CAPTURES_PER_VISIT) return;

    const next = queue[0]!;

    // Mark as processed immediately to prevent re-queuing on refetch
    processedRef.current.add(next.id);

    let cancelled = false;
    apiFetchRaw(`/assets/${next.id}/content`)
      .then((res) => {
        if (!res.ok) throw new Error("fetch failed");
        return res.text();
      })
      .then((text) => {
        if (cancelled) return;
        // The budget counts captures, not attempts. Spending it here — where
        // the content is in hand and the capture is about to run — is what
        // keeps an abandoned attempt from consuming a slot and eventually
        // wedging the queue with a budget it never spent on anything.
        capturedThisVisit.current++;
        setCurrent({ asset: next, content: text });
      })
      .catch(() => {
        // Skip this asset on error
        if (!cancelled) setQueue((q) => q.slice(1));
      });
    return () => {
      cancelled = true;
    };
  }, [idle, queue, current]);

  const advance = useCallback(() => {
    setCurrent(null);
    setQueue((q) => q.slice(1));
  }, []);

  const handleCaptured = useCallback(() => {
    // Defer the asset-list refresh to the drain effect below so a batch of
    // captures triggers a single refetch instead of one per capture.
    dirtyRef.current = true;
    advance();
  }, [advance]);

  const handleFailed = useCallback(() => {
    // Move on to the next asset without marking dirty.
    advance();
  }, [advance]);

  // Refresh the asset list once, when the queue has fully drained and at least
  // one capture uploaded. This flips the freshly captured assets from the
  // placeholder icon to their thumbnail in a single grid re-render.
  useEffect(() => {
    if (!current && queue.length === 0 && dirtyRef.current) {
      dirtyRef.current = false;
      void qc.invalidateQueries({ queryKey: ["assets"] });
    }
  }, [current, queue.length, qc]);

  if (!current) return null;

  return (
    <Suspense fallback={null}>
      <ThumbnailGenerator
        assetId={current.asset.id}
        content={current.content}
        contentType={current.asset.content_type}
        onCaptured={handleCaptured}
        onFailed={handleFailed}
      />
    </Suspense>
  );
}

/**
 * Whether this asset is worth capturing on this visit: a supported type, not
 * already attempted, missing a variant, and small enough to render twice
 * without stalling the page.
 *
 * A variant recorded under a legacy filename counts as missing. The object is
 * a non-hidden file beside the content, which is what a CSV asset registered
 * as a table must not have, and nothing else replaces it: capture is the only
 * writer of a thumbnail key.
 */
function needsCapture(a: Asset, processed: ReadonlySet<string>): boolean {
  if (!isThumbnailSupported(a.content_type) || processed.has(a.id)) return false;
  if (a.size_bytes > THUMBNAIL_SOURCE_LIMIT) return false;
  const missingLight = !a.thumbnail_s3_key || isLegacyThumbnailKey(a.thumbnail_s3_key);
  const missingDark =
    isThemeable(a.content_type) &&
    (!a.thumbnail_dark_s3_key || isLegacyThumbnailKey(a.thumbnail_dark_s3_key));
  return missingLight || missingDark;
}
