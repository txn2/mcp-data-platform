import { useEffect, useRef } from "react";
import { contentPath, downscaleImage, uploadThumbnail, type ThumbnailTarget } from "@/lib/thumbnail";
import { failCapture, type CaptureFailure } from "@/lib/captureFailure";

/**
 * A raster image, downscaled onto a canvas.
 *
 * The source is read from the target's own content route rather than from the
 * `content` prop: that prop is text, and an image read as text is corrupt
 * before it reaches here. The element fetches it itself, which also lets the
 * browser do the decoding it is good at.
 *
 * Drawn to cover the tile, matching how a tile displays it, so the stored image
 * is what the card shows rather than something it then has to crop again.
 */
export function ImageCapture({
  target,
  contentType,
  version,
  onCaptured,
  onFailed,
}: {
  target: ThumbnailTarget;
  contentType: string;
  version?: number;
  onCaptured?: () => void;
  /** Why no tile was produced (#1752). */
  onFailed?: (failure: CaptureFailure) => void;
}) {
  const capturedRef = useRef(false);

  useEffect(() => {
    if (capturedRef.current) return;
    capturedRef.current = true;

    let cancelled = false;
    const src = contentPath(target);

    void (async () => {
      let blob: Blob;
      try {
        blob = await downscaleImage(src, contentType);
      } catch (err) {
        // The bytes could not be read or the browser could not decode them;
        // either way the reason is the only record this capture leaves (#1752).
        if (!cancelled) failCapture(target, onFailed, "content", err);
        return;
      }
      if (cancelled) return;
      try {
        await uploadThumbnail(target, blob, "light", version);
      } catch (err) {
        if (!cancelled) failCapture(target, onFailed, "upload", err);
        return;
      }
      if (!cancelled) onCaptured?.();
    })();

    return () => {
      cancelled = true;
    };
  }, [target, contentType, version, onCaptured, onFailed]);

  return null;
}
