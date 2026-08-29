import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import type { ScratchTable } from "@/api/tables/types";
import {
  useScratchTable,
  useScratchTables,
  useUnregisterTable,
  TableApiError,
} from "@/api/tables/hooks";
import { ScratchTablesTable } from "./ScratchTablesTable";
import { ScratchTableDetailPage } from "./ScratchTableDetailPage";
import { ScratchTablesPage, connectionOptions } from "./ScratchTablesPage";
import { sourceKindLabel, sourcePath } from "./source";

// The detail page composes two hooks over real components, so every assertion
// below is what a reader actually sees on the page.
vi.mock("@/api/tables/hooks", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/api/tables/hooks")>()),
  useScratchTables: vi.fn(),
  useScratchTable: vi.fn(),
  useUnregisterTable: vi.fn(),
}));

// The Scratch Tables section (#1472). What these hold is that the listing
// answers the three questions no per-source panel could -- which tables exist,
// which file each is over, and whether it is still reading that file's current
// contents -- and that the action to drop one is offered exactly where the
// route behind it would accept it.

function row(overrides: Partial<ScratchTable> = {}): ScratchTable {
  return {
    id: "reg_1",
    source_kind: "asset",
    source_id: "ast-008",
    connection: "acme-scratch",
    catalog: "scratch",
    schema: "uploads",
    table: "analyst_regional_sales",
    location: "s3://portal-assets/assets/ast-008/",
    columns: [
      { name: "region", type: "VARCHAR" },
      { name: "revenue", type: "VARCHAR" },
    ],
    registered_by: "alice@example.com",
    registered_at: "2026-08-20T14:12:00Z",
    query_table: "scratch.uploads.analyst_regional_sales",
    sample_sql: "SELECT * FROM scratch.uploads.analyst_regional_sales",
    stale: false,
    follow: true,
    source: { kind: "asset", id: "ast-008", name: "Regional sales summary", missing: false },
    can_unregister: true,
    ...overrides,
  };
}

afterEach(cleanup);

describe("the scratch table listing", () => {
  it("shows what to query, where it lives, and which file it reads", () => {
    render(<ScratchTablesTable rows={[row()]} isLoading={false} onOpen={vi.fn()} />);

    expect(screen.getByText("scratch.uploads.analyst_regional_sales")).toBeTruthy();
    expect(screen.getByText("acme-scratch")).toBeTruthy();
    expect(screen.getByText("Regional sales summary")).toBeTruthy();
    expect(screen.getByText("alice@example.com")).toBeTruthy();
  });

  it("opens the registration on row click, like every other portal list", () => {
    const onOpen = vi.fn();
    render(<ScratchTablesTable rows={[row()]} isLoading={false} onOpen={onOpen} />);

    fireEvent.click(screen.getByText("scratch.uploads.analyst_regional_sales"));
    expect(onOpen).toHaveBeenCalledWith("reg_1");
  });

  it("flags a table that has fallen behind its file, without opening the file", () => {
    render(<ScratchTablesTable rows={[row({ stale: true, follow: false })]} isLoading={false} onOpen={vi.fn()} />);

    expect(screen.getByText("Behind the file")).toBeTruthy();
    expect(screen.queryByText("Follows the file")).toBeNull();
  });

  it("marks a registration whose file is no longer on the platform", () => {
    render(
      <ScratchTablesTable
        rows={[row({ stale: true, source: { kind: "asset", id: "gone", missing: true } })]}
        isLoading={false}
        onOpen={vi.fn()}
      />,
    );

    expect(screen.getByText("Source deleted")).toBeTruthy();
    expect(screen.getByText("Deleted")).toBeTruthy();
  });

  it("says a current table follows its file, quietly", () => {
    render(<ScratchTablesTable rows={[row()]} isLoading={false} onOpen={vi.fn()} />);
    expect(screen.getByText("Follows the file")).toBeTruthy();
  });

  // A pinned table is current only until the file moves, so the listing says
  // it is pinned rather than calling it current; a following one that fell
  // behind carries the reason on the badge (#1536).
  it("says a pinned table is pinned, and why a following one is behind", () => {
    render(
      <ScratchTablesTable
        rows={[
          row({ follow: false }),
          row({ id: "reg_2", stale: true, follow: true, follow_error: "the coordinator refused the statement" }),
        ]}
        isLoading={false}
        onOpen={vi.fn()}
      />,
    );
    expect(screen.getByText("Pinned")).toBeTruthy();
    expect(screen.getByText("Behind the file").closest("[title]")?.getAttribute("title")).toBe(
      "the coordinator refused the statement",
    );
  });

  it("says so when the filters match nothing", () => {
    render(<ScratchTablesTable rows={[]} isLoading={false} onOpen={vi.fn()} />);
    expect(screen.getByText("No table matches these filters.")).toBeTruthy();
  });
});

describe("the connection facet", () => {
  it("offers the connections present on the page, since a name absent from it can only empty the list", () => {
    const options = connectionOptions(
      [row(), row({ id: "reg_2", connection: "acme-lake" })],
      undefined,
    );

    expect(options.map((o) => o.value)).toEqual(["", "acme-lake", "acme-scratch"]);
  });

  it("keeps the connection already chosen, so a facet cannot filter itself out of its own dropdown", () => {
    const options = connectionOptions([], "acme-scratch");

    expect(options.map((o) => o.value)).toEqual(["", "acme-scratch"]);
  });
});

describe("where a source opens", () => {
  it("sends each kind to its own page", () => {
    expect(sourcePath("asset", "ast-008", false)).toBe("/assets/ast-008");
    expect(sourcePath("resource", "res-015", false)).toBe("/resources/res-015");
    expect(sourceKindLabel("resource")).toBe("Resource");
  });

  it("sends nobody to a page for a record that is gone, or to a kind with no page", () => {
    expect(sourcePath("asset", "ast-008", true)).toBeNull();
    expect(sourcePath("dataset", "urn:x", false)).toBeNull();
  });
});


describe("one registration at an address of its own", () => {
  const unregister = { mutate: vi.fn(), isPending: false };

  beforeEach(() => {
    unregister.mutate = vi.fn();
    vi.mocked(useUnregisterTable).mockReturnValue(
      unregister as unknown as ReturnType<typeof useUnregisterTable>,
    );
    vi.mocked(useScratchTable).mockReturnValue({
      data: row(),
      isLoading: false,
      error: null,
    } as unknown as ReturnType<typeof useScratchTable>);
  });

  function open(overrides: Partial<ScratchTable> = {}) {
    if (Object.keys(overrides).length > 0) {
      vi.mocked(useScratchTable).mockReturnValue({
        data: row(overrides),
        isLoading: false,
        error: null,
      } as unknown as ReturnType<typeof useScratchTable>);
    }
    return render(
      <ScratchTableDetailPage
        registrationId="reg_1"
        onBack={vi.fn()}
        onNavigate={vi.fn()}
      />,
    );
  }

  it("shows the table to query, its columns with their types, and the directory it reads", () => {
    open();

    expect(screen.getByText("scratch.uploads.analyst_regional_sales")).toBeTruthy();
    expect(screen.getByText("SELECT * FROM scratch.uploads.analyst_regional_sales")).toBeTruthy();
    expect(screen.getByText("Columns (2)")).toBeTruthy();
    expect(screen.getByText("region")).toBeTruthy();
    expect(screen.getByText("s3://portal-assets/assets/ast-008/")).toBeTruthy();
  });

  it("links to the file the table reads", () => {
    const onNavigate = vi.fn();
    vi.mocked(useScratchTable).mockReturnValue({
      data: row(),
      isLoading: false,
      error: null,
    } as unknown as ReturnType<typeof useScratchTable>);
    render(
      <ScratchTableDetailPage registrationId="reg_1" onBack={vi.fn()} onNavigate={onNavigate} />,
    );

    fireEvent.click(screen.getByText("Regional sales summary"));
    expect(onNavigate).toHaveBeenCalledWith("/assets/ast-008");
  });

  it("says what to do about a table that has fallen behind its file", () => {
    open({ stale: true, follow: false });

    expect(screen.getByText(/register it again to move the table/i)).toBeTruthy();
  });

  it("says which rule the table is under, and why a following one could not be moved", () => {
    open();
    expect(screen.getByText(/Follows the file: each new version/)).toBeTruthy();

    cleanup();
    open({ follow: false });
    expect(screen.getByText(/Pinned to the version of the file/)).toBeTruthy();

    cleanup();
    open({ stale: true, follow: true, follow_error: "the coordinator refused the statement." });
    expect(screen.getByText(/could not be moved onto the current version: the coordinator refused/)).toBeTruthy();
  });

  it("offers no link for a file that is gone, and says why", () => {
    open({ stale: true, source: { kind: "asset", id: "gone", missing: true } });

    expect(screen.getByText(/Asset gone — no longer on the platform/)).toBeTruthy();
    expect(
      screen.getByText(/no longer on the platform, so the table reads a directory/),
    ).toBeTruthy();
  });

  it("drops the table through the source's own route, after asking", () => {
    open();

    fireEvent.click(screen.getByRole("button", { name: "Unregister" }));
    expect(screen.getByText("Drop this table?")).toBeTruthy();

    fireEvent.click(screen.getAllByRole("button", { name: "Unregister" })[0]!);
    expect(vi.mocked(useUnregisterTable)).toHaveBeenCalledWith("asset", "ast-008");
    expect(unregister.mutate).toHaveBeenCalledWith("reg_1", expect.anything());
  });

  it("withholds the action from a reader the route behind it would refuse", () => {
    open({ can_unregister: false });

    expect(screen.queryByRole("button", { name: "Unregister" })).toBeNull();
  });

  it("answers a registration this reader cannot reach the way it answers one that never existed", () => {
    vi.mocked(useScratchTable).mockReturnValue({
      data: undefined,
      isLoading: false,
      error: new TableApiError(404, "no such registered table"),
    } as unknown as ReturnType<typeof useScratchTable>);

    render(
      <ScratchTableDetailPage registrationId="reg_x" onBack={vi.fn()} onNavigate={vi.fn()} />,
    );

    expect(screen.getByText("No registered table with this id, or none you can reach.")).toBeTruthy();
  });

  it("reports a failed read as a failure rather than as an absence", () => {
    vi.mocked(useScratchTable).mockReturnValue({
      data: undefined,
      isLoading: false,
      error: new Error("network down"),
    } as unknown as ReturnType<typeof useScratchTable>);

    render(
      <ScratchTableDetailPage registrationId="reg_1" onBack={vi.fn()} onNavigate={vi.fn()} />,
    );

    expect(screen.getByText(/could not be read/)).toBeTruthy();
    expect(screen.queryByText(/No registered table with this id/)).toBeNull();
  });
});


describe("the Scratch Tables page", () => {
  function listing(result: Partial<{ data: unknown; isLoading: boolean; isError: boolean }>) {
    vi.mocked(useScratchTables).mockReturnValue({
      isLoading: false,
      isError: false,
      ...result,
    } as unknown as ReturnType<typeof useScratchTables>);
    return render(<ScratchTablesPage onNavigate={vi.fn()} />);
  }

  it("says what a scratch table is and where one is made, rather than showing an empty table", () => {
    listing({ data: { data: [], total: 0, page: 1, per_page: 25 } });

    expect(screen.getByText("No file is registered as a table yet.")).toBeTruthy();
    expect(screen.getByText(/Open a resource or an asset and use/)).toBeTruthy();
  });

  it("keeps the table in front of a reader whose own filters emptied it, so they can undo them", () => {
    listing({ data: { data: [], total: 0, page: 1, per_page: 25 } });
    fireEvent.change(screen.getByLabelText("Search registered tables by name"), {
      target: { value: "nothing" },
    });

    expect(screen.getByText("No table matches these filters.")).toBeTruthy();
    expect(screen.queryByText("No file is registered as a table yet.")).toBeNull();
  });

  it("reports a failed read as a failure rather than as an empty platform", () => {
    listing({ data: undefined, isError: true });

    expect(screen.getByText(/could not be read/)).toBeTruthy();
    expect(screen.queryByText("No file is registered as a table yet.")).toBeNull();
  });

  it("lists what a reader may see", () => {
    listing({ data: { data: [row()], total: 1, page: 1, per_page: 25 } });

    expect(screen.getByText("scratch.uploads.analyst_regional_sales")).toBeTruthy();
  });
});
