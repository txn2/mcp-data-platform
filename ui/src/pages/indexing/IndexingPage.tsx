import { useMemo, useState } from "react";
import { Loader2, Database } from "lucide-react";
import {
  useIndexJobsSummary,
  useIndexJobs,
  useIndexJobFailures,
  useReindex,
  useDismissFailure,
} from "@/api/admin/indexjobs";
import { EmptyState } from "@/components/patterns/EmptyState";
import { failureKey } from "./components/helpers";
import { IndexingBanners } from "./components/banners";
import { KindCard } from "./components/kindcard";
import { InFlightPanel, RetryBackoffPanel, Section } from "./components/panels";
import { QueueMetricPanels } from "./components/metricpanels";
import { FailureTriage } from "./components/triage";
import { JobsSection } from "./components/JobsSection";

// IndexingPage is the admin-only cross-kind Indexing dashboard. It leads
// with a plain health verdict per kind (Healthy / Indexing… / Degraded /
// Idle complete) so an operator can answer "is indexing healthy?" at a
// glance, then exposes throughput, latency, in-flight progress, retry
// backoff, and a self-resolving failure triage. The two metric families
// are kept visually distinct: vector coverage (how much is indexed) and
// per-unit job state (each unit's most recent run).
//
// Each panel reads the source that can answer it whole (#1837): the kind
// cards and the counts come from the summary, which aggregates the table;
// In flight and Retry backoff ask the job list for exactly the rows they
// show; Throughput and Embed latency come from the index-job metrics in
// Prometheus. Only the drill-down reads the newest page of rows, because
// that is what it is.

// RETRY_LIST_LIMIT is how many backoff rows the panel lists; the summary
// counts the rest.
const RETRY_LIST_LIMIT = 50;

export function IndexingPage() {
  const summaryQ = useIndexJobsSummary();
  const jobsQ = useIndexJobs({ limit: 500 });
  const runningQ = useIndexJobs({ status: "running", limit: 500 });
  const retryingQ = useIndexJobs({ retrying: true, limit: RETRY_LIST_LIMIT });
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

  const running = useMemo(() => runningQ.data?.jobs ?? [], [runningQ.data]);
  const retrying = useMemo(() => retryingQ.data?.jobs ?? [], [retryingQ.data]);

  if (summaryQ.isLoading) {
    return (
      <div className="flex items-center justify-center py-16 text-muted-foreground">
        <Loader2 className="mr-2 h-5 w-5 animate-spin" /> Loading indexing health…
      </div>
    );
  }

  const provider = summary?.provider;
  const kinds = summary?.kinds ?? [];
  const retryingTotal = kinds.reduce((sum, k) => sum + k.retrying, 0);

  return (
    <div className="space-y-4">
      <IndexingBanners
        provider={provider}
        actionErrors={[reindex.error, dismiss.error].filter(Boolean)}
        jobsFailed={[jobsQ, runningQ, retryingQ].some((q) => q.isError)}
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

          <QueueMetricPanels />

          <div className="grid gap-4 lg:grid-cols-2">
            <Section title="In flight" hint={`${running.length.toLocaleString()} running now`}>
              <InFlightPanel jobs={running} />
            </Section>
            <Section
              title="Retry backoff"
              hint={`${retryingTotal.toLocaleString()} pending after a failure`}
            >
              <RetryBackoffPanel jobs={retrying} total={retryingTotal} />
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
