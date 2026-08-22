/**
 * Which content types get a thumbnail, and how big a document is worth
 * capturing.
 *
 * These are separate from lib/thumbnail because the surfaces that ask the
 * questions — the assets list deciding what to queue, the asset viewer
 * deciding whether to offer a capture — must be able to ask them without
 * pulling in the capturer. lib/thumbnail imports html2canvas and the JSX
 * transformer, some 200 KB that only the capture itself needs (#1351).
 */

/** Thumbnail image dimensions, in CSS pixels. */
export const THUMB_WIDTH = 400;
export const THUMB_HEIGHT = 300;

/**
 * Largest asset body a thumbnail is captured from, in bytes.
 *
 * Capture renders the asset a second time and rasterizes it on the main
 * thread, so its cost tracks the size of the document. A large interactive
 * dashboard is exactly the asset whose thumbnail would be most useful and
 * exactly the one whose capture stalls the page it was requested from
 * (#1351); above this it keeps the placeholder icon instead. The limit is
 * generous — a dashboard of a few hundred KB still gets a thumbnail — because
 * the point is to exclude the outliers, not the common case.
 */
export const THUMBNAIL_SOURCE_LIMIT = 1024 * 1024; // 1 MB

/**
 * Returns true if the content type supports thumbnail generation.
 */
export function isThumbnailSupported(contentType: string): boolean {
  const ct = contentType.toLowerCase();
  return ct.includes("html") || ct.includes("jsx") || ct.includes("svg") || ct.includes("markdown") || ct.includes("csv");
}

/**
 * Returns true if the content type is rendered on a forced (non-themed)
 * background and therefore needs a separate dark-mode thumbnail. HTML, JSX, and
 * SVG carry their own colors, so they reuse the single light/default thumbnail
 * in both modes.
 */
export function isThemeable(contentType: string): boolean {
  const ct = contentType.toLowerCase();
  return ct.includes("markdown") || ct.includes("csv");
}

/**
 * Thumbnail filenames written before the leading-dot rename.
 *
 * They are ordinary files to Trino, which reads every non-hidden object under
 * an external location as CSV rows, so a CSV asset thumbnailed under them
 * cannot be registered as a table until they are replaced. An asset carrying
 * one is queued for capture again even though it already has a thumbnail; the
 * capture endpoint removes the object it supersedes.
 */
const LEGACY_THUMBNAIL_FILENAMES = ["thumbnail.png", "thumbnail_dark.png"];

/**
 * Returns true if a recorded thumbnail key uses one of the legacy filenames.
 */
export function isLegacyThumbnailKey(key: string): boolean {
  const name = key.slice(key.lastIndexOf("/") + 1);
  return LEGACY_THUMBNAIL_FILENAMES.includes(name);
}

/** The parts of an asset that say whether its capture is current. */
interface ThumbnailState {
  content_type: string;
  current_version: number;
  thumbnail_s3_key?: string;
  thumbnail_dark_s3_key?: string;
  thumbnail_version: number;
  thumbnail_dark_version: number;
}

/**
 * The URL an asset's thumbnail is served from, with the capture's version on it.
 *
 * The endpoint answers under one URL per asset and variant, and its response is
 * cacheable for an hour, so a re-captured image would not reach a browser that
 * already holds the old one until that hour was up -- which is most of the point
 * of refreshing it (#1431). The version the capture was taken from is the
 * identity of the image, so putting it in the query string makes a new capture a
 * new URL and leaves an unchanged one cached. The server does not read it.
 *
 * Returns undefined when no capture has been recorded, which is what tells the
 * card to show its content-type icon instead.
 */
export function assetThumbnailSrc(
  asset: ThumbnailState & { id: string },
  isDark = false,
): string | undefined {
  if (!asset.thumbnail_s3_key) return undefined;
  const dark = isDark && !!asset.thumbnail_dark_s3_key;
  const version = dark ? asset.thumbnail_dark_version : asset.thumbnail_version;
  const variant = dark ? "variant=dark&" : "";
  return `/api/v1/portal/assets/${asset.id}/thumbnail?${variant}c=${version}`;
}

/**
 * Whether this asset's thumbnail has fallen behind what the asset now holds:
 * never captured, captured from an earlier version, or written under a legacy
 * filename that has to be replaced before the asset can be registered as a
 * table (#1327).
 *
 * A version write leaves the recorded capture in place, so an asset that has
 * been rewritten still shows an image — of the body it had one or more versions
 * ago. This is the question that says so. The dark variant is asked only of the
 * types that carry one; a type with its own colors serves the single capture in
 * both modes, so its empty dark key is not a gap.
 *
 * The refresh queue asks the same question of every asset at once, in SQL
 * (internal/portal/portalstore); this copy is for the one asset on screen.
 */
export function thumbnailBehind(a: ThumbnailState): boolean {
  const light = a.thumbnail_s3_key ?? "";
  const dark = a.thumbnail_dark_s3_key ?? "";
  const lightBehind = !light || a.thumbnail_version < a.current_version || isLegacyThumbnailKey(light);
  const darkBehind =
    isThemeable(a.content_type) &&
    (!dark || a.thumbnail_dark_version < a.current_version || isLegacyThumbnailKey(dark));
  return lightBehind || darkBehind;
}
