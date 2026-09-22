import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import type {
  IndexJobsSummary,
  IndexJob,
  IndexJobsFilter,
  IndexFailedUnit,
} from "@/api/admin/indexjobs";
import { ApiError } from "@/api/admin/client";
import type {
  PromMatrixResponse,
  PromVectorResponse,
} from "@/api/observability/types";

// Mock the index-jobs hooks so the page renders against canned state,
// exercising the verdict / coverage / triage branches and the
// retry/dismiss interactions without a network or query client.
const reindexMutate = vi.fn();
const dismissMutate = vi.fn();
let summaryState: { data?: IndexJobsSummary; isLoading: boolean };
let jobsState: { data?: { jobs: IndexJob[] }; isError?: boolean };
let failuresState: {
  data?: { failures: IndexFailedUnit[] };
  isError?: boolean;
};

// jobsFor answers useIndexJobs the way the server does: jobsState is the whole
// table, and a request gets its filter applied, newest first, cut at its
// limit. That is what lets a test put a backlog in front of the rows a panel
// needs and see whether the panel still finds them (#1837).
function jobsFor(filter: IndexJobsFilter = {}) {
  if (!jobsState.data) return jobsState;
  const rows = jobsState.data.jobs
    .filter((j) => !filter.status || j.status === filter.status)
    .filter(
      (j) => !filter.retrying || (j.status === "pending" && j.attempts > 0),
    )
    .sort((a, b) => b.id - a.id)
    .slice(0, filter.limit ?? 500);
  return { ...jobsState, data: { jobs: rows } };
}

// The metrics backend, answered per query string. promUnconfigured makes every
// query fail the way the proxy does with no Prometheus behind it.
let promUnconfigured = false;
let promInstant: (q: string) => PromVectorResponse | undefined = () =>
  undefined;
let promRange: (q: string) => PromMatrixResponse | undefined = () => undefined;
function promResult<T>(data: T | undefined) {
  if (promUnconfigured) {
    return {
      data: undefined,
      isLoading: false,
      isError: true,
      error: new ApiError(503, "metrics backend not configured"),
    };
  }
  return { data, isLoading: false, isError: false, error: null };
}
vi.mock("@/api/observability/hooks", () => ({
  isBackendUnconfigured: (err: unknown) =>
    err instanceof ApiError && err.status === 503,
  useObservabilityQuery: (q: string) => promResult(promInstant(q)),
  useObservabilityQueryRange: (q: string) => promResult(promRange(q)),
}));

vi.mock("@/api/admin/indexjobs", () => ({
  useIndexJobsSummary: () => summaryState,
  useIndexJobs: (filter?: IndexJobsFilter) => jobsFor(filter),
  useIndexJobFailures: () => failuresState,
  useReindex: () => ({
    mutate: reindexMutate,
    isPending: false,
    isError: false,
    error: null,
  }),
  useDismissFailure: () => ({
    mutate: dismissMutate,
    isPending: false,
    isError: false,
    error: null,
  }),
}));

import { IndexingPage } from "./IndexingPage";

const summary: IndexJobsSummary = {
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
      retrying: 0,
      last_activity: new Date().toISOString(),
      coverage: { indexed: 142, expected: 168, expected_known: true },
    },
    {
      kind: "tools",
      verdict: "indexing",
      pending: 0,
      running: 1,
      succeeded: 1,
      failed: 0,
      retrying: 0,
      last_activity: new Date().toISOString(),
      coverage: { indexed: 87, expected: 87, expected_known: true },
    },
    {
      // Every unit failing and being re-queued: the pending count is the
      // failure repeating, not a pass in flight (#1349).
      kind: "calls",
      verdict: "degraded",
      pending: 12,
      running: 0,
      succeeded: 0,
      failed: 9,
      retrying: 0,
      last_activity: new Date().toISOString(),
      coverage: { indexed: 0, expected: 12, expected_known: true },
    },
  ],
};

const jobs: IndexJob[] = [
  {
    id: 1,
    source_kind: "tools",
    source_id: "platform",
    trigger: "reconciler",
    status: "running",
    attempts: 1,
    worker_id: "w1",
    items_done: 12,
  },
  // Two routine reconciler successes for the same unit, which the table
  // must collapse into a single "synced ×2" row.
  {
    id: 2,
    source_kind: "tools",
    source_id: "platform",
    trigger: "reconciler",
    status: "succeeded",
    attempts: 1,
    completed_at: new Date().toISOString(),
    started_at: new Date(Date.now() - 1000).toISOString(),
    items_done: 87,
  },
  {
    id: 3,
    source_kind: "tools",
    source_id: "platform",
    trigger: "reconciler",
    status: "succeeded",
    attempts: 1,
    completed_at: new Date().toISOString(),
    started_at: new Date(Date.now() - 1000).toISOString(),
    items_done: 87,
  },
];

const failures: IndexFailedUnit[] = [
  {
    source_kind: "api_catalog",
    source_id: "acme|v1",
    latest_job_id: 106,
    last_error: 'embed batch: provider timeout after 30s on spec "acme"',
    attempts: 5,
    occurrences: 2,
    first_failed_at: new Date(Date.now() - 120 * 60_000).toISOString(),
    last_failed_at: new Date(Date.now() - 38 * 60_000).toISOString(),
    last_succeeded_at: new Date(Date.now() - 300 * 60_000).toISOString(),
  },
];

beforeEach(() => {
  promUnconfigured = false;
  promInstant = () => undefined;
  promRange = () => undefined;
  reindexMutate.mockReset();
  dismissMutate.mockReset();
  summaryState = { data: summary, isLoading: false };
  jobsState = { data: { jobs } };
  failuresState = { data: { failures } };
});

describe("IndexingPage", () => {
  it("renders a loading state", () => {
    summaryState = { isLoading: true };
    render(<IndexingPage />);
    expect(screen.getByText(/Loading indexing health/i)).toBeInTheDocument();
  });

  it("renders an empty state when no consumers are registered", () => {
    summaryState = {
      data: {
        provider: { kind: "ollama", model: "m", dimension: 768, status: "ok" },
        kinds: [],
      },
      isLoading: false,
    };
    render(<IndexingPage />);
    expect(screen.getByText(/No indexing consumers/i)).toBeInTheDocument();
  });

  it("surfaces a degraded provider banner", () => {
    summaryState = {
      data: {
        provider: {
          kind: "noop",
          model: "",
          dimension: 0,
          status: "unconfigured",
        },
        kinds: summary.kinds,
      },
      isLoading: false,
    };
    render(<IndexingPage />);
    expect(
      screen.getByText(/Embedding provider unconfigured/i),
    ).toBeInTheDocument();
  });

  it("leads with a health verdict per kind and shows the active provider banner", () => {
    render(<IndexingPage />);
    expect(screen.getByText(/Embedding provider active/i)).toBeInTheDocument();
    expect(screen.getByText("Degraded")).toBeInTheDocument();
    // Two kinds are mid-pass (api_catalog is retrying two units while
    // still producing vectors), so the badge appears more than once.
    expect(screen.getAllByText("Indexing…").length).toBe(2);
  });

  it("shows a triage error state instead of 'all clear' when failures fail to load", () => {
    failuresState = { data: { failures: [] }, isError: true };
    render(<IndexingPage />);
    // Both the panel body and the section hint surface the error.
    expect(
      screen.getAllByText(/Could not load failures/i).length,
    ).toBeGreaterThan(0);
    expect(screen.queryByText(/No open failures/i)).not.toBeInTheDocument();
  });

  it("labels coverage as vectors, distinct from the job-state counts", () => {
    render(<IndexingPage />);
    // Every kind shows a real indexed/expected ratio, including tools
    // (its expected is the stamped descriptor count).
    expect(screen.getByText(/142 \/ 168 indexed/)).toBeInTheDocument();
    expect(screen.getByText(/87 \/ 87 indexed/)).toBeInTheDocument();
    // Job-state family is labelled and shown for the active/degraded kinds.
    expect(screen.getAllByText(/Units by last run/i).length).toBeGreaterThan(0);
  });

  it("states open failures once, and never as a zero cell (#1349)", () => {
    render(<IndexingPage />);
    // api_catalog carries two units with an open failure. That number is
    // stated once, on the attention line. A failing unit is re-queued, so
    // a "failed" cell in the by-last-run grid would read zero right next
    // to it -- the contradiction the card used to render.
    expect(screen.getByText(/2 units need attention/i)).toBeInTheDocument();
    expect(screen.queryByText("failed")).not.toBeInTheDocument();
  });

  it("renders a kind whose every unit is failing as degraded, not as work in flight (#1349)", () => {
    summaryState = {
      data: {
        provider: summary.provider,
        kinds: [
          {
            kind: "calls",
            // The server's verdict: queued work with nothing indexed is
            // the failure repeating, not a pass in flight.
            verdict: "degraded",
            pending: 67,
            running: 0,
            succeeded: 0,
            failed: 57,
            retrying: 0,
            last_activity: new Date().toISOString(),
            coverage: { indexed: 0, expected: 68, expected_known: true },
          },
        ],
      },
      isLoading: false,
    };
    render(<IndexingPage />);
    expect(screen.getByText("Degraded")).toBeInTheDocument();
    expect(screen.getByText(/57 units need attention/i)).toBeInTheDocument();
    expect(screen.getByText(/0 \/ 68 indexed/)).toBeInTheDocument();
  });

  it("renders a fully-indexed kind with no job history as up to date, never 'never'", () => {
    summaryState = {
      data: {
        provider: summary.provider,
        kinds: [
          {
            kind: "seeded",
            verdict: "healthy",
            pending: 0,
            running: 0,
            succeeded: 0,
            failed: 0,
            retrying: 0,
            coverage: { indexed: 34, expected: 34, expected_known: true },
          },
        ],
      },
      isLoading: false,
    };
    render(<IndexingPage />);
    // Same green resting badge as any other complete kind, recency line
    // reads "fully indexed" (no timestamp), never "never", and the noisy
    // all-zero per-state row is hidden.
    expect(screen.getByText("Up to date")).toBeInTheDocument();
    expect(screen.getByText(/fully indexed/i)).toBeInTheDocument();
    // The card's recency line must not read "... never" for a kind with
    // no job timestamp (the broad word "never" can legitimately appear in
    // the unrelated job-table Updated column).
    expect(
      screen.queryByText(/(indexed|synced) never/i),
    ).not.toBeInTheDocument();
    expect(screen.queryByText(/Units by last run/i)).not.toBeInTheDocument();
  });

  it("renders a kind with nothing to index as one coherent empty state", () => {
    summaryState = {
      data: {
        provider: summary.provider,
        kinds: [
          {
            kind: "prompts",
            verdict: "healthy",
            pending: 0,
            running: 0,
            succeeded: 0,
            failed: 0,
            retrying: 0,
            coverage: { indexed: 0, expected: 0, expected_known: true },
          },
        ],
      },
      isLoading: false,
    };
    render(<IndexingPage />);
    // A kind with zero items renders a single coherent "nothing to index" line,
    // never the contradictory "not yet indexed" + "fully indexed" pairing.
    expect(screen.getByText(/nothing to index/i)).toBeInTheDocument();
    expect(screen.queryByText(/not yet indexed/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/fully indexed/i)).not.toBeInTheDocument();
  });

  it("renders failure triage from the failures endpoint with timestamps", () => {
    render(<IndexingPage />);
    expect(
      screen.getByText(/embed batch: provider timeout/i),
    ).toBeInTheDocument();
    expect(screen.getByText(/last succeeded/i)).toBeInTheDocument();
    expect(screen.getByText(/2 failures · 5 attempts/i)).toBeInTheDocument();
  });

  it("says when automatic retries are paused, and stays silent when they are not", () => {
    // A parked unit is one the sweep has stopped re-queueing every five
    // minutes. Saying so is what stops it reading as "failing constantly",
    // which is what it looked like before (#1350).
    failuresState = {
      data: {
        failures: [
          {
            ...failures[0]!,
            parked_until: new Date(Date.now() + 90 * 60_000).toISOString(),
          },
        ],
      },
    };
    render(<IndexingPage />);
    expect(
      screen.getByText(/retries paused, resuming in 1h/i),
    ).toBeInTheDocument();
  });

  it("omits the paused-retry note for a unit still being re-queued", () => {
    render(<IndexingPage />);
    expect(screen.queryByText(/retries paused/i)).not.toBeInTheDocument();
  });

  it("retries a failing unit via the reindex mutation", () => {
    render(<IndexingPage />);
    fireEvent.click(screen.getByRole("button", { name: /Retry/i }));
    expect(reindexMutate).toHaveBeenCalledWith(
      { kind: "api_catalog", source_id: "acme|v1" },
      expect.anything(),
    );
  });

  it("dismisses a failing unit via the dismiss mutation", () => {
    render(<IndexingPage />);
    fireEvent.click(screen.getByRole("button", { name: /Dismiss/i }));
    expect(dismissMutate).toHaveBeenCalledWith(
      { kind: "api_catalog", source_id: "acme|v1" },
      expect.anything(),
    );
  });

  it("re-indexes a whole kind from the kind card", () => {
    render(<IndexingPage />);
    const reindexButtons = screen.getAllByRole("button", { name: /Re-index/i });
    fireEvent.click(reindexButtons[0]!);
    expect(reindexMutate).toHaveBeenCalledWith(
      { kind: "api_catalog" },
      expect.anything(),
    );
  });

  it("collapses routine reconciler heartbeats into a single synced row", () => {
    render(<IndexingPage />);
    // The two succeeded reconciler runs for tools/platform collapse to one.
    expect(screen.getByText(/synced ×2/i)).toBeInTheDocument();
  });

  it("surfaces a banner when job details fail to load", () => {
    jobsState = { data: { jobs: [] }, isError: true };
    render(<IndexingPage />);
    expect(screen.getByText(/Could not load job details/i)).toBeInTheDocument();
  });

  it("does not show pager controls when a single page suffices", () => {
    // The default 3-job fixture collapses to two rows, well under one page.
    render(<IndexingPage />);
    expect(
      screen.queryByRole("button", { name: /Next page/i }),
    ).not.toBeInTheDocument();
  });

  it("paginates the jobs table and advances pages", () => {
    // 30 distinct write jobs do not collapse, so the table has 30 rows >
    // one 25-row page.
    const many: IndexJob[] = Array.from({ length: 30 }, (_, i) => ({
      id: 100 + i,
      source_kind: "api_catalog",
      source_id: `unit-${i}`,
      trigger: "write",
      status: "succeeded",
      attempts: 1,
      completed_at: new Date(Date.now() - i * 1000).toISOString(),
      started_at: new Date(Date.now() - i * 1000 - 500).toISOString(),
      items_done: 1,
    }));
    jobsState = { data: { jobs: many } };
    render(<IndexingPage />);

    // First page shows rows 1–25 of 30.
    expect(screen.getByText(/Page 1 of 2/i)).toBeInTheDocument();
    expect(screen.getByText(/1–25 of 30/)).toBeInTheDocument();
    expect(screen.getByText("unit-0")).toBeInTheDocument();
    expect(screen.queryByText("unit-29")).not.toBeInTheDocument();

    // Advancing reveals the remaining rows.
    fireEvent.click(screen.getByRole("button", { name: /Next page/i }));
    expect(screen.getByText(/Page 2 of 2/i)).toBeInTheDocument();
    expect(screen.getByText(/26–30 of 30/)).toBeInTheDocument();
    expect(screen.getByText("unit-29")).toBeInTheDocument();
    expect(screen.queryByText("unit-0")).not.toBeInTheDocument();
  });
});

// kindSummary is one kind card's payload, zeros unless the test says otherwise.
function kindSummary(
  kind: string,
  over: Partial<IndexJobsSummary["kinds"][number]> = {},
): IndexJobsSummary["kinds"][number] {
  return {
    kind,
    verdict: "indexing",
    pending: 0,
    running: 0,
    succeeded: 0,
    failed: 0,
    retrying: 0,
    ...over,
  };
}

function job(id: number, over: Partial<IndexJob>): IndexJob {
  return {
    id,
    source_kind: "resources",
    source_id: `r-${id}`,
    trigger: "write",
    status: "pending",
    attempts: 0,
    items_done: 0,
    ...over,
  };
}

function vec(rows: [string, number][]): PromVectorResponse {
  return {
    status: "success",
    data: {
      resultType: "vector",
      result: rows.map(([kind, v]) => ({
        metric: { kind },
        value: [0, String(v)],
      })),
    },
  };
}

// The four panels during a backlog (#1837): more than 500 pending jobs
// enqueued after the running, retried and succeeded ones, so the newest page
// of the job table holds none of them. Every panel must still show them.
describe("IndexingPage during a backlog larger than one page", () => {
  beforeEach(() => {
    summaryState = {
      isLoading: false,
      data: {
        provider: summary.provider,
        kinds: [
          kindSummary("resources", { running: 2, pending: 1992, retrying: 1 }),
          kindSummary("calls", { running: 0, pending: 2558 }),
        ],
      },
    };
    const backlog = Array.from({ length: 600 }, (_, i) =>
      job(1000 + i, { source_kind: "calls", source_id: `c-${i}` }),
    );
    jobsState = {
      data: {
        jobs: [
          job(1, {
            status: "running",
            source_id: "running-a",
            worker_id: "w1",
          }),
          job(2, {
            status: "running",
            source_id: "running-b",
            worker_id: "w2",
          }),
          job(3, { status: "pending", attempts: 2, source_id: "backing-off" }),
          job(4, { status: "succeeded", attempts: 1, source_id: "done" }),
          ...backlog,
        ],
      },
    };
    failuresState = { data: { failures: [] } };
    promRange = (q) =>
      q.includes("indexjob_jobs_total")
        ? {
            status: "success",
            data: {
              resultType: "matrix",
              result: [
                {
                  metric: {},
                  values: [
                    [1_000, "3"],
                    [1_900, "7"],
                  ],
                },
              ],
            },
          }
        : undefined;
    promInstant = (q) => {
      if (q.includes("_count")) return vec([["resources", 922]]);
      if (q.includes("0.99")) return vec([["resources", 9]]);
      if (q.includes("0.95")) return vec([["resources", 4]]);
      if (q.includes("0.5")) return vec([["resources", 1.5]]);
      return undefined;
    };
  });

  it("lists every running job, as many as the kind cards count", () => {
    render(<IndexingPage />);
    expect(screen.getByText("resources/running-a")).toBeInTheDocument();
    expect(screen.getByText("resources/running-b")).toBeInTheDocument();
    // The kind cards' running sum is 2; the panel says the same.
    expect(screen.getByText("2 running now")).toBeInTheDocument();
    expect(screen.queryByText("No jobs in flight.")).not.toBeInTheDocument();
  });

  it("lists the jobs in retry backoff under the whole table's count", () => {
    render(<IndexingPage />);
    expect(screen.getByText("resources/backing-off")).toBeInTheDocument();
    expect(screen.getByText("1 pending after a failure")).toBeInTheDocument();
    expect(
      screen.queryByText("No jobs in retry backoff."),
    ).not.toBeInTheDocument();
  });

  it("draws throughput and latency from the index-job metrics", () => {
    render(<IndexingPage />);
    expect(
      screen.getByText(/jobs completed per 15 min · last 24h/),
    ).toBeInTheDocument();
    expect(
      screen.queryByText("No jobs completed in this window."),
    ).not.toBeInTheDocument();
    expect(
      screen.getByText(/p50 1\.5s · p95 4\.0s · p99 9\.0s/),
    ).toBeInTheDocument();
  });

  it("says once that there is no metrics backend, and keeps the table-backed panels", () => {
    promUnconfigured = true;
    render(<IndexingPage />);
    expect(
      screen.getByText(/no metrics backend configured/i),
    ).toBeInTheDocument();
    expect(screen.queryByText("Throughput")).not.toBeInTheDocument();
    expect(screen.getByText("resources/running-a")).toBeInTheDocument();
  });

  it("says how many backoff jobs the list leaves out", () => {
    summaryState = {
      isLoading: false,
      data: {
        provider: summary.provider,
        kinds: [kindSummary("calls", { pending: 120, retrying: 120 })],
      },
    };
    jobsState = {
      data: {
        jobs: Array.from({ length: 120 }, (_, i) =>
          job(i + 1, { source_kind: "calls", attempts: 1 }),
        ),
      },
    };
    render(<IndexingPage />);
    expect(
      screen.getByText("Showing the newest 50 of 120."),
    ).toBeInTheDocument();
  });
});

// Coverage never reads complete while a vector is missing (#1837).
describe("IndexingPage coverage figure", () => {
  function withCoverage(indexed: number, expected: number) {
    summaryState = {
      isLoading: false,
      data: {
        provider: summary.provider,
        kinds: [
          kindSummary("calls", {
            pending: 2558,
            coverage: { indexed, expected, expected_known: true },
          }),
        ],
      },
    };
  }

  it("reads below 100% in the in-progress color while short", () => {
    withCoverage(833_090, 835_775);
    render(<IndexingPage />);
    const figure = screen.getByText("99.6%");
    expect(figure).not.toHaveClass("text-emerald-500");
    expect(screen.queryByText("100%")).not.toBeInTheDocument();
  });

  it("reads 100% in green once every vector is indexed", () => {
    withCoverage(835_775, 835_775);
    render(<IndexingPage />);
    expect(screen.getByText("100%")).toHaveClass("text-emerald-500");
  });
});
