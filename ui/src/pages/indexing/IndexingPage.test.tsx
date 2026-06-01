import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import type {
  IndexJobsSummary,
  IndexJob,
  IndexFailedUnit,
} from "@/api/admin/indexjobs";

// Mock the index-jobs hooks so the page renders against canned state,
// exercising the verdict / coverage / triage branches and the
// retry/dismiss interactions without a network or query client.
const reindexMutate = vi.fn();
const dismissMutate = vi.fn();
let summaryState: { data?: IndexJobsSummary; isLoading: boolean };
let jobsState: { data?: { jobs: IndexJob[] }; isError?: boolean };
let failuresState: { data?: { failures: IndexFailedUnit[] }; isError?: boolean };

vi.mock("@/api/admin/indexjobs", () => ({
  useIndexJobsSummary: () => summaryState,
  useIndexJobs: () => jobsState,
  useIndexJobFailures: () => failuresState,
  useReindex: () => ({ mutate: reindexMutate, isPending: false, isError: false, error: null }),
  useDismissFailure: () => ({ mutate: dismissMutate, isPending: false, isError: false, error: null }),
}));

import { IndexingPage } from "./IndexingPage";

const summary: IndexJobsSummary = {
  provider: { kind: "ollama", model: "nomic-embed-text", dimension: 768, status: "ok" },
  kinds: [
    {
      kind: "api_catalog",
      verdict: "degraded",
      pending: 1,
      running: 0,
      succeeded: 6,
      failed: 2,
      unresolved_failures: 2,
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
      unresolved_failures: 0,
      last_activity: new Date().toISOString(),
      coverage: { indexed: 87, expected: 87, expected_known: true },
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
      data: { provider: { kind: "ollama", model: "m", dimension: 768, status: "ok" }, kinds: [] },
      isLoading: false,
    };
    render(<IndexingPage />);
    expect(screen.getByText(/No indexing consumers/i)).toBeInTheDocument();
  });

  it("surfaces a degraded provider banner", () => {
    summaryState = {
      data: {
        provider: { kind: "noop", model: "", dimension: 0, status: "unconfigured" },
        kinds: summary.kinds,
      },
      isLoading: false,
    };
    render(<IndexingPage />);
    expect(screen.getByText(/Embedding provider unconfigured/i)).toBeInTheDocument();
  });

  it("leads with a health verdict per kind and shows the active provider banner", () => {
    render(<IndexingPage />);
    expect(screen.getByText(/Embedding provider active/i)).toBeInTheDocument();
    expect(screen.getByText("Degraded")).toBeInTheDocument();
    expect(screen.getByText("Indexing…")).toBeInTheDocument();
  });

  it("shows a triage error state instead of 'all clear' when failures fail to load", () => {
    failuresState = { data: { failures: [] }, isError: true };
    render(<IndexingPage />);
    // Both the panel body and the section hint surface the error.
    expect(screen.getAllByText(/Could not load failures/i).length).toBeGreaterThan(0);
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
            unresolved_failures: 0,
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
    expect(screen.queryByText(/(indexed|synced) never/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/Units by last run/i)).not.toBeInTheDocument();
  });

  it("renders failure triage from the failures endpoint with timestamps", () => {
    render(<IndexingPage />);
    expect(screen.getByText(/embed batch: provider timeout/i)).toBeInTheDocument();
    expect(screen.getByText(/last succeeded/i)).toBeInTheDocument();
    expect(screen.getByText(/2 failures · 5 attempts/i)).toBeInTheDocument();
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
    expect(reindexMutate).toHaveBeenCalledWith({ kind: "api_catalog" }, expect.anything());
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
    expect(screen.queryByRole("button", { name: /Next page/i })).not.toBeInTheDocument();
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
