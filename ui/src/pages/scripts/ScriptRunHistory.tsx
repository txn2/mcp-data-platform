import { useState } from "react";
import { RUN_PAGE_SIZE, useScriptRun, useScriptRuns } from "@/api/portal/hooks/scripts";
import type { ScriptRun, ScriptRunDetail } from "@/api/portal/hooks/scripts";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { formatDuration } from "@/lib/formatDuration";
import { formatWhen, outputLink, runStatusLabel, runStatusVariant, runWhen } from "./runFormat";

// ScriptRunHistory is the refresh history of an automation: every run, what
// triggered it, how it ended, and what it produced. A recurring script writes
// new versions of the SAME portal asset rather than a new asset each time, so
// this list read next to that asset's version history is the whole story of
// what the dashboard has been showing.

export function ScriptRunHistory({
  scriptId,
  onNavigate,
}: {
  scriptId: string;
  onNavigate: (path: string) => void;
}) {
  const { data, isLoading, error } = useScriptRuns(scriptId, true);
  const [openRun, setOpenRun] = useState<string | null>(null);
  const runs = data?.data ?? [];

  return (
    <SectionCard title="Run history">
      {isLoading && <p className="text-sm text-muted-foreground">Loading runs...</p>}
      {error && (
        <p className="text-sm text-muted-foreground">This script's runs could not be loaded.</p>
      )}
      {!isLoading && !error && runs.length === 0 && (
        <p className="text-sm text-muted-foreground">
          This script has never run. A run happens when its schedule fires or when someone
          asks for one, and only an approved version ever executes.
        </p>
      )}
      {runs.length > 0 && (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Status</TableHead>
              <TableHead>Trigger</TableHead>
              <TableHead>Version</TableHead>
              <TableHead>When</TableHead>
              <TableHead>Duration</TableHead>
              <TableHead>Outputs</TableHead>
              <TableHead className="text-right">Log</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {runs.map((run) => (
              <RunRows
                key={run.id}
                run={run}
                scriptId={scriptId}
                open={openRun === run.id}
                onToggle={() => setOpenRun(openRun === run.id ? null : run.id)}
                onNavigate={onNavigate}
              />
            ))}
          </TableBody>
        </Table>
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
      <TableRow>
        <TableCell>
          <Badge variant={runStatusVariant(run.status)}>{runStatusLabel(run.status)}</Badge>
        </TableCell>
        <TableCell className="text-xs">{run.trigger}</TableCell>
        <TableCell className="font-mono text-xs">v{run.version}</TableCell>
        <TableCell className="text-xs">{runWhen(run)}</TableCell>
        <TableCell className="text-xs">
          {run.duration_ms > 0 ? formatDuration(run.duration_ms) : "—"}
        </TableCell>
        <TableCell className="text-xs">{run.output_count}</TableCell>
        <TableCell className="text-right">
          <Button size="sm" variant="outline" onClick={onToggle}>
            {open ? "Hide" : "Open"}
          </Button>
        </TableCell>
      </TableRow>
      {run.error && !open && (
        <TableRow>
          <TableCell colSpan={7} className="pt-0 text-xs text-red-700 dark:text-red-300">
            {run.error}
          </TableCell>
        </TableRow>
      )}
      {open && (
        <TableRow>
          <TableCell colSpan={7} className="bg-muted/30">
            <RunDetail scriptId={scriptId} runId={run.id} onNavigate={onNavigate} />
          </TableCell>
        </TableRow>
      )}
    </>
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
    </dl>
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
