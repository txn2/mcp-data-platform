import { useState, useEffect, useCallback } from "react";
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
 */
export function ThumbnailQueue({ assets }: Props) {
  const qc = useQueryClient();
  const [queue, setQueue] = useState<Asset[]>([]);
  const [current, setCurrent] = useState<{ asset: Asset; content: string } | null>(null);

  // Build the queue of assets needing thumbnails
  useEffect(() => {
    const needsThumbnail = assets.filter(
      (a) => !a.thumbnail_s3_key && isThumbnailSupported(a.content_type),
    );
    setQueue(needsThumbnail);
    setCurrent(null);
  }, [assets]);

  // Process the next item in the queue
  useEffect(() => {
    if (current || queue.length === 0) return;

    const next = queue[0]!;

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

  const handleCaptured = useCallback(() => {
    void qc.invalidateQueries({ queryKey: ["assets"] });
    setCurrent(null);
    setQueue((q) => q.slice(1));
  }, [qc]);

  if (!current) return null;

  return (
    <ThumbnailGenerator
      assetId={current.asset.id}
      content={current.content}
      contentType={current.asset.content_type}
      onCaptured={handleCaptured}
    />
  );
}
