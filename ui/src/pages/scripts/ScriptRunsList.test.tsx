import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import type { PortalScriptRun } from "@/api/portal/hooks/scripts";
import { ScriptRunsList } from "./ScriptRunsList";

vi.mock("@/api/portal/hooks/scripts", () => ({
  useScriptRunListing: vi.fn(),
}));

import { useScriptRunListing } from "@/api/portal/hooks/scripts";

const mockRuns = vi.mocked(useScriptRunListing);
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

// owned renders the listing as the person who owns the scripts reads it.
function owned() {
  return render(<ScriptRunsList audience="owner" basePath="/scripts" onNavigate={onNavigate} />);
}

beforeEach(() => {
  vi.clearAllMocks();
  mockRuns.mockReturnValue(query({ data: [run()], total: 1, limit: 50 }));
});

afterEach(cleanup);

describe("ScriptRunsList", () => {
  it("reports what each run was, how it ended, and how long it took", () => {
    owned();
    expect(screen.getByText("Daily Sales Report")).toBeInTheDocument();
    expect(screen.getByText("succeeded")).toBeInTheDocument();
    expect(screen.getByText("schedule")).toBeInTheDocument();
  });

  // The row opens the run itself: a listing across scripts exists to send
  // somebody to the one run they came for.
  it("opens the run from the row", () => {
    owned();
    fireEvent.click(screen.getByRole("row", { name: /Daily Sales Report/ }));
    expect(onNavigate).toHaveBeenCalledWith("/scripts/script-001/runs/run-042");
  });

  it("opens the script from its name, without opening the run", () => {
    owned();
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
    owned();
    expect(screen.getByText(reason)).toBeInTheDocument();
  });

  // A run whose script is outside the listing the server resolved names from
  // still names something a person can search for.
  it("falls back to the script id when the run carries no name", () => {
    mockRuns.mockReturnValue(
      query({ data: [run({ script_name: undefined })], total: 1, limit: 50 }),
    );
    owned();
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
    owned();
    expect(screen.getByText("—")).toBeInTheDocument();
  });

  // A listing that filled its cap and said nothing would read as the whole
  // history, which is the failure this notice exists to prevent.
  it("says a full page has older runs behind it", () => {
    mockRuns.mockReturnValue(query({ data: [run(), run({ id: "run-043" })], total: 2, limit: 2 }));
    owned();
    expect(
      screen.getByText(/Showing the 2 most recent runs across your scripts/),
    ).toBeInTheDocument();
  });

  it("does not warn about a cap a listing did not reach", () => {
    owned();
    expect(screen.queryByText(/most recent runs/)).not.toBeInTheDocument();
  });

  it("says plainly when nothing has run yet", () => {
    mockRuns.mockReturnValue(query({ data: [], total: 0, limit: 50 }));
    owned();
    expect(screen.getByText(/None of your scripts has run yet/)).toBeInTheDocument();
  });

  it("reports a listing that could not be loaded instead of showing it as empty", () => {
    mockRuns.mockReturnValue(query(undefined, { error: new Error("boom") }));
    owned();
    expect(screen.getByText(/run history could not be loaded/)).toBeInTheDocument();
    expect(screen.queryByText(/None of your scripts has run yet/)).not.toBeInTheDocument();
  });

  it("shows a loading state rather than an empty one while the listing is in flight", () => {
    mockRuns.mockReturnValue(query(undefined, { isLoading: true }));
    owned();
    expect(screen.getByText("Loading runs...")).toBeInTheDocument();
  });
});

// The same listing, read by an administrator (#1407): every run rather than
// one person's, opened under the administrator's own section.
describe("ScriptRunsList: the administrator's reading", () => {
  it("opens a run under the section the reader came from", () => {
    render(
      <ScriptRunsList audience="admin" basePath="/admin/scripts" onNavigate={onNavigate} />,
    );
    fireEvent.click(screen.getByRole("row", { name: /Daily Sales Report/ }));
    expect(onNavigate).toHaveBeenCalledWith("/admin/scripts/script-001/runs/run-042");
  });

  it("says nothing has run in the platform's terms rather than the reader's", () => {
    mockRuns.mockReturnValue(query({ data: [], total: 0, limit: 50 }));
    render(
      <ScriptRunsList audience="admin" basePath="/admin/scripts" onNavigate={onNavigate} />,
    );
    expect(screen.getByText(/^Nothing has run yet/)).toBeInTheDocument();
  });

  // A narrowed listing asks the server for that script's runs, so the cap
  // counts that script's history rather than the platform's.
  it("asks for one script's runs when narrowed to it", () => {
    const clear = vi.fn();
    render(
      <ScriptRunsList
        audience="admin"
        basePath="/admin/scripts"
        onNavigate={onNavigate}
        scriptId="script-001"
        scriptName="Daily Sales Report"
        onClearScript={clear}
      />,
    );
    expect(mockRuns).toHaveBeenCalledWith("script-001");
    expect(screen.getByText(/Narrowed to/)).toHaveTextContent("Daily Sales Report");

    fireEvent.click(screen.getByRole("button", { name: "Show every script" }));
    expect(clear).toHaveBeenCalled();
  });

  it("offers no way to clear a narrowing there is none of", () => {
    render(
      <ScriptRunsList audience="admin" basePath="/admin/scripts" onNavigate={onNavigate} />,
    );
    expect(screen.queryByRole("button", { name: "Show every script" })).not.toBeInTheDocument();
  });
});
