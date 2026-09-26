/**
 * The naming rules a bulk upload applies before it sends anything (#1862).
 *
 * The server sanitizes every filename and validates every folder path again
 * and is the authority. These mirror pkg/resource/validate.go and path.go so
 * the list shows the name and folder a file will actually be filed under, and
 * a file the server would refuse is marked before the batch starts rather than
 * after a round trip.
 */

import { extensionOf } from "./webImage";

/** Extensions the server refuses (resource.DeniedExtensions). */
const DENIED_EXTENSIONS = new Set(["exe", "sh", "bat", "cmd", "ps1", "msi", "com", "scr"]);

/** The longest a display name may be, in characters (resource.MaxDisplayNameLen). */
export const MAX_DISPLAY_NAME = 200;

/** The longest one folder name may be (resource.MaxPathSegmentLen). */
const MAX_SEGMENT = 31;

/** Either the name a file is filed under, or why it cannot be filed. */
export type FilenameResult = { filename: string; problem?: undefined } | { filename?: undefined; problem: string };

/**
 * sanitizeFilename is resource.SanitizeFilename: the base name, lowercased,
 * spaces to hyphens, and every character but letters, digits, dot, hyphen and
 * underscore removed. A denied extension is refused rather than renamed.
 */
export function sanitizeFilename(name: string): FilenameResult {
  const base = name.trim().split(/[/\\]/).pop() ?? "";
  if (base === "") return { problem: "The file has no name." };
  const cleaned = base
    .toLowerCase()
    .split(" ").join("-")
    .replace(/[^\p{L}\p{N}._-]/gu, "");
  if (cleaned === "" || cleaned === ".") {
    return { problem: "The file name has no characters a resource name can use." };
  }
  const ext = cleaned.includes(".") ? extensionOf(cleaned) : "";
  if (DENIED_EXTENSIONS.has(ext)) {
    return { problem: `Files ending in .${ext} are not accepted.` };
  }
  return { filename: cleaned };
}

/**
 * displayNameFor is the name a file is listed under until someone edits it:
 * the original file name without its extension, as it was written.
 */
export function displayNameFor(name: string): string {
  const base = name.split(/[/\\]/).pop() ?? name;
  const dot = base.lastIndexOf(".");
  const stem = (dot > 0 ? base.slice(0, dot) : base).trim();
  const chosen = stem === "" ? base.trim() : stem;
  return Array.from(chosen).slice(0, MAX_DISPLAY_NAME).join("");
}

/**
 * folderSegment turns one directory name from a picked folder or an archive
 * into a resource folder name: lowercase letters, digits and hyphens, starting
 * with a letter or digit, at most 31 characters, so "2024" files under "2024".
 * Null when nothing usable is left.
 */
export function folderSegment(name: string): string | null {
  const seg = name
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
  if (seg === "") return null;
  return seg.slice(0, MAX_SEGMENT).replace(/-+$/, "");
}

/**
 * describe expands a description template for one file: every {name} becomes
 * the file's display name. The server requires a description, so a template
 * that expands to nothing leaves the display name itself.
 */
export function describe(template: string, displayName: string): string {
  const text = template.split("{name}").join(displayName).trim();
  return text === "" ? displayName : text;
}
