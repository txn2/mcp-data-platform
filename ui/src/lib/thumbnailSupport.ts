/**
 * Which content types get a thumbnail, how big a document is worth drawing,
 * and where a stored tile is read from.
 *
 * Tiles are drawn by the platform's own headless renderer (#1787); the tile
 * page (src/tile-entry.tsx) dispatches on the families below. The surfaces that
 * only show a tile or ask for another read this module and nothing heavier.
 */

import type { RendererKind } from "@/components/renderers/registry";

/** Thumbnail image dimensions, in CSS pixels. */
export const THUMB_WIDTH = 400;
export const THUMB_HEIGHT = 300;

/**
 * Largest document a thumbnail is drawn from, in bytes, for every family held
 * to the default bound. The server applies the same bound when it picks what
 * to draw; above it a file keeps its content-type icon.
 */
export const THUMBNAIL_SOURCE_LIMIT = 1024 * 1024; // 1 MB

/**
 * The bound the families in LARGE_SOURCE_FAMILIES are held to instead.
 *
 * The Go definition of both bounds, and of which families take this one, is
 * internal/thumbtypes; a test there fails when the two languages disagree.
 */
export const LARGE_THUMBNAIL_SOURCE_LIMIT = 32 * 1024 * 1024; // 32 MB

/**
 * How the tile page draws one family. Its dispatch (components/thumbnail/Tile)
 * is over this rather than over its own list of content types, so a family
 * cannot be offered by a surface that nothing can draw (#1568).
 *
 * "iframe" is content that carries its own document (HTML, JSX); "image" is a
 * raster file scaled to cover the tile; "pdf" is page one of a document, which
 * the tile page rasterizes itself.
 */
export type CaptureFamily = "iframe" | "svg" | "csv" | "json" | "markdown" | "text" | "image" | "pdf";

/**
 * What the tile page draws each renderer kind as, or why it draws none.
 *
 * If a browser can render it in the viewer, it can have a tile: the two
 * questions were answered by two unrelated tables and the second was a
 * hand-kept subset of the first, so seven families the viewer lays out every
 * day -- YAML, XML, SQL, Python, JavaScript, CSS, TSV -- kept a content-type
 * icon forever (#1754). This is the bridge, over the renderer registry's own
 * kinds rather than over a second list of content types: a kind added there is
 * a missing key here and does not compile, and the test beside it holds the
 * fragments below to what the registry resolves.
 *
 * The kinds that are rendered and deliberately not drawn say why, here, rather
 * than being absent and leaving the reason to be guessed.
 */
export type KindCapture = CaptureFamily | { drawn: false; because: string };

export const CAPTURE_BY_RENDERER_KIND: Record<RendererKind, KindCapture> = {
  html: "iframe",
  jsx: "iframe",
  svg: "svg",
  markdown: "markdown",
  json: "json",
  ndjson: "json",
  table: "csv",
  // Every code family is a plain text document the viewer lays out, which is
  // exactly the shape the text family draws. The tile is not syntax-highlighted
  // and does not need to be: at 400x300 what a reader recognizes is the shape
  // of the file.
  code: "text",
  text: "text",
  image: "image",
  // Not through the viewer's renderer, which hands a PDF to the browser's own
  // plugin that headless-shell does not ship. Page one is a raster the tile
  // page produces itself with pdf.js, which needs no plugin (#1794). The
  // viewer still shows a reader the plugin's PDF; this is the tile only.
  pdf: "pdf",
  audio: {
    drawn: false,
    because: "there is no page to draw, and a waveform would be a picture of a decoder, not of the file",
  },
  video: {
    drawn: false,
    because:
      "a frame grab needs the media element to decode far enough to seek, which " +
      "is a different capture from rendering a document",
  },
  binary: {
    drawn: false,
    because: "the viewer does not render it either -- it offers the download",
  },
  parquet: {
    drawn: false,
    because:
      "a Parquet file is columnar and compressed, and its viewer reads it by range with a " +
      "decoder the tile page does not carry; the tile is the content-type icon",
  },
};

/**
 * The families drawn twice, once per color scheme.
 *
 * A property of the family rather than of each content type. Everything the
 * tile page lays out itself is drawn on the scheme's background. HTML and JSX
 * are drawn with the renderer emulating the scheme, which is what their own
 * prefers-color-scheme rules answer to, as they do in the viewer's frame: a
 * dashboard with a dark stylesheet opened dark and had a white card (#1789).
 * SVG, a PDF page and a raster image are drawn as stored and serve one image
 * in both modes.
 */
const THEMEABLE_FAMILIES: ReadonlySet<CaptureFamily> = new Set<CaptureFamily>([
  "iframe",
  "markdown",
  "csv",
  "json",
  "text",
]);

/**
 * The families held to LARGE_THUMBNAIL_SOURCE_LIMIT rather than to the default
 * bound.
 *
 * What the default bound protects against is the renderer holding a whole
 * document, and a family is here when its tile costs less of that than the
 * file's size suggests, because the tile is drawn from a part of the file.
 * Only page one of a PDF is decoded (#1794); a table's tile is its header row
 * and its first rows, which is all the platform hands the tile page of a large
 * one (#1802).
 */
const LARGE_SOURCE_FAMILIES: ReadonlySet<CaptureFamily> = new Set<CaptureFamily>(["pdf", "csv"]);

/** One capturable family: how a content type is recognized, and what is done with it. */
interface CapturableFamily {
  /**
   * The fragment of the media type that names this family. A fragment rather
   * than an exact type because a stored type carries parameters and vendor
   * prefixes ("text/markdown; charset=utf-8", "application/vnd.acme+json").
   */
  fragment: string;
  family: CaptureFamily;
}

/**
 * Every content family that gets a thumbnail, in the order a content type is
 * matched against.
 *
 * This is the one browser-side definition. The rule used to be written out in
 * four places -- two Go stores, the browser gate here, and the tile's own
 * dispatch -- and the four stopped agreeing (#1568). The one Go definition is
 * internal/thumbtypes, and a Go test reads this table and the themeable family
 * set above and fails when the two languages disagree.
 *
 * It is written as fragments, rather than resolved through the renderer
 * registry, because the other half of the rule is a SQL query: the server
 * picks the next documents to draw with ILIKE over a content_type column, and
 * cannot resolve a registry. What keeps it from drifting into a subset of what
 * the viewer renders -- which is what it had become (#1754) -- is the test that
 * derives the expectation from CAPTURE_BY_RENDERER_KIND and fails naming the
 * type the two disagree on.
 *
 * A stored type is canonical: the platform settles a declaration against its
 * alias table when the file is written (#1568), so "text/tsv" is stored as
 * "text/tab-separated-values" and no fragment has to cover both spellings.
 *
 * Order is part of the definition, because the first fragment a type contains
 * wins.
 *
 * The "json" fragment covers both JSON families: every spelling of
 * newline-delimited JSON contains it ("application/x-ndjson",
 * "application/jsonl"), as do the vendor dialects. Which of the two is drawn is
 * a refinement the tile page makes inside the family. "text/plain" is spelled
 * in full because the bare word is a substring of "text/html", "text/csv" and
 * "text/markdown", each of which is drawn differently.
 *
 * The raster families are named one by one rather than as "image/". A tile of
 * a raster image is the browser decoding it, and TIFF, HEIC and PSD are images
 * no browser decodes: offering one is offering work that fails every time.
 */
const CAPTURABLE_FAMILIES: CapturableFamily[] = [
  // SVG and PDF lead: they are the families ahead of a themeable one that are
  // not themeable themselves, which is the shape the server's SQL form of the
  // rule relies on (internal/thumbtypes, ThemeableShadows).
  { fragment: "svg", family: "svg" },
  { fragment: "pdf", family: "pdf" },
  { fragment: "html", family: "iframe" },
  { fragment: "jsx", family: "iframe" },
  { fragment: "markdown", family: "markdown" },
  { fragment: "csv", family: "csv" },
  { fragment: "tab-separated", family: "csv" },
  { fragment: "json", family: "json" },
  // The code families, each a plain text document drawn as one. "svg" above
  // takes image/svg+xml before "xml" is reached, and "jsx" takes text/jsx
  // before "javascript" is.
  { fragment: "yaml", family: "text" },
  { fragment: "xml", family: "text" },
  { fragment: "sql", family: "text" },
  { fragment: "python", family: "text" },
  { fragment: "javascript", family: "text" },
  { fragment: "css", family: "text" },
  { fragment: "text/plain", family: "text" },
  { fragment: "image/png", family: "image" },
  { fragment: "image/jpeg", family: "image" },
  { fragment: "image/gif", family: "image" },
  { fragment: "image/webp", family: "image" },
  { fragment: "image/avif", family: "image" },
  { fragment: "image/bmp", family: "image" },
  { fragment: "image/x-icon", family: "image" },
  { fragment: "image/vnd.microsoft.icon", family: "image" },
];

/** The family a content type is drawn as, or null when nothing draws it. */
export function captureFamily(contentType: string): CaptureFamily | null {
  return matchFamily(contentType)?.family ?? null;
}

/**
 * The largest file of this content type a tile is drawn from. The families
 * drawn from part of the file have a bound of their own; every other family
 * shares the default.
 */
export function thumbnailSourceLimit(contentType: string): number {
  const family = captureFamily(contentType);
  return family !== null && LARGE_SOURCE_FAMILIES.has(family)
    ? LARGE_THUMBNAIL_SOURCE_LIMIT
    : THUMBNAIL_SOURCE_LIMIT;
}

/** Returns true if the content type supports thumbnail generation. */
export function isThumbnailSupported(contentType: string): boolean {
  return matchFamily(contentType) !== undefined;
}

/**
 * Returns true if the content type is drawn once per color scheme and so has a
 * separate dark-mode thumbnail. SVG and a raster image are drawn as stored, so
 * they reuse the single light/default thumbnail in both modes.
 *
 * Read off the family rather than off the content type, so a content type
 * added to the table above cannot get this wrong.
 */
export function isThemeable(contentType: string): boolean {
  const family = captureFamily(contentType);
  return family !== null && THEMEABLE_FAMILIES.has(family);
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
 * one is drawn again even though it already has a thumbnail, and the object it
 * supersedes is removed when the new one is recorded.
 */
const LEGACY_THUMBNAIL_FILENAMES = ["thumbnail.png", "thumbnail_dark.png"];

/**
 * Returns true if a recorded thumbnail key uses one of the legacy filenames.
 */
export function isLegacyThumbnailKey(key: string): boolean {
  const name = key.slice(key.lastIndexOf("/") + 1);
  return LEGACY_THUMBNAIL_FILENAMES.includes(name);
}

/** What a tile belongs to: a portal asset, or a managed resource (#1554). */
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
 * The route a target's tile is served from and cleared through, in full.
 *
 * An absolute path rather than a fragment for one client to prefix: an asset
 * lives under /api/v1/portal and a resource under /api/v1/resources, so a
 * fragment handed to the wrong client is a 404 (#1554).
 */
export function thumbnailPath(target: ThumbnailTarget): string {
  return `${thumbnailBase(target)}/${target.id}/thumbnail`;
}

/** The route a target's own bytes are read from. */
export function contentPath(target: ThumbnailTarget): string {
  return `${thumbnailBase(target)}/${target.id}/content`;
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
  /**
   * The renderer generation that drew the captures. A redraw by a new
   * generation keeps the version or capture time the stamp is made of, so it
   * is part of the URL too (#1789).
   */
  renderer?: number;
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
 * The dark variant is asked for only when one was captured: SVG and a raster
 * image store a single image and serve it in both modes, and a tile drawn
 * before its family was themeable has no dark one yet, so an empty dark key
 * means "use the light one", not "no thumbnail".
 */
function thumbnailQuery(c: Captures, isDark: boolean): string | undefined {
  if (!c.light) return undefined;
  const dark = isDark && !!c.dark;
  const stamp = (dark ? c.darkStamp : c.stamp) ?? 0;
  const renderer = c.renderer ? `&r=${c.renderer}` : "";
  return `${dark ? "variant=dark&" : ""}c=${encodeURIComponent(String(stamp))}${renderer}`;
}

/** The parts of an asset that say whether its capture is current. */
export interface ThumbnailState {
  content_type: string;
  current_version: number;
  thumbnail_s3_key?: string;
  thumbnail_dark_s3_key?: string;
  thumbnail_version: number;
  thumbnail_dark_version: number;
  thumbnail_renderer?: number;
  thumbnail_failure?: string;
  thumbnail_failed_version?: number;
}

/** The same of a managed resource, which dates its captures rather than versioning them. */
export interface ResourceThumbnailState {
  id: string;
  mime_type: string;
  updated_at: string;
  thumbnail_s3_key?: string;
  thumbnail_dark_s3_key?: string;
  thumbnail_captured_at?: string;
  thumbnail_dark_captured_at?: string;
  thumbnail_renderer?: number;
  thumbnail_failure?: string;
  thumbnail_failed_at?: string;
}

/** An asset's captures, under the field names an asset spells them with. */
export function assetCaptures(a: ThumbnailState): Captures {
  return {
    light: a.thumbnail_s3_key,
    dark: a.thumbnail_dark_s3_key,
    stamp: a.thumbnail_version,
    darkStamp: a.thumbnail_dark_version,
    renderer: a.thumbnail_renderer,
  };
}

/** A resource's captures, under the field names a resource spells them with. */
export function resourceCaptures(r: ResourceThumbnailState): Captures {
  return {
    light: r.thumbnail_s3_key,
    dark: r.thumbnail_dark_s3_key,
    stamp: r.thumbnail_captured_at ?? "",
    darkStamp: r.thumbnail_dark_captured_at ?? "",
    renderer: r.thumbnail_renderer,
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
  asset_thumbnail_renderer?: number;
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
      renderer: item.asset_thumbnail_renderer,
    },
    isDark,
    assetBase,
  );
}

/**
 * The URL a collection's own tile -- the mosaic of its first members -- is
 * fetched from, or undefined when it has none.
 *
 * A collection has a dark mosaic composed from its members' dark tiles (#1789).
 * It is stored beside the light one rather than recorded on the row, so it is
 * always asked for in a dark portal, and the route answers with the light
 * mosaic for a collection composed before it had one.
 */
export function collectionMosaicSrc(
  collection: { id: string; thumbnail_s3_key?: string },
  isDark = false,
): string | undefined {
  if (!collection.thumbnail_s3_key) return undefined;
  const path = `/api/v1/portal/collections/${collection.id}/thumbnail`;
  return isDark ? `${path}?variant=dark` : path;
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
 * types that carry one; SVG and a raster image serve the single capture in both
 * modes, so their empty dark key is not a gap.
 *
 * The renderer's claim asks the same question of every asset at once, in SQL
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
 * before the file was last written -- which is the comparison the renderer's
 * claim makes in SQL (pkg/resource, buildThumbnailClaim). There is no legacy
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
 * Why the renderer could not draw this asset's tile, while that still stands.
 *
 * A failure is recorded against the version the renderer tried, and holds the
 * asset off the renderer's list until the document changes (#1787). Once the
 * asset has moved past that version the renderer tries again, so an older
 * failure is no longer the answer.
 */
export function assetThumbnailFailure(a: ThumbnailState): string | undefined {
  if (!a.thumbnail_failure) return undefined;
  return (a.thumbnail_failed_version ?? 0) >= a.current_version ? a.thumbnail_failure : undefined;
}

/** The same of a managed resource, whose failure is dated to the file it tried. */
export function resourceThumbnailFailure(r: ResourceThumbnailState): string | undefined {
  if (!r.thumbnail_failure || !r.thumbnail_failed_at) return undefined;
  return Date.parse(r.thumbnail_failed_at) >= Date.parse(r.updated_at) ? r.thumbnail_failure : undefined;
}
