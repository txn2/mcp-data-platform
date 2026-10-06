import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup, waitFor, within } from "@testing-library/react";
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
  return render(<ScriptListing audience="owner" basePath="/automations" onNavigate={onNavigate} />);
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

  // #1912: the section lists automations, and each row says which kind it is
  // so a second kind has a place to appear. Every automation is a script today.
  it("states each automation's kind as Script", () => {
    mockScripts.mockReturnValue(answer([row()]));
    list();

    expect(screen.getByRole("columnheader", { name: "Kind" })).toBeInTheDocument();
    expect(screen.getByTestId("automation-kind")).toHaveTextContent("Script");
  });

  it("says no automations exist when every script is listed and there are none", () => {
    mockScripts.mockReturnValue(answer([]));
    list();

    expect(screen.getByText(/No automations exist yet/)).toBeInTheDocument();
  });

  it("tells a reader with none of their own where everyone's are, and to ask an agent", async () => {
    mockScripts.mockImplementation((f) => (f?.scope === "mine" ? answer([]) : answer([row({ owned: false })])));
    list();

    chooseScope("Mine");
    expect(await screen.findByText(/You have no automations of your own yet/)).toBeInTheDocument();
    expect(screen.getByText(/Choose All to see everyone's/)).toBeInTheDocument();
    expect(
      screen.getByText(/Ask an agent to automate a report or an export you run repeatedly/),
    ).toBeInTheDocument();
  });

  it("shows a loading state rather than an empty one while the listing is in flight", () => {
    mockScripts.mockReturnValue(loading());
    list();

    expect(screen.getByText("Loading...")).toBeInTheDocument();
    expect(screen.queryByText(/No automations exist yet/)).not.toBeInTheDocument();
  });

  it("opens the script when its row is clicked", () => {
    mockScripts.mockReturnValue(answer([row()]));
    list();

    fireEvent.click(screen.getByText("Daily Sales Report"));
    expect(onNavigate).toHaveBeenCalledWith("/automations/script-001");
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

    expect(screen.getByTestId("script-health-total")).toHaveTextContent("240 automations");
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

    fireEvent.click(screen.getByText("Automation"));
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

    fireEvent.change(screen.getByLabelText("Search automations"), {
      target: { value: "sales" },
    });
    await waitFor(() => {
      expect(filters().some((f) => f["search"] === "sales")).toBe(true);
    });
  });

  it("carries no search once the box is cleared", async () => {
    mockScripts.mockReturnValue(answer(corpus));
    list();

    const box = screen.getByLabelText("Search automations");
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

    fireEvent.change(screen.getByLabelText("Search automations"), {
      target: { value: "nothing-matches-this" },
    });
    await waitFor(() => {
      expect(screen.getByText(/No automation you can see matches that/)).toBeInTheDocument();
    });
  });
});

// A script is visible to everyone, and so is how it is going (#1795, #1994).
describe("ScriptListing: scope", () => {
  it("opens on every script, the ones built for the reader included", () => {
    mockScripts.mockReturnValue(answer([row()]));
    list();

    expect(lastFilter()).toMatchObject({ scope: "all" });
  });

  it("narrows to the reader's own when they ask for mine", async () => {
    mockScripts.mockReturnValue(answer([row()]));
    list();

    chooseScope("Mine");
    await waitFor(() => expect(lastFilter()["scope"]).toBe("mine"));
  });

  // Persistence is asserted in the browser (e2e/interactive/scripts-listing
  // .spec.ts), where the store outlives a reload; each test here opens on an
  // empty one (src/test/setup.ts).

  it("shows how a script the reader does not own last ran", () => {
    const theirs = row({
      script: { ...row().script, id: "script-003", display_name: "Someone Else's" },
      owned: false,
      last_run: { ...row().last_run!, status: "failed" },
    });
    mockScripts.mockReturnValue(answer([theirs]));
    list();

    expect(screen.getByText("Someone Else's")).toBeInTheDocument();
    expect(screen.getByText("failed")).toBeInTheDocument();
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
      <ScriptListing audience="admin" basePath="/admin/automations" onNavigate={onNavigate} />,
    );

    expect(screen.getByText(/sarah\.chen/)).toBeInTheDocument();
    // An administrator's listing is every script by definition.
    expect(screen.queryByRole("tab", { name: "Mine" })).not.toBeInTheDocument();
  });
});

// #1909: the listing offers a grid whose tiles are each script's flow diagram,
// beside the list it has always been, and remembers the choice.
describe("ScriptListing: grid and list", () => {
  it("opens as the list, switches to the grid, and keeps the choice across a reload", () => {
    const store = new Map<string, string>();
    vi.stubGlobal("localStorage", {
      getItem: (k: string) => store.get(k) ?? null,
      setItem: (k: string, v: string) => void store.set(k, v),
      clear: () => store.clear(),
    });
    mockScripts.mockReturnValue(answer([row()]));
    list();
    expect(screen.getByRole("columnheader", { name: "Kind" })).toBeInTheDocument();
    expect(screen.queryByTestId("script-grid")).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Grid view" }));
    const card = screen.getByTestId("script-card-script-001");
    expect(card).toHaveTextContent("Daily Sales Report");
    expect(card).toHaveTextContent("Every weekday");
    expect(document.querySelector('img[src*="/api/v1/portal/scripts/script-001/thumbnail?v=2"]')).not.toBeNull();

    cleanup();
    list();
    expect(screen.getByTestId("script-grid")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /Daily Sales Report/ }));
    expect(onNavigate).toHaveBeenCalledWith(expect.stringContaining("/script-001"));

    fireEvent.click(screen.getByRole("button", { name: "Table view" }));
    cleanup();
    list();
    expect(screen.queryByTestId("script-grid")).not.toBeInTheDocument();
    vi.unstubAllGlobals();
  });
});

// A library (#1941) is code other scripts load and is never run or scheduled,
// so the listing marks it and lets a reader narrow to either kind.
describe("ScriptListing: libraries", () => {
  function library(): PortalScriptRow {
    return row({
      script: {
        ...row().script,
        id: "script-006",
        name: "date-windows",
        display_name: "Date Windows",
        library: true,
        loads: [],
      },
      schedule: undefined,
      last_run: undefined,
    });
  }

  // listingFilter is the filter the rows were read under: each render asks
  // for the rows first and the facet vocabulary second, and once anything is
  // narrowed the vocabulary's filter carries an ordering too, so lastFilter
  // cannot tell them apart.
  function listingFilter(): Record<string, unknown> {
    const all = filters();
    return all[all.length - 2] ?? {};
  }

  // chooseKind opens the kind listbox from the keyboard (jsdom has no
  // PointerEvent, see ui/README.md) and picks an option.
  function chooseKind(option: string) {
    fireEvent.keyDown(screen.getByLabelText("Filter by kind"), { key: "Enter" });
    fireEvent.click(screen.getByRole("option", { name: option }));
  }

  it("badges a library and no other row", () => {
    mockScripts.mockReturnValue(answer([row(), library()]));
    list();

    const lib = screen.getByTestId("script-row-script-006");
    // The badge beside the name, and the Kind column that tells kinds apart.
    expect(within(lib).getAllByText("Library")).toHaveLength(2);
    expect(within(lib).getByTestId("automation-kind")).toHaveTextContent("Library");
    const automation = screen.getByTestId("script-row-script-001");
    expect(within(automation).queryByText("Library")).not.toBeInTheDocument();
    expect(within(automation).getByTestId("automation-kind")).toHaveTextContent("Script");
  });

  it("promises a library neither a schedule nor a run", () => {
    mockScripts.mockReturnValue(answer([library()]));
    list();

    const lib = screen.getByTestId("script-row-script-006");
    expect(within(lib).queryByText("On demand")).not.toBeInTheDocument();
    expect(within(lib).queryByText("Never run")).not.toBeInTheDocument();
  });

  it("badges a library's card in the grid", () => {
    mockScripts.mockReturnValue(answer([library()]));
    list();

    fireEvent.click(screen.getByRole("button", { name: "Grid view" }));
    expect(screen.getByTestId("script-card-script-006")).toHaveTextContent("Library");
    fireEvent.click(screen.getByRole("button", { name: "Table view" }));
  });

  it("offers All, Scripts and Libraries, and asks for no kind until one is chosen", () => {
    mockScripts.mockReturnValue(answer([row(), library()]));
    list();

    expect(lastFilter()).not.toHaveProperty("kind");
    fireEvent.keyDown(screen.getByLabelText("Filter by kind"), { key: "Enter" });
    expect(screen.getAllByRole("option").map((o) => o.textContent)).toEqual([
      "All",
      "Scripts",
      "Libraries",
    ]);
  });

  it("asks the server for the kind chosen, and for none once All is chosen again", () => {
    mockScripts.mockReturnValue(answer([row(), library()]));
    list();

    chooseKind("Libraries");
    expect(listingFilter()["kind"]).toBe("library");
    // The vocabulary is read without it, so switching back stays possible.
    expect(filters()[filters().length - 1]).not.toHaveProperty("kind");

    chooseKind("Scripts");
    expect(listingFilter()["kind"]).toBe("script");

    chooseKind("All");
    expect(listingFilter()).not.toHaveProperty("kind");
  });

  it("reads a kind that matched nothing as a narrowed listing, not an empty one", () => {
    mockScripts.mockImplementation((filter) =>
      (filter as Record<string, unknown> | undefined)?.["kind"] === "library"
        ? answer([])
        : answer([row()]),
    );
    list();

    chooseKind("Libraries");
    expect(screen.getByText(/No automation you can see matches that/)).toBeInTheDocument();
  });
});
