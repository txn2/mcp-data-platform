import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup, waitFor } from "@testing-library/react";
import type { PortalScriptRow } from "@/api/portal/hooks/scripts";
import { ScriptListing } from "./ScriptListing";

vi.mock("@/api/portal/hooks/scripts", () => ({
  useScriptListing: vi.fn(),
}));

import { useScriptListing } from "@/api/portal/hooks/scripts";

const mockScripts = vi.mocked(useScriptListing);
const onNavigate = vi.fn();

// answer is a listing response. The three counts are the server's: total and
// scheduled are counted over the predicate, failing over the page (#1795), so
// a test that wants them says so rather than having them derived from rows.
function answer(
  rows: PortalScriptRow[],
  counts: Partial<{ total: number; scheduled: number; failing: number }> = {},
) {
  return {
    data: {
      data: rows,
      total: counts.total ?? rows.length,
      scheduled: counts.scheduled ?? rows.filter((r) => r.schedule).length,
      failing: counts.failing ?? rows.filter((r) => r.last_run?.status === "failed").length,
    },
    isLoading: false,
    error: null,
  } as never;
}

function loading() {
  return { data: undefined, isLoading: true, error: null } as never;
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

// list renders the listing as the person who owns the scripts reads it, which
// is what most of these assertions are about.
function list() {
  return render(<ScriptListing audience="owner" basePath="/scripts" onNavigate={onNavigate} />);
}

// The listing calls the hook TWICE per render: once for the rows and once,
// narrowed by scope alone, for the facet vocabulary. Only the first carries an
// ordering, which is what separates them here -- reading "the last call" would
// read the vocabulary query and assert nothing about the listing.
function lastFilter(): Record<string, unknown> {
  const listings = filters().filter((f) => "sort" in f);
  return listings[listings.length - 1] ?? {};
}

// A Radix tab activates on mousedown, not on click: the scope tabs are a
// `ui/tabs` list, so a plain click leaves the listing on the scope it opened
// with and asserts nothing.
function chooseScope(label: string) {
  fireEvent.mouseDown(screen.getByRole("tab", { name: label }));
}

/** The filters the listing has asked for, newest last. */
function filters(): Record<string, unknown>[] {
  return mockScripts.mock.calls.map(([f]) => (f ?? {}) as Record<string, unknown>);
}

beforeEach(() => {
  mockScripts.mockReset();
  onNavigate.mockReset();
  // globalThis.localStorage rather than the bare global: ScopeFilter reads it
  // through globalThis and guards it, and this file runs in environments where
  // the bare identifier is not defined.
  globalThis.localStorage?.clear();
});

afterEach(cleanup);

describe("ScriptListing", () => {
  it("lists a script with its name, author, cadence and last run", () => {
    mockScripts.mockReturnValue(answer([row()]));
    list();

    expect(screen.getByText("Daily Sales Report")).toBeInTheDocument();
    expect(screen.getByText("daily-sales-report")).toBeInTheDocument();
    expect(screen.getByText(/sarah\.chen/)).toBeInTheDocument();
    // The cadence reads in words rather than as a cron expression (#1405).
    expect(screen.queryByText("0 7 * * 1-5")).not.toBeInTheDocument();
  });

  it("shows a loading state rather than an empty one while the listing is in flight", () => {
    mockScripts.mockReturnValue(loading());
    list();

    expect(screen.getByText("Loading...")).toBeInTheDocument();
    expect(screen.queryByText(/You have no scripts yet/)).not.toBeInTheDocument();
  });

  it("opens the script when its row is clicked", () => {
    mockScripts.mockReturnValue(answer([row()]));
    list();

    fireEvent.click(screen.getByText("Daily Sales Report"));
    expect(onNavigate).toHaveBeenCalledWith("/scripts/script-001");
  });
});

// The health line replaces three bordered tiles (#1795). The counts are the
// server's, and only the failed one is a control.
describe("ScriptListing: the health line", () => {
  it("states the totals the server counted, not the rows on screen", () => {
    mockScripts.mockReturnValue(
      answer([row()], { total: 240, scheduled: 96, failing: 0 }),
    );
    list();

    expect(screen.getByTestId("script-health-total")).toHaveTextContent("240 scripts");
    expect(screen.getByText(/96 scheduled/)).toBeInTheDocument();
  });

  it("says so when the page is not the whole answer", () => {
    mockScripts.mockReturnValue(answer([row()], { total: 240 }));
    list();

    expect(screen.getByText(/showing 1/)).toBeInTheDocument();
  });

  it("says nothing about a page that is the whole answer", () => {
    mockScripts.mockReturnValue(answer([row()], { total: 1 }));
    list();

    expect(screen.queryByText(/showing/)).not.toBeInTheDocument();
  });

  it("offers the failed count as the one control on the line", () => {
    const failed = row({
      script: { ...row().script, id: "script-002", display_name: "Churn Watch" },
      last_run: { ...row().last_run!, status: "failed" },
    });
    mockScripts.mockReturnValue(answer([row(), failed]));
    list();

    const control = screen.getByTestId("script-health-failing");
    expect(control).toHaveTextContent("1 failed its last run");
    expect(control).toHaveAttribute("aria-pressed", "false");

    fireEvent.click(control);
    expect(control).toHaveAttribute("aria-pressed", "true");
    // Pressed, the listing is the scripts it counted.
    expect(screen.getByText("Churn Watch")).toBeInTheDocument();
    expect(screen.queryByText("Daily Sales Report")).not.toBeInTheDocument();

    fireEvent.click(control);
    expect(screen.getByText("Daily Sales Report")).toBeInTheDocument();
  });

  it("offers nothing to press when nothing failed", () => {
    mockScripts.mockReturnValue(answer([row()]));
    list();

    expect(screen.queryByTestId("script-health-failing")).not.toBeInTheDocument();
  });
});

// Ordering is the server's, which is the whole point: sorting in the browser
// would order the page the cap returned (#1795).
describe("ScriptListing: ordering", () => {
  it("opens on most recently updated, descending", () => {
    mockScripts.mockReturnValue(answer([row()]));
    list();

    expect(lastFilter()).toMatchObject({ sort: "updated_at", dir: "desc" });
  });

  it("asks the server for the column whose header was clicked", async () => {
    mockScripts.mockReturnValue(answer([row()]));
    list();

    fireEvent.click(screen.getByText("Script"));
    await waitFor(() => {
      // A text column reads A-Z when it first becomes the sorted one.
      expect(lastFilter()).toMatchObject({ sort: "display_name", dir: "asc" });
    });
  });

  it("reverses the column when its header is clicked again", async () => {
    mockScripts.mockReturnValue(answer([row()]));
    list();

    fireEvent.click(screen.getByText("Author"));
    await waitFor(() => expect(lastFilter()).toMatchObject({ sort: "owner_email", dir: "asc" }));

    fireEvent.click(screen.getByText("Author"));
    await waitFor(() => expect(lastFilter()).toMatchObject({ sort: "owner_email", dir: "desc" }));
  });

  it("offers no ordering on Last run, which is attached per page", () => {
    mockScripts.mockReturnValue(answer([row()]));
    list();

    fireEvent.click(screen.getByText("Last run"));
    expect(lastFilter()).toMatchObject({ sort: "updated_at" });
  });
});

describe("ScriptListing: the filter bar", () => {
  const corpus = [
    row(),
    row({
      script: {
        ...row().script,
        id: "script-002",
        name: "churn-watch",
        display_name: "Churn Watch",
        category: "ops",
        tags: ["churn"],
        owner_email: "marcus.webb@example.com",
      },
    }),
  ];

  it("renders one row of controls and no chip cloud", () => {
    mockScripts.mockReturnValue(answer(corpus));
    list();

    expect(screen.getByLabelText("Filter by author")).toBeInTheDocument();
    expect(screen.getByLabelText("Filter by category")).toBeInTheDocument();
    expect(screen.getByLabelText("Filter by tag")).toBeInTheDocument();
    expect(screen.getByLabelText("Filter by status")).toBeInTheDocument();
    // The tag vocabulary is reachable only through its facet: no chip per tag.
    expect(screen.queryByRole("button", { name: /^churn/ })).not.toBeInTheDocument();
  });

  it("asks the server for what was typed", async () => {
    mockScripts.mockReturnValue(answer(corpus));
    list();

    fireEvent.change(screen.getByLabelText("Search scripts"), {
      target: { value: "sales" },
    });
    await waitFor(() => {
      expect(filters().some((f) => f["search"] === "sales")).toBe(true);
    });
  });

  it("carries no search once the box is cleared", async () => {
    mockScripts.mockReturnValue(answer(corpus));
    list();

    const box = screen.getByLabelText("Search scripts");
    fireEvent.change(box, { target: { value: "sales" } });
    await waitFor(() => expect(filters().some((f) => f["search"] === "sales")).toBe(true));

    fireEvent.change(box, { target: { value: "" } });
    await waitFor(() => expect(lastFilter()["search"]).toBeUndefined());
  });

  it("distinguishes a filter that matched nothing from having no scripts", async () => {
    mockScripts.mockImplementation((filter) => {
      const narrowed = Boolean(filter && "search" in filter && filter.search);
      return answer(narrowed ? [] : corpus);
    });
    list();

    fireEvent.change(screen.getByLabelText("Search scripts"), {
      target: { value: "nothing-matches-this" },
    });
    await waitFor(() => {
      expect(screen.getByText(/No script you can see matches that/)).toBeInTheDocument();
    });
  });
});

// A script is visible to everyone; what is readable is not (#1795).
describe("ScriptListing: scope", () => {
  it("opens on the caller's own scripts", () => {
    mockScripts.mockReturnValue(answer([row()]));
    list();

    expect(lastFilter()).toMatchObject({ scope: "mine" });
  });

  it("lists every script when the reader asks for all", async () => {
    mockScripts.mockReturnValue(answer([row()]));
    list();

    chooseScope("All");
    await waitFor(() => expect(lastFilter()["scope"]).toBe("all"));
  });

  // Persistence is asserted in the browser (e2e/interactive/scripts-listing
  // .spec.ts), not here: this jsdom environment provides no localStorage at
  // all, so a test of it would pass on a component that stored nothing.

  it("shows a row the reader does not own without its run state", () => {
    const theirs = row({
      script: { ...row().script, id: "script-003", display_name: "Someone Else's" },
      owned: false,
      last_run: undefined,
    });
    mockScripts.mockReturnValue(answer([theirs]));
    list();

    expect(screen.getByText("Someone Else's")).toBeInTheDocument();
    expect(screen.getByText("—")).toBeInTheDocument();
  });
});

describe("ScriptListing: a row", () => {
  it("states its category once, beside the name, and carries no tag badges", () => {
    const filed = row({
      script: {
        ...row().script,
        category: "reports",
        tags: ["finance", "weekly", "board"],
      },
    });
    mockScripts.mockReturnValue(answer([filed]));
    list();

    expect(screen.getByText("reports")).toBeInTheDocument();
    for (const tag of ["finance", "weekly", "board"]) {
      expect(screen.queryByText(tag)).not.toBeInTheDocument();
    }
  });

  it("marks a script that will execute nothing", () => {
    const off = row({ script: { ...row().script, enabled: false } });
    mockScripts.mockReturnValue(answer([off]));
    list();

    expect(screen.getByText("disabled")).toBeInTheDocument();
  });
});

describe("ScriptListing: the administrator's reading", () => {
  it("names who each script belongs to, and offers no scope tabs", () => {
    mockScripts.mockReturnValue(answer([row()]));
    render(
      <ScriptListing audience="admin" basePath="/admin/scripts" onNavigate={onNavigate} />,
    );

    expect(screen.getByText(/sarah\.chen/)).toBeInTheDocument();
    // An administrator's listing is every script by definition.
    expect(screen.queryByRole("tab", { name: "Mine" })).not.toBeInTheDocument();
  });
});
