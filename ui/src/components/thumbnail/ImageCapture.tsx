import { useEffect, useRef } from "react";
import { contentPath, downscaleImage, uploadThumbnail, type ThumbnailTarget } from "@/lib/thumbnail";

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
  onFailed?: () => void;
}) {
  const capturedRef = useRef(false);

  useEffect(() => {
    if (capturedRef.current) return;
    capturedRef.current = true;

    let cancelled = false;
    const src = contentPath(target);

    void (async () => {
      try {
        const blob = await downscaleImage(src, contentType);
        if (cancelled) return;
        await uploadThumbnail(target, blob, "light", version);
        if (!cancelled) onCaptured?.();
      } catch {
        if (!cancelled) onFailed?.();
      }
    })();

    return () => {
      cancelled = true;
    };
  }, [target, contentType, version, onCaptured, onFailed]);

  return null;
}
