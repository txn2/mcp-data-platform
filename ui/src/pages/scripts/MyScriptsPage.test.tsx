import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup, waitFor } from "@testing-library/react";
import type { PortalScriptRow } from "@/api/portal/hooks/scripts";
import { MyScriptsPage } from "./MyScriptsPage";

vi.mock("@/api/portal/hooks/scripts", () => ({
  useMyScripts: vi.fn(),
  useMyScriptRuns: vi.fn(),
}));

import { useMyScripts, useMyScriptRuns } from "@/api/portal/hooks/scripts";

const mockScripts = vi.mocked(useMyScripts);
const mockRuns = vi.mocked(useMyScriptRuns);
const onNavigate = vi.fn();

function query<T>(data: T, extra: Record<string, unknown> = {}) {
  return { data, isLoading: false, error: null, ...extra } as never;
}

function row(overrides: Partial<PortalScriptRow> = {}): PortalScriptRow {
  return {
    script: {
      id: "script-001",
      name: "daily-sales-report",
      display_name: "Daily Sales Report",
      description: "Yesterday's sales by region.",
      owner_email: "sarah.chen@example.com",
      status: "active",
      enabled: true,
      version: 2,
      updated_at: new Date().toISOString(),
    },
    schedule: {
      id: "sched-001",
      script_id: "script-001",
      cron_spec: "0 7 * * 1-5",
      timezone: "America/Los_Angeles",
      enabled: true,
      next_run_at: new Date("2026-08-15T14:00:00Z").toISOString(),
    },
    last_run: {
      id: "run-001",
      status: "succeeded",
      trigger: "schedule",
      version: 2,
      fire_time: new Date("2026-08-14T07:00:00Z").toISOString(),
      finished_at: new Date("2026-08-14T07:00:08Z").toISOString(),
      duration_ms: 8_420,
      output_count: 1,
    },
    owned: true,
    ...overrides,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  mockScripts.mockReturnValue(query({ data: [row()], total: 1 }));
  mockRuns.mockReturnValue(query({ data: [], total: 0, limit: 50 }));
});

afterEach(cleanup);

describe("MyScriptsPage", () => {
  it("reports what a script is executing, its cadence, and how it last ran", () => {
    render(<MyScriptsPage onNavigate={onNavigate} />);

    expect(screen.getByText("Daily Sales Report")).toBeInTheDocument();
    expect(screen.getByText("Runs v2")).toBeInTheDocument();
    expect(screen.getByText("succeeded")).toBeInTheDocument();
  });

  // #1358: the column an owner scans to answer "what is running and when" said
  // "0 7 * * 1-5", while the editor two clicks away said the same cadence in
  // words. The sentence is the one the editor states.
  it("states a cadence in the words the schedule editor states it in", () => {
    render(<MyScriptsPage onNavigate={onNavigate} />);
    expect(
      screen.getByText("Every weekday at 7:00 AM, America/Los_Angeles"),
    ).toBeInTheDocument();
    expect(screen.queryByText("0 7 * * 1-5")).not.toBeInTheDocument();
  });

  // #1405: this column is in words whatever the expression is. A step cadence
  // is the shape an agent writes and the builder has no control for.
  it("states a step cadence in words", () => {
    mockScripts.mockReturnValue(
      query({
        data: [row({ schedule: { ...row().schedule!, cron_spec: "*/30 * * * *" } })],
        total: 1,
      }),
    );
    render(<MyScriptsPage onNavigate={onNavigate} />);
    expect(
      screen.getByText("Every 30 minutes, America/Los_Angeles"),
    ).toBeInTheDocument();
  });

  // An expression this page cannot state is named as one, not printed. A cron
  // expression belongs in the schedule editor, where somebody is editing one.
  it("never shows a cron expression, whatever the cadence is", () => {
    mockScripts.mockReturnValue(
      query({
        data: [row({ schedule: { ...row().schedule!, cron_spec: "*/7 3 * 2 1-5" } })],
        total: 1,
      }),
    );
    render(<MyScriptsPage onNavigate={onNavigate} />);
    expect(screen.getByText("Custom cadence, America/Los_Angeles")).toBeInTheDocument();
    expect(screen.queryByText("*/7 3 * 2 1-5")).not.toBeInTheDocument();
  });

  it("says an enabled schedule with nothing due is not overdue", () => {
    mockScripts.mockReturnValue(
      query({
        data: [row({ schedule: { ...row().schedule!, next_run_at: undefined } })],
        total: 1,
      }),
    );
    render(<MyScriptsPage onNavigate={onNavigate} />);
    expect(screen.getByText("No fire due")).toBeInTheDocument();
  });

  it("says a disabled script runs nothing, whatever else is true of it", () => {
    mockScripts.mockReturnValue(
      query({
        data: [row({ script: { ...row().script, enabled: false } })],
        total: 1,
      }),
    );
    render(<MyScriptsPage onNavigate={onNavigate} />);
    expect(screen.getByText("disabled")).toBeInTheDocument();
    expect(screen.queryByText("Runs v2")).not.toBeInTheDocument();
  });

  it("reports a paused schedule rather than a next fire that will not happen", () => {
    mockScripts.mockReturnValue(
      query({ data: [row({ schedule: { ...row().schedule!, enabled: false } })], total: 1 }),
    );
    render(<MyScriptsPage onNavigate={onNavigate} />);
    expect(screen.getByText("Paused")).toBeInTheDocument();
  });

  it("says a script with no schedule runs on demand", () => {
    mockScripts.mockReturnValue(query({ data: [row({ schedule: undefined })], total: 1 }));
    render(<MyScriptsPage onNavigate={onNavigate} />);
    expect(screen.getByText("On demand")).toBeInTheDocument();
  });

  it("distinguishes a script that has never run from one whose runs are not the caller's", () => {
    mockScripts.mockReturnValue(
      query({
        data: [
          row({ last_run: undefined }),
          row({
            script: { ...row().script, id: "script-002", display_name: "Someone Else's" },
            owned: false,
            last_run: undefined,
          }),
        ],
        total: 2,
      }),
    );
    render(<MyScriptsPage onNavigate={onNavigate} />);
    expect(screen.getByText("Never run")).toBeInTheDocument();
    expect(screen.getAllByText("—").length).toBeGreaterThan(0);
  });

  // #1405: three tiles, each of them the control that shows what it counted,
  // and each named plainly enough to need no sentence explaining its own title.
  it("counts the listing in three tiles that need no caption", () => {
    render(<MyScriptsPage onNavigate={onNavigate} />);
    expect(screen.getByRole("button", { name: /^Scripts/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /^Scheduled/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /^Failing/ })).toBeInTheDocument();
    expect(screen.queryByText(/Automation/i)).not.toBeInTheDocument();
    expect(screen.queryByText("scripts you own")).not.toBeInTheDocument();
  });

  // The row is the target, as it is on every other listing in the portal.
  it("opens a script's detail page from the row", () => {
    render(<MyScriptsPage onNavigate={onNavigate} />);
    fireEvent.click(screen.getByRole("row", { name: /Daily Sales Report/ }));
    expect(onNavigate).toHaveBeenCalledWith("/scripts/script-001");
  });

  it("says plainly when there are no scripts at all", () => {
    mockScripts.mockReturnValue(query({ data: [], total: 0 }));
    render(<MyScriptsPage onNavigate={onNavigate} />);
    expect(screen.getByText(/You have no scripts yet/)).toBeInTheDocument();
  });

  // Nothing to narrow is nothing to narrow with: a search box over an empty
  // listing is a control that cannot do anything.
  it("offers no filter bar to a caller who owns no scripts", () => {
    mockScripts.mockReturnValue(query({ data: [], total: 0 }));
    render(<MyScriptsPage onNavigate={onNavigate} />);
    expect(screen.queryByLabelText("Search scripts")).not.toBeInTheDocument();
  });

  // A search that matched nothing keeps the control that clears it.
  it("keeps the filter bar when a narrowed listing came back empty", () => {
    mockScripts.mockImplementation((filter) =>
      filter?.search ? query({ data: [], total: 0 }) : query({ data: [row()], total: 1 }),
    );
    render(<MyScriptsPage onNavigate={onNavigate} />);
    fireEvent.change(screen.getByLabelText("Search scripts"), { target: { value: "nothing" } });
    expect(screen.getByLabelText("Search scripts")).toBeInTheDocument();
  });

  it("reports a listing that could not be loaded instead of showing it as empty", () => {
    mockScripts.mockReturnValue(query(undefined, { error: new Error("boom") }));
    render(<MyScriptsPage onNavigate={onNavigate} />);
    expect(screen.getByText(/scripts could not be loaded/)).toBeInTheDocument();
  });

  it("shows a loading state rather than an empty one while the listing is in flight", () => {
    mockScripts.mockReturnValue(query(undefined, { isLoading: true }));
    render(<MyScriptsPage onNavigate={onNavigate} />);
    expect(screen.getByText("Loading scripts...")).toBeInTheDocument();
  });
});

// The two axes a script is filed on (#1369) and the free text over the rest
// (#1405). All three were stored and searched on long before any of them was
// shown anywhere, so a listing that could not narrow by them was a flat list.
describe("MyScriptsPage: filing and filtering", () => {
  const sales = row();
  const margins = row({
    script: {
      ...row().script,
      id: "script-004",
      name: "my-margin-check",
      display_name: "My Margin Check",
      category: "finance",
      tags: ["margins"],
    },
  });

  beforeEach(() => {
    sales.script.category = "reporting";
    sales.script.tags = ["sales", "weekly"];
    mockScripts.mockReturnValue(query({ data: [sales, margins], total: 2 }));
  });

  it("shows how each script is filed, on its row", () => {
    render(<MyScriptsPage onNavigate={onNavigate} />);
    const salesRow = screen.getByRole("row", { name: /Daily Sales Report/ });
    expect(salesRow).toHaveTextContent("reporting");
    expect(salesRow).toHaveTextContent("sales");
    expect(salesRow).toHaveTextContent("weekly");
  });

  it("offers a chip per category and tag, counted over the listing", () => {
    render(<MyScriptsPage onNavigate={onNavigate} />);
    expect(screen.getByRole("button", { name: /reporting/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /finance/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /margins/ })).toBeInTheDocument();
  });

  // The narrowing is the SERVER's, not the table's: the listing is capped, so a
  // page filtering the rows it received would answer from a truncated set.
  it("asks the server for the narrowed listing", () => {
    render(<MyScriptsPage onNavigate={onNavigate} />);
    fireEvent.click(screen.getByRole("button", { name: /reporting/ }));
    expect(mockScripts).toHaveBeenCalledWith({ category: "reporting" });
  });

  it("clears an axis when its active chip is pressed again", () => {
    render(<MyScriptsPage onNavigate={onNavigate} />);
    fireEvent.click(screen.getByRole("button", { name: /reporting/ }));
    fireEvent.click(screen.getByRole("button", { name: /reporting/ }));

    // The unfiltered facet-vocabulary read passes no argument at all, so the
    // filter under test is the last call that carried one. A cleared axis is
    // absent rather than undefined, which is what keeps the two reads on one
    // query key when nothing is filtered.
    const filters = mockScripts.mock.calls.map(([f]) => f).filter((f) => f !== undefined);
    expect(filters[filters.length - 1]).toEqual({});
  });

  // The chips are read from the UNFILTERED listing, so narrowing to one
  // category does not delete every other chip and strand the reader there.
  it("keeps every chip on screen while a filter is applied", () => {
    render(<MyScriptsPage onNavigate={onNavigate} />);
    fireEvent.click(screen.getByRole("button", { name: /reporting/ }));
    expect(screen.getByRole("button", { name: /finance/ })).toBeInTheDocument();
  });

  it("distinguishes a filter that matched nothing from having no scripts", () => {
    render(<MyScriptsPage onNavigate={onNavigate} />);
    // The filtered query answers empty; the unfiltered one still has the rows
    // the chips are built from, which is what the second mock return models.
    mockScripts.mockImplementation((filter) =>
      filter?.category ? query({ data: [], total: 0 }) : query({ data: [sales, margins], total: 2 }),
    );
    fireEvent.click(screen.getByRole("button", { name: /reporting/ }));

    expect(screen.getByText(/No script you can see matches that/)).toBeInTheDocument();
    expect(screen.queryByText(/You have no scripts yet/)).not.toBeInTheDocument();
  });

  // A script nobody has filed carries no chips, and the search stays: free
  // text is how a listing is narrowed before anybody has filed anything.
  it("offers the search with no chips when nothing is filed", () => {
    mockScripts.mockReturnValue(query({ data: [row()], total: 1 }));
    render(<MyScriptsPage onNavigate={onNavigate} />);
    expect(screen.getByLabelText("Search scripts")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^All$/ })).not.toBeInTheDocument();
    expect(screen.queryByText("Tag")).not.toBeInTheDocument();
  });
});

// The tiles are the page's own filters (#1405): the number an owner opens this
// page for — the reports that failed this morning — used to be a red badge
// they had to find by reading the table row by row.
describe("MyScriptsPage: the tiles filter", () => {
  const failing = row({
    script: { ...row().script, id: "script-005", display_name: "Broken Report" },
    last_run: { ...row().last_run!, status: "failed" },
  });
  const onDemand = row({
    script: { ...row().script, id: "script-006", display_name: "On Demand Report" },
    schedule: undefined,
  });

  beforeEach(() => {
    mockScripts.mockReturnValue(query({ data: [row(), failing, onDemand], total: 3 }));
    mockRuns.mockReturnValue(query({ data: [], total: 0, limit: 50 }));
  });

  it("counts the scripts, the scheduled ones, and the ones whose last run failed", () => {
    render(<MyScriptsPage onNavigate={onNavigate} />);
    expect(screen.getByRole("button", { name: "Scripts 3" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Scheduled 2" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Failing 1" })).toBeInTheDocument();
  });

  it("shows the scripts a tile counted when the tile is pressed", () => {
    render(<MyScriptsPage onNavigate={onNavigate} />);
    fireEvent.click(screen.getByRole("button", { name: /^Failing/ }));

    expect(screen.getByRole("button", { name: /^Failing/ })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(screen.getByText("Broken Report")).toBeInTheDocument();
    expect(screen.queryByText("On Demand Report")).not.toBeInTheDocument();
  });

  // A paused schedule is still a schedule. Counting only the firing ones would
  // report "Scheduled 0" over a row that states a cadence.
  it("counts a paused schedule as scheduled", () => {
    const paused = row({
      script: { ...row().script, id: "script-007", display_name: "Paused Report" },
      schedule: { ...row().schedule!, enabled: false },
    });
    mockScripts.mockReturnValue(query({ data: [paused, onDemand], total: 2 }));
    render(<MyScriptsPage onNavigate={onNavigate} />);

    expect(screen.getByRole("button", { name: "Scheduled 1" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /^Scheduled/ }));
    expect(screen.getByText("Paused Report")).toBeInTheDocument();
    expect(screen.queryByText("On Demand Report")).not.toBeInTheDocument();
  });

  it("clears the filter when the pressed tile is pressed again", () => {
    render(<MyScriptsPage onNavigate={onNavigate} />);
    fireEvent.click(screen.getByRole("button", { name: /^Failing/ }));
    fireEvent.click(screen.getByRole("button", { name: /^Failing/ }));
    expect(screen.getByText("On Demand Report")).toBeInTheDocument();
  });

  // "Scripts" is the whole listing, so pressing it is how a reader clears one
  // of the other two — and it is what reads as pressed while nothing is.
  it("clears the filter from the Scripts tile", () => {
    render(<MyScriptsPage onNavigate={onNavigate} />);
    expect(screen.getByRole("button", { name: /^Scripts/ })).toHaveAttribute(
      "aria-pressed",
      "true",
    );

    fireEvent.click(screen.getByRole("button", { name: /^Scheduled/ }));
    expect(screen.queryByText("On Demand Report")).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /^Scripts/ }));
    expect(screen.getByText("On Demand Report")).toBeInTheDocument();
  });

  // A tile counts what the server answered, so a tile and a chip pressed
  // together agree with the table under them rather than with the whole corpus.
  it("says plainly when a tile matched nothing", () => {
    mockScripts.mockReturnValue(query({ data: [onDemand], total: 1 }));
    render(<MyScriptsPage onNavigate={onNavigate} />);
    fireEvent.click(screen.getByRole("button", { name: /^Failing/ }));

    expect(screen.getByText(/No script you can see matches that/)).toBeInTheDocument();
    expect(screen.queryByText(/You have no scripts yet/)).not.toBeInTheDocument();
  });
});

// The search is the SERVER's, for the same reason the chips are: the listing is
// capped, so a page searching the rows it received would answer from a
// truncated set and report a count to match.
describe("MyScriptsPage: the search", () => {
  it("asks the server for the scripts matching what was typed", async () => {
    render(<MyScriptsPage onNavigate={onNavigate} />);
    fireEvent.change(screen.getByLabelText("Search scripts"), { target: { value: "sales" } });

    await waitFor(() => {
      const filters = mockScripts.mock.calls.map(([f]) => f).filter((f) => f !== undefined);
      expect(filters[filters.length - 1]).toMatchObject({ search: "sales" });
    });
  });

  // A cleared box is an unfiltered listing, on the same query key the facet
  // vocabulary is already read under, rather than a search for nothing.
  it("carries no search once the box is cleared", async () => {
    render(<MyScriptsPage onNavigate={onNavigate} />);
    const box = screen.getByLabelText("Search scripts");
    fireEvent.change(box, { target: { value: "sales" } });
    fireEvent.change(box, { target: { value: "  " } });

    await waitFor(() => {
      const filters = mockScripts.mock.calls.map(([f]) => f).filter((f) => f !== undefined);
      expect(filters[filters.length - 1]).toEqual({});
    });
  });
});

// The Runs tab (#1405): how are my scripts going, all of them.
describe("MyScriptsPage: the Runs tab", () => {
  it("shows the caller's runs across every script, with the reason each failed", () => {
    mockRuns.mockReturnValue(
      query({
        data: [
          {
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
          },
        ],
        total: 1,
        limit: 50,
      }),
    );
    render(<MyScriptsPage onNavigate={onNavigate} />);
    fireEvent.mouseDown(screen.getByRole("tab", { name: "Runs" }));

    expect(screen.getByText("trino: table not found: sales.daily")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("row", { name: /Daily Sales Report/ }));
    expect(onNavigate).toHaveBeenCalledWith("/scripts/script-001/runs/run-042");
  });
});
