import {
  isRunInFlight,
  useCancelScriptRun,
  useScriptLiveRuns,
} from "@/api/portal/hooks/scripts";
import type { ScriptRun, ScriptRunDetail } from "@/api/portal/hooks/scripts";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  attemptOutcomeLabel,
  formatWhen,
  livenessNote,
  progressText,
  runBadge,
  runWhen,
} from "./runFormat";

// A run's queue history (#1860). A worker that is killed mid-run leaves its run
// marked running with nobody executing it, and the platform takes it over when
// its lease ends; these say which worker holds a run, whether it is still
// reporting, and how every earlier attempt ended, so an orphaned or looping run
// is visible here rather than only in the run table.

// RunHolder is who holds a run still executing: the worker, when its lease
// ends, when it last reported, and how many times the run was taken over.
export function RunHolder({ run }: { run: ScriptRunDetail }) {
  if (run.status !== "running") return null;
  const note = livenessNote(run);
  return (
    <div className="space-y-1">
      {note && <p className="text-xs text-amber-700 dark:text-amber-300">{note}</p>}
      <dl className="grid gap-x-6 gap-y-2 text-xs sm:grid-cols-3">
        <div>
          <dt className="text-muted-foreground">Held by</dt>
          <dd className="font-mono break-all">{run.locked_by || "—"}</dd>
        </div>
        <div>
          <dt className="text-muted-foreground">Last report</dt>
          <dd>{formatWhen(run.heartbeat_at ?? run.claimed_at)}</dd>
        </div>
        <div>
          <dt className="text-muted-foreground">Lease ends</dt>
          <dd>{formatWhen(run.locked_until)}</dd>
        </div>
        <div>
          <dt className="text-muted-foreground">Attempt</dt>
          <dd className="tabular-nums">
            {run.attempt}
            {(run.reclaims ?? 0) > 0 && ` · taken over ${run.reclaims} time${run.reclaims === 1 ? "" : "s"}`}
          </dd>
        </div>
      </dl>
    </div>
  );
}

// RunAttempts is how each earlier attempt of a run ended. A run that finished
// on its first attempt has nothing to tell here, so it shows nothing.
export function RunAttempts({ run }: { run: ScriptRunDetail }) {
  const attempts = run.attempts ?? [];
  if (!attempts.some((a) => a.outcome !== "finished")) return null;
  return (
    <div className="space-y-1">
      <p className="text-xs text-muted-foreground">Attempts</p>
      <ol className="space-y-1 text-xs">
        {attempts.map((a) => (
          <li key={`${a.attempt}-${a.outcome}-${a.ended_at ?? ""}`} className="break-words">
            <span className="tabular-nums">#{a.attempt}</span>{" "}
            <span className={a.outcome === "finished" ? "" : "text-amber-700 dark:text-amber-300"}>
              {attemptOutcomeLabel(a.outcome)}
            </span>
            <span className="text-muted-foreground">
              {" "}
              · {a.worker || "no worker"} · {formatWhen(a.ended_at)}
            </span>
            {a.error && <div className="text-muted-foreground">{a.error}</div>}
          </li>
        ))}
      </ol>
    </div>
  );
}

// ScriptLiveRuns lists the script's runs that have not ended, above its
// history: a run whose worker died can be older than the page of history, and
// it is the one run an owner has to see. It shows nothing when every run has
// ended.
export function ScriptLiveRuns({ scriptId }: { scriptId: string }) {
  const { data } = useScriptLiveRuns(scriptId, true);
  const runs = (data?.data ?? []).filter(isRunInFlight);
  if (runs.length === 0) return null;
  return (
    <SectionCard title="Running now">
      <ul className="space-y-3">
        {runs.map((run) => (
          <LiveRun key={run.id} scriptId={scriptId} run={run} />
        ))}
      </ul>
    </SectionCard>
  );
}

function LiveRun({ scriptId, run }: { scriptId: string; run: ScriptRun }) {
  const cancel = useCancelScriptRun(scriptId);
  const badge = runBadge(run);
  const note = livenessNote(run);
  const progress = progressText(run.progress);
  return (
    <li className="space-y-1 text-xs">
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
        <Badge variant={badge.variant}>{badge.label}</Badge>
        <span>{runWhen(run)}</span>
        <span className="text-muted-foreground">
          {run.trigger} · v{run.version}
        </span>
        {run.cancel_requested || cancel.isSuccess ? (
          <span className="text-muted-foreground">Stopping</span>
        ) : (
          <Button
            size="sm"
            variant="outline"
            disabled={cancel.isPending}
            onClick={() => cancel.mutate(run.id)}
          >
            {run.status === "pending" ? "Cancel run" : "Stop run"}
          </Button>
        )}
      </div>
      {progress && <div className="text-muted-foreground">{progress}</div>}
      {note && <div className="break-words text-amber-700 dark:text-amber-300">{note}</div>}
      {cancel.isError && <div className="text-red-700 dark:text-red-300">The run could not be stopped.</div>}
    </li>
  );
}
