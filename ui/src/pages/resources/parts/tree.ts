import type { Resource } from "@/api/resources/types";

/**
 * The folder tree a library is browsed as (#1530).
 *
 * A folder is not a stored row. It exists because a resource is filed under it
 * and stops existing when the last one leaves, so every folder on screen is
 * derived here from the paths of the resources in view. That is the whole model:
 * there is no folder to create empty and none to clean up.
 */

/** One child folder of the location being shown. */
export interface FolderEntry {
  /** The folder's own name: the one segment below the current location. */
  name: string;
  /** Its full path inside the library, which is the address it opens at. */
  path: string;
  /** How many resources are filed under it, at every depth. */
  count: number;
}

/** What one location in the tree holds: its child folders and its own files. */
export interface FolderView {
  folders: FolderEntry[];
  files: Resource[];
}

/** True when path is at, or beneath, prefix. An empty prefix is the root. */
export function isUnder(path: string, prefix: string): boolean {
  if (prefix === "") return true;
  return path === prefix || path.startsWith(prefix + "/");
}

/** The folder chain a path names. The root has none. */
export function segments(path: string): string[] {
  return path === "" ? [] : path.split("/");
}

/** The folder holding this one; the root's parent is the root. */
export function parentPath(path: string): string {
  const i = path.lastIndexOf("/");
  return i < 0 ? "" : path.slice(0, i);
}

/** Join a folder path onto a location, either of which may be the root. */
export function joinPath(at: string, name: string): string {
  return at === "" ? name : `${at}/${name}`;
}

/**
 * folderView divides what is loaded into the folders and the files at one
 * location.
 *
 * Only the resources beneath the location count, so the same function serves a
 * page that fetched the whole library and one that fetched a single subtree.
 * Folders are sorted by name and files are left in the server's order, because
 * the sort control orders files and ordering folders by it would make a control
 * over recency look like a control over the tree.
 */
export function folderView(resources: Resource[], at: string): FolderView {
  const counts = new Map<string, number>();
  const files: Resource[] = [];
  const depth = at === "" ? 0 : segments(at).length;

  for (const r of resources) {
    if (!isUnder(r.path, at)) continue;
    if (r.path === at) {
      files.push(r);
      continue;
    }
    const name = segments(r.path)[depth];
    if (name === undefined) continue;
    counts.set(name, (counts.get(name) ?? 0) + 1);
  }

  const folders = [...counts.entries()]
    .map(([name, count]) => ({ name, path: joinPath(at, name), count }))
    .sort((a, b) => a.name.localeCompare(b.name));
  return { folders, files };
}

/**
 * everyFolder is every folder path in view, which is what a destination picker
 * offers and what a path field suggests.
 *
 * It includes each intermediate folder, not only the paths resources are
 * actually filed at: "data/media-manager/shows" means "data" and
 * "data/media-manager" are folders too, and a picker that offered only the leaf
 * would make the levels above it unreachable.
 */
export function everyFolder(resources: Resource[]): string[] {
  const all = new Set<string>();
  for (const r of resources) {
    const parts = segments(r.path);
    for (let i = 1; i <= parts.length; i++) all.add(parts.slice(0, i).join("/"));
  }
  return [...all].sort((a, b) => a.localeCompare(b));
}
