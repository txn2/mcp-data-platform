import { useState, useEffect, useCallback, useRef } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { apiFetchRaw } from "@/api/portal/client";
import type { Asset } from "@/api/portal/types";
import { isThumbnailSupported, isThemeable } from "@/lib/thumbnail";
import { ThumbnailGenerator } from "./ThumbnailGenerator";

interface Props {
  assets: Asset[];
}

/**
 * Background queue that auto-generates missing thumbnails when the asset
 * list page loads. Processes one asset at a time to avoid overloading the
 * browser. Renders nothing visible.
 *
 * Tracks processed asset IDs to prevent duplicate captures when the asset
 * list is refetched after a successful upload.
 */
export function ThumbnailQueue({ assets }: Props) {
  const qc = useQueryClient();
  const [queue, setQueue] = useState<Asset[]>([]);
  const [current, setCurrent] = useState<{ asset: Asset; content: string } | null>(null);
  const processedRef = useRef(new Set<string>());
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
    const needsThumbnail = assets.filter((a) => {
      if (!isThumbnailSupported(a.content_type) || processedRef.current.has(a.id)) {
        return false;
      }
      const missingLight = !a.thumbnail_s3_key;
      const missingDark = isThemeable(a.content_type) && !a.thumbnail_dark_s3_key;
      return missingLight || missingDark;
    });
    setQueue(needsThumbnail);
    setCurrent(null);
  }, [assets]);

  // Process the next item in the queue
  useEffect(() => {
    if (current || queue.length === 0) return;

    const next = queue[0]!;

    // Mark as processed immediately to prevent re-queuing on refetch
    processedRef.current.add(next.id);

    apiFetchRaw(`/assets/${next.id}/content`)
      .then((res) => {
        if (!res.ok) throw new Error("fetch failed");
        return res.text();
      })
      .then((text) => {
        setCurrent({ asset: next, content: text });
      })
      .catch(() => {
        // Skip this asset on error
        setQueue((q) => q.slice(1));
      });
  }, [queue, current]);

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
    <ThumbnailGenerator
      assetId={current.asset.id}
      content={current.content}
      contentType={current.asset.content_type}
      onCaptured={handleCaptured}
      onFailed={handleFailed}
    />
  );
}
