import { useMemo, useState } from "react";
import { Loader2, Database } from "lucide-react";
import {
  useIndexJobsSummary,
  useIndexJobs,
  useIndexJobFailures,
  useReindex,
  useDismissFailure,
} from "@/api/admin/indexjobs";
import { IndexThroughputTimeline } from "@/components/charts/IndexThroughputTimeline";
import { IndexLatencyTrack, type KindLatency } from "@/components/charts/IndexLatencyTrack";
import { EmptyState } from "@/components/patterns/EmptyState";
import { percentile, failureKey } from "./components/helpers";
import { IndexingBanners } from "./components/banners";
import { KindCard } from "./components/kindcard";
import { InFlightPanel, RetryBackoffPanel, Section } from "./components/panels";
import { FailureTriage } from "./components/triage";
import { JobsSection } from "./components/JobsSection";

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
      <IndexingBanners
        provider={provider}
        actionErrors={[reindex.error, dismiss.error].filter(Boolean)}
        jobsFailed={jobsQ.isError ?? false}
      />

      {kinds.length === 0 ? (
        <EmptyState icon={Database} className="py-16">
          <p className="text-sm font-medium text-foreground">No indexing consumers</p>
          <p className="mx-auto mt-1 max-w-md text-xs">
            Indexing runs when the platform has both a database and a configured embedding provider.
            Once a consumer (api-catalog, tools) registers, its health appears here.
          </p>
        </EmptyState>
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
          <JobsSection jobs={jobs} kinds={kinds} />
        </>
      )}
    </div>
  );
}
