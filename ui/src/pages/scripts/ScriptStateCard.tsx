import { useState } from "react";
import {
  useClearScriptState,
  useScriptState,
  useSetScriptState,
} from "@/api/portal/hooks/scripts";
import type { ScriptContract, ScriptState } from "@/api/portal/hooks/scripts";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { formatWhen } from "./runFormat";

// ScriptStateCard is the one JSON object a script carries from run to run
// (#1537): what the next run will read as run.state, at the revision the
// platform holds it, and who wrote it. A run reads it at creation and saves it
// with platform.save_state when it succeeds, so an incremental job continues
// from where the last successful run stopped and a fire missed to downtime is
// covered by the next one.
//
// The card is where a wrong watermark is corrected. Replacing the object or
// clearing it moves the revision, and a run already in flight that read the
// previous revision fails at its write, which is correct: the reset was after
// its premise. Both are the owner's and an administrator's, and both are
// confirmed, because a reset is a statement about what the next run computes.

// MAX_STATE_BYTES mirrors the platform's bound on the serialized object: state
// is a cursor or a summary, not a dataset.
const MAX_STATE_BYTES = 64 * 1024;

interface Props {
  scriptId: string;
  contract: ScriptContract;
}

export function ScriptStateCard({ scriptId, contract }: Props) {
  const { data, isLoading, error } = useScriptState(scriptId, true);
  const use = contract.state;
  return (
    <SectionCard
      title="State"
      collapsible
      defaultOpen={false}
      summary={stateSummary(data ?? undefined, use)}
    >
      <div className="space-y-4">
        <UseLine use={use} />
        <StateBody
          state={data ?? undefined}
          isLoading={isLoading}
          failed={!!error}
          unavailable={data === null}
        />
        {data && <StateResets scriptId={scriptId} state={data} />}
      </div>
    </SectionCard>
  );
}

// StateResets is the owner's two writes: replace the object, or clear it.
// Each is confirmed or edited before it lands, and the outcome is reported in
// the server's own words, because a reset is a statement about what the next
// run computes.
function StateResets({ scriptId, state }: { scriptId: string; state: ScriptState }) {
  const setState = useSetScriptState(scriptId);
  const clearState = useClearScriptState(scriptId);
  const [mode, setMode] = useState<"idle" | "editing" | "clearing">("idle");
  const [notice, setNotice] = useState<string | null>(null);
  const [failure, setFailure] = useState<string | null>(null);

  const begin = (next: "editing" | "clearing") => {
    setNotice(null);
    setFailure(null);
    setMode(next);
  };
  const landed = (outcome: ScriptState, fallback: string) => {
    setNotice(outcome.message ?? fallback);
    setMode("idle");
  };
  const refused = (e: unknown, fallback: string) => {
    setFailure(e instanceof Error ? e.message : fallback);
    setMode("idle");
  };
  const nothingToClear = state.revision === 0 && Object.keys(state.state).length === 0;

  return (
    <>
      {mode === "idle" && (
        <div className="flex flex-wrap items-center gap-2">
          <Button size="sm" variant="outline" onClick={() => begin("editing")}>
            Edit state
          </Button>
          <Button
            size="sm"
            variant="outline"
            disabled={nothingToClear}
            onClick={() => begin("clearing")}
          >
            Clear state
          </Button>
        </div>
      )}
      {mode === "editing" && (
        <StateEditor
          initial={state.state}
          busy={setState.isPending}
          onCancel={() => setMode("idle")}
          onSave={(next) =>
            setState.mutate(next, {
              onSuccess: (outcome) => landed(outcome, "State replaced."),
              onError: (e: unknown) => refused(e, "The state could not be saved"),
            })
          }
        />
      )}
      {mode === "clearing" && (
        <ClearConfirm
          busy={clearState.isPending}
          onCancel={() => setMode("idle")}
          onConfirm={() =>
            clearState.mutate(undefined, {
              onSuccess: (outcome) => landed(outcome, "State cleared."),
              onError: (e: unknown) => refused(e, "The state could not be cleared"),
            })
          }
        />
      )}
      {notice && (
        <Alert>
          <AlertDescription>{notice}</AlertDescription>
        </Alert>
      )}
      {failure && (
        <Alert variant="destructive">
          <AlertDescription>{failure}</AlertDescription>
        </Alert>
      )}
    </>
  );
}

// ClearConfirm states what a clear does before it is done: the next run
// starts over, and a run in flight fails at its write.
function ClearConfirm({
  busy,
  onConfirm,
  onCancel,
}: {
  busy: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  return (
    <div className="space-y-2 rounded-md border p-3">
      <p className="text-sm">
        Clear this script's state? The next run starts from an empty object, and a run
        already in flight that read the current revision fails at its write.
      </p>
      <div className="flex items-center gap-2">
        <Button size="sm" disabled={busy} onClick={onConfirm}>
          {busy ? "Clearing..." : "Clear"}
        </Button>
        <Button size="sm" variant="ghost" disabled={busy} onClick={onCancel}>
          Cancel
        </Button>
      </div>
    </div>
  );
}

// stateSummary is what the folded header says: whether the script keeps
// state, and where it stands.
function stateSummary(state: ScriptState | undefined, use: ScriptContract["state"]): string {
  const revision = state?.revision ?? use?.revision ?? 0;
  if (revision === 0) return keepsState(use, revision) ? "Nothing saved yet" : "Keeps none";
  const changed = state?.updated_at ? `, changed ${formatWhen(state.updated_at)}` : "";
  return `Revision ${revision}${changed}`;
}

// keepsState reads whether the script does anything with state: from the
// source where the contract says, and from the revision where it does not.
function keepsState(use: ScriptContract["state"], revision: number): boolean {
  return use ? use.reads_state || use.saves_state : revision > 0;
}

// UseLine states what the source does with state, read from the code rather
// than from the object: a script that neither reads nor saves keeps none,
// whatever an old revision says.
function UseLine({ use }: { use: ScriptContract["state"] }) {
  if (!use) return null;
  let text: string;
  if (use.reads_state && use.saves_state) {
    text =
      "This script reads its state at the start of a run and saves it when the run succeeds, so a run continues from the previous run's save.";
  } else if (use.saves_state) {
    text = "This script saves state and never reads it.";
  } else if (use.reads_state) {
    text = "This script reads state and never saves it.";
  } else {
    text =
      "This script neither reads nor saves state. A script reads it as run.state and saves it with platform.save_state.";
  }
  return <p className="text-sm text-muted-foreground">{text}</p>;
}

// StateBody is the object itself, with the revision and its writer, or the
// reason there is nothing to show.
function StateBody({
  state,
  isLoading,
  failed,
  unavailable,
}: {
  state?: ScriptState;
  isLoading: boolean;
  failed: boolean;
  unavailable: boolean;
}) {
  if (isLoading) return <p className="text-sm text-muted-foreground">Loading state...</p>;
  if (failed) {
    return <p className="text-sm text-muted-foreground">This script's state could not be loaded.</p>;
  }
  if (unavailable || !state) {
    return (
      <p className="text-sm text-muted-foreground">This deployment keeps no script state.</p>
    );
  }
  return (
    <div className="space-y-2">
      <dl className="grid gap-x-6 gap-y-2 text-xs sm:grid-cols-3">
        <div>
          <dt className="text-muted-foreground">Revision</dt>
          <dd className="tabular-nums">{state.revision}</dd>
        </div>
        <div>
          <dt className="text-muted-foreground">Last changed</dt>
          <dd>{state.revision > 0 ? formatWhen(state.updated_at) : "never"}</dd>
        </div>
        <div>
          <dt className="text-muted-foreground">Written by</dt>
          <dd className="truncate">{writtenBy(state)}</dd>
        </div>
      </dl>
      <pre
        data-testid="script-state"
        className="max-h-64 overflow-auto rounded-md border bg-background p-3 font-mono text-xs whitespace-pre-wrap"
      >
        {JSON.stringify(state.state, null, 2)}
      </pre>
    </div>
  );
}

// writtenBy names who wrote the current revision: the run, or the person who
// reset it.
function writtenBy(state: ScriptState): string {
  if (state.run_id) return `run ${state.run_id}`;
  if (state.updated_by) return state.updated_by;
  return "nobody";
}

// StateEditor is the whole object as JSON, saved as one replacement. It parses
// before it submits, so a typo is answered here rather than by the server, and
// it refuses anything but an object, because state is an object by contract.
function StateEditor({
  initial,
  busy,
  onSave,
  onCancel,
}: {
  initial: Record<string, unknown>;
  busy: boolean;
  onSave: (state: Record<string, unknown>) => void;
  onCancel: () => void;
}) {
  const [text, setText] = useState(JSON.stringify(initial, null, 2));
  const problem = parseProblem(text);
  return (
    <div className="space-y-2">
      <Label htmlFor="script-state-editor" className="text-xs text-muted-foreground">
        The whole object the next run reads as run.state
      </Label>
      <Textarea
        id="script-state-editor"
        value={text}
        onChange={(e) => setText(e.target.value)}
        rows={8}
        className="font-mono text-xs"
        spellCheck={false}
      />
      {problem && <p className="text-xs text-red-700 dark:text-red-300">{problem}</p>}
      <div className="flex items-center gap-2">
        <Button
          size="sm"
          disabled={busy || !!problem}
          onClick={() => onSave(JSON.parse(text) as Record<string, unknown>)}
        >
          {busy ? "Saving..." : "Replace state"}
        </Button>
        <Button size="sm" variant="ghost" disabled={busy} onClick={onCancel}>
          Cancel
        </Button>
      </div>
    </div>
  );
}

// parseProblem says why the text cannot be saved, or nothing when it can.
export function parseProblem(text: string): string | null {
  let parsed: unknown;
  try {
    parsed = JSON.parse(text);
  } catch {
    return "This is not valid JSON.";
  }
  if (parsed === null || typeof parsed !== "object" || Array.isArray(parsed)) {
    return "State is a JSON object: keys and values, not a list or a scalar.";
  }
  if (new TextEncoder().encode(text).length > MAX_STATE_BYTES) {
    return `State is bounded at ${MAX_STATE_BYTES / 1024} KiB; keep a table as a resource instead.`;
  }
  return null;
}
