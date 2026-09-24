import type { Folder } from "@/api/resources/types";

/** Path arithmetic over folder paths, which the file manager and the dialogs share. */

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

/** Every folder path the library holds, for a picker's completions. */
export function folderPaths(folders: Folder[]): string[] {
  return folders.map((f) => f.path).sort((a, b) => a.localeCompare(b));
}
