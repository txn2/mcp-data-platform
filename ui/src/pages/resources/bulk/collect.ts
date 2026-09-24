/**
 * Gathering the files a person picked, dropped, or packed into an archive
 * (#1862). Each arrives as a SourceFile: the bytes and their path relative to
 * what was picked, which is what the folder structure is built from.
 */

import { MAX_ZIP_BYTES, type SourceFile } from "./plan";
import { extensionOf, typeFromName } from "./webImage";
import { formatBytes } from "@/lib/format";

/**
 * fromFileList reads a file input. A folder pick carries each file's path from
 * the picked folder down (webkitRelativePath, the picked folder's own name
 * first), so the folder arrives as a folder; a plain multi-select has none.
 */
export function fromFileList(files: Iterable<File>): SourceFile[] {
  return Array.from(files, (file) => ({ file, relPath: file.webkitRelativePath || file.name }));
}

/** The part of FileSystemEntry a drop walk needs. */
export interface DropEntry {
  isFile: boolean;
  isDirectory: boolean;
  name: string;
  fullPath: string;
}

interface DropFileEntry extends DropEntry {
  file(success: (file: File) => void, failure?: (err: unknown) => void): void;
}

interface DropDirectoryEntry extends DropEntry {
  createReader(): {
    readEntries(success: (entries: DropEntry[]) => void, failure?: (err: unknown) => void): void;
  };
}

/** readAll drains a directory reader, which answers in batches until empty. */
async function readAll(dir: DropDirectoryEntry): Promise<DropEntry[]> {
  const reader = dir.createReader();
  const out: DropEntry[] = [];
  for (;;) {
    const batch = await new Promise<DropEntry[]>((resolve, reject) => reader.readEntries(resolve, reject));
    if (batch.length === 0) return out;
    out.push(...batch);
  }
}

/** walk collects every file beneath one dropped entry. */
async function walk(entry: DropEntry): Promise<SourceFile[]> {
  const relPath = entry.fullPath.replace(/^\/+/, "") || entry.name;
  if (entry.isFile) {
    try {
      const file = await new Promise<File>((resolve, reject) => (entry as DropFileEntry).file(resolve, reject));
      return [{ file, relPath }];
    } catch {
      return [{ file: new File([], entry.name), relPath, problem: "The browser could not read this file." }];
    }
  }
  if (!entry.isDirectory) return [];
  const children = await readAll(entry as DropDirectoryEntry);
  const nested = await Promise.all(children.map(walk));
  return nested.flat();
}

/**
 * fromDrop reads a drop. A dropped folder is walked through the entry API, so
 * its subfolders arrive as folders; a drop from somewhere that offers no
 * entries falls back to the plain file list.
 */
export async function fromDrop(dt: DataTransfer): Promise<SourceFile[]> {
  const entries = Array.from(dt.items ?? [])
    .map((item) => item.webkitGetAsEntry?.() ?? null)
    .filter((e): e is FileSystemEntry => e !== null);
  if (entries.length === 0) return fromFileList(dt.files ?? []);
  const nested = await Promise.all(entries.map((e) => walk(e as unknown as DropEntry)));
  return nested.flat();
}

/** isZip reports whether a picked file is an archive the batch unpacks. */
export function isZip(file: File): boolean {
  return extensionOf(file.name) === "zip";
}

/** dirOf is the directory part of a relative path, with its trailing slash. */
function dirOf(relPath: string): string {
  const slash = relPath.lastIndexOf("/");
  return slash < 0 ? "" : relPath.slice(0, slash + 1);
}

/** The unzip function fflate exports, injectable for a test. */
export type Unzip = (data: Uint8Array) => Promise<Record<string, Uint8Array>>;

/** fflateUnzip loads fflate only when an archive is actually picked. */
export const fflateUnzip: Unzip = async (data) => {
  const { unzip } = await import("fflate");
  return new Promise((resolve, reject) =>
    unzip(data, (err, files) => (err ? reject(err) : resolve(files))),
  );
};

/**
 * unpackOne lists an archive's files beneath the directory the archive sat in.
 * Directory entries are skipped; the files inside them carry the path.
 */
async function unpackOne(source: SourceFile, unzip: Unzip): Promise<SourceFile[]> {
  if (source.file.size > MAX_ZIP_BYTES) {
    return [{ ...source, problem: `An archive is unpacked only up to ${formatBytes(MAX_ZIP_BYTES)}.` }];
  }
  try {
    const entries = await unzip(new Uint8Array(await source.file.arrayBuffer()));
    const prefix = dirOf(source.relPath);
    return Object.entries(entries)
      .filter(([name]) => !name.endsWith("/"))
      .map(([name, data]) => {
        const base = name.split("/").pop() ?? name;
        return {
          file: new File([data as BlobPart], base, { type: typeFromName(base) }),
          relPath: prefix + name,
        };
      });
  } catch (err) {
    const why = err instanceof Error ? err.message : "unreadable";
    return [{ ...source, problem: `The archive could not be unpacked: ${why}.` }];
  }
}

/**
 * expandArchives replaces every .zip with the files inside it when `unpack` is
 * on, and leaves it as one file to store when it is off.
 */
export async function expandArchives(
  sources: SourceFile[],
  unpack: boolean,
  unzip: Unzip = fflateUnzip,
): Promise<SourceFile[]> {
  if (!unpack) return sources;
  const out: SourceFile[] = [];
  for (const source of sources) {
    if (source.problem || !isZip(source.file)) out.push(source);
    else out.push(...(await unpackOne(source, unzip)));
  }
  return out;
}
