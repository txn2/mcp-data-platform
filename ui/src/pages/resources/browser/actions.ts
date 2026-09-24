import { resourceFetch, resourceFetchRaw } from "@/api/resources/client";
import type { FolderMoveResult, Resource, ResourceListResponse } from "@/api/resources/types";
import type { ResourceRoot } from "../scopes";
import { isUnder, joinPath, parentPath } from "../parts/tree";

/**
 * What the file manager does to a selection (#1872), as plain requests.
 *
 * Each file is its own request, so a refusal stops nothing else and the report
 * names what happened to each, as the bulk actions always have. A folder moves
 * in one request, because the server moves its subtree in one transaction.
 */

/** One item's outcome, which is how a report says what happened to each. */
export interface Outcome {
  name: string;
  error?: string;
}

/** The rows an action was given: files, and folders standing for their subtree. */
export interface Picked {
  files: Resource[];
  folders: string[];
}

/** How to put one moved item back, which is what Undo on the toast runs. */
export type UndoStep =
  | { kind: "file"; id: string; name: string; path: string }
  | { kind: "folder"; from: string; to: string };

/** The most files one folder's contents are read for, as the server's move cap. */
export const MAX_FOLDER_FILES = 500;

const PAGE = 200;

function message(err: unknown): string {
  return err instanceof Error ? err.message : "failed";
}

async function ok(res: Response): Promise<void> {
  if (res.ok) return;
  const body = (await res.json().catch(() => ({ error: res.statusText }))) as { error?: string };
  throw new Error(body.error || res.statusText);
}

function libraryBody(root: ResourceRoot): { scope: string; scope_id?: string } {
  return root.target.scope_id
    ? { scope: root.target.scope, scope_id: root.target.scope_id }
    : { scope: root.target.scope };
}

/**
 * filesUnder reads every file at and beneath a folder, page by page. It refuses
 * a folder holding more than MAX_FOLDER_FILES rather than acting on part of it.
 */
export async function filesUnder(root: ResourceRoot, path: string): Promise<Resource[]> {
  const out: Resource[] = [];
  for (let offset = 0; ; offset += PAGE) {
    const sp = new URLSearchParams({ ...root.params, path, limit: String(PAGE), offset: String(offset) });
    const page = await resourceFetch<ResourceListResponse>(`?${sp.toString()}`);
    out.push(...page.resources);
    if (page.total > MAX_FOLDER_FILES) {
      throw new Error(
        `${page.total} files are in "${path}", more than the ${MAX_FOLDER_FILES} one action covers; act on a folder inside it`,
      );
    }
    if (page.resources.length === 0 || out.length >= page.total) return out;
  }
}

/** The files a selection covers: its files, and every file under its folders. */
export async function expandPicked(root: ResourceRoot, picked: Picked): Promise<Resource[]> {
  const byID = new Map(picked.files.map((r) => [r.id, r]));
  for (const f of picked.folders) {
    for (const r of await filesUnder(root, f)) byID.set(r.id, r);
  }
  return [...byID.values()];
}

async function patch(id: string, body: Record<string, unknown>): Promise<void> {
  await resourceFetch<Resource>(`/${id}`, { method: "PATCH", body: JSON.stringify(body) });
}

async function moveFolderRequest(root: ResourceRoot, from: string, to: string): Promise<void> {
  await resourceFetch<FolderMoveResult>("/folders/move", {
    method: "POST",
    body: JSON.stringify({ ...libraryBody(root), from, to }),
  });
}

/** The last segment of a folder path. */
function baseName(path: string): string {
  return path.slice(path.lastIndexOf("/") + 1);
}

/**
 * moveMany files every picked file into `to`, and nests every picked folder
 * under it. An item already there, and a folder dropped into itself, are left
 * alone and reported so. The steps returned put back what moved.
 */
export async function moveMany(
  root: ResourceRoot,
  picked: Picked,
  to: string,
): Promise<{ outcomes: Outcome[]; undo: UndoStep[] }> {
  const outcomes: Outcome[] = [];
  const undo: UndoStep[] = [];
  for (const from of picked.folders) {
    const name = baseName(from);
    if (isUnder(to, from)) {
      outcomes.push({ name, error: "cannot go inside itself" });
      continue;
    }
    if (parentPath(from) === to) continue;
    const dest = joinPath(to, name);
    try {
      await moveFolderRequest(root, from, dest);
      outcomes.push({ name });
      undo.push({ kind: "folder", from: dest, to: from });
    } catch (err) {
      outcomes.push({ name, error: message(err) });
    }
  }
  for (const r of picked.files) {
    if (r.path === to) continue;
    try {
      await patch(r.id, { path: to });
      outcomes.push({ name: r.display_name });
      undo.push({ kind: "file", id: r.id, name: r.display_name, path: r.path });
    } catch (err) {
      outcomes.push({ name: r.display_name, error: message(err) });
    }
  }
  return { outcomes, undo };
}

/** undoMoves puts back what a move carried, newest first. */
export async function undoMoves(root: ResourceRoot, steps: UndoStep[]): Promise<Outcome[]> {
  const outcomes: Outcome[] = [];
  for (const step of [...steps].reverse()) {
    try {
      if (step.kind === "file") await patch(step.id, { path: step.path });
      else await moveFolderRequest(root, step.from, step.to);
      outcomes.push({ name: step.kind === "file" ? step.name : baseName(step.to) });
    } catch (err) {
      outcomes.push({ name: step.kind === "file" ? step.name : baseName(step.to), error: message(err) });
    }
  }
  return outcomes;
}

/** renameFile changes the name a file is listed under. */
export function renameFile(id: string, name: string): Promise<void> {
  return patch(id, { display_name: name });
}

/** renameFolder gives a folder a new last segment, carrying its subtree. */
export function renameFolder(root: ResourceRoot, from: string, name: string): Promise<void> {
  return moveFolderRequest(root, from, joinPath(parentPath(from), name));
}

/** createFolder records an empty folder. */
export async function createFolder(root: ResourceRoot, path: string): Promise<void> {
  await ok(
    await resourceFetchRaw("/folders", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ ...libraryBody(root), path }),
    }),
  );
}

async function deleteFolderRequest(root: ResourceRoot, path: string): Promise<void> {
  const res = await resourceFetchRaw("/folders", {
    method: "DELETE",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ ...libraryBody(root), path }),
  });
  // A folder nothing stored is gone once its last file is: there is nothing
  // left to delete, which is the outcome asked for.
  if (res.status === 404) return;
  await ok(res);
}

async function deleteFile(id: string): Promise<void> {
  await ok(await resourceFetchRaw(`/${id}`, { method: "DELETE" }));
}

/**
 * deleteMany deletes every file the selection covers, one request each, then
 * each picked folder once nothing is left in it. A folder whose files were not
 * all deleted stays, and says why.
 */
export async function deleteMany(root: ResourceRoot, files: Resource[], folders: string[]): Promise<Outcome[]> {
  const outcomes: Outcome[] = [];
  for (const r of files) {
    try {
      await deleteFile(r.id);
      outcomes.push({ name: r.display_name });
    } catch (err) {
      outcomes.push({ name: r.display_name, error: message(err) });
    }
  }
  for (const path of folders) {
    try {
      await deleteFolderRequest(root, path);
      outcomes.push({ name: `${baseName(path)}/` });
    } catch (err) {
      outcomes.push({ name: `${baseName(path)}/`, error: message(err) });
    }
  }
  return outcomes;
}

/** tagMany adds tags to each file, keeping the ones it already carries. */
export async function tagMany(files: Resource[], tags: string[]): Promise<Outcome[]> {
  const outcomes: Outcome[] = [];
  for (const r of files) {
    try {
      await patch(r.id, { tags: [...new Set([...(r.tags ?? []), ...tags])] });
      outcomes.push({ name: r.display_name });
    } catch (err) {
      outcomes.push({ name: r.display_name, error: message(err) });
    }
  }
  return outcomes;
}

/** downloadResource hands the file's current content to the browser. */
export async function downloadResource(r: Resource): Promise<void> {
  const res = await resourceFetchRaw(`/${r.id}/content`);
  if (!res.ok) return;
  const url = URL.createObjectURL(await res.blob());
  const a = document.createElement("a");
  a.href = url;
  a.download = r.filename;
  a.click();
  URL.revokeObjectURL(url);
}

/** A report's one-line summary: "3 moved", "2 of 3 deleted, 1 refused". */
export function summarize(outcomes: Outcome[], verb: string): string {
  const failed = outcomes.filter((o) => o.error).length;
  const done = outcomes.length - failed;
  if (failed === 0) return `${done} ${verb}`;
  return `${done} of ${outcomes.length} ${verb}, ${failed} refused`;
}
