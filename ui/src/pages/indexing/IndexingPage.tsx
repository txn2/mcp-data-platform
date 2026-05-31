import { useMemo, useState } from "react";
import {
  AlertTriangle,
  CheckCircle2,
  Loader2,
  RefreshCw,
  Database,
  Activity,
} from "lucide-react";
import {
  useIndexJobsSummary,
  useIndexJobs,
  useReindex,
  type IndexKindSummary,
  type IndexJob,
} from "@/api/admin/indexjobs";
import { IndexCoverageHeatmap } from "@/components/charts/IndexCoverageHeatmap";
import { IndexThroughputTimeline } from "@/components/charts/IndexThroughputTimeline";
import { IndexLatencyTrack, type KindLatency } from "@/components/charts/IndexLatencyTrack";

// IndexingPage is the admin-only cross-kind Indexing dashboard: embedding
// health, coverage, throughput, latency, in-flight progress, retry
// backoff, and failure triage for every index_jobs consumer (api-catalog
// operation vectors, tool descriptors, and any future consumer, which
// gets visibility here for free). All data is real index_jobs / vector
// state from the admin index-jobs endpoints — no mocked dimensions.

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

// StatusBar is a compact stacked bar of a kind's job-state distribution.
function StatusBar({ summary }: { summary: IndexKindSummary }) {
  const total = summary.pending + summary.running + summary.succeeded + summary.failed;
  if (total === 0) {
    return <div className="h-2 w-full rounded-full bg-muted" />;
  }
  const segs: { key: string; val: number }[] = [
    { key: "succeeded", val: summary.succeeded },
    { key: "running", val: summary.running },
    { key: "pending", val: summary.pending },
    { key: "failed", val: summary.failed },
  ];
  return (
    <div className="flex h-2 w-full overflow-hidden rounded-full bg-muted" role="img" aria-label={`${summary.kind} job states`}>
      {segs.map((s) =>
        s.val === 0 ? null : (
          <div
            key={s.key}
            style={{ width: `${(s.val / total) * 100}%`, backgroundColor: STATUS_COLORS[s.key] }}
            title={`${s.key}: ${s.val}`}
          />
        ),
      )}
    </div>
  );
}

function CoverageIndicator({ summary }: { summary: IndexKindSummary }) {
  const cov = summary.coverage;
  if (!cov) {
    return <span className="text-xs text-muted-foreground">coverage n/a</span>;
  }
  if (!cov.expected_known) {
    // Tools-style: no stored expected count. Derive a sync indicator from
    // the latest job states instead of an indexed/expected ratio.
    const syncing = summary.running > 0 || summary.pending > 0;
    return (
      <span className="flex items-center gap-1 text-xs">
        <span className="font-medium tabular-nums">{cov.indexed.toLocaleString()}</span>
        <span className="text-muted-foreground">indexed</span>
        {syncing ? (
          <span className="flex items-center gap-1 text-blue-500">
            <Loader2 className="h-3 w-3 animate-spin" /> re-syncing
          </span>
        ) : summary.failed > 0 ? (
          <span className="text-red-500">degraded</span>
        ) : (
          <span className="text-emerald-500">in sync</span>
        )}
      </span>
    );
  }
  const pct = cov.expected > 0 ? Math.round((cov.indexed / cov.expected) * 100) : 100;
  return (
    <div className="space-y-1">
      <div className="flex items-center justify-between text-xs">
        <span className="tabular-nums">
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

function KindCard({
  summary,
  onReindex,
  reindexing,
}: {
  summary: IndexKindSummary;
  onReindex: (kind: string) => void;
  reindexing: boolean;
}) {
  return (
    <div className="flex flex-col gap-3 rounded-lg border bg-card p-4">
      <div className="flex items-center justify-between">
        <span className="font-mono text-sm font-medium">{summary.kind}</span>
        <button
          type="button"
          onClick={() => onReindex(summary.kind)}
          disabled={reindexing}
          className="flex items-center gap-1 rounded-md border px-2 py-1 text-xs text-muted-foreground transition-colors hover:bg-accent disabled:opacity-50"
          title="Re-index every out-of-sync unit of this kind"
        >
          <RefreshCw className={`h-3 w-3 ${reindexing ? "animate-spin" : ""}`} /> Re-index
        </button>
      </div>
      <StatusBar summary={summary} />
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
      <CoverageIndicator summary={summary} />
      <div className="text-[11px] text-muted-foreground">
        last activity {relTime(summary.last_activity)}
      </div>
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
            lease {relTime(j.lease_expires_at).replace(" ago", "")}
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

function FailureTriage({
  jobs,
  onRetry,
  activeKey,
}: {
  jobs: IndexJob[];
  onRetry: (kind: string, sourceID: string) => void;
  // The reindex target currently in flight, so only its Retry button
  // shows busy rather than disabling every retry at once.
  activeKey: string | null;
}) {
  const failed = jobs.filter((j) => j.status === "failed");
  const groups = useMemo(() => {
    const m = new Map<string, IndexJob[]>();
    for (const j of failed) {
      const sig = errorSignature(j.last_error ?? "unknown error");
      const arr = m.get(sig) ?? [];
      arr.push(j);
      m.set(sig, arr);
    }
    return [...m.entries()].sort((a, b) => b[1].length - a[1].length);
  }, [failed]);

  if (failed.length === 0) {
    return (
      <p className="flex items-center justify-center gap-2 py-4 text-center text-sm text-emerald-600 dark:text-emerald-400">
        <CheckCircle2 className="h-4 w-4" /> No failed jobs.
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
              {items.length}
            </span>
          </div>
          <ul className="space-y-1">
            {items.map((j) => (
              <li key={j.id} className="flex items-center justify-between gap-2 text-xs">
                <span className="truncate font-mono">
                  {j.source_kind}/{j.source_id} · {j.attempts} attempts
                </span>
                <button
                  type="button"
                  onClick={() => onRetry(j.source_kind, j.source_id)}
                  disabled={activeKey === `${j.source_kind}::${j.source_id}`}
                  className="flex shrink-0 items-center gap-1 rounded border px-2 py-0.5 text-muted-foreground transition-colors hover:bg-accent disabled:opacity-50"
                >
                  <RefreshCw
                    className={`h-3 w-3 ${activeKey === `${j.source_kind}::${j.source_id}` ? "animate-spin" : ""}`}
                  />{" "}
                  Retry
                </button>
              </li>
            ))}
          </ul>
        </div>
      ))}
    </div>
  );
}

function JobTable({ jobs }: { jobs: IndexJob[] }) {
  if (jobs.length === 0) {
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
          {jobs.map((j) => (
            <tr key={j.id} className="border-b last:border-0">
              <td className="py-1.5 pr-3 font-mono">{j.source_kind}</td>
              <td className="max-w-[200px] truncate py-1.5 pr-3 font-mono">{j.source_id}</td>
              <td className="py-1.5 pr-3">
                <JobStatusChip status={j.status} />
              </td>
              <td className="py-1.5 pr-3 text-muted-foreground">{j.trigger}</td>
              <td className="py-1.5 pr-3 tabular-nums">{j.attempts}</td>
              <td className="py-1.5 pr-3 text-muted-foreground">
                {relTime(j.completed_at ?? j.started_at ?? j.created_at)}
              </td>
              <td className="max-w-[260px] truncate py-1.5 text-red-600 dark:text-red-400">
                {j.last_error ?? ""}
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

// reindexKey identifies a single in-flight re-index target so only the
// clicked button shows a busy state (a kind-wide re-index keys on the
// kind; a per-unit retry keys on kind + source id).
function reindexKey(kind: string, sourceID?: string): string {
  return sourceID ? `${kind}::${sourceID}` : kind;
}

export function IndexingPage() {
  const summaryQ = useIndexJobsSummary();
  const jobsQ = useIndexJobs({ limit: 500 });
  const reindex = useReindex();
  const [kindFilter, setKindFilter] = useState<string>("");
  const [statusFilter, setStatusFilter] = useState<string>("");
  // Which re-index target is currently in flight (null = none), so a
  // single shared mutation does not disable every button at once.
  const [activeReindex, setActiveReindex] = useState<string | null>(null);

  const runReindex = (kind: string, sourceID?: string) => {
    const key = reindexKey(kind, sourceID);
    setActiveReindex(key);
    reindex.mutate(
      sourceID ? { kind, source_id: sourceID } : { kind },
      { onSettled: () => setActiveReindex((k) => (k === key ? null : k)) },
    );
  };

  const summary = summaryQ.data;
  const jobs = useMemo(() => jobsQ.data?.jobs ?? [], [jobsQ.data]);

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

      {reindex.isError && (
        <div className="flex items-center gap-2 rounded-lg border border-red-500/40 bg-red-500/10 px-4 py-2 text-sm text-red-700 dark:text-red-300">
          <AlertTriangle className="h-4 w-4 shrink-0" /> Re-index request failed
          {reindex.error instanceof Error ? `: ${reindex.error.message}` : ""}.
        </div>
      )}
      {jobsQ.isError && (
        <div className="flex items-center gap-2 rounded-lg border border-amber-500/40 bg-amber-500/10 px-4 py-2 text-sm text-amber-700 dark:text-amber-400">
          <AlertTriangle className="h-4 w-4 shrink-0" /> Could not load job details; the
          throughput, latency, in-flight, retry, and failure panels below may be incomplete.
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
          {/* Centerpiece: cross-kind job-state heatmap. */}
          <Section title="Index state by kind" hint="units per state · brighter = more">
            <IndexCoverageHeatmap kinds={kinds} />
          </Section>

          {/* Per-kind health cards. */}
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            {kinds.map((k) => (
              <KindCard
                key={k.kind}
                summary={k}
                reindexing={activeReindex === k.kind}
                onReindex={(kind) => runReindex(kind)}
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

          <Section title="Failure triage" hint="grouped by error signature">
            <FailureTriage
              jobs={jobs}
              activeKey={activeReindex}
              onRetry={(kind, sourceID) => runReindex(kind, sourceID)}
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
            </div>
            <JobTable jobs={filteredJobs} />
          </Section>
        </>
      )}
    </div>
  );
}
