import type {
  IndexJob,
  IndexJobsSummary,
  IndexFailedUnit,
} from "@/api/admin/indexjobs";

// Deterministic timestamps relative to a fixed "now" so the dashboard's
// relative-time and throughput panels render rich, stable data under MSW.
const NOW = Date.now();
const minsAgo = (m: number) => new Date(NOW - m * 60_000).toISOString();

// mockIndexJobsSummary exercises every panel and every verdict:
// api_catalog indexing with a real indexed/expected ratio and two units
// under retry, tools in sync with a job in flight, and calls degraded --
// every unit failing and being re-queued, so the pending count is the
// failure repeating rather than a pass in flight (#1349).
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
      verdict: "indexing",
      pending: 1,
      running: 0,
      succeeded: 6,
      failed: 2,
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
      last_activity: minsAgo(1),
      coverage: { indexed: 87, expected: 87, expected_known: true },
    },
    {
      kind: "calls",
      verdict: "degraded",
      pending: 12,
      running: 0,
      succeeded: 0,
      failed: 9,
      last_activity: minsAgo(4),
      coverage: { indexed: 0, expected: 12, expected_known: true },
    },
  ],
};

// mockIndexJobsFailures is the failure-triage surface: two units sharing
// an error signature (so they group) plus the timestamps and last-success
// context the triage cards render, and a calls unit that has never
// succeeded, which is what the degraded kind above is made of and which
// has failed often enough that its automatic retries are deferred.
// Mirrors the failed rows in mockIndexJobs.
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
  {
    source_kind: "calls",
    source_id: "9f2c1b7e-4a05-4c3d-9f18-2b6e05a1c744",
    latest_job_id: 108,
    last_error: "callindex: upsert vectors: write rejected by the store",
    attempts: 5,
    occurrences: 41,
    first_failed_at: minsAgo(2880),
    last_failed_at: minsAgo(4),
    // 41 failures with no success in between is the shape the sweep stops
    // re-queueing: the deferral has reached its cap, so the next automatic
    // attempt is ~6h after the last one. This is the only unit here that
    // renders the paused-retry note.
    parked_until: minsAgo(4 - 6 * 60),
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
