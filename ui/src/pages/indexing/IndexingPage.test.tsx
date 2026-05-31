import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import type {
  IndexJobsSummary,
  IndexJob,
} from "@/api/admin/indexjobs";

// Mock the index-jobs hooks so the page renders against canned state,
// exercising the empty / degraded / healthy branches and the retry
// interaction without a network or query client.
const mutate = vi.fn();
let summaryState: { data?: IndexJobsSummary; isLoading: boolean };
let jobsState: { data?: { jobs: IndexJob[] }; isError?: boolean };

vi.mock("@/api/admin/indexjobs", () => ({
  useIndexJobsSummary: () => summaryState,
  useIndexJobs: () => jobsState,
  useReindex: () => ({ mutate, isPending: false, isError: false, error: null }),
}));

import { IndexingPage } from "./IndexingPage";

const healthySummary: IndexJobsSummary = {
  provider: { kind: "ollama", model: "nomic-embed-text", dimension: 768, status: "ok" },
  kinds: [
    {
      kind: "api_catalog",
      pending: 1,
      running: 0,
      succeeded: 6,
      failed: 2,
      last_activity: new Date().toISOString(),
      coverage: { indexed: 142, expected: 168, expected_known: true },
    },
    {
      kind: "tools",
      pending: 0,
      running: 1,
      succeeded: 1,
      failed: 0,
      last_activity: new Date().toISOString(),
      coverage: { indexed: 87, expected: 0, expected_known: false },
    },
  ],
};

const healthyJobs: IndexJob[] = [
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
  {
    id: 2,
    source_kind: "api_catalog",
    source_id: "acme|v1",
    trigger: "reconciler",
    status: "failed",
    attempts: 5,
    last_error: "embed batch: provider timeout",
    items_done: 0,
  },
];

beforeEach(() => {
  mutate.mockReset();
  summaryState = { data: healthySummary, isLoading: false };
  jobsState = { data: { jobs: healthyJobs } };
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
        provider: { kind: "noop", model: "", dimension: 0, status: "unconfigured" },
        kinds: healthySummary.kinds,
      },
      isLoading: false,
    };
    render(<IndexingPage />);
    expect(screen.getByText(/Embedding provider unconfigured/i)).toBeInTheDocument();
  });

  it("renders cross-kind health, coverage, and failure triage", () => {
    render(<IndexingPage />);
    expect(screen.getByText(/Embedding provider active/i)).toBeInTheDocument();
    // Both kinds appear (kind card + job table rows).
    expect(screen.getAllByText("api_catalog").length).toBeGreaterThan(0);
    expect(screen.getAllByText("tools").length).toBeGreaterThan(0);
    // api_catalog coverage ratio is shown.
    expect(screen.getByText(/142 \/ 168 indexed/)).toBeInTheDocument();
    // tools shows the indexed-only sync indicator (expected_known=false).
    expect(screen.getByText(/87/)).toBeInTheDocument();
    // Failure triage surfaces the error (also appears in the job table).
    expect(screen.getAllByText(/embed batch: provider timeout/i).length).toBeGreaterThan(0);
  });

  it("retries a failed job via the reindex mutation", () => {
    render(<IndexingPage />);
    fireEvent.click(screen.getByRole("button", { name: /Retry/i }));
    expect(mutate).toHaveBeenCalledWith(
      { kind: "api_catalog", source_id: "acme|v1" },
      expect.anything(),
    );
  });

  it("re-indexes a whole kind from the kind card", () => {
    render(<IndexingPage />);
    const reindexButtons = screen.getAllByRole("button", { name: /Re-index/i });
    fireEvent.click(reindexButtons[0]!);
    expect(mutate).toHaveBeenCalledWith({ kind: "api_catalog" }, expect.anything());
  });

  it("surfaces a banner when job details fail to load", () => {
    jobsState = { data: { jobs: [] }, isError: true };
    render(<IndexingPage />);
    expect(screen.getByText(/Could not load job details/i)).toBeInTheDocument();
  });
});
