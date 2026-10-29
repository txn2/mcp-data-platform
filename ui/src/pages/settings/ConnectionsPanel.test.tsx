import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, cleanup, fireEvent, within } from "@testing-library/react";
import type { EffectiveConnection } from "@/api/admin/types";

// The panel's hooks are mocked so a list of connections can be put in front
// of it; the viewer is a stub naming the connection it shows, since what is
// under test is the rail's search and kind filter (#2350) and the address
// they are kept in.
vi.mock("@/api/admin/hooks", () => ({
  useEffectiveConnections: vi.fn(),
  useSystemInfo: vi.fn(() => ({ data: { config_mode: "database" } })),
  useConnectionsOAuthHealth: vi.fn(() => ({ data: { connections: [] } })),
}));

vi.mock("./connections/ConnectionViewer", () => ({
  ConnectionViewer: ({ connection }: { connection: EffectiveConnection }) => (
    <div data-testid="viewing">{connection.name}</div>
  ),
}));

vi.mock("./connections/ConnectionEditor", () => ({ ConnectionEditor: () => <div /> }));

import { useEffectiveConnections } from "@/api/admin/hooks";
import { ConnectionsPanel } from "./ConnectionsPanel";

const conn = (kind: string, name: string, description = ""): EffectiveConnection => ({
  kind,
  name,
  connection: name,
  description,
  source: "database",
  tools: [],
});

const connections = [
  conn("trino", "acme-warehouse", "Sales tables"),
  { ...conn("api", "acme-crm", "Customer records"), config: { catalog_id: "crm" } },
  { ...conn("api", "billing", "Invoices"), config: { base_url: "https://billing.example.com" } },
  conn("s3", "acme-drop"),
];

function rail(): string[] {
  const names = screen.queryAllByRole("button").map((b) => b.textContent ?? "");
  return connections.map((c) => c.name).filter((n) => names.some((t) => t.startsWith(n)));
}

beforeEach(() => {
  vi.mocked(useEffectiveConnections).mockReturnValue({
    data: connections,
    isLoading: false,
    isFetching: false,
  } as unknown as ReturnType<typeof useEffectiveConnections>);
  window.history.replaceState(null, "", "/portal/admin/connections");
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("ConnectionsPanel search and filter (#2350)", () => {
  it("narrows the list as text is typed and shows it all again when cleared", () => {
    render(<ConnectionsPanel />);
    expect(rail()).toEqual(["acme-warehouse", "acme-crm", "billing", "acme-drop"]);

    const search = screen.getByRole("textbox", { name: "Search connections" });
    fireEvent.change(search, { target: { value: "invoices" } });
    expect(rail()).toEqual(["billing"]);
    expect(new URLSearchParams(window.location.search).get("q")).toBe("invoices");

    fireEvent.change(search, { target: { value: "nothing like it" } });
    expect(rail()).toEqual([]);
    expect(screen.getByText("No connections match")).toBeTruthy();

    fireEvent.change(search, { target: { value: "" } });
    expect(rail()).toEqual(["acme-warehouse", "acme-crm", "billing", "acme-drop"]);
    expect(new URLSearchParams(window.location.search).has("q")).toBe(false);
  });

  it("restores the search and kind from the address, combined", () => {
    window.history.replaceState(null, "", "/portal/admin/connections?q=acme&type=api");
    render(<ConnectionsPanel />);
    expect(rail()).toEqual(["acme-crm"]);
    expect((screen.getByRole("textbox", { name: "Search connections" }) as HTMLInputElement).value).toBe("acme");
    expect(within(screen.getByRole("combobox", { name: "Filter by kind" })).getByText("API")).toBeTruthy();
    const params = new URLSearchParams(window.location.search);
    expect(params.get("type")).toBe("api");
    expect(params.get("q")).toBe("acme");
    expect(screen.getByTestId("viewing").textContent).toBe("acme-crm");
  });

  it("marks an api connection with no catalog, and only that one (#2271)", () => {
    render(<ConnectionsPanel />);
    const marks = screen.getAllByText("no catalog");
    expect(marks).toHaveLength(1);
    expect(marks[0]!.closest("button")?.textContent).toMatch(/^billing/);
  });

  it("keeps the selected connection in the address beside the filter", () => {
    window.history.replaceState(null, "", "/portal/admin/connections?type=api");
    render(<ConnectionsPanel />);
    fireEvent.click(screen.getByRole("button", { name: /^billing/ }));
    const params = new URLSearchParams(window.location.search);
    expect(params.get("kind")).toBe("api");
    expect(params.get("name")).toBe("billing");
    expect(params.get("type")).toBe("api");
  });
});
