import { useEffect, useState } from "react";
import type { Folder, Resource } from "@/api/resources/types";
import { BulkUploadModal } from "../modals/BulkUploadModal";
import { UploadModal } from "../modals/UploadModal";
import type { SourceFile } from "../bulk/plan";
import type { ResourceRoot } from "../scopes";
import { deleteMany, expandPicked, summarize, tagMany, type Picked } from "./actions";
import { DeleteDialog, MoveDialog, TagDialog } from "./ActionDialogs";
import type { useActions } from "./useActions";

/** Which dialog the file manager has open, if any. */
export type FmDialog =
  | { kind: "move" | "tag" | "delete"; picked: Picked }
  | { kind: "upload" | "uploadMany"; folder: string; files?: SourceFile[]; root: ResourceRoot }
  | null;

/** The open dialog: an action over the selection, or an upload. */
export function FmDialogs({
  dialog,
  root,
  path,
  folders,
  admin,
  personaNames,
  actions,
  onClose,
  onDone,
}: {
  dialog: FmDialog;
  root: ResourceRoot;
  path: string;
  folders: Folder[];
  admin: boolean;
  personaNames: string[];
  actions: ReturnType<typeof useActions>;
  onClose: () => void;
  /** A dialog that finished cleanly, with what the toast says about it. */
  onDone: (message?: string) => void;
}) {
  if (!dialog) return null;
  const paths = folders.map((f) => f.path);
  switch (dialog.kind) {
    case "upload":
      return (
        <UploadModal
          admin={admin}
          personaNames={personaNames}
          destination={dialog.root.target}
          folder={dialog.folder}
          folders={dialog.root.key === root.key ? paths : []}
          onClose={onClose}
        />
      );
    case "uploadMany":
      return (
        <BulkUploadModal
          personaNames={personaNames}
          destination={dialog.root.target}
          folder={dialog.folder}
          folders={dialog.root.key === root.key ? paths : []}
          initial={dialog.files}
          onClose={onClose}
        />
      );
    case "move":
      return (
        <MoveDialog
          root={root}
          folders={folders}
          from={path}
          picked={dialog.picked}
          onClose={onClose}
          onMove={async (to) => {
            const outcomes = await actions.move(dialog.picked, to);
            if (!outcomes.some((o) => o.error)) onDone();
            return outcomes;
          }}
        />
      );
    case "tag":
      return (
        <TagDialog
          picked={dialog.picked}
          onClose={onClose}
          onTag={async (tags) => {
            const outcomes = await tagMany(await expandPicked(root, dialog.picked), tags);
            await actions.refresh();
            if (!outcomes.some((o) => o.error)) onDone(summarize(outcomes, "tagged"));
            return outcomes;
          }}
        />
      );
    case "delete":
      return <Deleting root={root} picked={dialog.picked} actions={actions} onClose={onClose} onDone={onDone} />;
  }
}

/**
 * The delete dialog. It opens at once and counts the files inside the picked
 * folders itself, holding its confirm until the count is in, because the count
 * is what it exists to show and the read behind it can take seconds (#1887). A
 * count that fails is shown in the dialog with Retry.
 */
function Deleting({
  root,
  picked,
  actions,
  onClose,
  onDone,
}: {
  root: ResourceRoot;
  picked: Picked;
  actions: ReturnType<typeof useActions>;
  onClose: () => void;
  onDone: (message?: string) => void;
}) {
  const [files, setFiles] = useState<Resource[] | null>(picked.folders.length ? null : picked.files);
  const [error, setError] = useState("");
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    if (!picked.folders.length) return;
    let live = true;
    expandPicked(root, picked).then(
      (all) => live && setFiles(all),
      (err: unknown) => live && setError(err instanceof Error ? err.message : "The folder could not be read."),
    );
    return () => {
      live = false;
    };
  }, [root, picked, attempt]);
  return (
    <DeleteDialog
      files={files}
      picked={picked}
      countError={error}
      onRetry={() => {
        setError("");
        setAttempt((n) => n + 1);
      }}
      onClose={onClose}
      onDelete={async () => {
        if (!files) return [];
        const outcomes = await deleteMany(root, files, picked.folders);
        await actions.refresh();
        if (!outcomes.some((o) => o.error)) onDone(summarize(outcomes, "deleted"));
        return outcomes;
      }}
    />
  );
}
