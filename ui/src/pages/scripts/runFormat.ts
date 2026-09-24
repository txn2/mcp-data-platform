import type { ScriptContract, ScriptRun, ScriptRunOutput } from "@/api/portal/hooks/scripts";

// Shared rendering rules for a script's runs and its execution state, so the
// listing and the detail page say the same thing about the same row.

// runStatusVariant maps a run status onto the badge tints. A skipped overlap is
// neither a success nor a failure: it names a fire that was never executed
// because the previous run was still going, which is a fact about the cadence
// rather than an error. A canceled run is somebody's decision, not a fault,
// so it is muted rather than red.
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

// runBadge is the badge a run row carries: its status, except that a running
// run whose worker has stopped reporting says so (#1860) rather than reading as
// a run being executed.
export function runBadge(run: Pick<ScriptRun, "status" | "liveness">): {
  label: string;
  variant: "success" | "danger" | "warning" | "info" | "muted";
} {
  if (run.status === "running" && run.liveness === "unresponsive") {
    return { label: "worker not responding", variant: "warning" };
  }
  if (run.status === "running" && run.liveness === "lease_expired") {
    return { label: "worker gone", variant: "warning" };
  }
  return { label: runStatusLabel(run.status), variant: runStatusVariant(run.status) };
}

// livenessNote says what happens to a running run whose worker has stopped
// reporting, and empty for one whose worker is executing it.
export function livenessNote(run: Pick<ScriptRun, "status" | "liveness">): string {
  if (run.status !== "running") return "";
  switch (run.liveness) {
    case "unresponsive":
      return "The worker executing this run stopped reporting, most likely a replica that was killed. It is taken over when its lease ends, or failed if it has been taken over too often; stopping it ends it now.";
    case "lease_expired":
      return "The worker that held this run stopped reporting and its lease has ended. The next worker takes it over, or fails it if it has been taken over too often; stopping it ends it now.";
    default:
      return "";
  }
}

// causeNote says what a failure means for the owner (#1859): only a script
// error asks for a fix, and a temporary one says the next run should succeed.
// Empty for a script error, whose message is the error itself.
export function causeNote(run: Pick<ScriptRun, "status" | "cause">): string {
  if (run.status !== "failed") return "";
  switch (run.cause) {
    case "upstream":
      return "Temporary: a service the script called was unavailable. Nothing in the script needs fixing, and the next run should succeed.";
    case "state_conflict":
      return "Temporary: another run saved the script's state first. Its outputs stand, and the next run reads the newer state.";
    case "memory":
      return "The run held more memory than it is allowed. Page the work and export each page with append=True.";
    case "worker_lost":
      return "Its workers kept stopping without a result, most often from running out of memory, so it is not run again.";
    case "platform":
      return "The platform could not execute it. Nothing in the script needs fixing; run it again.";
    default:
      return "";
  }
}

// attemptOutcomeLabel names how one attempt of a run ended.
export function attemptOutcomeLabel(outcome: string): string {
  switch (outcome) {
    case "finished":
      return "finished";
    case "retried":
      return "retried after a platform fault";
    case "released":
      return "released at shutdown";
    case "shed":
      return "requeued to relieve memory";
    case "lease_expired":
      return "worker stopped reporting (lease expired)";
    case "unresponsive":
      return "worker stopped reporting";
    default:
      return outcome;
  }
}

// formatBytes renders a size in the unit a memory budget is written in.
export function formatBytes(n: number): string {
  if (n >= 1 << 30) return `${(n / (1 << 30)).toFixed(1)} GiB`;
  if (n >= 1 << 20) return `${Math.round(n / (1 << 20))} MiB`;
  if (n >= 1 << 10) return `${Math.round(n / (1 << 10))} KiB`;
  return `${n} bytes`;
}

// progressText renders a platform.progress report as one line: the count when
// the script gave one, then its message (#1847).
export function progressText(progress?: { message: string; done?: number; total?: number }): string {
  if (!progress) return "";
  let count = "";
  if (progress.done !== undefined && progress.total !== undefined) {
    count = `${progress.done} of ${progress.total}`;
  } else if (progress.done !== undefined) {
    count = `${progress.done}`;
  }
  if (count && progress.message) return `${count} · ${progress.message}`;
  return count || progress.message;
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
// still serves carries a path to it, a file in the resource library carries a
// path to the file and the version this run wrote of it, and an object
// delivered to a bucket carries only where it was written, because the bytes
// left the platform and nothing here will serve them back.
export interface OutputLink {
  label: string;
  detail: string;
  href?: string;
}

// assetDetail reads an asset output's line: a whole write, or the data-region
// refresh that replaced part of one.
function assetDetail(output: ScriptRunOutput): string {
  const version = output.asset_version ?? 1;
  return output.refresh ? `data refresh, asset version ${version}` : `asset version ${version}`;
}

// outputLink describes one recorded output of a run. Which locator the record
// carries decides the line: an asset, a file in the resource library, an object
// in a bucket, or none of the three.
export function outputLink(output: ScriptRunOutput): OutputLink {
  const link = outputLocation(output);
  // An output an export tool wrote says which (#1854): a trino_export file is
  // a new asset every run, where a platform.export output is one asset
  // versioned run after run.
  if (output.tool) link.detail = `${link.detail} · via ${output.tool}`;
  return link;
}

function outputLocation(output: ScriptRunOutput): OutputLink {
  if (output.asset_id) {
    return { label: output.name, detail: assetDetail(output), href: `/assets/${output.asset_id}` };
  }
  if (output.resource_id) {
    return {
      label: output.name,
      detail: `library file ${output.key ?? ""}, version ${output.resource_version ?? 1}`,
      href: `/resources/${output.resource_id}`,
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
  canceled: number;
  /** medianMs is the median duration of the runs that recorded one. */
  medianMs: number;
  /** lastFailure is the most recent failed run, if the window holds one. */
  lastFailure?: ScriptRun;
}

// OUTCOME_COUNTER names the summary count each terminal status adds to. A
// status not listed (pending, running) is still in flight and counts only
// toward the total.
const OUTCOME_COUNTER: Record<string, "succeeded" | "failed" | "skipped" | "canceled"> = {
  succeeded: "succeeded",
  failed: "failed",
  skipped_overlap: "skipped",
  canceled: "canceled",
};

/** summarize folds a run history into what it adds up to. */
export function summarize(runs: ScriptRun[]): RunSummary {
  const out: RunSummary = { total: runs.length, succeeded: 0, failed: 0, skipped: 0, canceled: 0, medianMs: 0 };
  const durations: number[] = [];
  for (const run of runs) {
    const counter = OUTCOME_COUNTER[run.status];
    if (counter) out[counter]++;
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
