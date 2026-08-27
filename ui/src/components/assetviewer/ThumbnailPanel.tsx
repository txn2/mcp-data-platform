import { useEffect, useRef, useState } from "react";
import { ImageOff, RefreshCw } from "lucide-react";
import type { Asset } from "@/api/portal/types";
import { useClearAssetThumbnail } from "@/api/portal/hooks/assets";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Button } from "@/components/ui/button";
import { useResolvedDark } from "@/stores/theme";
import {
  assetThumbnailSrc,
  isThumbnailSupported,
  thumbnailBehind,
  ASSET_THUMBNAIL_BASE,
  THUMBNAIL_SOURCE_LIMIT,
} from "@/lib/thumbnailSupport";

/**
 * The asset's tile, and the control that asks for it to be taken again.
 *
 * The tile is what everyone else sees of this asset -- in the grid, in a
 * collection, on a share page -- and until now the owner could neither see the
 * stored image beside the asset nor do anything about it when it was wrong. A
 * capture that rendered the artifact's own error branch is a valid PNG and was
 * stored like any other, and only a new version would replace it (#1497).
 *
 * Recapturing is the owner's, so this is absent for a reader who could not
 * store the result, and for an asset nothing rasterizes -- there is no tile to
 * be wrong about.
 */
export function ThumbnailPanel({
  asset,
  isOwner,
  assetApiBase = ASSET_THUMBNAIL_BASE,
  onRecapture,
}: {
  asset: Asset;
  isOwner: boolean;
  /**
   * Where this reader fetches an asset's stored tile from. An administrator
   * reading someone else's asset is refused the portal route -- the portal's
   * view grant is owner, share and collection, with no admin arm -- and reads
   * it through the admin one, which is the same reason a collection item's tile
   * takes a base (#1292). Without it the panel would report "No thumbnail
   * stored" for an image that exists, and the control beside that sentence
   * destroys it.
   */
  assetApiBase?: string;
  /**
   * Called once a clear this panel asked for has landed.
   *
   * Clearing a tile that is already cleared moves nothing on the asset row, so
   * the row cannot tell a second press from the first and the capturer the
   * viewer has already mounted would go on showing its finished result. This is
   * what says a press happened, so each one is its own capture (#1501).
   */
  onRecapture?: () => void;
}) {
  const clear = useClearAssetThumbnail();
  const isDark = useResolvedDark();
  const [failed, setFailed] = useState(false);

  const src = assetThumbnailSrc(asset, isDark, assetApiBase);
  // The same question the viewer asks to decide whether to mount a capturer, so
  // the panel says "being taken" exactly while one is wanted -- including the
  // moment after the clear lands and before the new image arrives.
  const capturing = thumbnailBehind(asset);
  const { markRecaptured, shown } = useReplaceCachedTile(src, capturing);
  // An image that would not load is a verdict on one URL, not on the asset. The
  // panel is pointed at a new one when a replacement lands, and without this the
  // placeholder that stood in for the broken tile outlives it and reports "No
  // thumbnail stored" for an asset that has one (#1501).
  useEffect(() => setFailed(false), [shown]);

  if (!capturable(asset, isOwner)) {
    return null;
  }

  return (
    <div className="border-t pt-4" data-testid="asset-thumbnail-panel">
      <SectionCard
        title="Thumbnail"
        action={
          <Button
            variant="outline"
            size="xs"
            onClick={() => {
              setFailed(false);
              clear.mutate(asset.id, {
                onSuccess: () => {
                  markRecaptured();
                  onRecapture?.();
                },
              });
            }}
            // Pressable even while a capture is wanted: a capture whose
            // references cannot load is discarded every time, so an asset in
            // that state would otherwise leave its owner a control they could
            // never use again.
            disabled={clear.isPending}
            title="Discard this image and take it again"
          >
            <RefreshCw /> Recapture
          </Button>
        }
      >
        <div className="space-y-2">
          {shown && !failed ? (
            <img
              src={shown}
              alt={`Thumbnail for ${asset.name}`}
              className="w-full rounded border bg-muted object-cover"
              onError={() => setFailed(true)}
            />
          ) : (
            <div className="flex h-24 items-center justify-center gap-2 rounded border border-dashed text-xs text-muted-foreground">
              <ImageOff className="size-4" aria-hidden />
              {capturing ? "Being taken" : "No thumbnail stored"}
            </div>
          )}
          <p className="text-xs text-muted-foreground">{explain(capturing)}</p>
          {clear.isError && (
            <p className="text-xs text-destructive">Could not discard the stored image.</p>
          )}
        </div>
      </SectionCard>
    </div>
  );
}

/** Whether this reader has a tile to act on at all. */
function capturable(asset: Asset, isOwner: boolean): boolean {
  return (
    isOwner &&
    isThumbnailSupported(asset.content_type) &&
    asset.size_bytes <= THUMBNAIL_SOURCE_LIMIT
  );
}

/** What the panel says about the image it is showing, or about the one coming. */
function explain(capturing: boolean): string {
  return capturing
    ? "A new capture is taken in the browser the next time this page is idle. A capture whose referenced files did not load is discarded, so an asset naming something that is gone keeps waiting."
    : "This image is what the asset shows on cards, in collections, and on a share page.";
}

/**
 * Keeps this browser's copy of the tile from outliving a recapture.
 *
 * A recapture stores the new image at the same asset version, so the URL is the
 * one the browser already holds a cached copy of and the thumbnail route is
 * cacheable for an hour: without this the person who pressed the button keeps
 * seeing the picture they asked to replace. A reload-mode fetch replaces that
 * cache entry once the replacement has landed, and the counter it returns takes
 * the panel's own image off the in-memory copy so the new tile is on screen at
 * once.
 *
 * Returns the URL to render and the callback the clear reports success to.
 */
function useReplaceCachedTile(src: string | undefined, capturing: boolean) {
  // Set when a clear this panel asked for succeeds, and spent below once the
  // replacement capture has landed.
  const [recaptured, setRecaptured] = useState(false);
  const [cacheBust, setCacheBust] = useState(0);
  // Whether the cleared row has reached this panel yet.
  //
  // The clear's own invalidation is what delivers it, so for the render the
  // press returns on the asset still carries the image the reader asked to be
  // rid of. Spending the trigger there reload-fetched that image's URL and left
  // nothing to spend when the replacement actually landed -- and put the panel
  // on a cache-busted URL of a row the server had already cleared, which 404s
  // and reports "No thumbnail stored" for an asset that goes on to have one
  // (#1501). A capture cannot land before the cleared row does, because the
  // cleared row is what starts it.
  const clearLanded = useRef(false);
  useEffect(() => {
    if (capturing) clearLanded.current = true;
  }, [capturing]);

  useEffect(() => {
    if (!recaptured || capturing || !clearLanded.current || !src) return;
    setRecaptured(false);
    clearLanded.current = false;
    void fetch(src, { cache: "reload", credentials: "include" })
      .catch(() => {})
      .finally(() => setCacheBust((n) => n + 1));
  }, [recaptured, capturing, src]);

  return {
    markRecaptured: () => setRecaptured(true),
    shown: src && cacheBust > 0 ? `${src}&r=${cacheBust}` : src,
  };
}
