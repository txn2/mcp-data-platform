import { useEffect, useState } from "react";
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
              clear.mutate(asset.id, { onSuccess: markRecaptured });
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

  useEffect(() => {
    if (!recaptured || capturing || !src) return;
    setRecaptured(false);
    void fetch(src, { cache: "reload", credentials: "include" })
      .catch(() => {})
      .finally(() => setCacheBust((n) => n + 1));
  }, [recaptured, capturing, src]);

  return {
    markRecaptured: () => setRecaptured(true),
    shown: src && cacheBust > 0 ? `${src}&r=${cacheBust}` : src,
  };
}
