import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiFetch } from "./client";

// Types mirror the server-side admin index-jobs payloads
// (pkg/admin/indexjobs_handler.go). The Indexing dashboard renders
// cross-kind embedding health, coverage, and failure triage from these.

// IndexCoverage is a kind's indexed-vs-expected vector totals.
// expected_known distinguishes a real ratio (api-catalog, which stamps an
// operation_count per spec) from an indexed-only count (tools, which
// re-syncs continuously and stamps no expected count).
export interface IndexCoverage {
  indexed: number;
  expected: number;
  expected_known: boolean;
}

// IndexProviderStatus is the embedding-provider health banner. A degraded
// provider (noop / unconfigured) makes every index meaningless, so the
// dashboard surfaces it prominently.
export interface IndexProviderStatus {
  kind: string;
  model: string;
  dimension: number;
  status: "ok" | "unconfigured";
}

// IndexKindSummary is one registered kind's job-state rollup, last
// activity, and optional coverage.
export interface IndexKindSummary {
  kind: string;
  pending: number;
  running: number;
  succeeded: number;
  failed: number;
  last_activity?: string;
  coverage?: IndexCoverage;
}

// IndexJobsSummary is the cross-kind health payload rendered on load.
export interface IndexJobsSummary {
  provider: IndexProviderStatus;
  kinds: IndexKindSummary[];
}

// IndexJob is one index_jobs row in the drill-down list.
export interface IndexJob {
  id: number;
  source_kind: string;
  source_id: string;
  trigger: string;
  status: string;
  attempts: number;
  last_error?: string;
  next_run_at?: string;
  worker_id?: string;
  lease_expires_at?: string;
  created_at?: string;
  started_at?: string;
  completed_at?: string;
  items_done: number;
}

// useIndexJobsSummary polls the cross-kind health summary. The worker,
// reconciler, and reaper all run off the request path, so a 5s cadence
// keeps the dashboard reflecting work as it completes.
export function useIndexJobsSummary() {
  return useQuery({
    queryKey: ["admin", "index-jobs", "summary"],
    queryFn: () => apiFetch<IndexJobsSummary>("/index-jobs"),
    refetchInterval: 5000,
  });
}

// IndexJobsFilter narrows the drill-down list.
export interface IndexJobsFilter {
  kind?: string;
  status?: string;
  source_id?: string;
  limit?: number;
}

// useIndexJobs polls the job list / drill-down. The dashboard fetches a
// generous page once and derives its throughput, latency, in-flight,
// retry, and failure panels from it client-side.
export function useIndexJobs(filter: IndexJobsFilter = {}) {
  const qs = new URLSearchParams();
  if (filter.kind) qs.set("kind", filter.kind);
  if (filter.status) qs.set("status", filter.status);
  if (filter.source_id) qs.set("source_id", filter.source_id);
  qs.set("limit", String(filter.limit ?? 500));
  const query = qs.toString();
  return useQuery({
    queryKey: ["admin", "index-jobs", "jobs", filter],
    queryFn: () => apiFetch<{ jobs: IndexJob[] }>(`/index-jobs/jobs?${query}`),
    refetchInterval: 5000,
  });
}

// useReindex enqueues a manual re-index. With source_id it targets one
// unit (the failure-triage retry button); without, every out-of-sync
// unit of the kind. Returns 202 Accepted; the embedding happens off the
// request path, so the caller relies on the polling summary/list to show
// progress.
export function useReindex() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { kind: string; source_id?: string }) =>
      apiFetch<{ status: string; enqueued: string[]; count: number }>(
        "/index-jobs/reindex",
        { method: "POST", body: JSON.stringify(body) },
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["admin", "index-jobs"] });
    },
  });
}
