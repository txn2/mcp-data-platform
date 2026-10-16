import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { mockReconciliation } from "@/mocks/handlers/tableReconcile";
import { useAuthStore } from "@/stores/auth";

const h = vi.hoisted(() => ({
  reconcile: { data: undefined as unknown, isLoading: false, isError: false, error: null as unknown },
  repair: vi.fn(),
  repairError: null as Error | null,
  connections: ["acme-scratch-resources"] as string[],
  asked: [] as string[],
}));

vi.mock("@/api/admin/hooks", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/api/admin/hooks")>()),
  useReconciliation: (connection: string) => {
    h.asked.push(connection);
    return h.reconcile;
  },
  useRepairTables: () => ({ mutate: h.repair, isPending: false, error: h.repairError }),
}));

vi.mock("@/api/tables/hooks", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/api/tables/hooks")>()),
  useTableConnections: () => ({
    data: { connections: h.connections.map((name) => ({ name, catalog: "scratch_resources", schema: "uploads" })) },
    isSuccess: true,
  }),
  useScratchTables: () => ({ data: { data: [], total: 0 }, isLoading: false, isError: false }),
}));

import { ReconcilePage } from "./ReconcilePage";
import { ScratchTableRoutes } from "./ScratchTableRoutes";

const admin = { is_admin: true } as unknown as NonNullable<ReturnType<typeof useAuthStore.getState>["user"]>;

beforeEach(() => {
  h.reconcile = { data: mockReconciliation("acme-scratch-resources"), isLoading: false, isError: false, error: null };
  h.repair.mockReset();
  h.repairError = null;
  h.connections = ["acme-scratch-resources"];
  h.asked = [];
});
afterEach(() => {
  cleanup();
  useAuthStore.setState({ user: null });
});

describe("the reconcile (#2136)", () => {
  it("reads the first connection and lists the three groups", () => {
    render(<ReconcilePage />);
    expect(h.asked).toContain("acme-scratch-resources");
    expect(screen.getByText("Tables with no registration")).toBeInTheDocument();
    expect(screen.getByText("Registrations whose table is gone")).toBeInTheDocument();
    expect(screen.getByText("Tables reading somewhere else")).toBeInTheDocument();
    expect(screen.getByText("Resource res_7f2a")).toBeInTheDocument();
    expect(screen.getByText("Resource res_1c90 (deleted)")).toBeInTheDocument();
    expect(screen.getByText("Not the platform's; left alone")).toBeInTheDocument();
    expect(screen.getByText("s3://portal-assets/artifacts/u3/ast_3b7/v4/")).toBeInTheDocument();
  });

  it("offers only what can be proved, and repairs what was chosen", () => {
    render(<ReconcilePage />);
    expect(screen.queryByLabelText("Drop scratch_by_hand")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Forget webhook_esp_events")).not.toBeInTheDocument();
    const repair = screen.getByRole("button", { name: "Repair selected" });
    expect(repair).toBeDisabled();

    fireEvent.click(screen.getByLabelText("Drop admin_daily_orders"));
    fireEvent.click(screen.getByLabelText("Forget analyst_store_rebates"));
    fireEvent.click(screen.getByLabelText("Drop analyst_q3_vendors"));
    fireEvent.click(screen.getByLabelText("Drop analyst_q3_vendors"));
    expect(repair).toBeEnabled();
    fireEvent.click(repair);
    expect(h.repair).toHaveBeenCalledWith(
      {
        connection: "acme-scratch-resources",
        repair: [
          { action: "drop", table: "admin_daily_orders" },
          { action: "forget", registration_id: "reg_5d11" },
        ],
      },
      expect.anything(),
    );
  });

  it("lists the repairs that were not made with their reasons", () => {
    render(<ReconcilePage />);
    fireEvent.click(screen.getByLabelText("Drop admin_daily_orders"));
    fireEvent.click(screen.getByRole("button", { name: "Repair selected" }));
    const { onSuccess } = h.repair.mock.calls[0]![1] as { onSuccess: (r: unknown) => void };
    act(() =>
      onSuccess({
        outcomes: [{ action: "drop", table: "admin_daily_orders", done: false, reason: "the drop failed: metastore down" }],
      }),
    );
    expect(screen.getByText(/the drop failed: metastore down/)).toBeInTheDocument();
  });

  it("says when a group is empty, the listing was cut, or the schema cannot be read", () => {
    h.reconcile = {
      data: { ...mockReconciliation("c"), orphans: [], missing: [], drifted: [], truncated: true },
      isLoading: false, isError: false, error: null,
    };
    const { unmount } = render(<ReconcilePage />);
    expect(screen.getAllByText("None.")).toHaveLength(3);
    expect(screen.getByText(/the list below is partial/)).toBeInTheDocument();
    unmount();

    h.reconcile = { data: undefined, isLoading: false, isError: true, error: new Error("coordinator down") };
    render(<ReconcilePage />);
    expect(screen.getByText("coordinator down")).toBeInTheDocument();
  });

  it("says when no connection holds a scratch schema", () => {
    h.connections = [];
    render(<ReconcilePage />);
    expect(screen.getByText("No connection here holds a scratch schema.")).toBeInTheDocument();
  });

  it("reports a repair request that failed", () => {
    h.repairError = new Error("forbidden");
    render(<ReconcilePage />);
    expect(screen.getByText("forbidden")).toBeInTheDocument();
  });
});

describe("the Scratch Tables tabs", () => {
  it("gives an administrator a Reconcile tab and nobody else", () => {
    const onNavigate = vi.fn();
    const { unmount } = render(<ScratchTableRoutes route="/scratch-tables" onNavigate={onNavigate} onBack={vi.fn()} />);
    expect(screen.queryByRole("tab", { name: "Reconcile" })).not.toBeInTheDocument();
    unmount();
    const none = render(<ScratchTableRoutes route="/scratch-tables/reconcile" onNavigate={onNavigate} onBack={vi.fn()} />);
    expect(none.container).toBeEmptyDOMElement();
    none.unmount();

    useAuthStore.setState({ user: admin });
    render(<ScratchTableRoutes route="/scratch-tables/reconcile" onNavigate={onNavigate} onBack={vi.fn()} />);
    const tabs = screen.getByRole("tablist");
    expect(within(tabs).getByRole("tab", { name: "Reconcile" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByText("Tables with no registration")).toBeInTheDocument();
    fireEvent.mouseDown(within(tabs).getByRole("tab", { name: "Tables" }));
    expect(onNavigate).toHaveBeenCalledWith("/scratch-tables");
  });
});
