import { Suspense, lazy, useCallback, useEffect, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { authedFetch } from "@/api/authed";
import { failCapture, type CaptureFailure } from "@/lib/captureFailure";
import {
  captureFamily,
  contentPath,
  type ThumbnailSubject,
  type ThumbnailTarget,
} from "@/lib/thumbnailSupport";

// The capturer carries html2canvas, the markdown renderer and the diagram
// engine. A reader who never presses Recapture should not pay for it, so it
// arrives on demand -- the same reason the queue and the asset viewer load it
// this way (#1351).
const ThumbnailGenerator = lazy(() =>
  import("@/components/ThumbnailGenerator").then((m) => ({ default: m.ThumbnailGenerator })),
);

/**
 * One capture, started because somebody asked for it.
 *
 * Recapture used to be "discard the stored image, and some capturer will take
 * another". On a file that had never been captured there was nothing to
 * discard, so the press moved no row state and nothing happened: for an asset
 * the only effect was a remount in the viewer, and for a managed resource --
 * whose viewer mounts no capturer at all -- there was no effect whatsoever
 * (#1753). Here the press runs the capture itself, for both kinds, and says how
 * it went.
 *
 * Mounted keyed on the press, so each one is its own capture rather than a
 * reused finished result.
 */
export function PanelCapture({
  subject,
  onCaptured,
  onFailed,
}: {
  subject: ThumbnailSubject;
  onCaptured: () => void;
  onFailed: (failure: CaptureFailure) => void;
}) {
  const qc = useQueryClient();
  const { kind, id } = subject.target;
  // Stable, because the capturer keys its own effects on what it is handed: a
  // fresh identity each render re-arms its capture timeout. The target is
  // rebuilt by every render of the panel above, so this depends on its parts.
  const captured = useCallback(() => {
    refreshTile(qc, { kind, id });
    onCaptured();
  }, [qc, kind, id, onCaptured]);
  const content = useCaptureContent(subject, onFailed);

  if (content === null) return null;

  return (
    <Suspense fallback={null}>
      <ThumbnailGenerator
        assetId={subject.target.id}
        kind={subject.target.kind}
        content={content}
        contentType={subject.contentType}
        version={subject.version}
        onCaptured={captured}
        onFailed={onFailed}
      />
    </Suspense>
  );
}

/**
 * The document the capture draws, read from the target's own content route.
 *
 * Null until it is in hand. A raster image is never fetched here: its bytes are
 * not text, and the capturer's image path reads them itself through an element
 * so the browser does the decoding (#1554) -- so the empty string it ignores is
 * what it is handed.
 */
function useCaptureContent(
  subject: ThumbnailSubject,
  onFailed: (failure: CaptureFailure) => void,
): string | null {
  const [content, setContent] = useState<string | null>(
    captureFamily(subject.contentType) === "image" ? "" : null,
  );

  useEffect(() => {
    if (content !== null) return;
    let cancelled = false;
    void (async () => {
      try {
        const res = await authedFetch(contentPath(subject.target));
        if (!res.ok) throw new Error(`the content route answered ${res.status}`);
        const text = await res.text();
        if (!cancelled) setContent(text);
      } catch (err) {
        if (!cancelled) failCapture(subject.target, onFailed, "content", err);
      }
    })();
    return () => {
      cancelled = true;
    };
    // Read once per mount: the component is keyed on the press, so a new
    // capture is a new mount rather than a re-read here.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return content;
}

/**
 * Put the new tile in front of the reader.
 *
 * The same invalidations the clear mutations make, because the same rows say
 * what the tile is: the single-item query the viewer reads, the listing every
 * card is drawn from, and -- for a resource -- the pending list, which no
 * longer holds this one.
 */
function refreshTile(qc: ReturnType<typeof useQueryClient>, target: ThumbnailTarget): void {
  if (target.kind === "resource") {
    void qc.invalidateQueries({ queryKey: ["resources", target.id] });
    void qc.invalidateQueries({ queryKey: ["resources"] });
    void qc.invalidateQueries({ queryKey: ["resource-thumbnails-pending"] });
    return;
  }
  void qc.invalidateQueries({ queryKey: ["asset", target.id] });
  void qc.invalidateQueries({ queryKey: ["assets"] });
  void qc.invalidateQueries({ queryKey: ["thumbnails-pending"] });
}
