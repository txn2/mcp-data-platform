/**
 * Turning what a person picked into the files a bulk upload sends (#1862).
 *
 * A picked folder, a dropped folder and an unpacked archive all arrive as
 * files with a relative path. The directories in that path become resource
 * folders beneath the base folder the dialog names, so a brand kit filed as
 * acme/logos/color arrives in the same shape. Everything the batch will refuse
 * is decided here, before a byte is sent, and stays in the list with its
 * reason beside it.
 */

import { formatBytes } from "@/lib/format";
import { pathProblem } from "../parts/pathRules";
import { displayNameFor, folderSegment, sanitizeFilename } from "./names";
import { fileIsWebImage } from "./webImage";

/** The most files one batch sends. Past it a file waits for another batch. */
export const MAX_BATCH_FILES = 5000;

/** The largest archive the browser unpacks (1 GiB). */
export const MAX_ZIP_BYTES = 1024 * 1024 * 1024;

/** The longest a description may be (resource.MaxDescriptionLen). */
export const MAX_DESCRIPTION = 2000;

/**
 * One file as it was picked: the bytes and where they sat relative to what was
 * picked. A source that could not be read (an archive that would not unpack)
 * carries the reason instead of being dropped, so it is listed rather than lost.
 */
export interface SourceFile {
  file: File;
  /** Slash-separated, the file name last, e.g. "logos/color/acme.png". */
  relPath: string;
  problem?: string;
}

/** One file the batch will send, or has refused. */
export interface BulkItem {
  /** Stable across re-planning: the relative path plus its position. */
  key: string;
  file: File;
  /** The resource folders beneath the base folder, "" for none. */
  relDir: string;
  /** The name the server will file it under. Empty when refused. */
  filename: string;
  /** Editable before upload; defaults to the file name without extension. */
  displayName: string;
  webImage: boolean;
  /** Why this file will not be sent, or null. */
  problem: string | null;
}

/**
 * isIgnored reports a path the batch skips without listing: operating-system
 * litter a person never meant to upload. Any dotfile or dot-directory (.DS_Store,
 * .git), the __MACOSX folder an archive made on a Mac carries, and Thumbs.db.
 */
export function isIgnored(relPath: string): boolean {
  const parts = relPath.split("/").filter(Boolean);
  if (parts.some((p) => p.startsWith(".") || p === "__MACOSX")) return true;
  return (parts[parts.length - 1] ?? "").toLowerCase() === "thumbs.db";
}

/** folderFor converts a path's directories, or names the one that cannot be. */
function folderFor(dirs: string[]): { relDir: string; problem: string | null } {
  const segments: string[] = [];
  for (const dir of dirs) {
    const seg = folderSegment(dir);
    if (seg === null) {
      return { relDir: "", problem: `The folder "${dir}" has no letters or digits to name a resource folder with.` };
    }
    segments.push(seg);
  }
  return { relDir: segments.join("/"), problem: null };
}

/** planOne builds the item for one source, with the first reason it cannot go. */
function planOne(source: SourceFile, index: number, maxBytes: number): BulkItem {
  const parts = source.relPath.split("/").filter(Boolean);
  const name = parts[parts.length - 1] ?? source.file.name;
  const folder = folderFor(parts.slice(0, -1));
  const named = sanitizeFilename(name);
  const tooBig = source.file.size > maxBytes ? `The file is larger than the ${formatBytes(maxBytes)} limit.` : null;
  const overCap =
    index >= MAX_BATCH_FILES
      ? `A batch holds at most ${MAX_BATCH_FILES.toLocaleString("en-US")} files; upload this one in another batch.`
      : null;
  return {
    key: `${index}:${source.relPath}`,
    file: source.file,
    relDir: folder.relDir,
    filename: named.filename ?? "",
    displayName: displayNameFor(name),
    webImage: fileIsWebImage({ name, type: source.file.type }),
    problem: source.problem ?? named.problem ?? folder.problem ?? tooBig ?? overCap,
  };
}

/**
 * planItems lists every picked file the batch will consider, in the order
 * picked. Two files that would land on the same folder and name are one
 * address, so the second is refused rather than silently replacing the first.
 */
export function planItems(sources: SourceFile[], maxBytes: number): BulkItem[] {
  const kept = sources.filter((s) => !isIgnored(s.relPath));
  const seen = new Set<string>();
  return kept.map((source, index) => {
    const item = planOne(source, index, maxBytes);
    if (item.problem) return item;
    const address = `${item.relDir}/${item.filename}`;
    if (seen.has(address)) {
      return { ...item, problem: "Another file in this batch has the same folder and file name." };
    }
    seen.add(address);
    return item;
  });
}

/** targetPath is the folder one item is filed in, beneath the base folder. */
export function targetPath(base: string, relDir: string): string {
  if (relDir === "") return base;
  return base === "" ? relDir : `${base}/${relDir}`;
}

/**
 * sendProblem is why one item cannot be sent with the batch's current
 * settings: its own refusal, then the folder it would land in, then the name
 * and description it would carry. Asked at send time because the base folder
 * and the template can change after the files were picked.
 */
export function sendProblem(item: BulkItem, base: string, description: string): string | null {
  if (item.problem) return item.problem;
  const path = pathProblem(targetPath(base, item.relDir));
  if (path) return path;
  if (item.displayName.trim() === "") return "A display name is required.";
  if (Array.from(item.displayName).length > 200) return "A display name is at most 200 characters.";
  if (Array.from(description).length > MAX_DESCRIPTION) {
    return `A description is at most ${MAX_DESCRIPTION} characters.`;
  }
  return null;
}
