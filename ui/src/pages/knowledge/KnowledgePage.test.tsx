import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, within } from "@testing-library/react";

// Mock the admin hooks so KnowledgeCaptureTab renders against controlled data
// with no network. Each hook is a vi.fn configured per test.
vi.mock("@/api/admin/hooks", () => ({
  useInsights: vi.fn(),
  useInsightStats: vi.fn(),
  useUpdateInsightStatus: vi.fn(),
  useChangesets: vi.fn(),
  useRollbackChangeset: vi.fn(),
  useAuditFilters: vi.fn(),
}));

import { KnowledgeCaptureTab } from "./KnowledgePage";
import {
  useInsights,
  useInsightStats,
  useUpdateInsightStatus,
  useAuditFilters,
} from "@/api/admin/hooks";
import type { Insight, InsightStats } from "@/api/admin/types";

const q = (data: unknown) =>
  ({ data, isLoading: false, isError: false }) as never;
const noopMut = () =>
  ({ mutate: vi.fn(), isPending: false, isError: false, error: null }) as never;

const DAY = 86_400_000;
const iso = (daysAgo: number) => new Date(Date.now() - daysAgo * DAY).toISOString();

function insight(over: Partial<Insight> & { id: string }): Insight {
  return {
    session_id: "s1",
    captured_by: "user@example.com",
    persona: "analyst",
    category: "correction",
    insight_text: "an insight",
    confidence: "high",
    entity_urns: [],
    related_columns: [],
    suggested_actions: [],
    status: "pending",
    created_at: iso(3),
    ...over,
  };
}

const stats: InsightStats = {
  total_pending: 3,
  by_entity: [],
  by_category: { correction: 3 },
  by_confidence: { high: 3 },
  by_status: { pending: 3 },
  oldest_pending_at: iso(94),
  pending_over_30d: 2,
};

beforeEach(() => {
  vi.mocked(useInsights).mockReturnValue(
    q({
      data: [
        insight({ id: "fresh", created_at: iso(3) }),
        insight({ id: "aging", created_at: iso(10) }),
        insight({ id: "stale", created_at: iso(94) }),
      ],
      total: 3,
      page: 1,
      per_page: 20,
    }),
  );
  vi.mocked(useInsightStats).mockReturnValue(q(stats));
  vi.mocked(useUpdateInsightStatus).mockReturnValue(noopMut());
  vi.mocked(useAuditFilters).mockReturnValue(q({ user_labels: {} }));
});

describe("KnowledgeCaptureTab staleness (#764)", () => {
  it("shows oldest pending age and over-30d debt on the Pending Review card", () => {
    render(<KnowledgeCaptureTab />);
    // "Oldest 94 days · 2 over 30d": the accumulating review debt.
    expect(screen.getByText(/Oldest 94 days/)).toBeInTheDocument();
    expect(screen.getByText(/2 over 30d/)).toBeInTheDocument();
  });

  it("renders a per-row age badge for each insight", () => {
    render(<KnowledgeCaptureTab />);
    const rows = screen.getAllByRole("row");
    // Header row + 3 data rows.
    const staleRow = rows.find((r) => within(r).queryByText("94 days"));
    expect(staleRow).toBeTruthy();
    expect(screen.getByText("3 days")).toBeInTheDocument();
    expect(screen.getByText("10 days")).toBeInTheDocument();
  });

  it("passes order=oldest to useInsights when Oldest First is selected", () => {
    render(<KnowledgeCaptureTab />);
    fireEvent.change(screen.getByLabelText("Sort by age"), {
      target: { value: "oldest" },
    });
    const calls = vi.mocked(useInsights).mock.calls;
    const lastCall = calls[calls.length - 1];
    expect(lastCall?.[0]).toMatchObject({ order: "oldest" });
  });

  it("defaults to newest-first order", () => {
    render(<KnowledgeCaptureTab />);
    const firstCall = vi.mocked(useInsights).mock.calls[0];
    expect(firstCall?.[0]).toMatchObject({ order: "newest" });
  });
});
