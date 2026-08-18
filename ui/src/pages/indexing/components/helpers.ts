// Shared helpers for the Indexing dashboard components. Pure functions and
// constants used across the badge, card, panel, and job-table modules.

// STATUS_COLORS tints the by-last-run cells on a kind card and the coverage
// bar. Failures are not in it: they are stated as a destructive-toned sentence
// rather than a cell, because a failing unit is re-queued and its last run is
// pending (see KindCard).
export const STATUS_COLORS: Record<string, string> = {
  pending: "hsl(38, 92%, 50%)",
  running: "hsl(217, 91%, 60%)",
  succeeded: "hsl(142, 71%, 45%)",
};

export function relTime(iso?: string): string {
  if (!iso) return "never";
  const ms = Date.now() - new Date(iso).getTime();
  if (!Number.isFinite(ms)) return "never";
  if (ms < 0) return "just now";
  const s = Math.floor(ms / 1000);
  if (s < 60) return `${s}s ago`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  return `${Math.floor(h / 24)}d ago`;
}

// untilTime renders a future instant as a forward-looking distance ("in
// 2h"). It is the mirror of relTime, which collapses everything in the
// future to "just now" because every timestamp it formats is in the past.
// A deferral deadline is the one field on this surface that is not.
export function untilTime(iso?: string): string {
  if (!iso) return "";
  const ms = new Date(iso).getTime() - Date.now();
  if (!Number.isFinite(ms)) return "";
  if (ms <= 0) return "shortly";
  const s = Math.floor(ms / 1000);
  if (s < 60) return `in ${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `in ${m}m`;
  const h = Math.floor(m / 60);
  if (h < 24) return `in ${h}h`;
  return `in ${Math.floor(h / 24)}d`;
}

// percentile returns the nearest-rank p-th percentile of an ascending
// sorted series: the smallest value at or below which p% of the data
// falls. Using ceil(p/100*n)-1 (not floor) keeps p50 of [10,5000] at the
// lower median (10) rather than the max, and keeps p95 below the max for
// series longer than ~20 points, which is the distinction the latency
// track exists to show.
export function percentile(sorted: number[], p: number): number {
  if (sorted.length === 0) return 0;
  const rank = Math.ceil((p / 100) * sorted.length);
  const idx = Math.min(sorted.length - 1, Math.max(0, rank - 1));
  return sorted[idx]!;
}

// leaseRemaining describes how long a running job's lease has left.
// lease_expires_at is in the future for a healthy renewing job, so the
// relative-past relTime() would collapse it to "just now"; this renders
// the forward delta ("4m") or "expired" once it elapses.
export function leaseRemaining(iso?: string): string {
  if (!iso) return "no lease";
  const ms = new Date(iso).getTime() - Date.now();
  if (!Number.isFinite(ms)) return "no lease";
  if (ms <= 0) return "expired";
  const s = Math.floor(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m`;
  return `${Math.floor(m / 60)}h`;
}

// fmtClock formats an ISO timestamp as a local clock time, guarding the
// malformed / missing case so the panel never renders "Invalid Date".
export function fmtClock(iso?: string): string {
  if (!iso) return "soon";
  const t = new Date(iso).getTime();
  if (!Number.isFinite(t)) return "soon";
  return new Date(t).toLocaleTimeString();
}

// errorSignature normalizes a last_error into a grouping key by stripping
// digits and quoted ids so transient variations (a different spec name, a
// timestamp) collapse to one triage bucket.
export function errorSignature(err: string): string {
  return err
    .replace(/[0-9a-f]{8}-[0-9a-f-]{27,}/gi, "<id>")
    .replace(/\d+/g, "<n>")
    .replace(/"[^"]*"/g, "<v>")
    .trim()
    .slice(0, 120);
}

// failureKey identifies one failing unit across props/state.
export function failureKey(kind: string, sourceID: string): string {
  return `${kind}::${sourceID}`;
}
