import type { ScriptContract, ScriptRun, ScriptRunOutput } from "@/api/portal/hooks/scripts";

// Shared rendering rules for a script's runs and its execution state, so the
// listing and the detail page say the same thing about the same row.

// runStatusVariant maps a run status onto the badge tints. A skipped overlap is
// neither a success nor a failure: it names a fire that was never executed
// because the previous run was still going, which is a fact about the cadence
// rather than an error.
export function runStatusVariant(status: string): "success" | "danger" | "warning" | "info" | "muted" {
  switch (status) {
    case "succeeded":
      return "success";
    case "failed":
      return "danger";
    case "running":
      return "info";
    case "skipped_overlap":
      return "warning";
    default:
      return "muted";
  }
}

// runStatusLabel renders a status for a human. Only skipped_overlap needs
// translating; the rest are already words.
export function runStatusLabel(status: string): string {
  return status === "skipped_overlap" ? "Skipped (overlap)" : status;
}

// formatWhen renders a timestamp in the reader's own locale, or a dash when
// there is none — a run that has not started has no start time, and inventing
// one would read as though it had.
export function formatWhen(iso?: string): string {
  if (!iso) return "—";
  const at = new Date(iso);
  if (Number.isNaN(at.getTime())) return "—";
  return at.toLocaleString();
}

// runWhen is the time a run row is read against: when it finished, falling back
// to when it started and then to the fire it was created for. A pending run has
// only the last of those, and it is the honest answer to "when is this for".
export function runWhen(run: Pick<ScriptRun, "finished_at" | "started_at" | "fire_time">): string {
  return formatWhen(run.finished_at ?? run.started_at ?? run.fire_time);
}

// dryRunOutputPhrase says what one previewed output IS — a data-region
// refresh, a document, or a table — with its size and destination. One phrase,
// because the draft-checks panel and the review drawer both describe the same
// preview and must not drift.
export function dryRunOutputPhrase(o: {
  format: string;
  row_count: number;
  document?: boolean;
  refresh?: boolean;
  bytes: number;
  destination?: string;
}): string {
  if (o.refresh) {
    return `a data-region refresh (${o.bytes} bytes of JSON)`;
  }
  const shape = o.document
    ? `a ${o.format} document`
    : `${o.row_count} row${o.row_count === 1 ? "" : "s"} as ${o.format}`;
  return `${shape} (${o.bytes} bytes) to ${o.destination || "the portal"}`;
}

// OutputLink is how one output of a run is presented: an asset the platform
// still serves carries a path to it, and an object delivered to a bucket
// carries only where it was written, because the bytes left the platform and
// nothing here will serve them back.
export interface OutputLink {
  label: string;
  detail: string;
  href?: string;
}

// outputLink describes one recorded output of a run.
export function outputLink(output: ScriptRunOutput): OutputLink {
  if (output.asset_id) {
    return {
      label: output.name,
      detail: output.refresh
        ? `data refresh, asset version ${output.asset_version ?? 1}`
        : `asset version ${output.asset_version ?? 1}`,
      href: `/assets/${output.asset_id}`,
    };
  }
  if (output.bucket) {
    return { label: output.name, detail: `delivered to ${output.bucket}/${output.key ?? ""}` };
  }
  return { label: output.name, detail: output.format || "output" };
}

// executionState is what a script is doing, in the one form both the listing
// and the detail page report it.
//
// The refusal is the run gate's own answer to "would a run requested now be
// admitted", so a page never has to re-derive runnability from a status and an
// enabled flag and reach a different conclusion from the platform.
export function executionState(contract: Pick<ScriptContract, "version" | "refusal">): {
  label: string;
  variant: "success" | "muted" | "warning";
  detail?: string;
} {
  if (contract.refusal) {
    return {
      label: "Not running",
      variant: "warning",
      detail: contract.refusal,
    };
  }
  return { label: `Runs v${contract.version}`, variant: "success" };
}

// RunSummary is what a stretch of run history adds up to. It is computed from
// the runs a page actually loaded, and carries the count it was computed over,
// because "92% succeeded" means nothing without it (#1307).
export interface RunSummary {
  total: number;
  succeeded: number;
  failed: number;
  skipped: number;
  /** medianMs is the median duration of the runs that recorded one. */
  medianMs: number;
  /** lastFailure is the most recent failed run, if the window holds one. */
  lastFailure?: ScriptRun;
}

/** summarize folds a run history into what it adds up to. */
export function summarize(runs: ScriptRun[]): RunSummary {
  const out: RunSummary = { total: runs.length, succeeded: 0, failed: 0, skipped: 0, medianMs: 0 };
  const durations: number[] = [];
  for (const run of runs) {
    if (run.status === "succeeded") out.succeeded++;
    else if (run.status === "failed") out.failed++;
    else if (run.status === "skipped_overlap") out.skipped++;
    if (run.duration_ms > 0) durations.push(run.duration_ms);
    if (run.status === "failed" && !out.lastFailure) out.lastFailure = run;
  }
  durations.sort((a, b) => a - b);
  if (durations.length > 0) {
    const mid = Math.floor(durations.length / 2);
    out.medianMs =
      durations.length % 2 === 0 ? Math.round((durations[mid - 1]! + durations[mid]!) / 2) : durations[mid]!;
  }
  return out;
}

/** successRate is the share of runs that succeeded, or undefined when the
 * window holds none — no runs is not a 0% success rate. */
export function successRate(summary: RunSummary): number | undefined {
  if (summary.total === 0) return undefined;
  return Math.round((summary.succeeded / summary.total) * 100);
}
