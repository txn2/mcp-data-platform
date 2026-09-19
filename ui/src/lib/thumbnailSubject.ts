import {
  ASSET_THUMBNAIL_BASE,
  RESOURCE_THUMBNAIL_BASE,
  assetCaptures,
  assetThumbnailFailure,
  isLegacyThumbnailKey,
  isThemeable,
  resourceCaptures,
  resourceThumbnailBehind,
  resourceThumbnailFailure,
  thumbnailBehind,
  type Captures,
  type ResourceThumbnailState,
  type ThumbnailState,
  type ThumbnailTarget,
} from "./thumbnailSupport";

/**
 * One target's tile as the panel that shows and re-takes it needs to see it.
 *
 * The panel is over a target rather than over an asset (#1568): a resource
 * owner had no picture of their tile and no way to replace one that was wrong,
 * which is the same gap #1497 closed for assets. Everything that differs
 * between the two kinds -- where the captures are recorded, what dates them,
 * which route reads them -- is resolved into this before the panel sees it.
 */
export interface ThumbnailSubject {
  target: ThumbnailTarget;
  /** What the tile is of, for the image's alt text. */
  name: string;
  contentType: string;
  sizeBytes: number;
  captures: Captures;
  /**
   * Whether the renderer is drawing a tile right now: the stored one is missing
   * or behind the file, and no failure stands against the file as it is.
   */
  behind: boolean;
  /** Why the renderer could not draw this file, while that still stands. */
  failure?: string;
  /** What a standing failure applies to; absent when there is none. */
  failedPart?: FailedPart;
  /** Which route this reader is entitled to read the tile through. */
  base: string;
}

/**
 * What a standing failure applies to, which is not always the whole file
 * (#1791).
 *
 * The renderer draws a file's light tile first, stores it, then draws the dark
 * one, and records the failure of whichever stopped it beside the tiles it had
 * already stored. So a failure can stand beside a tile, and the tile says which
 * part failed: a light tile of this very version means only the dark one could
 * not be drawn; a tile of an earlier version means this version could not be
 * drawn and the reader is looking at the one before; no tile means the file
 * never had one.
 */
export type FailedPart = "dark" | "version" | "file";

function failedPart(lightCurrent: boolean, hasLight: boolean, contentType: string): FailedPart {
  if (lightCurrent) return isThemeable(contentType) ? "dark" : "file";
  return hasLight ? "version" : "file";
}

/** One asset as a thumbnail subject. */
export function assetSubject(
  asset: ThumbnailState & { id: string; name: string; size_bytes: number },
  base = ASSET_THUMBNAIL_BASE,
): ThumbnailSubject {
  return {
    target: { kind: "asset", id: asset.id },
    name: asset.name,
    contentType: asset.content_type,
    sizeBytes: asset.size_bytes,
    captures: assetCaptures(asset),
    ...drawState(thumbnailBehind(asset), assetThumbnailFailure(asset), () => {
      const light = asset.thumbnail_s3_key ?? "";
      const current = !!light && asset.thumbnail_version >= asset.current_version && !isLegacyThumbnailKey(light);
      return failedPart(current, !!light, asset.content_type);
    }),
    base,
  };
}

/** One managed resource as a thumbnail subject. */
export function resourceSubject(
  resource: ResourceThumbnailState & { display_name: string; size_bytes: number },
): ThumbnailSubject {
  return {
    target: { kind: "resource", id: resource.id },
    name: resource.display_name,
    contentType: resource.mime_type,
    sizeBytes: resource.size_bytes,
    captures: resourceCaptures(resource),
    ...drawState(resourceThumbnailBehind(resource), resourceThumbnailFailure(resource), () => {
      const light = resource.thumbnail_s3_key ?? "";
      const at = resource.thumbnail_captured_at;
      const current = !!light && !!at && Date.parse(at) >= Date.parse(resource.updated_at);
      return failedPart(current, !!light, resource.mime_type);
    }),
    base: RESOURCE_THUMBNAIL_BASE,
  };
}

/**
 * A tile behind its file is being drawn unless a failure stands against it,
 * in which case what the failure applies to is worked out too.
 */
function drawState(
  behind: boolean,
  failure: string | undefined,
  part: () => FailedPart,
): { behind: boolean; failure?: string; failedPart?: FailedPart } {
  return failure ? { behind: false, failure, failedPart: part() } : { behind };
}
