import { useState, useEffect, useCallback, useRef } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { apiFetchRaw } from "@/api/portal/client";
import type { Asset } from "@/api/portal/types";
import { isThumbnailSupported } from "@/lib/thumbnail";
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

  // Build the queue of assets needing thumbnails, excluding already-processed ones
  useEffect(() => {
    const needsThumbnail = assets.filter(
      (a) =>
        !a.thumbnail_s3_key &&
        isThumbnailSupported(a.content_type) &&
        !processedRef.current.has(a.id),
    );
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
    void qc.invalidateQueries({ queryKey: ["assets"] });
    advance();
  }, [qc, advance]);

  const handleFailed = useCallback(() => {
    // Move on to the next asset without invalidating
    advance();
  }, [advance]);

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
