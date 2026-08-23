import { Suspense, lazy, useState, useEffect, useCallback, useRef } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { apiFetchRaw } from "@/api/portal/client";
import { usePendingThumbnails } from "@/api/portal/hooks/assets";
import type { Asset } from "@/api/portal/types";
import { useIdleGate } from "@/lib/idle";

// The capturer pulls in html2canvas, the markdown renderer and the diagram
// engine — roughly 200 KB that no page has a use for until the queue finds an
// asset actually needing a capture. Loading it on demand keeps it out of the
// landing chunk (#1351).
const ThumbnailGenerator = lazy(() =>
  import("./ThumbnailGenerator").then((m) => ({ default: m.ThumbnailGenerator })),
);

/**
 * How many assets one batch will capture.
 *
 * Capture is a long main-thread task per asset, so an unbounded backfill of a
 * large library is a tab that stalls repeatedly rather than once. The server
 * offers more than this per poll so that assets whose capture fails do not
 * crowd out the ones that would succeed; the rest are picked up by the next
 * poll, and a library fills in over a few of them.
 */
const MAX_CAPTURES_PER_BATCH = 8;

/**
 * Background queue that fills in thumbnails the portal is missing or holding a
 * stale copy of.
 *
 * The work comes from the server, not from what a page happens to be
 * displaying: nothing rasterizes an asset outside a browser, so an asset a
 * managed script rewrote on a schedule has no image at all until some tab is
 * told about it (#1431). Mounted once in the shell, any open portal tab does
 * that work, whatever page the reader is on.
 *
 * It runs one asset at a time, and only while the browser is idle and the tab
 * is visible: the reader's page is what the main thread is for. It renders
 * nothing visible.
 *
 * An asset that has been attempted is not attempted again in this session, so a
 * document the capturer cannot render costs one try rather than one per poll.
 */
export function ThumbnailQueue() {
  const qc = useQueryClient();
  const { data, dataUpdatedAt } = usePendingThumbnails();
  const [current, setCurrent] = useState<{ asset: Asset; content: string } | null>(null);
  // Bumped when an asset is passed over, which is the one transition that
  // changes what comes next without changing any state of its own.
  const [, forceRecheck] = useState(0);
  const attemptedRef = useRef(new Set<string>());
  const capturedThisBatch = useRef(0);
  // Set when a capture uploads during the current drain. The asset list is
  // refreshed once, when the queue goes idle, rather than after every capture:
  // a per-capture invalidation refetched the list and re-rendered the grid on
  // each thumbnail, which tore down and re-requested every <img> (and aborted
  // in-flight loads) so thumbnails never settled.
  const dirtyRef = useRef(false);

  // A fresh answer from the server starts a fresh batch: the budget below is
  // per batch, so a tab left open all day keeps picking up work rather than
  // spending one allowance and stopping.
  useEffect(() => {
    capturedThisBatch.current = 0;
  }, [dataUpdatedAt]);

  const pending = data?.data;
  const next =
    !current && capturedThisBatch.current < MAX_CAPTURES_PER_BATCH
      ? pending?.find((a) => !attemptedRef.current.has(a.id))
      : undefined;

  const idle = useIdleGate(!!next);

  // Fetch the next asset's content, but only once the browser has gone idle
  // with the tab in front. The fetch is deferred along with the capture
  // because it pulls the asset's whole body over the wire.
  useEffect(() => {
    if (!idle || !next) return;

    // Marked attempted before the fetch, so a failure moves the queue on rather
    // than offering the same asset again the moment this effect re-runs.
    attemptedRef.current.add(next.id);

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
        capturedThisBatch.current++;
        setCurrent({ asset: next, content: text });
      })
      .catch(() => {
        if (!cancelled) forceRecheck((n) => n + 1);
      });
    return () => {
      cancelled = true;
    };
  }, [idle, next]);

  const handleCaptured = useCallback(() => {
    // Deferred to the drain effect below so a batch of captures triggers a
    // single refetch instead of one per capture.
    dirtyRef.current = true;
    setCurrent(null);
  }, []);

  const handleFailed = useCallback(() => {
    setCurrent(null);
  }, []);

  // Refresh what the portal is showing once the queue has drained and at least
  // one capture uploaded. This flips the freshly captured assets from the
  // placeholder icon, or from an image a version behind, in one grid re-render.
  useEffect(() => {
    if (!current && !next && dirtyRef.current) {
      dirtyRef.current = false;
      void qc.invalidateQueries({ queryKey: ["assets"] });
    }
  }, [current, next, qc]);

  if (!current) return null;

  return (
    <Suspense fallback={null}>
      <ThumbnailGenerator
        assetId={current.asset.id}
        content={current.content}
        contentType={current.asset.content_type}
        version={current.asset.current_version}
        onCaptured={handleCaptured}
        onFailed={handleFailed}
      />
    </Suspense>
  );
}
