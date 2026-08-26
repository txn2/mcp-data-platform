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
 *
 * The "json" fragment covers both JSON families: every spelling of
 * newline-delimited JSON contains it ("application/x-ndjson",
 * "application/jsonl"), as do the vendor dialects ("application/vnd.acme+json").
 * The capturer draws each family in its own form -- see domKind in
 * components/ThumbnailGenerator.
 */
export function isThumbnailSupported(contentType: string): boolean {
  const ct = contentType.toLowerCase();
  return (
    ct.includes("html") ||
    ct.includes("jsx") ||
    ct.includes("svg") ||
    ct.includes("markdown") ||
    ct.includes("csv") ||
    ct.includes("json")
  );
}

/**
 * Returns true if the content type is rendered on a forced (non-themed)
 * background and therefore needs a separate dark-mode thumbnail. HTML, JSX, and
 * SVG carry their own colors, so they reuse the single light/default thumbnail
 * in both modes.
 */
export function isThemeable(contentType: string): boolean {
  const ct = contentType.toLowerCase();
  return ct.includes("markdown") || ct.includes("csv") || ct.includes("json");
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

/** The route an asset's stored tile is read through by default. */
export const ASSET_THUMBNAIL_BASE = "/api/v1/portal/assets";

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
 * identity of the image, so putting it in the query string makes a capture of a
 * rewritten asset a new URL and leaves an unchanged one cached. The server does
 * not read it.
 *
 * A recapture asked for on the asset's CURRENT version (#1497) is the one case
 * this does not cover: the version has not moved, so the URL has not either.
 * The panel that asks for it replaces its own browser's cached copy through a
 * reload-mode fetch; a browser holding the superseded image that did not ask
 * for the recapture keeps it until the hour is up.
 *
 * Returns undefined when no capture has been recorded, which is what tells the
 * card to show its content-type icon instead.
 *
 * The base is which route the reader is entitled to read the tile through, for
 * the reason collectionItemThumbnailSrc below takes one: the portal route's
 * view grant is owner, share and collection, with no admin arm, so an
 * administrator reading someone else's asset reads it through the admin route
 * (#1292).
 */
export function assetThumbnailSrc(
  asset: ThumbnailState & { id: string },
  isDark = false,
  base = ASSET_THUMBNAIL_BASE,
): string | undefined {
  const query = thumbnailQuery(
    {
      light: asset.thumbnail_s3_key,
      dark: asset.thumbnail_dark_s3_key,
      version: asset.thumbnail_version,
      darkVersion: asset.thumbnail_dark_version,
    },
    isDark,
  );
  if (query === undefined) return undefined;
  return `${base}/${asset.id}/thumbnail?${query}`;
}

/** The parts of a collection item that say which capture its tile shows. */
interface ItemThumbnailState {
  asset_id: string;
  asset_thumbnail_s3_key?: string;
  asset_thumbnail_dark_s3_key?: string;
  asset_thumbnail_version?: number;
  asset_thumbnail_dark_version?: number;
}

/**
 * The URL a collection item's tile is fetched from.
 *
 * A collection item names the same captures as the asset it points at, under
 * the item's own field names, and its tile is fetched from whichever asset
 * route the reader is entitled to: the portal route for a collection they own
 * or a share gives them, the admin route for an administrator reading someone
 * else's (#1292). Hence the base rather than the fixed portal path.
 *
 * Everything else -- which variant, and the version that makes a re-capture a
 * new URL -- is the question assetThumbnailSrc answers, asked here of the
 * item's spelling of the same fields.
 */
export function collectionItemThumbnailSrc(
  item: ItemThumbnailState,
  assetBase: string,
  isDark = false,
): string | undefined {
  const query = thumbnailQuery(
    {
      light: item.asset_thumbnail_s3_key,
      dark: item.asset_thumbnail_dark_s3_key,
      version: item.asset_thumbnail_version,
      darkVersion: item.asset_thumbnail_dark_version,
    },
    isDark,
  );
  if (query === undefined) return undefined;
  return `${assetBase}/${item.asset_id}/thumbnail?${query}`;
}

/** One asset's stored captures, however the surface asking names them. */
interface Captures {
  light?: string;
  dark?: string;
  version?: number;
  darkVersion?: number;
}

/**
 * The query string that selects a capture, or undefined when none was ever
 * taken -- which is what tells a card to show its content-type icon instead.
 *
 * The dark variant is asked for only when one was captured: a content type
 * that carries its own colors (HTML, JSX, SVG) stores a single image and
 * serves it in both modes, so its empty dark key means "use the light one",
 * not "no thumbnail".
 */
function thumbnailQuery(c: Captures, isDark: boolean): string | undefined {
  if (!c.light) return undefined;
  const dark = isDark && !!c.dark;
  const version = (dark ? c.darkVersion : c.version) ?? 0;
  return `${dark ? "variant=dark&" : ""}c=${version}`;
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
