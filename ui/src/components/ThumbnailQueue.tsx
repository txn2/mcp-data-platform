import { Suspense, lazy, useState, useEffect, useCallback, useRef } from "react";
import { useQueryClient, type QueryKey } from "@tanstack/react-query";
import { apiFetchRaw } from "@/api/portal/client";
import { resourceFetchRaw } from "@/api/resources/client";
import { usePendingThumbnails } from "@/api/portal/hooks/assets";
import { usePendingResourceThumbnails } from "@/api/resources/hooks";
import type { Asset } from "@/api/portal/types";
import type { Resource } from "@/api/resources/types";
import { useIdleGate } from "@/lib/idle";

// The capturer pulls in html2canvas, the markdown renderer and the diagram
// engine — roughly 200 KB that no page has a use for until the queue finds an
// asset actually needing a capture. Loading it on demand keeps it out of the
// landing chunk (#1351).
const ThumbnailGenerator = lazy(() =>
  import("./ThumbnailGenerator").then((m) => ({ default: m.ThumbnailGenerator })),
);

/**
 * How many attempts one asset gets before the queue leaves it alone.
 *
 * A capture that fails used to be recorded and never offered again for the life
 * of the tab, so one transient failure -- a fetch that lost a race, a renderer
 * that had not finished loading -- meant that asset kept its placeholder until
 * somebody reloaded the page (#1554). A bounded retry is the difference between
 * "this document cannot be rasterized" and "not this time".
 */
const MAX_ATTEMPTS_PER_ASSET = 3;

/**
 * The state that put this asset on the pending list.
 *
 * The queue spends one attempt per reason rather than one per asset. Keying it
 * on the id alone recorded the asset for as long as the tab lived, so an owner
 * who cleared a wrong tile was skipped by their own tab for the rest of the
 * session and the capture only happened after a reload (#1501).
 *
 * It is built from everything lib/thumbnailSupport's thumbnailBehind reads, so
 * every answer the server can change its mind on moves it: clearing a tile
 * drops the recorded keys and zeroes their versions, a write moves the current
 * one, and a capture written under the pre-rename filename is replaced by one
 * under the deterministic key at the same version. Both variants are in it
 * because both are asked about -- a themeable asset whose dark capture is
 * missing is pending on that alone.
 */
function attemptKey(a: Asset): string {
  return [
    a.id,
    a.current_version,
    a.thumbnail_version,
    a.thumbnail_dark_version,
    a.thumbnail_s3_key ?? "",
    a.thumbnail_dark_s3_key ?? "",
  ].join("\u0000");
}

/**
 * Where one kind's capture work comes from.
 *
 * An asset and a managed resource are captured by the same capturer in the same
 * tab under the same idle gate; what differs is the work list, the route the
 * content is read from, and what makes an item's pending state different from
 * the one already tried. Forking the queue per kind would have duplicated the
 * accounting that took three tickets to get right.
 */
interface CaptureSource<T> {
  kind: "asset" | "resource";
  usePending: () => { data?: { data: T[] }; dataUpdatedAt: number };
  fetchContent: (item: T) => Promise<Response>;
  attemptKey: (item: T) => string;
  id: (item: T) => string;
  contentType: (item: T) => string;
  /** The version a capture is stamped with, for a kind that has one. */
  version?: (item: T) => number | undefined;
  /** What to refresh once a run of captures has landed. */
  invalidateKey: QueryKey;
}

const assetSource: CaptureSource<Asset> = {
  kind: "asset",
  usePending: usePendingThumbnails,
  fetchContent: (a) => apiFetchRaw(`/assets/${a.id}/content`),
  attemptKey,
  id: (a) => a.id,
  contentType: (a) => a.content_type,
  version: (a) => a.current_version,
  invalidateKey: ["assets"],
};

/**
 * The same for a managed resource (#1554).
 *
 * A resource row carries no version: its revisions live in resource_versions
 * and the server stamps a capture with the resource's own updated_at, so
 * nothing is sent and the attempt key turns on that timestamp instead.
 */
const resourceSource: CaptureSource<Resource> = {
  kind: "resource",
  usePending: usePendingResourceThumbnails,
  fetchContent: (r) => resourceFetchRaw(`/${r.id}/content`),
  attemptKey: (r) =>
    [
      r.id,
      r.updated_at,
      r.thumbnail_captured_at ?? "",
      r.thumbnail_dark_captured_at ?? "",
      r.thumbnail_s3_key ?? "",
      r.thumbnail_dark_s3_key ?? "",
    ].join("\u0000"),
  id: (r) => r.id,
  contentType: (r) => r.mime_type,
  invalidateKey: ["resources"],
};

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
 * An attempt is spent against the state that made the asset pending, not
 * against the asset, so a document the capturer cannot render costs one try
 * rather than one per poll while a tile that was cleared -- or a body that
 * moved on -- is a new reason and is offered again (#1501).
 */
export function ThumbnailQueue() {
  return (
    <>
      <CaptureQueue source={assetSource} />
      <CaptureQueue source={resourceSource} />
    </>
  );
}

/**
 * One kind's queue. The capturer, the idle gate and the attempt accounting are
 * the same for both; a source says where the work list comes from, where the
 * content is read, and what makes one item's pending state different from the
 * last (#1554).
 */
function CaptureQueue<T>({ source }: { source: CaptureSource<T> }) {
  const qc = useQueryClient();
  const { data, dataUpdatedAt } = source.usePending();
  const [current, setCurrent] = useState<{ item: T; content: string; key: string } | null>(null);
  // Bumped when an asset is passed over, which is the one transition that
  // changes what comes next without changing any state of its own.
  const [, forceRecheck] = useState(0);
  // Attempts per reason rather than a set of reasons seen, so a failure costs
  // one try instead of the whole tab (#1554).
  const attemptsRef = useRef(new Map<string, number>());
  // Set when a capture uploads during the current drain. The asset list is
  // refreshed once, when the queue goes idle, rather than after every capture:
  // a per-capture invalidation refetched the list and re-rendered the grid on
  // each thumbnail, which tore down and re-requested every <img> (and aborted
  // in-flight loads) so thumbnails never settled.
  const dirtyRef = useRef(false);

  // A fresh answer from the server is a fresh set of reasons: an asset whose
  // state moved is a new key and is offered again on its own.
  useEffect(() => {
    // Read so the effect re-runs on a refetch; the map is keyed on state that
    // the refetch may have changed, so nothing has to be cleared.
    void dataUpdatedAt;
  }, [dataUpdatedAt]);

  const pending = data?.data;
  // The whole pending list, one asset at a time, until it is done. There used
  // to be a budget of eight per poll here, and the poll is five minutes apart:
  // a library needing two hundred captures filled in at eight per five minutes,
  // which to anybody watching is a queue that captured a few and quit (#1554).
  // What protects the reader's page is the idle gate below -- the queue works
  // only while the browser is idle with the tab in front -- not an arbitrary
  // count.
  const next = current
    ? undefined
    : pending?.find(
        (a) => (attemptsRef.current.get(source.attemptKey(a)) ?? 0) < MAX_ATTEMPTS_PER_ASSET,
      );

  const idle = useIdleGate(!!next);

  // Fetch the next asset's content, but only once the browser has gone idle
  // with the tab in front. The fetch is deferred along with the capture
  // because it pulls the asset's whole body over the wire.
  useEffect(() => {
    if (!idle || !next) return;

    // Counted before the fetch, so a failure moves the queue on rather than
    // offering the same asset again the moment this effect re-runs. The count
    // is what lets it come back later rather than never.
    const key = source.attemptKey(next);
    attemptsRef.current.set(key, (attemptsRef.current.get(key) ?? 0) + 1);

    let cancelled = false;
    source
      .fetchContent(next)
      .then((res) => {
        if (!res.ok) throw new Error("fetch failed");
        return res.text();
      })
      .then((text) => {
        if (cancelled) return;
        setCurrent({ item: next, content: text, key });
      })
      .catch(() => {
        if (!cancelled) forceRecheck((n) => n + 1);
      });
    return () => {
      cancelled = true;
    };
  }, [idle, next]);

  const handleCaptured = useCallback(() => {
    // A captured reason is spent, whatever its attempt count: the server stops
    // offering the asset once it holds the image, and until that answer arrives
    // the queue must not pick the same one up again.
    setCurrent((c) => {
      if (c) attemptsRef.current.set(c.key, MAX_ATTEMPTS_PER_ASSET);
      return null;
    });
    // Deferred to the drain effect below so a run of captures triggers a
    // single refetch instead of one per capture.
    dirtyRef.current = true;
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
      void qc.invalidateQueries({ queryKey: source.invalidateKey });
    }
  }, [current, next, qc, source]);

  if (!current) return null;

  return (
    <Suspense fallback={null}>
      <ThumbnailGenerator
        assetId={source.id(current.item)}
        kind={source.kind}
        content={current.content}
        contentType={source.contentType(current.item)}
        version={source.version?.(current.item)}
        onCaptured={handleCaptured}
        onFailed={handleFailed}
      />
    </Suspense>
  );
}
