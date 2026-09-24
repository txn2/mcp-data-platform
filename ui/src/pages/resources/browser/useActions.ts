import { useCallback } from "react";
import { useQueryClient } from "@tanstack/react-query";
import type { Resource } from "@/api/resources/types";
import { displayPath, type ResourceRoot } from "../scopes";
import { joinPath, parentPath } from "../parts/tree";
import {
  createFolder,
  moveMany,
  renameFile,
  renameFolder,
  undoMoves,
  type Outcome,
  type Picked,
} from "./actions";
import { folderKey, folderOfKey, plural, type Entry } from "./model";

/**
 * useActions is the page's writes over the top-level folder in view, each
 * followed by one refresh of the listing and the tree, and each reported on the
 * toast (#1872).
 */
export function useActions(
  root: ResourceRoot,
  entries: Entry[],
  toast: (message: string, undo?: () => void) => void,
) {
  const qc = useQueryClient();
  const refresh = useCallback(() => qc.invalidateQueries({ queryKey: ["resources"] }), [qc]);

  /** The rows a set of keys names, as files and folders. */
  const pick = useCallback(
    (keys: string[]): Picked => {
      const byKey = new Map(entries.map((e) => [e.key, e]));
      const files: Resource[] = [];
      const folders: string[] = [];
      for (const k of keys) {
        const folder = folderOfKey(k);
        if (folder !== null) folders.push(folder);
        const e = byKey.get(k);
        if (e?.kind === "file") files.push(e.resource);
      }
      return { files, folders };
    },
    [entries],
  );

  /** Moves the rows into a folder; the toast offers to put them back. */
  const move = useCallback(
    async (picked: Picked, to: string): Promise<Outcome[]> => {
      const { outcomes, undo } = await moveMany(root, picked, to);
      await refresh();
      if (outcomes.length === 0) return outcomes;
      const put = undo.length
        ? async () => {
            const back = await undoMoves(root, undo);
            await refresh();
            toast(report("Put back", back));
          }
        : undefined;
      toast(`${report("Moved", outcomes)} to ${displayPath(root, to)}`, put);
      return outcomes;
    },
    [root, refresh, toast],
  );

  /** Renames a row inline: a file's name, or a folder's last segment. */
  const rename = useCallback(
    async (entry: Entry, name: string): Promise<string | null> => {
      const clean = name.trim().split("/").join("-");
      if (!clean || clean === entry.name) return null;
      try {
        if (entry.kind === "file") {
          await renameFile(entry.resource.id, clean);
          await refresh();
          return entry.key;
        }
        await renameFolder(root, entry.path, clean);
        await refresh();
        return folderKey(joinPath(parentPath(entry.path), clean));
      } catch (err) {
        toast(err instanceof Error ? err.message : "The rename failed.");
        return null;
      }
    },
    [root, refresh, toast],
  );

  /** Creates an empty folder in the folder in view. */
  const create = useCallback(
    async (at: string, name: string): Promise<string | null> => {
      const clean = name.trim().split("/").join("-");
      if (!clean) return null;
      try {
        await createFolder(root, joinPath(at, clean));
        await refresh();
        return folderKey(joinPath(at, clean));
      } catch (err) {
        toast(err instanceof Error ? err.message : "The folder was not created.");
        return null;
      }
    },
    [root, refresh, toast],
  );

  return { pick, move, rename, create, refresh };
}

/** "Moved 3 items", or "Moved 2 of 3 items (1 refused: <why>)". */
function report(verb: string, outcomes: Outcome[]): string {
  const failed = outcomes.filter((o) => o.error);
  const done = outcomes.length - failed.length;
  if (failed.length === 0) return `${verb} ${plural(done, "item")}`;
  return `${verb} ${done} of ${plural(outcomes.length, "item")} (${failed.length} refused: ${failed[0]!.error})`;
}
