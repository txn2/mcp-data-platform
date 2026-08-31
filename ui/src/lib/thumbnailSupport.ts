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
 * How the capturer draws one family. The dispatch in
 * components/ThumbnailGenerator is over this rather than over its own list of
 * content types, so a family cannot be offered for capture by a surface that
 * nothing can draw -- which is what let a JSX resource sit on the pending list
 * forever (#1568).
 *
 * "iframe" is content that carries its own document (HTML, JSX); "image" is not
 * rendered at all but downscaled.
 */
export type CaptureFamily = "iframe" | "svg" | "csv" | "json" | "markdown" | "text" | "image";

/** One capturable family: how a content type is recognized, and what is done with it. */
interface CapturableFamily {
  /**
   * The fragment of the media type that names this family. A fragment rather
   * than an exact type because a stored type carries parameters and vendor
   * prefixes ("text/markdown; charset=utf-8", "application/vnd.acme+json").
   */
  fragment: string;
  family: CaptureFamily;
  /**
   * Whether the family is drawn on a forced background and so captured twice,
   * once per color scheme. A family carrying its own colors stores one image
   * and serves it in both modes.
   */
  themeable: boolean;
}

/**
 * Every content family that gets a thumbnail, in the order a content type is
 * matched against.
 *
 * This is the one browser-side definition. The rule used to be written out in
 * four places -- two Go stores, the browser gate here, and the capturer's own
 * dispatch -- and the four stopped agreeing (#1568). The one Go definition is
 * internal/thumbtypes, and a Go test reads the fragments and themeable flags out
 * of this table and fails when the two languages disagree.
 *
 * Order is part of the definition, because the first fragment a type contains
 * wins.
 *
 * The "json" fragment covers both JSON families: every spelling of
 * newline-delimited JSON contains it ("application/x-ndjson",
 * "application/jsonl"), as do the vendor dialects. Which of the two is drawn is
 * a refinement the capturer makes inside the family. "text/plain" is spelled in
 * full because the bare word is a substring of "text/html", "text/csv" and
 * "text/markdown", each of which is drawn differently.
 *
 * The raster families are named one by one rather than as "image/". A capture
 * downscales a raster image by decoding it in the browser, and TIFF, HEIC and
 * PSD are images no browser decodes: offering one is offering work that fails
 * every time, and the server's pending query is a bounded window, so a library
 * of them would starve the documents behind them of a capture that could
 * actually complete.
 */
const CAPTURABLE_FAMILIES: CapturableFamily[] = [
  { fragment: "html", family: "iframe", themeable: false },
  { fragment: "jsx", family: "iframe", themeable: false },
  { fragment: "svg", family: "svg", themeable: false },
  { fragment: "markdown", family: "markdown", themeable: true },
  { fragment: "csv", family: "csv", themeable: true },
  { fragment: "json", family: "json", themeable: true },
  { fragment: "text/plain", family: "text", themeable: true },
  { fragment: "image/png", family: "image", themeable: false },
  { fragment: "image/jpeg", family: "image", themeable: false },
  { fragment: "image/gif", family: "image", themeable: false },
  { fragment: "image/webp", family: "image", themeable: false },
  { fragment: "image/avif", family: "image", themeable: false },
  { fragment: "image/bmp", family: "image", themeable: false },
  { fragment: "image/x-icon", family: "image", themeable: false },
  { fragment: "image/vnd.microsoft.icon", family: "image", themeable: false },
];

/** The family a content type is drawn as, or null when nothing draws it. */
export function captureFamily(contentType: string): CaptureFamily | null {
  return matchFamily(contentType)?.family ?? null;
}

/** Returns true if the content type supports thumbnail generation. */
export function isThumbnailSupported(contentType: string): boolean {
  return matchFamily(contentType) !== undefined;
}

/**
 * Returns true if the content type is rendered on a forced (non-themed)
 * background and therefore needs a separate dark-mode thumbnail. HTML, JSX, SVG
 * and a raster image carry their own colors, so they reuse the single
 * light/default thumbnail in both modes.
 */
export function isThemeable(contentType: string): boolean {
  return matchFamily(contentType)?.themeable ?? false;
}

/** The first family whose fragment the type contains. */
function matchFamily(contentType: string): CapturableFamily | undefined {
  const ct = contentType.toLowerCase();
  return CAPTURABLE_FAMILIES.find((f) => ct.includes(f.fragment));
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

/**
 * What a capture belongs to: a portal asset, or a managed resource (#1554).
 *
 * The capturer is the same for both -- nothing on a server can rasterize a
 * document -- so the kind travels with the id rather than being forked into a
 * second component. It lives here rather than in lib/thumbnail because the
 * surfaces that only name a target, rather than capture one, must be able to do
 * it without pulling in html2canvas.
 */
export interface ThumbnailTarget {
  kind: "asset" | "resource";
  id: string;
}

/** The routes a kind's stored tiles are read through by default. */
export const ASSET_THUMBNAIL_BASE = "/api/v1/portal/assets";
export const RESOURCE_THUMBNAIL_BASE = "/api/v1/resources";

/** The collection route a target's tile is read through, absent an override. */
export function thumbnailBase(target: ThumbnailTarget): string {
  return target.kind === "resource" ? RESOURCE_THUMBNAIL_BASE : ASSET_THUMBNAIL_BASE;
}

/**
 * One target's stored captures, and what makes a re-capture a new URL.
 *
 * The stamp is the version an asset's capture was taken from and the moment a
 * resource's was taken: a resource row carries no version, so its captures are
 * dated against the file's own updated_at instead (#1554). Both are opaque here
 * -- all this does with a stamp is put it in the query string.
 */
export interface Captures {
  light?: string;
  dark?: string;
  stamp?: string | number;
  darkStamp?: string | number;
}

/**
 * The URL a target's thumbnail is served from, with the capture's stamp on it.
 *
 * The endpoint answers under one URL per target and variant, and its response is
 * cacheable for an hour, so a re-captured image would not reach a browser that
 * already holds the old one until that hour was up -- which is most of the point
 * of refreshing it (#1431). The stamp the capture was taken at is the identity of
 * the image, so putting it in the query string makes a capture of a rewritten
 * document a new URL and leaves an unchanged one cached. The server does not
 * read it.
 *
 * A recapture asked for on the target's CURRENT stamp (#1497) is the one case
 * this does not cover: the stamp has not moved, so the URL has not either. The
 * panel that asks for it replaces its own browser's cached copy through a
 * reload-mode fetch; a browser holding the superseded image that did not ask for
 * the recapture keeps it until the hour is up.
 *
 * Returns undefined when no capture has been recorded, which is what tells the
 * card to show its content-type icon instead.
 *
 * The base is which route the reader is entitled to read the tile through: the
 * portal route's view grant is owner, share and collection, with no admin arm,
 * so an administrator reading someone else's asset reads it through the admin
 * route (#1292).
 */
export function thumbnailSrc(
  target: ThumbnailTarget,
  captures: Captures,
  isDark = false,
  base = thumbnailBase(target),
): string | undefined {
  const query = thumbnailQuery(captures, isDark);
  if (query === undefined) return undefined;
  return `${base}/${target.id}/thumbnail?${query}`;
}

/**
 * The query string that selects a capture, or undefined when none was ever
 * taken -- which is what tells a card to show its content-type icon instead.
 *
 * The dark variant is asked for only when one was captured: a content type
 * that carries its own colors (HTML, JSX, SVG, a raster image) stores a single
 * image and serves it in both modes, so its empty dark key means "use the light
 * one", not "no thumbnail".
 */
function thumbnailQuery(c: Captures, isDark: boolean): string | undefined {
  if (!c.light) return undefined;
  const dark = isDark && !!c.dark;
  const stamp = (dark ? c.darkStamp : c.stamp) ?? 0;
  return `${dark ? "variant=dark&" : ""}c=${encodeURIComponent(String(stamp))}`;
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

/** The same of a managed resource, which dates its captures rather than versioning them. */
interface ResourceThumbnailState {
  id: string;
  mime_type: string;
  updated_at: string;
  thumbnail_s3_key?: string;
  thumbnail_dark_s3_key?: string;
  thumbnail_captured_at?: string;
  thumbnail_dark_captured_at?: string;
}

/** An asset's captures, under the field names an asset spells them with. */
export function assetCaptures(a: ThumbnailState): Captures {
  return {
    light: a.thumbnail_s3_key,
    dark: a.thumbnail_dark_s3_key,
    stamp: a.thumbnail_version,
    darkStamp: a.thumbnail_dark_version,
  };
}

/** A resource's captures, under the field names a resource spells them with. */
export function resourceCaptures(r: ResourceThumbnailState): Captures {
  return {
    light: r.thumbnail_s3_key,
    dark: r.thumbnail_dark_s3_key,
    stamp: r.thumbnail_captured_at ?? "",
    darkStamp: r.thumbnail_dark_captured_at ?? "",
  };
}

/** The URL an asset's thumbnail is served from. */
export function assetThumbnailSrc(
  asset: ThumbnailState & { id: string },
  isDark = false,
  base = ASSET_THUMBNAIL_BASE,
): string | undefined {
  return thumbnailSrc({ kind: "asset", id: asset.id }, assetCaptures(asset), isDark, base);
}

/**
 * The URL a managed resource's thumbnail is served from.
 *
 * It takes isDark for the reason every other tile builder does, and the library
 * grid did not: a markdown, CSV, JSON or plain-text resource stores a dark
 * capture and a dark-mode portal was drawing the light one, so a themeable file
 * was a white card in a dark grid (#1568).
 */
export function resourceThumbnailSrc(
  resource: ResourceThumbnailState,
  isDark = false,
): string | undefined {
  return thumbnailSrc({ kind: "resource", id: resource.id }, resourceCaptures(resource), isDark);
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
 */
export function collectionItemThumbnailSrc(
  item: ItemThumbnailState,
  assetBase: string,
  isDark = false,
): string | undefined {
  return thumbnailSrc(
    { kind: "asset", id: item.asset_id },
    {
      light: item.asset_thumbnail_s3_key,
      dark: item.asset_thumbnail_dark_s3_key,
      stamp: item.asset_thumbnail_version,
      darkStamp: item.asset_thumbnail_dark_version,
    },
    isDark,
    assetBase,
  );
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

/**
 * The same question of a managed resource.
 *
 * A resource row carries no version, so a capture is behind when it was taken
 * before the file was last written -- which is the comparison the pending query
 * makes in SQL (pkg/resource, buildPendingThumbnails). There is no legacy
 * filename arm: resource captures have only ever been written under the hidden
 * name.
 */
export function resourceThumbnailBehind(r: ResourceThumbnailState): boolean {
  const behind = (key: string | undefined, at: string | undefined): boolean =>
    !key || !at || at < r.updated_at;
  return (
    behind(r.thumbnail_s3_key, r.thumbnail_captured_at) ||
    (isThemeable(r.mime_type) && behind(r.thumbnail_dark_s3_key, r.thumbnail_dark_captured_at))
  );
}

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
  /** Whether a capture is wanted right now. */
  behind: boolean;
  /** Which route this reader is entitled to read the tile through. */
  base: string;
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
    behind: thumbnailBehind(asset),
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
    behind: resourceThumbnailBehind(resource),
    base: RESOURCE_THUMBNAIL_BASE,
  };
}
