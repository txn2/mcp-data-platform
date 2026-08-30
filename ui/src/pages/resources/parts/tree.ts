import type { Folder, Resource } from "@/api/resources/types";

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
 * childFolders is the folders directly inside one location, built from the
 * server's answer rather than from the resources a page happens to hold.
 *
 * The counts are the server's and are exact. The browser used to derive this
 * from the paged listing, so a folder read "25+" until the last page landed and
 * a library root offered a Load-more control whose only visible effect was to
 * firm that number up (#1555).
 */
export function childFolders(folders: Folder[], at: string): FolderEntry[] {
  const depth = at === "" ? 0 : segments(at).length;
  const entries: FolderEntry[] = [];
  for (const f of folders) {
    if (!isUnder(f.path, at) || f.path === at) continue;
    const parts = segments(f.path);
    // Only the level directly below: a folder deeper than that is inside one of
    // these, and is already counted in it.
    if (parts.length !== depth + 1) continue;
    entries.push({ name: parts[depth]!, path: f.path, count: f.count });
  }
  return entries.sort((a, b) => a.name.localeCompare(b.name));
}

/** Every folder path the library holds, for a picker's completions. */
export function folderPaths(folders: Folder[]): string[] {
  return folders.map((f) => f.path).sort((a, b) => a.localeCompare(b));
}
