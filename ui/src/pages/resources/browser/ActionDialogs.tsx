import { useState, type ReactNode } from "react";
import { Folder, Loader2, X } from "lucide-react";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ModalShell } from "@/components/ModalShell";
import { parseTags } from "@/lib/tags";
import { cn } from "@/lib/utils";
import type { Folder as FolderRow, Resource } from "@/api/resources/types";
import { displayPath, type ResourceRoot } from "../scopes";
import { isUnder } from "../parts/tree";
import type { Outcome, Picked } from "./actions";
import { plural } from "./model";

/** The frame every action dialog shares: a title, a body, Cancel and the action. */
function Frame({
  title,
  busy,
  onClose,
  action,
  danger,
  onRun,
  report,
  children,
}: {
  title: string;
  busy: boolean;
  onClose: () => void;
  action: string;
  danger?: boolean;
  onRun: () => void;
  report: Outcome[] | null;
  children: ReactNode;
}) {
  return (
    <ModalShell
      onClose={onClose}
      label={title}
      busy={busy}
      bodyClass="space-y-3 p-4"
      header={
        <div className="flex items-center justify-between border-b p-4">
          <h2 className="text-lg font-semibold">{title}</h2>
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
            <Button onClick={onRun} disabled={busy} variant={danger ? "destructive" : "default"}>
              {busy && <Loader2 className="animate-spin" />}
              {action}
            </Button>
          )}
        </div>
      }
    >
      {report ? <Report outcomes={report} /> : children}
    </ModalShell>
  );
}

/**
 * What an action did, per item. The successes are named as well as the
 * refusals: somebody who selected forty files has to be able to tell whether
 * the other thirty-eight were touched.
 */
function Report({ outcomes }: { outcomes: Outcome[] }) {
  const failed = outcomes.filter((o) => o.error).length;
  return (
    <div className="space-y-2" data-testid="action-report">
      <p className="text-sm font-medium">
        {outcomes.length - failed} of {outcomes.length} done{failed > 0 && `, ${failed} refused`}
      </p>
      <ul className="max-h-64 space-y-1 overflow-y-auto rounded-md border p-2 text-xs">
        {outcomes.map((o, i) => (
          <li key={`${o.name}-${i}`} className="flex gap-2">
            <span className="min-w-0 flex-1 truncate">{o.name}</span>
            <span className={o.error ? "shrink-0 text-destructive" : "shrink-0 text-muted-foreground"}>
              {o.error ?? "done"}
            </span>
          </li>
        ))}
      </ul>
      <p className="text-xs text-muted-foreground">What was refused is where it was; nothing about it changed.</p>
    </div>
  );
}

/** Runs an action, closing on a clean run and showing the report otherwise. */
function useRun(run: () => Promise<Outcome[]>, onDone: (outcomes: Outcome[]) => void) {
  const [busy, setBusy] = useState(false);
  const [report, setReport] = useState<Outcome[] | null>(null);
  const [error, setError] = useState("");
  const go = async () => {
    setBusy(true);
    setError("");
    try {
      const outcomes = await run();
      if (outcomes.some((o) => o.error)) setReport(outcomes);
      else onDone(outcomes);
    } catch (err) {
      setError(err instanceof Error ? err.message : "The action failed.");
    } finally {
      setBusy(false);
    }
  };
  return { busy, report, error, go };
}

function describe(picked: Picked): string {
  const parts = [];
  if (picked.folders.length) parts.push(plural(picked.folders.length, "folder"));
  if (picked.files.length) parts.push(plural(picked.files.length, "file"));
  return parts.join(" and ");
}

/**
 * Move to: a picker over the folders of the top-level folder in view, and the
 * destination written as a path (#1872).
 */
export function MoveDialog({
  root,
  folders,
  from,
  picked,
  onClose,
  onMove,
}: {
  root: ResourceRoot;
  folders: FolderRow[];
  from: string;
  picked: Picked;
  onClose: () => void;
  onMove: (to: string) => Promise<Outcome[]>;
}) {
  const [target, setTarget] = useState(from);
  const run = useRun(() => onMove(target), onClose);
  // A folder cannot go inside itself, so its own subtree is not offered.
  const choices = folders
    .map((f) => f.path)
    .filter((p) => !picked.folders.some((f) => isUnder(p, f)))
    .sort((a, b) => a.localeCompare(b));
  const invalid = target === "";
  return (
    <Frame
      title={`Move ${describe(picked)}`}
      busy={run.busy}
      onClose={onClose}
      action="Move here"
      onRun={() => !invalid && void run.go()}
      report={run.report}
    >
      {run.error && (
        <Alert variant="destructive">
          <AlertDescription>{run.error}</AlertDescription>
        </Alert>
      )}
      <div className="max-h-64 overflow-auto rounded-md border p-1" role="listbox" aria-label="Destination folder" data-testid="move-picker">
        {choices.map((p) => (
          <button
            key={p}
            type="button"
            role="option"
            aria-selected={p === target}
            className={cn(
              "flex h-[26px] w-full items-center gap-1.5 rounded-[5px] pr-2 text-left text-sm hover:bg-accent",
              p === target && "bg-primary/10",
            )}
            style={{ paddingLeft: 8 + (p.split("/").length - 1) * 14 }}
            onClick={() => setTarget(p)}
          >
            <Folder className="size-4 shrink-0 text-[hsl(217_30%_55%)]" />
            <span className="truncate">{p.slice(p.lastIndexOf("/") + 1)}</span>
          </button>
        ))}
      </div>
      <p className="text-xs text-muted-foreground" data-testid="move-destination">
        {invalid ? "Choose a folder: files are always filed in one." : `Destination: ${displayPath(root, target)}`}
      </p>
    </Frame>
  );
}

/** Tag: tags added to every file the selection covers, keeping the ones each carries. */
export function TagDialog({
  picked,
  onClose,
  onTag,
}: {
  picked: Picked;
  onClose: () => void;
  onTag: (tags: string[]) => Promise<Outcome[]>;
}) {
  const [input, setInput] = useState("");
  const [problem, setProblem] = useState("");
  const run = useRun(() => onTag(parseTags(input)), onClose);
  const submit = () => {
    if (parseTags(input).length === 0) {
      setProblem("Name at least one tag.");
      return;
    }
    setProblem("");
    void run.go();
  };
  return (
    <Frame title={`Tag ${describe(picked)}`} busy={run.busy} onClose={onClose} action="Add tags" onRun={submit} report={run.report}>
      {(problem || run.error) && (
        <Alert variant="destructive">
          <AlertDescription>{problem || run.error}</AlertDescription>
        </Alert>
      )}
      <div className="space-y-1">
        <Label htmlFor="fm-tags" className="text-xs text-muted-foreground">
          Tags to add (comma-separated)
        </Label>
        <Input
          id="fm-tags"
          autoFocus
          value={input}
          onChange={(e) => setInput(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && submit()}
          placeholder="finance, q4"
        />
        <p className="text-xs text-muted-foreground">
          Added to what each file already carries, including every file inside a selected folder; nothing is removed.
        </p>
      </div>
    </Frame>
  );
}

/**
 * Delete: every file the selection covers, then the selected folders once
 * they are empty. It names what goes before anything does.
 */
export function DeleteDialog({
  files,
  folders,
  onClose,
  onDelete,
}: {
  /** Every file that will be deleted, the folders' contents included. */
  files: Resource[];
  folders: string[];
  onClose: () => void;
  onDelete: () => Promise<Outcome[]>;
}) {
  const run = useRun(onDelete, onClose);
  const names = [...folders.map((f) => `${f.slice(f.lastIndexOf("/") + 1)}/`), ...files.map((r) => r.display_name)];
  return (
    <Frame
      title={`Delete ${describe({ files, folders })}?`}
      busy={run.busy}
      onClose={onClose}
      action="Delete"
      danger
      onRun={() => void run.go()}
      report={run.report}
    >
      {run.error && (
        <Alert variant="destructive">
          <AlertDescription>{run.error}</AlertDescription>
        </Alert>
      )}
      <p className="text-sm text-muted-foreground" data-testid="delete-names">
        {names.slice(0, 6).join(", ")}
        {names.length > 6 && `, and ${names.length - 6} more`}.
      </p>
      <p className="text-sm text-muted-foreground">
        Each file&rsquo;s stored content and versions go with it, and it cannot be undone. Prompts and assets that
        reference a deleted file will show it missing.
      </p>
    </Frame>
  );
}
