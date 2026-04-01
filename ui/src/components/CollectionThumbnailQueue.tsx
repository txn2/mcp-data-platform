import { useState, useEffect, useRef } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { apiFetch, apiFetchRaw } from "@/api/portal/client";
import type { Collection, CollectionResponse } from "@/api/portal/types";
import { THUMB_WIDTH, THUMB_HEIGHT } from "@/lib/thumbnail";

/** Maximum number of asset thumbnails to compose into the mosaic. */
const MAX_TILES = 4;

// ---------------------------------------------------------------------------
// Queue component — mount on the collections list page
// ---------------------------------------------------------------------------

interface QueueProps {
  collections: Collection[];
}

/**
 * Background queue that auto-generates collection thumbnails by fetching
 * full collection data (with sections/items), then compositing a mosaic
 * of contained asset thumbnails. Processes one collection at a time.
 * Renders nothing visible.
 */
export function CollectionThumbnailQueue({ collections }: QueueProps) {
  const qc = useQueryClient();
  const [queue, setQueue] = useState<Collection[]>([]);
  const [busy, setBusy] = useState(false);
  const processedRef = useRef(new Set<string>());

  // Build queue of collections needing thumbnails
  useEffect(() => {
    const needs = collections.filter(
      (c) => !c.thumbnail_s3_key && !processedRef.current.has(c.id),
    );
    setQueue(needs);
  }, [collections]);

  // Process next item
  useEffect(() => {
    if (busy || queue.length === 0) return;
    const next = queue[0]!;
    processedRef.current.add(next.id);
    setBusy(true);

    processCollection(next.id)
      .then((uploaded) => {
        if (uploaded) {
          void qc.invalidateQueries({ queryKey: ["collections"] });
          void qc.invalidateQueries({ queryKey: ["collection"] });
        }
      })
      .catch(() => {
        // skip on error
      })
      .finally(() => {
        setBusy(false);
        setQueue((q) => q.slice(1));
      });
  }, [queue, busy, qc]);

  return null;
}

// ---------------------------------------------------------------------------
// Single-collection generator — mount on collection viewer page
// ---------------------------------------------------------------------------

interface GeneratorProps {
  collection: Collection;
}

/**
 * Auto-generates a thumbnail for a single collection that already has full
 * section/item data loaded. Runs once on mount.
 */
export function CollectionThumbnailGenerator({ collection }: GeneratorProps) {
  const qc = useQueryClient();
  const attemptedRef = useRef(false);

  useEffect(() => {
    if (attemptedRef.current) return;
    if (collection.thumbnail_s3_key) return;
    if (!hasAssetThumbnails(collection)) return;

    attemptedRef.current = true;

    generateAndUpload(collection)
      .then((uploaded) => {
        if (uploaded) {
          void qc.invalidateQueries({ queryKey: ["collections"] });
          void qc.invalidateQueries({ queryKey: ["collection"] });
        }
      })
      .catch(() => {
        // silently skip
      });
  }, [collection, qc]);

  return null;
}

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

/** Fetch full collection data (with sections/items), generate mosaic, upload. */
async function processCollection(collectionId: string): Promise<boolean> {
  const full = await apiFetch<CollectionResponse>(`/collections/${collectionId}`);
  if (!full || full.thumbnail_s3_key) return false;
  if (!hasAssetThumbnails(full)) return false;
  return generateAndUpload(full);
}

/** Generate mosaic and upload it. Returns true if uploaded. */
async function generateAndUpload(collection: Collection): Promise<boolean> {
  const blob = await generateCollectionThumbnail(collection);
  if (!blob) return false;
  const res = await apiFetchRaw(`/collections/${collection.id}/thumbnail`, {
    method: "PUT",
    headers: { "Content-Type": "image/png" },
    body: blob,
  });
  return res.ok;
}

function hasAssetThumbnails(collection: Collection): boolean {
  return (collection.sections ?? []).some((s) =>
    (s.items ?? []).some((item) => item.asset_thumbnail_s3_key),
  );
}

function getAssetIdsWithThumbnails(collection: Collection): string[] {
  const ids: string[] = [];
  for (const section of collection.sections ?? []) {
    for (const item of section.items ?? []) {
      if (item.asset_thumbnail_s3_key) {
        ids.push(item.asset_id);
        if (ids.length >= MAX_TILES) return ids;
      }
    }
  }
  return ids;
}

async function loadAssetThumbnail(assetId: string): Promise<HTMLImageElement> {
  const res = await apiFetchRaw(`/assets/${assetId}/thumbnail`);
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  const blob = await res.blob();
  const objectUrl = URL.createObjectURL(blob);

  return new Promise<HTMLImageElement>((resolve, reject) => {
    const img = new Image();
    img.onload = () => {
      URL.revokeObjectURL(objectUrl);
      resolve(img);
    };
    img.onerror = () => {
      URL.revokeObjectURL(objectUrl);
      reject(new Error("Image load failed"));
    };
    img.src = objectUrl;
  });
}

async function generateCollectionThumbnail(
  collection: Collection,
): Promise<Blob | null> {
  const assetIds = getAssetIdsWithThumbnails(collection);
  if (assetIds.length === 0) return null;

  const images: HTMLImageElement[] = [];
  for (const id of assetIds) {
    try {
      images.push(await loadAssetThumbnail(id));
    } catch {
      // skip failed loads
    }
  }
  if (images.length === 0) return null;

  const canvas = document.createElement("canvas");
  canvas.width = THUMB_WIDTH;
  canvas.height = THUMB_HEIGHT;
  const ctx = canvas.getContext("2d")!;

  ctx.fillStyle = "#1e293b";
  ctx.fillRect(0, 0, THUMB_WIDTH, THUMB_HEIGHT);

  const gap = 4;

  if (images.length === 1) {
    drawCover(ctx, images[0]!, 0, 0, THUMB_WIDTH, THUMB_HEIGHT);
  } else if (images.length === 2) {
    const half = (THUMB_WIDTH - gap) / 2;
    drawCover(ctx, images[0]!, 0, 0, half, THUMB_HEIGHT);
    drawCover(ctx, images[1]!, half + gap, 0, half, THUMB_HEIGHT);
  } else if (images.length === 3) {
    const halfH = (THUMB_HEIGHT - gap) / 2;
    const halfW = (THUMB_WIDTH - gap) / 2;
    drawCover(ctx, images[0]!, 0, 0, THUMB_WIDTH, halfH);
    drawCover(ctx, images[1]!, 0, halfH + gap, halfW, halfH);
    drawCover(ctx, images[2]!, halfW + gap, halfH + gap, halfW, halfH);
  } else {
    const halfW = (THUMB_WIDTH - gap) / 2;
    const halfH = (THUMB_HEIGHT - gap) / 2;
    drawCover(ctx, images[0]!, 0, 0, halfW, halfH);
    drawCover(ctx, images[1]!, halfW + gap, 0, halfW, halfH);
    drawCover(ctx, images[2]!, 0, halfH + gap, halfW, halfH);
    drawCover(ctx, images[3]!, halfW + gap, halfH + gap, halfW, halfH);
  }

  return new Promise<Blob | null>((resolve) => {
    canvas.toBlob((blob) => resolve(blob), "image/png");
  });
}

function drawCover(
  ctx: CanvasRenderingContext2D,
  img: HTMLImageElement,
  x: number,
  y: number,
  w: number,
  h: number,
) {
  const imgRatio = img.naturalWidth / img.naturalHeight;
  const boxRatio = w / h;

  let sx = 0;
  let sy = 0;
  let sw = img.naturalWidth;
  let sh = img.naturalHeight;

  if (imgRatio > boxRatio) {
    sw = img.naturalHeight * boxRatio;
    sx = (img.naturalWidth - sw) / 2;
  } else {
    sh = img.naturalWidth / boxRatio;
  }

  ctx.drawImage(img, sx, sy, sw, sh, x, y, w, h);
}
