import { useCallback, useState } from "react";
import { Loader2, X } from "lucide-react";
import { useDeleteResource, useUpdateResource } from "@/api/resources/hooks";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ModalShell } from "@/components/ModalShell";
import { parseTags } from "@/lib/tags";
import type { Resource } from "@/api/resources/types";
import { PathField } from "../parts/PathField";
import { pathProblem } from "../parts/pathRules";

/** What a bulk action does to each file it was given. */
export type BulkAction = "move" | "tag" | "delete";

/** One file's outcome, which is how the report says what happened to each. */
interface Outcome {
  name: string;
  error?: string;
}

const TITLES: Record<BulkAction, string> = {
  move: "Move to another folder",
  tag: "Add tags",
  delete: "Delete files",
};

/** What the submit button says, which is what the action does. */
const SUBMIT_LABELS: Record<BulkAction, string> = {
  move: "Move",
  tag: "Add tags",
  delete: "Delete",
};

/**
 * One action over a selection, reporting what it did to each file.
 *
 * Each file is its own request, so each is atomic on its own and a refusal
 * stops nothing else: the eight that could move do, and the two that collided
 * stay where they were with the server's reason next to them. A single
 * all-or-nothing request would have been the other design, and it would mean a
 * selection of forty is undone by one filename clash.
 *
 * A folder rename is the opposite and is not this dialog: it is one transaction
 * on the server, because a half-renamed folder is not a state anyone should be
 * able to observe.
 */
export function BulkActionModal({
  action,
  resources,
  folders,
  currentPath,
  onClose,
  onDone,
}: {
  action: BulkAction;
  /** The picked files, resolved to rows so the report can name them. */
  resources: Resource[];
  /** The folders in this library, offered as move destinations. */
  folders: string[];
  /** Where the person is standing, which is what a move starts from. */
  currentPath: string;
  onClose: () => void;
  /** Called once every file has been attempted and none failed. */
  onDone: () => void;
}) {
  const update = useUpdateResource();
  const remove = useDeleteResource();
  const [destination, setDestination] = useState(currentPath);
  const [tagsInput, setTagsInput] = useState("");
  const [running, setRunning] = useState(false);
  const [report, setReport] = useState<Outcome[] | null>(null);
  const [error, setError] = useState("");

  const apply = useCallback(
    async (r: Resource) => {
      if (action === "delete") return remove.mutateAsync(r.id);
      if (action === "move") return update.mutateAsync({ id: r.id, update: { path: destination } });
      // Tagging adds rather than replaces: a bulk edit that wrote only the tags
      // typed here would silently strip whatever each file already carried.
      const merged = [...new Set([...(r.tags ?? []), ...parseTags(tagsInput)])];
      return update.mutateAsync({ id: r.id, update: { tags: merged } });
    },
    [action, destination, tagsInput, update, remove],
  );

  const run = useCallback(async () => {
    const rejection = rejectBulk(action, destination, tagsInput);
    if (rejection) {
      setError(rejection);
      return;
    }
    setError("");
    setRunning(true);
    const outcomes = await applyToEach(resources, apply);
    setRunning(false);
    setReport(outcomes);
    if (!outcomes.some((o) => o.error)) onDone();
  }, [action, destination, tagsInput, resources, apply, onDone]);

  return (
    <ModalShell
      onClose={onClose}
      label={TITLES[action]}
      busy={running}
      bodyClass="space-y-4 p-4"
      header={
        <div className="flex items-center justify-between border-b p-4">
          <h2 className="text-lg font-semibold">{TITLES[action]}</h2>
          <Button variant="ghost" size="icon-sm" onClick={onClose} aria-label="Close">
            <X />
          </Button>
        </div>
      }
      footer={
        <div className="flex justify-end gap-2 border-t p-4">
          <Button variant="outline" onClick={onClose}>
            {report ? "Close" : "Cancel"}
          </Button>
          {!report && (
            <Button
              onClick={run}
              disabled={running}
              variant={action === "delete" ? "destructive" : "default"}
            >
              {running && <Loader2 className="animate-spin" />}
              {SUBMIT_LABELS[action]}
            </Button>
          )}
        </div>
      }
    >
      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      <p className="text-sm text-muted-foreground" data-testid="bulk-count">
        {resources.length} {resources.length === 1 ? "file" : "files"} selected.
      </p>

      {!report && (
        <BulkForm
          action={action}
          destination={destination}
          onDestinationChange={setDestination}
          tagsInput={tagsInput}
          onTagsChange={setTagsInput}
          folders={folders}
          disabled={running}
        />
      )}

      {report && <BulkReport outcomes={report} />}
    </ModalShell>
  );
}

/**
 * applyToEach runs the action over every file and records what happened to it.
 *
 * Sequential, and a refusal stops nothing else: each file is its own request,
 * so the eight that could move do and the two that collided stay where they
 * were with the server's reason beside them.
 */
async function applyToEach(
  resources: Resource[],
  apply: (r: Resource) => Promise<unknown>,
): Promise<Outcome[]> {
  const outcomes: Outcome[] = [];
  for (const r of resources) {
    try {
      await apply(r);
      outcomes.push({ name: r.display_name });
    } catch (err) {
      outcomes.push({ name: r.display_name, error: err instanceof Error ? err.message : "failed" });
    }
  }
  return outcomes;
}

/** The one field the chosen action needs, and the sentence a delete owes. */
function BulkForm({
  action,
  destination,
  onDestinationChange,
  tagsInput,
  onTagsChange,
  folders,
  disabled,
}: {
  action: BulkAction;
  destination: string;
  onDestinationChange: (value: string) => void;
  tagsInput: string;
  onTagsChange: (value: string) => void;
  folders: string[];
  disabled: boolean;
}) {
  if (action === "move") {
    return (
      <PathField
        label="Destination folder"
        value={destination}
        onChange={onDestinationChange}
        folders={folders}
        disabled={disabled}
        autoFocus
      />
    );
  }
  if (action === "tag") {
    return (
      <div className="space-y-1">
        <Label htmlFor="bulk-tags" className="text-xs text-muted-foreground">
          Tags to add (comma-separated)
        </Label>
        <Input
          id="bulk-tags"
          value={tagsInput}
          onChange={(e) => onTagsChange(e.target.value)}
          placeholder="finance, q4"
          disabled={disabled}
        />
        <p className="text-xs text-muted-foreground">
          These are added to whatever each file already carries; nothing is removed.
        </p>
      </div>
    );
  }
  return (
    <p className="text-sm text-muted-foreground">
      This removes the metadata and the stored file for each one. It cannot be undone, and an asset
      that references a deleted file will report a picture missing.
    </p>
  );
}

/**
 * rejectBulk states why the action cannot run, or null when it can. The server
 * checks all of this again; this only spares a request per file.
 */
function rejectBulk(action: BulkAction, destination: string, tagsInput: string): string | null {
  if (action === "move") return pathProblem(destination);
  if (action === "tag" && parseTags(tagsInput).length === 0) return "Name at least one tag.";
  return null;
}

/**
 * What the action did, per file.
 *
 * The successes are named as well as the refusals. A report that listed only
 * what failed would leave somebody who selected forty files unable to tell
 * whether the other thirty-eight were touched at all.
 */
function BulkReport({ outcomes }: { outcomes: Outcome[] }) {
  const failed = outcomes.filter((o) => o.error);
  return (
    <div className="space-y-2" data-testid="bulk-report">
      <p className="text-sm font-medium">
        {outcomes.length - failed.length} of {outcomes.length} done
        {failed.length > 0 && `, ${failed.length} refused`}
      </p>
      <ul className="max-h-64 space-y-1 overflow-y-auto rounded-md border p-2 text-xs">
        {outcomes.map((o, i) => (
          <li key={`${o.name}-${i}`} className="flex gap-2">
            <span className="min-w-0 flex-1 truncate">{o.name}</span>
            <span
              className={o.error ? "shrink-0 text-destructive" : "shrink-0 text-muted-foreground"}
            >
              {o.error ?? "done"}
            </span>
          </li>
        ))}
      </ul>
      {failed.length > 0 && (
        <p className="text-xs text-muted-foreground">
          The files above that were refused are where they were; nothing about them changed.
        </p>
      )}
    </div>
  );
}
