import type { Folder, Resource, ResourceSort } from "@/api/resources/types";
import { isUnder, segments } from "../parts/tree";

/**
 * One row of the listing: a folder or a file. They are one type because the
 * page lists them as one table (#1872), and every control that acts on a row --
 * selection, drag, the context menu, the keyboard -- acts on either.
 */
export type Entry =
  | { kind: "folder"; key: string; name: string; path: string; count: number; updatedAt?: string }
  | { kind: "file"; key: string; name: string; resource: Resource };

/** The selection key of a folder row. A file's key is its resource id. */
export function folderKey(path: string): string {
  return `d:${path}`;
}

/** The folder path a selection key names, or null for a file's key. */
export function folderOfKey(key: string): string | null {
  return key.startsWith("d:") ? key.slice(2) : null;
}

/** The folders directly inside a location, as rows. */
export function folderEntries(folders: Folder[], at: string): Entry[] {
  const depth = segments(at).length;
  const out: Entry[] = [];
  for (const f of folders) {
    if (f.path === at || !isUnder(f.path, at)) continue;
    const parts = segments(f.path);
    if (parts.length !== depth + 1) continue;
    out.push({
      kind: "folder",
      key: folderKey(f.path),
      name: parts[depth]!,
      path: f.path,
      count: f.count,
      updatedAt: f.updated_at,
    });
  }
  return out;
}

/** The files a listing returned, as rows. */
export function fileEntries(resources: Resource[]): Entry[] {
  return resources.map((r) => ({ kind: "file", key: r.id, name: r.display_name, resource: r }));
}

/**
 * sortFolders orders the folder rows the way the server orders the files, so
 * the two halves of one table follow the same column. Folders always come
 * first; the files are in the order the server returned them.
 */
export function sortFolders(folders: Entry[], sort: ResourceSort): Entry[] {
  const cmp = folderComparator(sort);
  return [...folders].sort(cmp);
}

type FolderEntry = Extract<Entry, { kind: "folder" }>;

function byName(a: Entry, b: Entry): number {
  return a.name.localeCompare(b.name, undefined, { sensitivity: "base" });
}

function byTime(a: Entry, b: Entry): number {
  const at = (a as FolderEntry).updatedAt ?? "";
  const bt = (b as FolderEntry).updatedAt ?? "";
  return at.localeCompare(bt) || byName(a, b);
}

function byCount(a: Entry, b: Entry): number {
  return (a as FolderEntry).count - (b as FolderEntry).count || byName(a, b);
}

const FOLDER_ORDER: Record<ResourceSort, (a: Entry, b: Entry) => number> = {
  name: byName,
  name_desc: (a, b) => byName(b, a),
  updated: (a, b) => byTime(b, a),
  updated_asc: byTime,
  last_read: (a, b) => byTime(b, a),
  size: byCount,
  size_desc: (a, b) => byCount(b, a),
};

function folderComparator(sort: ResourceSort): (a: Entry, b: Entry) => number {
  return FOLDER_ORDER[sort];
}

/** A column a header sorts by, and the two orders it toggles between. */
export interface SortColumn {
  asc: ResourceSort;
  desc: ResourceSort;
}

export const SORT_COLUMNS: Record<"name" | "modified" | "size" | "lastRead", SortColumn> = {
  name: { asc: "name", desc: "name_desc" },
  modified: { asc: "updated_asc", desc: "updated" },
  size: { asc: "size", desc: "size_desc" },
  // Most recently read first, never-read last: the one order a curator hunting
  // dead weight asks for, so it has no reverse.
  lastRead: { asc: "last_read", desc: "last_read" },
};

/**
 * nextSort is the order a header click selects: the column's first order when
 * another column is sorting, the other one when this column already is. Name
 * starts ascending and the others start newest or largest first, as a file
 * manager does.
 */
export function nextSort(column: keyof typeof SORT_COLUMNS, current: ResourceSort): ResourceSort {
  const c = SORT_COLUMNS[column];
  if (current === c.asc) return c.desc;
  if (current === c.desc) return c.asc;
  return column === "name" ? c.asc : c.desc;
}

/** The direction a header shows, or null when it is not the sorting column. */
export function sortDirection(column: keyof typeof SORT_COLUMNS, current: ResourceSort): "asc" | "desc" | null {
  const c = SORT_COLUMNS[column];
  if (current === c.desc) return "desc";
  if (current === c.asc) return "asc";
  return null;
}

/** "3 files", "1 folder". */
export function plural(n: number, word: string): string {
  return `${n} ${word}${n === 1 ? "" : "s"}`;
}

/** A short relative date for the Modified column. */
export function shortDate(iso: string | undefined, now = Date.now()): string {
  if (!iso) return "";
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return "";
  const days = (now - t) / 864e5;
  if (days < 1) return "Today";
  if (days < 2) return "Yesterday";
  if (days < 7) return `${Math.floor(days)} days ago`;
  return new Date(t).toLocaleDateString(undefined, { month: "short", day: "numeric", year: "numeric" });
}

/**
 * pickRange is the keys between the anchor and the clicked key, inclusive, in
 * listing order: what Shift-click selects. With no anchor on screen it is the
 * clicked key alone.
 */
export function pickRange(keys: string[], anchor: string | null, key: string): string[] {
  const a = anchor === null ? -1 : keys.indexOf(anchor);
  const b = keys.indexOf(key);
  if (a < 0 || b < 0) return [key];
  return keys.slice(Math.min(a, b), Math.max(a, b) + 1);
}

/** The name a new folder takes: "New folder", then "New folder 2", and so on. */
export function freshFolderName(taken: string[]): string {
  const names = new Set(taken);
  if (!names.has("New folder")) return "New folder";
  for (let i = 2; ; i++) {
    const n = `New folder ${i}`;
    if (!names.has(n)) return n;
  }
}
