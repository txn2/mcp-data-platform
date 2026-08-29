import { useCallback, useState } from "react";
import { Loader2, X } from "lucide-react";
import { useMoveFolder } from "@/api/resources/hooks";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { ModalShell } from "@/components/ModalShell";
import type { ScopeTarget } from "../scopes";
import { PathField } from "../parts/PathField";
import { pathProblem } from "../parts/pathRules";
import { isUnder } from "../parts/tree";

/**
 * Renaming a folder, or nesting it under another one.
 *
 * The whole subtree moves in one request because the server does it in one
 * transaction: every resource beneath the folder takes a new address and
 * records the one it left, or none of them do. That is why this is not the
 * bulk dialog with a folder's files selected -- that would move the files it
 * happened to have loaded and leave the rest behind.
 */
export function FolderMoveModal({
  library,
  from,
  suggestedTo,
  folders,
  onClose,
  onMoved,
}: {
  /** The library the folder lives in; a path is only unique inside one. */
  library: ScopeTarget;
  /** The folder being renamed. */
  from: string;
  /**
   * Where it is going, when the person already said so by dropping it on
   * another folder. Absent starts from the folder's own path, which is what
   * Rename means.
   */
  suggestedTo?: string;
  folders: string[];
  onClose: () => void;
  onMoved: (to: string) => void;
}) {
  const move = useMoveFolder();
  const [to, setTo] = useState(suggestedTo ?? from);
  const [error, setError] = useState("");

  const submit = useCallback(async () => {
    const rejection = rejectFolderMove(from, to);
    if (rejection) {
      setError(rejection);
      return;
    }
    try {
      await move.mutateAsync({ scope: library.scope, scope_id: library.scope_id, from, to });
      onMoved(to);
    } catch (err) {
      setError(err instanceof Error ? err.message : "The folder was not moved.");
    }
  }, [from, to, library, move, onMoved]);

  return (
    <ModalShell
      onClose={onClose}
      label="Rename or move folder"
      busy={move.isPending}
      bodyClass="space-y-4 p-4"
      header={
        <div className="flex items-center justify-between border-b p-4">
          <h2 className="text-lg font-semibold">Rename or move folder</h2>
          <Button variant="ghost" size="icon-sm" onClick={onClose} aria-label="Close">
            <X />
          </Button>
        </div>
      }
      footer={
        <div className="flex justify-end gap-2 border-t p-4">
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={move.isPending}>
            {move.isPending && <Loader2 className="animate-spin" />}
            Move
          </Button>
        </div>
      }
    >
      {error && (
        <Alert variant="destructive">
          <AlertDescription data-testid="folder-move-error">{error}</AlertDescription>
        </Alert>
      )}

      <p className="text-sm text-muted-foreground">
        Everything filed under <strong>{from}</strong>, at every depth, moves with it. Each file
        keeps answering at the address it had, so anything already pointing at one still resolves.
      </p>

      <PathField
        label="New path"
        value={to}
        onChange={setTo}
        folders={folders.filter((f) => !isUnder(f, from))}
        disabled={move.isPending}
        autoFocus
      />
    </ModalShell>
  );
}

/** rejectFolderMove states why the move cannot be sent, or null when it can. */
function rejectFolderMove(from: string, to: string): string | null {
  const problem = pathProblem(to);
  if (problem) return problem;
  if (to === from) return "That is where the folder already is.";
  // A folder cannot hold itself: every resource under it would be rewritten to
  // a path beneath its own new location.
  if (isUnder(to, from)) return `"${to}" is inside "${from}" and cannot hold it.`;
  return null;
}
