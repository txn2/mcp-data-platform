import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup, within } from "@testing-library/react";
import { MyScriptsPage } from "./MyScriptsPage";

// The page is two tabs over two listings (#1405). Each listing has its own
// tests; what this file covers is that the page is wired to both of them and
// that each opens the owner's own section.
vi.mock("@/api/portal/hooks/scripts", () => ({
  useScriptListing: vi.fn(),
  useScriptRunListing: vi.fn(),
}));

// The Schedules tab reads the fire layout (#1891); an account with nothing
// scheduled is what these page tests need from it.
vi.mock("@/api/portal/hooks/scheduleTimeline", async (importActual) => ({
  ...(await importActual<object>()),
  useScheduleTimeline: vi.fn(() => ({
    data: { timezone: "UTC", unreadable: [], sections: [] },
    isLoading: false,
    error: null,
  })),
}));

import { useScriptListing, useScriptRunListing } from "@/api/portal/hooks/scripts";

const mockScripts = vi.mocked(useScriptListing);
const mockRuns = vi.mocked(useScriptRunListing);
const onNavigate = vi.fn();

function query<T>(data: T) {
  return { data, isLoading: false, error: null } as never;
}

const script = {
  script: {
    id: "script-001",
    name: "daily-sales-report",
    display_name: "Daily Sales Report",
    status: "active",
    enabled: true,
    version: 2,
    updated_at: new Date().toISOString(),
  },
  owned: true,
};

const run = {
  id: "run-042",
  script_id: "script-001",
  script_name: "Daily Sales Report",
  status: "failed",
  trigger: "schedule",
  version: 2,
  fire_time: new Date("2026-08-14T07:00:00Z").toISOString(),
  finished_at: new Date("2026-08-14T07:00:02Z").toISOString(),
  duration_ms: 2_100,
  error: "trino: table not found: sales.daily",
  output_count: 0,
};

beforeEach(() => {
  vi.clearAllMocks();
  mockScripts.mockReturnValue(query({ data: [script], total: 1 }));
  mockRuns.mockReturnValue(query({ data: [run], total: 1, limit: 50 }));
});

afterEach(cleanup);

describe("MyScriptsPage", () => {
  // #1891: a third tab, between the two that were there, and the page still
  // opens on the listing.
  it("reads Automations, Schedules, Runs, and opens on Automations", () => {
    render(<MyScriptsPage onNavigate={onNavigate} />);
    // The page's own strip is the first; the listing has a scope switch below it.
    const strip = screen.getAllByRole("tablist")[0]!;
    expect(within(strip).getAllByRole("tab").map((t) => t.textContent)).toEqual([
      "Automations",
      "Schedules",
      "Runs",
    ]);
    expect(screen.getByRole("tab", { name: "Automations" })).toHaveAttribute("aria-selected", "true");
  });

  it("sends a reader with nothing scheduled from Schedules back to Automations", () => {
    render(<MyScriptsPage onNavigate={onNavigate} />);
    fireEvent.mouseDown(screen.getByRole("tab", { name: "Schedules" }));
    fireEvent.click(screen.getByRole("button", { name: "Go to Automations" }));
    expect(screen.getByRole("tab", { name: "Automations" })).toHaveAttribute("aria-selected", "true");
  });
  it("opens on the scripts a person owns", () => {
    render(<MyScriptsPage onNavigate={onNavigate} />);
    expect(screen.getByText("Daily Sales Report")).toBeInTheDocument();
    expect(screen.queryByRole("columnheader", { name: "Owner" })).not.toBeInTheDocument();
  });

  // The other question the per-script history cannot answer: how are my
  // scripts going, all of them, with the reason each failure failed.
  it("shows every run of every script on the Runs tab", () => {
    render(<MyScriptsPage onNavigate={onNavigate} />);
    fireEvent.mouseDown(screen.getByRole("tab", { name: "Runs" }));

    expect(screen.getByText("trino: table not found: sales.daily")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("row", { name: /Daily Sales Report/ }));
    expect(onNavigate).toHaveBeenCalledWith("/automations/script-001/runs/run-042");
  });
});
