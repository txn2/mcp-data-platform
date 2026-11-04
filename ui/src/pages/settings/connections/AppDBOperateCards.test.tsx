import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

const mutate = {
  adopt: vi.fn(),
  maintain: vi.fn(),
  signal: vi.fn(),
  console: vi.fn(),
  settings: vi.fn(),
};

vi.mock("@/api/admin/hooks", () => ({
  useAppDBHealth: vi.fn(),
  useAppDBHealthHistory: vi.fn(),
  useAppDBHealthSettings: vi.fn(),
  useSaveAppDBHealthSettings: vi.fn(),
  useAppDBSignal: vi.fn(),
  useAppDBDrift: vi.fn(),
  useAdoptAppDBSchema: vi.fn(),
  useAppDBMaintenance: vi.fn(),
  useAppDBConsole: vi.fn(),
  useNotificationChannels: vi.fn(() => ({ data: [] })),
}));

import * as hooks from "@/api/admin/hooks";
import type { AppDBAdoptResult, AppDBConsoleResult } from "@/api/admin/hooks";
import {
  mockAppDBHealth,
  mockAppDBHealthSettings,
} from "@/mocks/handlers/appdb";
import { AppDBConsoleCard } from "./AppDBConsoleCard";
import { AppDBDriftCard } from "./AppDBDriftCard";
import { AppDBHealthCard } from "./AppDBHealthCard";
import { AppDBMaintenanceCard } from "./AppDBMaintenanceCard";

const m = vi.mocked(hooks);

function mutation(
  fn: ReturnType<typeof vi.fn>,
  data?: unknown,
  error: Error | null = null,
) {
  return { mutate: fn, isPending: false, error, data } as never;
}

function wrap(node: React.ReactNode) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}>{node}</QueryClientProvider>);
}

/** open unfolds a collapsed SectionCard by its title. */
function open(title: string) {
  fireEvent.click(screen.getByRole("button", { name: new RegExp(title) }));
}

beforeEach(() => {
  Object.values(mutate).forEach((f) => f.mockReset());
  m.useAppDBHealth.mockReturnValue({
    data: mockAppDBHealth,
    isLoading: false,
    error: null,
  } as never);
  m.useAppDBHealthHistory.mockReturnValue({
    data: { connection: "acme-shop", days: 7, points: [] },
  } as never);
  m.useAppDBHealthSettings.mockReturnValue({
    data: mockAppDBHealthSettings,
  } as never);
  m.useSaveAppDBHealthSettings.mockReturnValue(mutation(mutate.settings));
  m.useAppDBSignal.mockReturnValue(mutation(mutate.signal));
  m.useAdoptAppDBSchema.mockReturnValue(mutation(mutate.adopt));
  m.useAppDBMaintenance.mockReturnValue(mutation(mutate.maintain));
  m.useAppDBConsole.mockReturnValue(mutation(mutate.console));
  m.useAppDBDrift.mockReturnValue({
    data: {
      connection: "acme-shop",
      schemas: [
        {
          connection: "acme-shop",
          schema: "public",
          checked_at: "",
          checked: true,
          applied_version: 2,
          differences: [
            { kind: "column", object: "orders.coupon", change: "added" },
          ],
        },
        {
          connection: "acme-shop",
          schema: "audit",
          checked_at: "",
          checked: false,
          applied_version: 0,
          differences: [],
          reason: "no migration is applied yet",
        },
        {
          connection: "acme-shop",
          schema: "legacy",
          checked_at: "",
          checked: false,
          applied_version: 0,
          differences: [],
          reason: "no migration is applied yet",
        },
      ],
    },
    isLoading: false,
    error: null,
  } as never);
});

describe("AppDBHealthCard", () => {
  it("shows dead tuples, the unused index and the idle session, and terminates it", () => {
    wrap(<AppDBHealthCard connectionName="acme-shop" />);
    open("Health");
    expect(screen.getByTestId("appdb-health-tables")).toHaveTextContent(
      "1,200 (23%)",
    );
    expect(screen.getByTestId("appdb-health-indexes")).toHaveTextContent(
      "public.orders_note_idx",
    );
    expect(screen.getByTestId("appdb-health-sessions")).toHaveTextContent(
      "idle in transaction",
    );
    expect(
      screen.getByText(/pg_stat_statements is not installed/),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Terminate" }));
    expect(mutate.signal).toHaveBeenCalledWith({
      pid: 4242,
      signal: "terminate",
    });
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(mutate.signal).toHaveBeenCalledWith({ pid: 4242, signal: "cancel" });
  });

  it("names a section the database refused", () => {
    m.useAppDBHealth.mockReturnValue({
      data: {
        ...mockAppDBHealth,
        errors: [{ section: "replication", error: "permission denied" }],
      },
      isLoading: false,
      error: null,
    } as never);
    wrap(<AppDBHealthCard connectionName="acme-shop" />);
    open("Health");
    expect(
      screen.getByText("replication: permission denied"),
    ).toBeInTheDocument();
  });
});

describe("AppDBDriftCard", () => {
  it("lists a column added outside migrations and adopts a schema", () => {
    wrap(<AppDBDriftCard connectionName="acme-shop" />);
    open("Drift");
    expect(screen.getByTestId("appdb-drift-differences")).toHaveTextContent(
      "orders.coupon",
    );
    const unchecked = screen.getByTestId("appdb-drift-unchecked");
    expect(unchecked).toHaveTextContent(
      "2 schema(s) not checked: no migration is applied yet",
    );
    expect(unchecked).toHaveTextContent("audit, legacy");
    fireEvent.change(screen.getByLabelText("Schema"), {
      target: { value: "legacy" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Adopt schema" }));
    expect(mutate.adopt).toHaveBeenCalledWith("legacy");
  });

  it("says why an adoption was refused", () => {
    const res: AppDBAdoptResult = {
      connection: "acme-shop",
      schema: "legacy",
      adopted: false,
      version: 1,
      unsupported: [{ kind: "rule", name: "orders_log" }],
      differences: [],
    };
    m.useAdoptAppDBSchema.mockReturnValue(mutation(mutate.adopt, res));
    wrap(<AppDBDriftCard connectionName="acme-shop" />);
    open("Drift");
    expect(screen.getByTestId("appdb-adopt-refused")).toHaveTextContent(
      "rule orders_log",
    );
  });
});

describe("AppDBMaintenanceCard", () => {
  it("runs analyze on the whole database and needs a table for vacuum full", () => {
    wrap(<AppDBMaintenanceCard connectionName="acme-shop" />);
    open("Maintenance");
    expect(screen.getByRole("button", { name: "Vacuum full" })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "Analyze" }));
    expect(mutate.maintain).toHaveBeenCalledWith({
      command: "analyze",
      table: undefined,
    });
    fireEvent.change(screen.getByLabelText("Table"), {
      target: { value: "public.orders" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Reindex" }));
    expect(mutate.maintain).toHaveBeenCalledWith({
      command: "reindex",
      table: "public.orders",
    });
  });
});

describe("AppDBConsoleCard", () => {
  it("runs read only by default and writes only with the toggle off", () => {
    wrap(<AppDBConsoleCard connectionName="acme-shop" />);
    open("Console");
    fireEvent.change(screen.getByLabelText("SQL"), {
      target: { value: "UPDATE orders SET total = 1" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Run" }));
    expect(mutate.console).toHaveBeenLastCalledWith({
      sql: "UPDATE orders SET total = 1",
      read_only: true,
    });
    fireEvent.click(screen.getByLabelText("Read only"));
    fireEvent.click(screen.getByRole("button", { name: "Run" }));
    expect(mutate.console).toHaveBeenLastCalledWith({
      sql: "UPDATE orders SET total = 1",
      read_only: false,
    });
  });

  it("shows the rows, the database's refusal and a commit", () => {
    const res: AppDBConsoleResult = {
      connection: "acme-shop",
      read_only: false,
      committed: true,
      results: [
        {
          index: 0,
          command: "SELECT",
          rows_affected: 1,
          columns: [{ name: "total", type: "numeric" }],
          rows: [[null]],
          truncated: false,
        },
      ],
      error: {
        index: 1,
        code: "statement_failed",
        summary: "SQLSTATE 25006",
        detail: "cannot execute UPDATE in a read-only transaction",
      },
    };
    m.useAppDBConsole.mockReturnValue(mutation(mutate.console, res));
    wrap(<AppDBConsoleCard connectionName="acme-shop" />);
    open("Console");
    const out = screen.getByTestId("appdb-console-results");
    expect(out).toHaveTextContent("NULL");
    expect(out).toHaveTextContent(
      "cannot execute UPDATE in a read-only transaction",
    );
    expect(out).toHaveTextContent("Committed.");
  });
});
