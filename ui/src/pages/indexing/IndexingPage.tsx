import { useMemo, useState } from "react";
import {
  AlertTriangle,
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  Loader2,
  RefreshCw,
  Database,
  Activity,
  X,
} from "lucide-react";
import {
  useIndexJobsSummary,
  useIndexJobs,
  useIndexJobFailures,
  useReindex,
  useDismissFailure,
  type IndexKindSummary,
  type IndexJob,
  type IndexFailedUnit,
  type IndexVerdict,
} from "@/api/admin/indexjobs";
import { IndexThroughputTimeline } from "@/components/charts/IndexThroughputTimeline";
import { IndexLatencyTrack, type KindLatency } from "@/components/charts/IndexLatencyTrack";

// IndexingPage is the admin-only cross-kind Indexing dashboard. It leads
// with a plain health verdict per kind (Healthy / Indexing… / Degraded /
// Idle complete) so an operator can answer "is indexing healthy?" at a
// glance, then exposes throughput, latency, in-flight progress, retry
// backoff, and a self-resolving failure triage. The two metric families
// are kept visually distinct: vector coverage (how much is indexed) and
// per-unit job state (each unit's most recent run). All data is real
// index_jobs / vector state from the admin index-jobs endpoints.

const STATUS_COLORS: Record<string, string> = {
  pending: "hsl(38, 92%, 50%)",
  running: "hsl(217, 91%, 60%)",
  succeeded: "hsl(142, 71%, 45%)",
  failed: "hsl(0, 72%, 51%)",
};

function relTime(iso?: string): string {
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

// percentile returns the nearest-rank p-th percentile of an ascending
// sorted series: the smallest value at or below which p% of the data
// falls. Using ceil(p/100*n)-1 (not floor) keeps p50 of [10,5000] at the
// lower median (10) rather than the max, and keeps p95 below the max for
// series longer than ~20 points, which is the distinction the latency
// track exists to show.
function percentile(sorted: number[], p: number): number {
  if (sorted.length === 0) return 0;
  const rank = Math.ceil((p / 100) * sorted.length);
  const idx = Math.min(sorted.length - 1, Math.max(0, rank - 1));
  return sorted[idx]!;
}

// leaseRemaining describes how long a running job's lease has left.
// lease_expires_at is in the future for a healthy renewing job, so the
// relative-past relTime() would collapse it to "just now"; this renders
// the forward delta ("4m") or "expired" once it elapses.
function leaseRemaining(iso?: string): string {
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
function fmtClock(iso?: string): string {
  if (!iso) return "soon";
  const t = new Date(iso).getTime();
  if (!Number.isFinite(t)) return "soon";
  return new Date(t).toLocaleTimeString();
}

// errorSignature normalizes a last_error into a grouping key by stripping
// digits and quoted ids so transient variations (a different spec name, a
// timestamp) collapse to one triage bucket.
function errorSignature(err: string): string {
  return err
    .replace(/[0-9a-f]{8}-[0-9a-f-]{27,}/gi, "<id>")
    .replace(/\d+/g, "<n>")
    .replace(/"[^"]*"/g, "<v>")
    .trim()
    .slice(0, 120);
}

function ProviderBanner({
  status,
  kind,
  model,
  dimension,
}: {
  status: "ok" | "unconfigured";
  kind: string;
  model: string;
  dimension: number;
}) {
  if (status === "ok") {
    return (
      <div className="flex flex-wrap items-center gap-x-4 gap-y-1 rounded-lg border border-emerald-500/30 bg-emerald-500/5 px-4 py-2 text-sm">
        <span className="flex items-center gap-1.5 font-medium text-emerald-600 dark:text-emerald-400">
          <CheckCircle2 className="h-4 w-4" /> Embedding provider active
        </span>
        <span className="text-muted-foreground">
          {kind || "provider"}
          {model ? ` · ${model}` : ""} · {dimension}-dim
        </span>
      </div>
    );
  }
  return (
    <div className="flex flex-wrap items-center gap-x-4 gap-y-1 rounded-lg border border-amber-500/40 bg-amber-500/10 px-4 py-2 text-sm">
      <span className="flex items-center gap-1.5 font-medium text-amber-700 dark:text-amber-400">
        <AlertTriangle className="h-4 w-4" /> Embedding provider unconfigured
      </span>
      <span className="text-muted-foreground">
        Semantic and hybrid ranking fall back to lexical until a provider is wired. Indexing is paused.
      </span>
    </div>
  );
}

// VERDICT_META maps each server-computed verdict to its label, palette,
// and icon so the lead health word is consistent everywhere it renders.
const VERDICT_META: Record<
  IndexVerdict,
  { label: string; text: string; bg: string; border: string; spin?: boolean; Icon: typeof CheckCircle2 }
> = {
  healthy: {
    label: "Up to date",
    text: "text-emerald-600 dark:text-emerald-400",
    bg: "bg-emerald-500/10",
    border: "border-emerald-500/30",
    Icon: CheckCircle2,
  },
  indexing: {
    label: "Indexing…",
    text: "text-blue-600 dark:text-blue-400",
    bg: "bg-blue-500/10",
    border: "border-blue-500/30",
    spin: true,
    Icon: Loader2,
  },
  degraded: {
    label: "Degraded",
    text: "text-red-600 dark:text-red-400",
    bg: "bg-red-500/10",
    border: "border-red-500/40",
    Icon: AlertTriangle,
  },
};

function VerdictBadge({ verdict }: { verdict: IndexVerdict }) {
  const m = VERDICT_META[verdict] ?? VERDICT_META.healthy;
  return (
    <span
      className={`inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[11px] font-medium ${m.border} ${m.bg} ${m.text}`}
    >
      <m.Icon className={`h-3 w-3 ${m.spin ? "animate-spin" : ""}`} /> {m.label}
    </span>
  );
}

// coverageLine renders the vector-coverage family (how much is indexed),
// labelled "Vectors" so it never reads as a job count. expected_known
// distinguishes a real ratio from a continuously-syncing kind.
function CoverageLine({ summary }: { summary: IndexKindSummary }) {
  const cov = summary.coverage;
  if (!cov) {
    return <span className="text-xs text-muted-foreground">Vectors: coverage n/a</span>;
  }
  if (!cov.expected_known) {
    // No fixed denominator (e.g. tools, sized by the live registry). Once
    // anything is indexed it is in sync, so render a full bar to match the
    // ratio-known kinds visually; an empty corpus shows no bar.
    if (cov.indexed === 0) {
      return <span className="text-xs text-muted-foreground">Vectors: not yet indexed</span>;
    }
    return (
      <div className="space-y-1">
        <div className="flex items-center justify-between text-xs">
          <span className="tabular-nums">
            <span className="text-muted-foreground">Vectors: </span>
            {cov.indexed.toLocaleString()} indexed
          </span>
          <span className="text-emerald-500">in sync</span>
        </div>
        <div className="h-1.5 w-full overflow-hidden rounded-full bg-muted">
          <div
            className="h-full rounded-full"
            style={{ width: "100%", backgroundColor: STATUS_COLORS.succeeded }}
          />
        </div>
      </div>
    );
  }
  if (cov.expected === 0 && cov.indexed === 0) {
    return <span className="text-xs text-muted-foreground">Vectors: not yet indexed</span>;
  }
  const pct = cov.expected > 0 ? Math.round((cov.indexed / cov.expected) * 100) : 100;
  return (
    <div className="space-y-1">
      <div className="flex items-center justify-between text-xs">
        <span className="tabular-nums">
          <span className="text-muted-foreground">Vectors: </span>
          {cov.indexed.toLocaleString()} / {cov.expected.toLocaleString()} indexed
        </span>
        <span className={pct >= 100 ? "text-emerald-500" : "text-muted-foreground"}>{pct}%</span>
      </div>
      <div className="h-1.5 w-full overflow-hidden rounded-full bg-muted">
        <div
          className="h-full rounded-full"
          style={{
            width: `${Math.min(100, pct)}%`,
            backgroundColor: pct >= 100 ? STATUS_COLORS.succeeded : STATUS_COLORS.running,
          }}
        />
      </div>
    </div>
  );
}

// nowText describes what is running for the kind, derived from the
// job-state counts. Distinct from the verdict so the card answers both
// "is it healthy?" and "what is it doing right now?".
function nowText(summary: IndexKindSummary): string {
  if (summary.running > 0) {
    return `embedding ${summary.running} unit${summary.running === 1 ? "" : "s"}`;
  }
  if (summary.pending > 0) {
    return `${summary.pending} queued`;
  }
  return "idle";
}

// syncedText is the recency line under the coverage bar. When the kind
// has job history it reads "last indexed <relative>"; a kind whose
// vectors were seeded outside the queue (no history) simply reads
// "fully indexed" rather than "never", since there is no job timestamp
// to report and the verdict already says it is up to date.
function syncedText(summary: IndexKindSummary): string {
  if (!summary.last_activity) {
    return "fully indexed";
  }
  return `last indexed ${relTime(summary.last_activity)}`;
}

function KindCard({
  summary,
  onReindex,
  reindexing,
}: {
  summary: IndexKindSummary;
  onReindex: (kind: string) => void;
  reindexing: boolean;
}) {
  // The per-state breakdown is only meaningful when something is in
  // flight or needs attention. For a kind that is simply up to date it
  // is all zeros (or, confusingly, a stale "N succeeded"), so it is
  // hidden: an up-to-date card is just the verdict, the coverage bar,
  // and recency. It reappears when there is real work or a failure.
  const showStates =
    summary.running > 0 || summary.pending > 0 || summary.unresolved_failures > 0;
  return (
    <div className="flex flex-col gap-3 rounded-lg border bg-card p-4">
      <div className="flex items-center justify-between gap-2">
        <div className="flex min-w-0 flex-col gap-1">
          <span className="truncate font-mono text-sm font-medium">{summary.kind}</span>
          <VerdictBadge verdict={summary.verdict} />
        </div>
        <button
          type="button"
          onClick={() => onReindex(summary.kind)}
          disabled={reindexing}
          className="flex shrink-0 items-center gap-1 rounded-md border px-2 py-1 text-xs text-muted-foreground transition-colors hover:bg-accent disabled:opacity-50"
          title="Re-index every out-of-sync unit of this kind"
        >
          <RefreshCw className={`h-3 w-3 ${reindexing ? "animate-spin" : ""}`} /> Re-index
        </button>
      </div>

      <CoverageLine summary={summary} />

      <div className="flex items-center justify-between text-[11px] text-muted-foreground">
        <span>{syncedText(summary)}</span>
        <span>
          now: <span className="text-foreground">{nowText(summary)}</span>
        </span>
      </div>

      {/* Job-state family, shown only when there is active work or an
          open failure; labelled so "succeeded" reads as "units whose
          last run succeeded", not a job count. */}
      {showStates && (
        <div className="border-t pt-2">
          <div className="mb-1 text-[10px] uppercase tracking-wide text-muted-foreground">
            Units by last run
          </div>
          <div className="grid grid-cols-4 gap-1 text-center text-xs">
            {(["pending", "running", "succeeded", "failed"] as const).map((s) => (
              <div key={s}>
                <div className="font-semibold tabular-nums" style={{ color: STATUS_COLORS[s] }}>
                  {summary[s].toLocaleString()}
                </div>
                <div className="text-[10px] text-muted-foreground">{s}</div>
              </div>
            ))}
          </div>
          {summary.unresolved_failures > 0 && (
            <div className="mt-1 text-[10px] text-red-500">
              {summary.unresolved_failures} unit{summary.unresolved_failures === 1 ? "" : "s"} need
              attention
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function JobStatusChip({ status }: { status: string }) {
  return (
    <span
      className="inline-flex items-center rounded px-1.5 py-0.5 text-[11px] font-medium"
      style={{
        color: STATUS_COLORS[status] ?? "hsl(var(--muted-foreground))",
        backgroundColor: `${STATUS_COLORS[status] ?? "hsl(var(--muted))"}1a`,
      }}
    >
      {status}
    </span>
  );
}

function InFlightPanel({ jobs }: { jobs: IndexJob[] }) {
  const running = jobs.filter((j) => j.status === "running");
  if (running.length === 0) {
    return <p className="py-4 text-center text-sm text-muted-foreground">No jobs in flight.</p>;
  }
  return (
    <ul className="space-y-2">
      {running.map((j) => (
        <li key={j.id} className="flex items-center justify-between gap-2 text-sm">
          <span className="flex min-w-0 items-center gap-2">
            <Loader2 className="h-3.5 w-3.5 shrink-0 animate-spin text-blue-500" />
            <span className="truncate font-mono text-xs">
              {j.source_kind}/{j.source_id}
            </span>
          </span>
          <span className="shrink-0 text-xs text-muted-foreground">
            {j.items_done > 0 ? `${j.items_done} items · ` : ""}
            {j.worker_id ? `${j.worker_id} · ` : ""}
            lease {leaseRemaining(j.lease_expires_at)}
          </span>
        </li>
      ))}
    </ul>
  );
}

function RetryBackoffPanel({ jobs }: { jobs: IndexJob[] }) {
  const waiting = jobs.filter((j) => j.status === "pending" && j.attempts > 0);
  if (waiting.length === 0) {
    return <p className="py-4 text-center text-sm text-muted-foreground">No jobs in retry backoff.</p>;
  }
  return (
    <ul className="space-y-2">
      {waiting.map((j) => (
        <li key={j.id} className="flex items-center justify-between gap-2 text-sm">
          <span className="truncate font-mono text-xs">
            {j.source_kind}/{j.source_id}
          </span>
          <span className="shrink-0 text-xs text-muted-foreground">
            attempt {j.attempts} · next run {fmtClock(j.next_run_at)}
          </span>
        </li>
      ))}
    </ul>
  );
}

// failureKey identifies one failing unit across props/state.
function failureKey(kind: string, sourceID: string): string {
  return `${kind}::${sourceID}`;
}

// FailedUnitRow renders one failing unit inside a triage group: its
// timestamps, last-success context, and Retry / Dismiss actions, with an
// expandable drill-in to the un-redacted error and the underlying job id.
function FailedUnitRow({
  unit,
  onRetry,
  onDismiss,
  retrying,
  dismissing,
}: {
  unit: IndexFailedUnit;
  onRetry: (kind: string, sourceID: string) => void;
  onDismiss: (kind: string, sourceID: string) => void;
  retrying: boolean;
  dismissing: boolean;
}) {
  const [open, setOpen] = useState(false);
  const busy = retrying || dismissing;
  return (
    <li className="rounded border border-red-500/20 bg-background/40 px-2 py-1.5 text-xs">
      <div className="flex items-center justify-between gap-2">
        <button
          type="button"
          onClick={() => setOpen((v) => !v)}
          className="flex min-w-0 items-center gap-1 text-left"
          aria-expanded={open}
        >
          {open ? (
            <ChevronDown className="h-3 w-3 shrink-0 text-muted-foreground" />
          ) : (
            <ChevronRight className="h-3 w-3 shrink-0 text-muted-foreground" />
          )}
          <span className="truncate font-mono">
            {unit.source_kind}/{unit.source_id}
          </span>
        </button>
        <div className="flex shrink-0 items-center gap-1">
          <button
            type="button"
            onClick={() => onRetry(unit.source_kind, unit.source_id)}
            disabled={busy}
            className="flex items-center gap-1 rounded border px-2 py-0.5 text-muted-foreground transition-colors hover:bg-accent disabled:opacity-50"
            title="Re-index this unit; the card clears once it succeeds"
          >
            <RefreshCw className={`h-3 w-3 ${retrying ? "animate-spin" : ""}`} /> Retry
          </button>
          <button
            type="button"
            onClick={() => onDismiss(unit.source_kind, unit.source_id)}
            disabled={busy}
            className="flex items-center gap-1 rounded border px-2 py-0.5 text-muted-foreground transition-colors hover:bg-accent disabled:opacity-50"
            title="Dismiss: mark this failure resolved without re-indexing"
          >
            <X className="h-3 w-3" /> Dismiss
          </button>
        </div>
      </div>
      <div className="mt-1 flex flex-wrap gap-x-3 gap-y-0.5 pl-4 text-[11px] text-muted-foreground">
        <span>
          {unit.occurrences} failure{unit.occurrences === 1 ? "" : "s"} · {unit.attempts} attempts
        </span>
        <span>first seen {relTime(unit.first_failed_at)}</span>
        <span>last seen {relTime(unit.last_failed_at)}</span>
        {unit.last_succeeded_at ? (
          <span className="text-emerald-600 dark:text-emerald-400">
            last succeeded {relTime(unit.last_succeeded_at)}
          </span>
        ) : (
          <span>never succeeded</span>
        )}
      </div>
      {open && (
        <div className="mt-2 space-y-1 pl-4">
          <div className="text-[11px] text-muted-foreground">
            job #{unit.latest_job_id} · source id <code className="font-mono">{unit.source_id}</code>
          </div>
          <pre className="overflow-x-auto whitespace-pre-wrap rounded bg-muted/60 p-2 text-[11px] text-red-700 dark:text-red-300">
            {unit.last_error || "no error recorded"}
          </pre>
        </div>
      )}
    </li>
  );
}

function FailureTriage({
  units,
  isError,
  onRetry,
  onDismiss,
  retryingKey,
  dismissingKey,
}: {
  units: IndexFailedUnit[];
  isError: boolean;
  onRetry: (kind: string, sourceID: string) => void;
  onDismiss: (kind: string, sourceID: string) => void;
  retryingKey: string | null;
  dismissingKey: string | null;
}) {
  const groups = useMemo(() => {
    const m = new Map<string, IndexFailedUnit[]>();
    for (const u of units) {
      const sig = errorSignature(u.last_error ?? "unknown error");
      const arr = m.get(sig) ?? [];
      arr.push(u);
      m.set(sig, arr);
    }
    return [...m.entries()].sort((a, b) => b[1].length - a[1].length);
  }, [units]);

  // A load error must NOT read as "all clear": failures fall back to an
  // empty list on error, which would otherwise render the green success
  // state and mask real failures while the index silently degrades.
  if (isError) {
    return (
      <p className="flex items-center justify-center gap-2 py-4 text-center text-sm text-amber-700 dark:text-amber-400">
        <AlertTriangle className="h-4 w-4" /> Could not load failures; this list may be stale or
        incomplete.
      </p>
    );
  }
  if (units.length === 0) {
    return (
      <p className="flex items-center justify-center gap-2 py-4 text-center text-sm text-emerald-600 dark:text-emerald-400">
        <CheckCircle2 className="h-4 w-4" /> No open failures. A failure clears automatically once
        the unit is re-indexed successfully.
      </p>
    );
  }
  return (
    <div className="space-y-3">
      {groups.map(([sig, items]) => (
        <div key={sig} className="rounded-md border border-red-500/30 bg-red-500/5 p-3">
          <div className="mb-2 flex items-start gap-2">
            <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-red-500" />
            <code className="break-all text-xs text-red-700 dark:text-red-300">{sig}</code>
            <span className="ml-auto shrink-0 rounded-full bg-red-500/15 px-2 text-xs text-red-600 dark:text-red-300">
              {items.length} unit{items.length === 1 ? "" : "s"}
            </span>
          </div>
          <ul className="space-y-1.5">
            {items.map((u) => {
              const key = failureKey(u.source_kind, u.source_id);
              return (
                <FailedUnitRow
                  key={u.latest_job_id}
                  unit={u}
                  onRetry={onRetry}
                  onDismiss={onDismiss}
                  retrying={retryingKey === key}
                  dismissing={dismissingKey === key}
                />
              );
            })}
          </ul>
        </div>
      ))}
    </div>
  );
}

// JobRow is a normalised job-table entry: either a single notable job or
// a collapsed summary of routine reconciler heartbeats for one unit.
interface JobRow {
  key: string;
  sourceKind: string;
  sourceID: string;
  status: string;
  trigger: string;
  attempts: number;
  updated?: string;
  error?: string;
  // routineCount > 0 marks a collapsed heartbeat summary; the value is
  // how many succeeded reconciler runs (across replicas) it stands for.
  routineCount: number;
}

// collapseJobs folds the job firehose into table rows. Timer-driven
// reconciler successes for the same unit (which every replica re-runs on
// its own schedule, producing duplicate rows) collapse to one summary
// row "synced ×N"; everything else stays an individual row. This keeps a
// kind that re-syncs on a timer from drowning the table.
function collapseJobs(jobs: IndexJob[]): JobRow[] {
  const routine = new Map<string, JobRow>();
  const rows: JobRow[] = [];
  for (const j of jobs) {
    const updated = j.completed_at ?? j.started_at ?? j.created_at;
    if (j.status === "succeeded" && j.trigger === "reconciler") {
      const k = failureKey(j.source_kind, j.source_id);
      const cur = routine.get(k);
      if (!cur) {
        routine.set(k, {
          key: `routine:${k}`,
          sourceKind: j.source_kind,
          sourceID: j.source_id,
          status: "succeeded",
          trigger: "reconciler",
          attempts: j.attempts,
          updated,
          routineCount: 1,
        });
      } else {
        cur.routineCount += 1;
        // Keep the most recent timestamp as the row's "last synced".
        if (updated && (!cur.updated || new Date(updated) > new Date(cur.updated))) {
          cur.updated = updated;
        }
      }
      continue;
    }
    rows.push({
      key: `job:${j.id}`,
      sourceKind: j.source_kind,
      sourceID: j.source_id,
      status: j.status,
      trigger: j.trigger,
      attempts: j.attempts,
      updated,
      error: j.last_error,
      routineCount: 0,
    });
  }
  const all = [...rows, ...routine.values()];
  all.sort((a, b) => {
    const ta = a.updated ? new Date(a.updated).getTime() : 0;
    const tb = b.updated ? new Date(b.updated).getTime() : 0;
    return tb - ta;
  });
  return all;
}

function JobTable({ jobs }: { jobs: IndexJob[] }) {
  const rows = useMemo(() => collapseJobs(jobs), [jobs]);
  if (rows.length === 0) {
    return <p className="py-6 text-center text-sm text-muted-foreground">No jobs match this filter.</p>;
  }
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-left text-xs">
        <thead className="text-muted-foreground">
          <tr className="border-b">
            <th className="py-1.5 pr-3 font-medium">Kind</th>
            <th className="py-1.5 pr-3 font-medium">Unit</th>
            <th className="py-1.5 pr-3 font-medium">Status</th>
            <th className="py-1.5 pr-3 font-medium">Trigger</th>
            <th className="py-1.5 pr-3 font-medium">Attempts</th>
            <th className="py-1.5 pr-3 font-medium">Updated</th>
            <th className="py-1.5 font-medium">Error</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr key={r.key} className="border-b last:border-0">
              <td className="py-1.5 pr-3 font-mono">{r.sourceKind}</td>
              <td className="max-w-[200px] truncate py-1.5 pr-3 font-mono">{r.sourceID}</td>
              <td className="py-1.5 pr-3">
                <JobStatusChip status={r.status} />
              </td>
              <td className="py-1.5 pr-3 text-muted-foreground">
                {r.routineCount > 0 ? `reconciler · synced ×${r.routineCount}` : r.trigger}
              </td>
              <td className="py-1.5 pr-3 tabular-nums">{r.routineCount > 0 ? "—" : r.attempts}</td>
              <td className="py-1.5 pr-3 text-muted-foreground">{relTime(r.updated)}</td>
              <td className="max-w-[260px] truncate py-1.5 text-red-600 dark:text-red-400">
                {r.error ?? ""}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function Section({ title, hint, children }: { title: string; hint?: string; children: React.ReactNode }) {
  return (
    <div className="rounded-lg border bg-card p-4">
      <div className="mb-3 flex items-baseline justify-between">
        <h2 className="text-sm font-medium">{title}</h2>
        {hint && <span className="text-xs text-muted-foreground">{hint}</span>}
      </div>
      {children}
    </div>
  );
}

export function IndexingPage() {
  const summaryQ = useIndexJobsSummary();
  const jobsQ = useIndexJobs({ limit: 500 });
  const failuresQ = useIndexJobFailures();
  const reindex = useReindex();
  const dismiss = useDismissFailure();
  const [kindFilter, setKindFilter] = useState<string>("");
  const [statusFilter, setStatusFilter] = useState<string>("");
  // Which unit each shared mutation is acting on (null = none), so a
  // single in-flight Retry/Dismiss does not disable every button at once.
  const [retryingKey, setRetryingKey] = useState<string | null>(null);
  const [dismissingKey, setDismissingKey] = useState<string | null>(null);
  const [activeReindex, setActiveReindex] = useState<string | null>(null);

  const runReindexKind = (kind: string) => {
    setActiveReindex(kind);
    reindex.mutate({ kind }, { onSettled: () => setActiveReindex((k) => (k === kind ? null : k)) });
  };

  const retryUnit = (kind: string, sourceID: string) => {
    const key = failureKey(kind, sourceID);
    setRetryingKey(key);
    reindex.mutate(
      { kind, source_id: sourceID },
      { onSettled: () => setRetryingKey((k) => (k === key ? null : k)) },
    );
  };

  const dismissUnit = (kind: string, sourceID: string) => {
    const key = failureKey(kind, sourceID);
    setDismissingKey(key);
    dismiss.mutate(
      { kind, source_id: sourceID },
      { onSettled: () => setDismissingKey((k) => (k === key ? null : k)) },
    );
  };

  const summary = summaryQ.data;
  const jobs = useMemo(() => jobsQ.data?.jobs ?? [], [jobsQ.data]);
  const failures = useMemo(() => failuresQ.data?.failures ?? [], [failuresQ.data]);

  const latency = useMemo<KindLatency[]>(() => {
    const byKind = new Map<string, number[]>();
    for (const j of jobs) {
      if (j.status !== "succeeded" || !j.started_at || !j.completed_at) continue;
      const ms = new Date(j.completed_at).getTime() - new Date(j.started_at).getTime();
      if (!Number.isFinite(ms) || ms < 0) continue;
      const arr = byKind.get(j.source_kind) ?? [];
      arr.push(ms);
      byKind.set(j.source_kind, arr);
    }
    return [...byKind.entries()].map(([kind, durations]) => {
      const sorted = durations.sort((a, b) => a - b);
      return {
        kind,
        p50Ms: percentile(sorted, 50),
        p95Ms: percentile(sorted, 95),
        maxMs: sorted[sorted.length - 1] ?? 0,
        count: sorted.length,
      };
    });
  }, [jobs]);

  const completedAt = useMemo(
    () => jobs.filter((j) => j.status === "succeeded" && j.completed_at).map((j) => j.completed_at!),
    [jobs],
  );

  const filteredJobs = useMemo(
    () =>
      jobs.filter(
        (j) =>
          (kindFilter === "" || j.source_kind === kindFilter) &&
          (statusFilter === "" || j.status === statusFilter),
      ),
    [jobs, kindFilter, statusFilter],
  );

  if (summaryQ.isLoading) {
    return (
      <div className="flex items-center justify-center py-16 text-muted-foreground">
        <Loader2 className="mr-2 h-5 w-5 animate-spin" /> Loading indexing health…
      </div>
    );
  }

  const provider = summary?.provider;
  const kinds = summary?.kinds ?? [];

  return (
    <div className="space-y-4">
      {provider && (
        <ProviderBanner
          status={provider.status}
          kind={provider.kind}
          model={provider.model}
          dimension={provider.dimension}
        />
      )}

      {(reindex.isError || dismiss.isError) && (
        <div className="flex items-center gap-2 rounded-lg border border-red-500/40 bg-red-500/10 px-4 py-2 text-sm text-red-700 dark:text-red-300">
          <AlertTriangle className="h-4 w-4 shrink-0" /> Action failed
          {reindex.error instanceof Error
            ? `: ${reindex.error.message}`
            : dismiss.error instanceof Error
              ? `: ${dismiss.error.message}`
              : ""}
          .
        </div>
      )}
      {jobsQ.isError && (
        <div className="flex items-center gap-2 rounded-lg border border-amber-500/40 bg-amber-500/10 px-4 py-2 text-sm text-amber-700 dark:text-amber-400">
          <AlertTriangle className="h-4 w-4 shrink-0" /> Could not load job details; the
          throughput, latency, in-flight, and retry panels below may be incomplete.
        </div>
      )}

      {kinds.length === 0 ? (
        <div className="flex flex-col items-center justify-center gap-2 rounded-lg border border-dashed py-16 text-center text-muted-foreground">
          <Database className="h-8 w-8 opacity-50" />
          <p className="text-sm font-medium">No indexing consumers</p>
          <p className="max-w-md text-xs">
            Indexing runs when the platform has both a database and a configured embedding provider.
            Once a consumer (api-catalog, tools) registers, its health appears here.
          </p>
        </div>
      ) : (
        <>
          {/* Summary-first: lead with a health verdict per kind. */}
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            {kinds.map((k) => (
              <KindCard
                key={k.kind}
                summary={k}
                reindexing={activeReindex === k.kind}
                onReindex={runReindexKind}
              />
            ))}
          </div>

          <div className="grid gap-4 lg:grid-cols-2">
            <Section title="Throughput" hint="jobs completed over time">
              <IndexThroughputTimeline completedAt={completedAt} />
            </Section>
            <Section title="Embed latency" hint="started → completed per kind">
              <IndexLatencyTrack rows={latency} />
            </Section>
          </div>

          <div className="grid gap-4 lg:grid-cols-2">
            <Section title="In flight" hint="running passes">
              <InFlightPanel jobs={jobs} />
            </Section>
            <Section title="Retry backoff" hint="pending after a failure">
              <RetryBackoffPanel jobs={jobs} />
            </Section>
          </div>

          <Section
            title="Failure triage"
            hint={
              failuresQ.isError
                ? "could not load failures"
                : "open failures · auto-resolve on success"
            }
          >
            <FailureTriage
              units={failures}
              isError={failuresQ.isError ?? false}
              onRetry={retryUnit}
              onDismiss={dismissUnit}
              retryingKey={retryingKey}
              dismissingKey={dismissingKey}
            />
          </Section>

          {/* Drill-down. */}
          <Section
            title="Jobs"
            hint={jobs.length >= 500 ? `${jobs.length} most recent` : `${jobs.length} jobs`}
          >
            <div className="mb-3 flex flex-wrap items-center gap-2">
              <Activity className="h-4 w-4 text-muted-foreground" />
              <select
                value={kindFilter}
                onChange={(e) => setKindFilter(e.target.value)}
                className="rounded-md border bg-background px-2 py-1 text-xs"
                aria-label="Filter by kind"
              >
                <option value="">All kinds</option>
                {kinds.map((k) => (
                  <option key={k.kind} value={k.kind}>
                    {k.kind}
                  </option>
                ))}
              </select>
              <select
                value={statusFilter}
                onChange={(e) => setStatusFilter(e.target.value)}
                className="rounded-md border bg-background px-2 py-1 text-xs"
                aria-label="Filter by status"
              >
                <option value="">All statuses</option>
                {["pending", "running", "succeeded", "failed"].map((s) => (
                  <option key={s} value={s}>
                    {s}
                  </option>
                ))}
              </select>
              <span className="text-[11px] text-muted-foreground">
                routine reconciler syncs are collapsed
              </span>
            </div>
            <JobTable jobs={filteredJobs} />
          </Section>
        </>
      )}
    </div>
  );
}
