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
      detail: `asset version ${output.asset_version ?? 1}`,
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
// The refusal is the execution gate's own answer to "would a run requested now
// be admitted", so a page never has to re-derive runnability from a status and
// an enabled flag and reach a different conclusion from the platform.
export function executionState(contract: Pick<ScriptContract, "approval">): {
  label: string;
  variant: "success" | "muted" | "warning";
  detail?: string;
} {
  if (!contract.approval.approved) {
    return {
      label: "Nothing approved",
      variant: "muted",
      detail: contract.approval.refusal || "No version is approved, so nothing will execute this script.",
    };
  }
  if (contract.approval.refusal) {
    return {
      label: `Approved v${contract.approval.version ?? "?"}`,
      variant: "warning",
      detail: contract.approval.refusal,
    };
  }
  return { label: `Approved v${contract.approval.version ?? "?"}`, variant: "success" };
}
