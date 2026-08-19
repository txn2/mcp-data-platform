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
