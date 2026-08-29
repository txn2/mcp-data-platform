import { useEffect, useState } from "react";
import { RUN_PAGE_SIZE, useScriptRun, useScriptRuns } from "@/api/portal/hooks/scripts";
import type { ScriptRun, ScriptRunDetail } from "@/api/portal/hooks/scripts";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Badge } from "@/components/ui/badge";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { formatDuration } from "@/lib/formatDuration";
import {
  formatWhen,
  outputLink,
  runStatusLabel,
  runStatusVariant,
  runWhen,
  successRate,
  summarize,
} from "./runFormat";

// ScriptRunHistory is the refresh history of one script: every run, what
// triggered it, how it ended, and what it produced. A recurring script writes
// new versions of the SAME portal asset rather than a new asset each time, so
// this list read next to that asset's version history is the whole story of
// what the dashboard has been showing.
//
// It sits directly under the source (#1406), because an error here is answered
// by the text above it, and nothing in it holds the page open sideways: every
// cell that can carry a long message wraps instead.

// RUN_COLUMNS is the width of the run table, named once so the error line and
// the expanded detail keep spanning all of it.
const RUN_COLUMNS = 3;

export function ScriptRunHistory({
  scriptId,
  openRunId = null,
  onNavigate,
}: {
  scriptId: string;
  /** openRunId is a run named by the address, opened without a click (#1405):
   * a link from the cross-script listing lands on the run it named rather than
   * on a history the reader has to find it in again. */
  openRunId?: string | null;
  onNavigate: (path: string) => void;
}) {
  const { data, isLoading, error } = useScriptRuns(scriptId, true);
  const [openRun, setOpenRun] = useState<string | null>(openRunId);
  // A run named by the address is opened whenever the address names a
  // different one, not only on the first mount: following one link and then
  // another leaves this section mounted, and a run that stayed closed because
  // the section happened to already exist is the same defect as one that never
  // opened. Nothing here fights a reader's clicks, because the address does not
  // change when they open a row.
  useEffect(() => {
    if (openRunId) setOpenRun(openRunId);
  }, [openRunId]);
  const runs = data?.data ?? [];

  return (
    <SectionCard
      title="Run history"
      action={<RunSummaryLine runs={runs} />}
    >
      <HistoryState isLoading={isLoading} failed={!!error} empty={runs.length === 0} />
      {runs.length > 0 && (
        <RunTable
          runs={runs}
          scriptId={scriptId}
          openRun={openRun}
          onOpenRun={setOpenRun}
          onNavigate={onNavigate}
        />
      )}
      {runs.length >= RUN_PAGE_SIZE && (
        <p className="pt-2 text-xs text-muted-foreground">
          Showing the {RUN_PAGE_SIZE} most recent runs. Older ones are kept until this
          deployment's retention window ends, and an agent reads them with manage_script.
        </p>
      )}
    </SectionCard>
  );
}

// HistoryState is what the section says before it has runs to show: still
// loading, could not be read, or a script that has never run — which are three
// different statements and are kept apart here so the section above cannot
// render two of them at once.
function HistoryState({
  isLoading,
  failed,
  empty,
}: {
  isLoading: boolean;
  failed: boolean;
  empty: boolean;
}) {
  if (isLoading) {
    return <p className="text-sm text-muted-foreground">Loading runs...</p>;
  }
  if (failed) {
    return (
      <p className="text-sm text-muted-foreground">This script's runs could not be loaded.</p>
    );
  }
  if (!empty) return null;
  return (
    <p className="text-sm text-muted-foreground">
      This script has never run. A run happens when its schedule fires or when someone
      asks for one, and a run always executes a saved version.
    </p>
  );
}

// RunTable is the history itself, once there is one to draw. It is its own
// component so the section above it stays the three states a history has —
// loading, never run, and drawn — rather than those plus a table.
function RunTable({
  runs,
  scriptId,
  openRun,
  onOpenRun,
  onNavigate,
}: {
  runs: ScriptRun[];
  scriptId: string;
  openRun: string | null;
  onOpenRun: (runId: string | null) => void;
  onNavigate: (path: string) => void;
}) {
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Run</TableHead>
          <TableHead>Duration</TableHead>
          <TableHead>Produced</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {runs.map((run) => (
          <RunRows
            key={run.id}
            run={run}
            scriptId={scriptId}
            open={openRun === run.id}
            onToggle={() => onOpenRun(openRun === run.id ? null : run.id)}
            onNavigate={onNavigate}
          />
        ))}
      </TableBody>
    </Table>
  );
}

function RunRows({
  run,
  scriptId,
  open,
  onToggle,
  onNavigate,
}: {
  run: ScriptRun;
  scriptId: string;
  open: boolean;
  onToggle: () => void;
  onNavigate: (path: string) => void;
}) {
  return (
    <>
      {/* The row opens the run, as every other expandable row in the portal
          does. A run's log is the reason anyone comes here, so it should not
          be behind a second target. */}
      <TableRow className="cursor-pointer" onClick={onToggle}>
        {/* How it ended and when, together, because they are read as one fact.
            What triggered it and which version ran are the qualifiers of that
            fact rather than facts of their own: the trigger is a three-value
            enumeration (a schedule, an agent's tool call, or this page) and the
            version is the same number down the whole column, so neither earns a
            column at the width this page has. */}
        <TableCell>
          <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
            <Badge variant={runStatusVariant(run.status)}>{runStatusLabel(run.status)}</Badge>
            <span className="text-xs">{runWhen(run)}</span>
          </div>
          <div className="text-xs text-muted-foreground">
            {run.trigger} · v{run.version}
          </div>
        </TableCell>
        <TableCell className="text-xs whitespace-nowrap tabular-nums">
          {run.duration_ms > 0 ? formatDuration(run.duration_ms) : "—"}
        </TableCell>
        <TableCell className="text-xs">{outputCount(run.output_count)}</TableCell>
      </TableRow>
      {run.error && !open && (
        <TableRow>
          {/* The reason a run failed is the whole reason to open this history,
              so it wraps to as many lines as it needs. A table cell does not
              wrap by default, which put a Starlark traceback on one line and
              left the page scrolling sideways to read it (#1406). */}
          <TableCell
            colSpan={RUN_COLUMNS}
            className="pt-0 text-xs break-words whitespace-normal text-red-700 dark:text-red-300"
          >
            {run.error}
          </TableCell>
        </TableRow>
      )}
      {open && (
        <TableRow>
          <TableCell colSpan={RUN_COLUMNS} className="bg-muted/30 whitespace-normal">
            <RunDetail scriptId={scriptId} runId={run.id} onNavigate={onNavigate} />
          </TableCell>
        </TableRow>
      )}
    </>
  );
}

// RunSummaryLine is what this stretch of history adds up to: how the script
// has actually been going, rather than leaving a reader to count badges. It
// states the window it was computed over, because a success rate without one is
// not a number anybody can act on.
function RunSummaryLine({ runs }: { runs: ScriptRun[] }) {
  if (runs.length === 0) return null;
  const summary = summarize(runs);
  const rate = successRate(summary);
  return (
    <span className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
      <span>
        <span className="text-foreground tabular-nums">{rate}%</span> succeeded over the last{" "}
        {summary.total} {summary.total === 1 ? "run" : "runs"}
      </span>
      {summary.failed > 0 && (
        <span className="text-red-700 dark:text-red-300">
          {summary.failed} failed
        </span>
      )}
      {summary.skipped > 0 && <span>{summary.skipped} skipped</span>}
      {summary.medianMs > 0 && <span>median {formatDuration(summary.medianMs)}</span>}
    </span>
  );
}

// RunDetail is one run's own account of itself: what it was given, what it
// wrote, and what it printed while working. The log is bounded at capture
// time, so showing it whole is bounded too.
function RunDetail({
  scriptId,
  runId,
  onNavigate,
}: {
  scriptId: string;
  runId: string;
  onNavigate: (path: string) => void;
}) {
  const { data: run, isLoading, error } = useScriptRun(scriptId, runId);
  if (isLoading) return <p className="text-xs text-muted-foreground">Loading run...</p>;
  if (error || !run) {
    return <p className="text-xs text-muted-foreground">This run could not be loaded.</p>;
  }
  return (
    <div className="space-y-3">
      <RunFacts run={run} />
      {run.error && (
        <pre className="overflow-x-auto rounded-md border border-red-500/30 bg-red-500/5 p-3 font-mono text-xs whitespace-pre-wrap text-red-700 dark:text-red-300">
          {run.error}
        </pre>
      )}
      <RunOutputs run={run} onNavigate={onNavigate} />
      <RunLog run={run} />
    </div>
  );
}

function RunFacts({ run }: { run: ScriptRunDetail }) {
  const params = Object.entries(run.params ?? {});
  return (
    <dl className="grid gap-x-6 gap-y-2 text-xs sm:grid-cols-3">
      <div>
        <dt className="text-muted-foreground">Requested by</dt>
        <dd>{run.requested_by || "the schedule"}</dd>
      </div>
      <div>
        <dt className="text-muted-foreground">Computed against</dt>
        <dd>{formatWhen(run.fire_time)}</dd>
      </div>
      <div>
        <dt className="text-muted-foreground">Cost</dt>
        <dd>
          {run.metrics.steps} steps · {run.metrics.queries} queries · {run.metrics.exports} exports
        </dd>
      </div>
      <div className="sm:col-span-3">
        <dt className="text-muted-foreground">Parameters</dt>
        <dd className="font-mono break-words">
          {params.length === 0
            ? "none"
            : params.map(([k, v]) => `${k}=${String(v)}`).join(", ")}
        </dd>
      </div>
      <RunStateFacts run={run} />
    </dl>
  );
}

// RunStateFacts is the state half of a run's own account (#1537): what it
// read, which is an input of the run beside its parameters, and what it saved.
// A run that read nothing and saved nothing says nothing here, because most
// scripts keep no state and a row of "{}" on every run would be noise.
function RunStateFacts({ run }: { run: ScriptRunDetail }) {
  const read = run.state_read ?? {};
  const readSomething = Object.keys(read).length > 0 || (run.state_revision ?? 0) > 0;
  if (!readSomething && !run.state_written) return null;
  return (
    <>
      <div className="sm:col-span-3">
        <dt className="text-muted-foreground">State read (revision {run.state_revision ?? 0})</dt>
        <dd className="font-mono break-words">{JSON.stringify(read)}</dd>
      </div>
      {run.state_written && (
        <div className="sm:col-span-3">
          <dt className="text-muted-foreground">
            State saved (revision {run.state_revision_written ?? "?"})
          </dt>
          <dd className="font-mono break-words">{JSON.stringify(run.state_written)}</dd>
        </div>
      )}
    </>
  );
}

// RunOutputs names what the run wrote. An asset the platform still serves is a
// link; an object delivered to a bucket is not, because the bytes left the
// platform and nothing here will serve them back.
function RunOutputs({
  run,
  onNavigate,
}: {
  run: ScriptRunDetail;
  onNavigate: (path: string) => void;
}) {
  const outputs = run.outputs ?? [];
  if (outputs.length === 0) {
    return <p className="text-xs text-muted-foreground">This run produced no output.</p>;
  }
  return (
    <ul className="space-y-1 text-xs">
      {outputs.map((output) => {
        const link = outputLink(output);
        return (
          <li key={`${output.name}-${output.destination ?? "portal"}`} className="flex items-center gap-2">
            {link.href ? (
              <button
                className="text-primary underline-offset-4 hover:underline"
                onClick={() => onNavigate(link.href!)}
              >
                {link.label}
              </button>
            ) : (
              <span className="font-medium">{link.label}</span>
            )}
            <span className="text-muted-foreground">{link.detail}</span>
          </li>
        );
      })}
    </ul>
  );
}

// outputCount says what a run produced in words. A count on its own is the
// least informative thing this column could hold: "0" and "2" both need the
// noun to mean anything, and the noun is what the reader is scanning for.
function outputCount(n: number): string {
  if (n <= 0) return "nothing";
  return `${n} output${n === 1 ? "" : "s"}`;
}

function RunLog({ run }: { run: ScriptRunDetail }) {
  if (!run.log) {
    return <p className="text-xs text-muted-foreground">This run printed nothing.</p>;
  }
  return (
    <div className="space-y-1">
      <pre className="max-h-80 overflow-auto rounded-md border bg-background p-3 font-mono text-xs whitespace-pre-wrap">
        {run.log}
      </pre>
      {run.log_truncated && (
        <p className="text-xs text-muted-foreground">
          The log was truncated at capture: anything larger than a log belongs in an output.
        </p>
      )}
    </div>
  );
}
