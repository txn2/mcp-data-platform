import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import type { PortalScriptRun } from "@/api/portal/hooks/scripts";
import { MyRunsTab } from "./MyRunsTab";

vi.mock("@/api/portal/hooks/scripts", () => ({
  useMyScriptRuns: vi.fn(),
}));

import { useMyScriptRuns } from "@/api/portal/hooks/scripts";

const mockRuns = vi.mocked(useMyScriptRuns);
const onNavigate = vi.fn();

function query<T>(data: T, extra: Record<string, unknown> = {}) {
  return { data, isLoading: false, error: null, ...extra } as never;
}

function run(overrides: Partial<PortalScriptRun> = {}): PortalScriptRun {
  return {
    id: "run-042",
    script_id: "script-001",
    script_name: "Daily Sales Report",
    status: "succeeded",
    trigger: "schedule",
    version: 2,
    fire_time: new Date("2026-08-14T07:00:00Z").toISOString(),
    finished_at: new Date("2026-08-14T07:00:08Z").toISOString(),
    duration_ms: 8_420,
    output_count: 1,
    ...overrides,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  mockRuns.mockReturnValue(query({ data: [run()], total: 1, limit: 50 }));
});

afterEach(cleanup);

describe("MyRunsTab", () => {
  it("reports what each run was, how it ended, and how long it took", () => {
    render(<MyRunsTab onNavigate={onNavigate} />);
    expect(screen.getByText("Daily Sales Report")).toBeInTheDocument();
    expect(screen.getByText("succeeded")).toBeInTheDocument();
    expect(screen.getByText("schedule")).toBeInTheDocument();
  });

  // The row opens the run itself: a listing across scripts exists to send
  // somebody to the one run they came for.
  it("opens the run from the row", () => {
    render(<MyRunsTab onNavigate={onNavigate} />);
    fireEvent.click(screen.getByRole("row", { name: /Daily Sales Report/ }));
    expect(onNavigate).toHaveBeenCalledWith("/scripts/script-001/runs/run-042");
  });

  it("opens the script from its name, without opening the run", () => {
    render(<MyRunsTab onNavigate={onNavigate} />);
    fireEvent.click(screen.getByRole("button", { name: "Daily Sales Report" }));
    expect(onNavigate).toHaveBeenCalledTimes(1);
    expect(onNavigate).toHaveBeenCalledWith("/scripts/script-001");
  });

  // The reason a run failed decides which run anybody opens, so it is in the
  // row rather than behind it, and it wraps rather than being cut off.
  it("carries the reason a run failed", () => {
    const reason = "trino: line 3:1: Table 'hive.sales.daily' does not exist";
    mockRuns.mockReturnValue(
      query({ data: [run({ status: "failed", error: reason })], total: 1, limit: 50 }),
    );
    render(<MyRunsTab onNavigate={onNavigate} />);
    expect(screen.getByText(reason)).toBeInTheDocument();
  });

  // A run whose script is outside the listing the server resolved names from
  // still names something a person can search for.
  it("falls back to the script id when the run carries no name", () => {
    mockRuns.mockReturnValue(
      query({ data: [run({ script_name: undefined })], total: 1, limit: 50 }),
    );
    render(<MyRunsTab onNavigate={onNavigate} />);
    expect(screen.getByRole("button", { name: "script-001" })).toBeInTheDocument();
  });

  it("says a run that never finished has no duration rather than showing zero", () => {
    mockRuns.mockReturnValue(
      query({
        data: [run({ status: "pending", duration_ms: 0, finished_at: undefined })],
        total: 1,
        limit: 50,
      }),
    );
    render(<MyRunsTab onNavigate={onNavigate} />);
    expect(screen.getByText("—")).toBeInTheDocument();
  });

  // A listing that filled its cap and said nothing would read as the whole
  // history, which is the failure this notice exists to prevent.
  it("says a full page has older runs behind it", () => {
    mockRuns.mockReturnValue(
      query({
        data: [run(), run({ id: "run-043" })],
        total: 2,
        limit: 2,
      }),
    );
    render(<MyRunsTab onNavigate={onNavigate} />);
    expect(screen.getByText(/Showing the 2 most recent runs across your scripts/)).toBeInTheDocument();
  });

  it("does not warn about a cap a listing did not reach", () => {
    render(<MyRunsTab onNavigate={onNavigate} />);
    expect(screen.queryByText(/most recent runs/)).not.toBeInTheDocument();
  });

  it("says plainly when nothing has run yet", () => {
    mockRuns.mockReturnValue(query({ data: [], total: 0, limit: 50 }));
    render(<MyRunsTab onNavigate={onNavigate} />);
    expect(screen.getByText(/None of your scripts has run yet/)).toBeInTheDocument();
  });

  it("reports a listing that could not be loaded instead of showing it as empty", () => {
    mockRuns.mockReturnValue(query(undefined, { error: new Error("boom") }));
    render(<MyRunsTab onNavigate={onNavigate} />);
    expect(screen.getByText(/Your runs could not be loaded/)).toBeInTheDocument();
    expect(screen.queryByText(/None of your scripts has run yet/)).not.toBeInTheDocument();
  });

  it("shows a loading state rather than an empty one while the listing is in flight", () => {
    mockRuns.mockReturnValue(query(undefined, { isLoading: true }));
    render(<MyRunsTab onNavigate={onNavigate} />);
    expect(screen.getByText("Loading runs...")).toBeInTheDocument();
  });
});
