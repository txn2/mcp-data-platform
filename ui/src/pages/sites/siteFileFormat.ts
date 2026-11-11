import type { SiteFile } from "@/api/portal/hooks/sites";
import { isTextualType } from "@/lib/contentType";
import { compareNames } from "@/lib/sortByName";

// The Files tab's arithmetic (#2590): a folder's entries out of the flat list
// the server returns, what opens in the editor, and the editor's address.

export type FolderEntry = { kind: "folder"; name: string; path: string; count: number } | { kind: "file"; name: string; path: string; file: SiteFile };

/** The folders and files directly in folder, folders first; with a filter, every file whose path holds it. */
export function folderEntries(files: SiteFile[], folder: string, filter: string): FolderEntry[] {
  if (filter) {
    const needle = filter.toLowerCase();
    return files.filter((f) => f.path.toLowerCase().includes(needle)).map((f) => ({ kind: "file", name: f.path, path: f.path, file: f }));
  }
  const folders = new Map<string, number>();
  const here: FolderEntry[] = [];
  for (const f of files) {
    if (!f.path.startsWith(folder)) continue;
    const rest = f.path.slice(folder.length);
    const slash = rest.indexOf("/");
    if (slash < 0) {
      here.push({ kind: "file", name: rest, path: f.path, file: f });
      continue;
    }
    const name = rest.slice(0, slash);
    folders.set(name, (folders.get(name) ?? 0) + 1);
  }
  const dirs: FolderEntry[] = [...folders.entries()]
    .sort(([a], [b]) => compareNames(a, b))
    .map(([name, count]) => ({ kind: "folder", name, path: `${folder}${name}/`, count }));
  return [...dirs, ...here.sort((a, b) => compareNames(a.name, b.name))];
}

/** A site whose source has a _layouts folder is rendered at publish. */
export function isRendered(files: SiteFile[]): boolean {
  return files.some((f) => f.path.startsWith("_layouts/"));
}

/** Whether a file opens in the editor: text, the redirect map, or a page. */
export function isText(f: Pick<SiteFile, "path" | "content_type">): boolean {
  return f.path === "_redirects" || isTextualType(f.content_type.split(";")[0]!.trim());
}

/** A path a site's source renders through: a layout, an include or a record. */
export function isTemplate(path: string): boolean {
  return /^(_layouts|_includes|_data)\//.test(path);
}

/** Where a file's editor opens; fresh is a new file that does not exist yet. */
export function filePagePath(site: string, path: string, fresh = false): string {
  return `/sites/${encodeURIComponent(site)}/${fresh ? "newfile" : "file"}/${encodeURIComponent(path)}`;
}

const PATH_RE = /^(?!\/)(?!.*\/\/)(?!.*(^|\/)\.(?!well-known(\/|$)))[^\\\0]+[^/]$/;

/** Why a path cannot be a new file, "" when it can. */
export function newPathProblem(path: string, taken: (p: string) => boolean): string {
  if (!path) return "";
  if (!PATH_RE.test(path) || path.split("/").includes("..")) {
    return "A path is relative, like pricing/faq.html, with no leading slash, no .. and no hidden folder other than .well-known.";
  }
  if (taken(path)) return `${path} already exists. Open it from the file list to change it.`;
  return "";
}
