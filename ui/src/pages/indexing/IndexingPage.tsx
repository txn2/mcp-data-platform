import { useMemo, useState } from "react";
import { AlertTriangle, Loader2, Database, Activity } from "lucide-react";
import {
  useIndexJobsSummary,
  useIndexJobs,
  useIndexJobFailures,
  useReindex,
  useDismissFailure,
} from "@/api/admin/indexjobs";
import { IndexThroughputTimeline } from "@/components/charts/IndexThroughputTimeline";
import { IndexLatencyTrack, type KindLatency } from "@/components/charts/IndexLatencyTrack";
import { percentile, failureKey } from "./components/helpers";
import { ProviderBanner } from "./components/badges";
import { KindCard } from "./components/kindcard";
import { InFlightPanel, RetryBackoffPanel, FailureTriage, Section } from "./components/panels";
import { JobTable } from "./components/jobtable";

// IndexingPage is the admin-only cross-kind Indexing dashboard. It leads
// with a plain health verdict per kind (Healthy / Indexing… / Degraded /
// Idle complete) so an operator can answer "is indexing healthy?" at a
// glance, then exposes throughput, latency, in-flight progress, retry
// backoff, and a self-resolving failure triage. The two metric families
// are kept visually distinct: vector coverage (how much is indexed) and
// per-unit job state (each unit's most recent run). All data is real
// index_jobs / vector state from the admin index-jobs endpoints.

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
            <JobTable jobs={filteredJobs} resetKey={`${kindFilter}::${statusFilter}`} />
          </Section>
        </>
      )}
    </div>
  );
}
