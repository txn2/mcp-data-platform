/**
 * Which stored files a generated document can reference directly (#1862).
 *
 * A document or deck draws an image the browser renders natively. EPS and PDF
 * source art is worth keeping, and nothing converts it, so a library holding a
 * logo in six formats has to say which of the six a document can point at.
 */

/** The media types a browser draws as an image without a plugin. */
const WEB_IMAGE_TYPES = new Set([
  "image/png",
  "image/jpeg",
  "image/gif",
  "image/webp",
  "image/avif",
  "image/svg+xml",
]);

/** Extension to media type, for a file the browser handed over untyped. */
const EXTENSION_TYPES: Record<string, string> = {
  png: "image/png",
  jpg: "image/jpeg",
  jpeg: "image/jpeg",
  gif: "image/gif",
  webp: "image/webp",
  avif: "image/avif",
  svg: "image/svg+xml",
};

/** extensionOf returns a file name's extension, lowercased and without the dot. */
export function extensionOf(name: string): string {
  const dot = name.lastIndexOf(".");
  return dot < 0 ? "" : name.slice(dot + 1).toLowerCase();
}

/**
 * typeFromName names a web image's media type from its extension, or "" when
 * the extension is not one. A file unpacked from an archive arrives untyped,
 * and the upload declares what this returns so the stored type is the image
 * type rather than a generic one.
 */
export function typeFromName(name: string): string {
  return EXTENSION_TYPES[extensionOf(name)] ?? "";
}

/** isWebImage reports whether a media type is one a document can draw. */
export function isWebImage(mimeType: string): boolean {
  const bare = mimeType.split(";")[0]?.trim().toLowerCase() ?? "";
  return WEB_IMAGE_TYPES.has(bare);
}

/** fileIsWebImage classifies a local file by its declared type or its name. */
export function fileIsWebImage(file: { name: string; type: string }): boolean {
  return isWebImage(file.type) || isWebImage(typeFromName(file.name));
}
