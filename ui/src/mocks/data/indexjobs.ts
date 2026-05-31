import type {
  IndexJob,
  IndexJobsSummary,
  IndexFailedUnit,
} from "@/api/admin/indexjobs";

// Deterministic timestamps relative to a fixed "now" so the dashboard's
// relative-time and throughput panels render rich, stable data under MSW.
const NOW = Date.now();
const minsAgo = (m: number) => new Date(NOW - m * 60_000).toISOString();

// mockIndexJobsSummary exercises every panel: api_catalog with a real
// indexed/expected ratio and a failure, tools with an indexed-only
// (expected_known=false) sync indicator and a job in flight.
export const mockIndexJobsSummary: IndexJobsSummary = {
  provider: {
    kind: "ollama",
    model: "nomic-embed-text",
    dimension: 768,
    status: "ok",
  },
  kinds: [
    {
      kind: "api_catalog",
      verdict: "degraded",
      pending: 1,
      running: 0,
      succeeded: 6,
      failed: 2,
      unresolved_failures: 2,
      last_activity: minsAgo(3),
      coverage: { indexed: 142, expected: 168, expected_known: true },
    },
    {
      kind: "tools",
      verdict: "indexing",
      pending: 0,
      running: 1,
      succeeded: 1,
      failed: 0,
      unresolved_failures: 0,
      last_activity: minsAgo(1),
      coverage: { indexed: 87, expected: 0, expected_known: false },
    },
  ],
};

// mockIndexJobsFailures is the failure-triage surface: two units sharing
// an error signature (so they group) plus the timestamps and last-success
// context the triage cards render. Mirrors the failed rows in
// mockIndexJobs.
export const mockIndexJobsFailures: IndexFailedUnit[] = [
  {
    source_kind: "api_catalog",
    source_id: "acme|v1",
    latest_job_id: 106,
    last_error: 'embed batch: provider timeout after 30s on spec "acme"',
    attempts: 5,
    occurrences: 2,
    first_failed_at: minsAgo(120),
    last_failed_at: minsAgo(38),
    last_succeeded_at: minsAgo(300),
  },
  {
    source_kind: "api_catalog",
    source_id: "globex|v2",
    latest_job_id: 107,
    last_error: 'embed batch: provider timeout after 30s on spec "globex"',
    attempts: 5,
    occurrences: 1,
    first_failed_at: minsAgo(33),
    last_failed_at: minsAgo(33),
  },
];

// mockIndexJobs spans every status the dashboard buckets: succeeded (for
// throughput + latency), running (in-flight), pending-after-failure
// (retry backoff), and failed (failure triage, two sharing an error
// signature so they group).
export const mockIndexJobs: IndexJob[] = [
  {
    id: 101,
    source_kind: "tools",
    source_id: "platform",
    trigger: "reconciler",
    status: "running",
    attempts: 1,
    worker_id: "worker-7d9f8c-abcde",
    lease_expires_at: new Date(NOW + 4 * 60_000).toISOString(),
    created_at: minsAgo(2),
    started_at: minsAgo(1),
    items_done: 54,
  },
  {
    id: 102,
    source_kind: "api_catalog",
    source_id: "salesforce|v2",
    trigger: "write",
    status: "succeeded",
    attempts: 1,
    created_at: minsAgo(20),
    started_at: minsAgo(20),
    completed_at: minsAgo(19),
    items_done: 48,
  },
  {
    id: 103,
    source_kind: "api_catalog",
    source_id: "stripe|v1",
    trigger: "write",
    status: "succeeded",
    attempts: 1,
    created_at: minsAgo(12),
    started_at: minsAgo(12),
    completed_at: minsAgo(10),
    items_done: 64,
  },
  {
    id: 104,
    source_kind: "tools",
    source_id: "platform",
    trigger: "write",
    status: "succeeded",
    attempts: 1,
    created_at: minsAgo(8),
    started_at: minsAgo(8),
    completed_at: minsAgo(7),
    items_done: 87,
  },
  {
    id: 105,
    source_kind: "api_catalog",
    source_id: "github|v3",
    trigger: "reconciler",
    status: "pending",
    attempts: 2,
    last_error: "embed batch: provider timeout after 30s",
    next_run_at: new Date(NOW + 90_000).toISOString(),
    created_at: minsAgo(6),
    items_done: 0,
  },
  {
    id: 106,
    source_kind: "api_catalog",
    source_id: "acme|v1",
    trigger: "reconciler",
    status: "failed",
    attempts: 5,
    last_error: 'embed batch: provider timeout after 30s on spec "acme"',
    created_at: minsAgo(40),
    started_at: minsAgo(40),
    completed_at: minsAgo(38),
    items_done: 0,
  },
  {
    id: 107,
    source_kind: "api_catalog",
    source_id: "globex|v2",
    trigger: "manual_retry",
    status: "failed",
    attempts: 5,
    last_error: 'embed batch: provider timeout after 30s on spec "globex"',
    created_at: minsAgo(35),
    started_at: minsAgo(35),
    completed_at: minsAgo(33),
    items_done: 0,
  },
];
