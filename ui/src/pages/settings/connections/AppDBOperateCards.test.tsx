import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, render, screen, within } from "@testing-library/react";
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
  const expand = () => fireEvent.click(screen.getByRole("button", { name: "Expand health" }));

  it("folded, states the status and the one-line summary", () => {
    wrap(<AppDBHealthCard connectionName="acme-shop" />);
    expect(screen.getByTestId("health-status")).toHaveTextContent("Needs attention · 2");
    expect(screen.getByText("9 MiB · 7 of 100 connections · cache 99.3%")).toBeInTheDocument();
  });

  it("lists the findings most severe first, with the session's actions, and ends a session only when confirmed", () => {
    wrap(<AppDBHealthCard connectionName="acme-shop" />);
    expand();
    expect(screen.getByRole("heading", { name: "Needs attention" })).toBeInTheDocument();
    const idle = screen.getByTestId("health-finding-idle_in_transaction");
    expect(idle).toHaveTextContent("A session has been idle in a transaction for 10 minutes");
    expect(idle).toHaveTextContent("The warning alert is set at 5 minutes.");
    expect(idle).toHaveTextContent("orders_app");
    expect(idle).toHaveTextContent("UPDATE orders SET total = total WHERE id = 7");
    expect(screen.getByTestId("health-finding-unused_index")).toHaveTextContent("Drop it in a migration");

    fireEvent.click(screen.getByRole("button", { name: "Cancel query" }));
    expect(mutate.signal).toHaveBeenCalledWith({ pid: 4242, signal: "cancel" });
    fireEvent.click(screen.getByRole("button", { name: "End session" }));
    expect(mutate.signal).not.toHaveBeenCalledWith({ pid: 4242, signal: "terminate" });
    const buttons = screen.getAllByRole("button", { name: "End session" });
    fireEvent.click(buttons[buttons.length - 1]!);
    expect(mutate.signal).toHaveBeenCalledWith({ pid: 4242, signal: "terminate" });
  });

  it("shows the tiles against the thresholds and the tables at the normal scale", () => {
    wrap(<AppDBHealthCard connectionName="acme-shop" />);
    expand();
    expect(screen.getByTestId("health-tile-Connections")).toHaveTextContent("7of 100");
    expect(screen.getByTestId("health-tile-Connections")).toHaveTextContent("Alert at 80%");
    expect(screen.getByTestId("health-tile-Transaction ID age")).toHaveTextContent("Alert at 500,000,000");
    expect(screen.getByTestId("appdb-health-tables")).toHaveTextContent("23%");
    expect(screen.getByText("Alerts are not posted anywhere yet. Thresholds apply to every application database.")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Alert settings" })).toHaveAttribute(
      "href",
      expect.stringMatching(/\/admin\/settings\/application-databases$/),
    );
  });

  it("lists statements one line each, counted correctly, opening to the whole statement", () => {
    wrap(<AppDBHealthCard connectionName="acme-shop" />);
    expand();
    const table = screen.getByTestId("health-statements");
    expect(table).not.toHaveTextContent("insufficient privilege");
    expect(screen.getByTestId("health-statements-note")).toHaveTextContent(
      "4 statements from other roles are hidden because this connection's role cannot read their text. Grant it pg_read_all_stats to show them.",
    );
    fireEvent.click(within(screen.getByTestId("statement-102")).getByRole("button", { expanded: false }));
    expect(screen.getByTestId("statement-full")).toHaveTextContent("DELETE FROM carts");
    expect(screen.getByText(/4\.1 s total · Slowest call 4\.1 s · 3,870 rows per call · 1 call$/)).toBeInTheDocument();
  });

  it("reads Healthy with nothing past a threshold, and says all clear", () => {
    m.useAppDBHealth.mockReturnValue({
      data: { ...mockAppDBHealth, status: "healthy", findings: [], sessions: [], tables: [] },
      isLoading: false,
      error: null,
    } as never);
    wrap(<AppDBHealthCard connectionName="acme-shop" />);
    expect(screen.getByTestId("health-status")).toHaveTextContent(/^Healthy$/);
    expand();
    expect(screen.getByTestId("health-all-clear")).toHaveTextContent(
      "This database has no tables yet. Health reports on them once a migration creates some.",
    );
    expect(screen.getByTestId("health-passed")).toHaveTextContent("No blocked sessions");
    expect(screen.getByText("No samples yet. The first arrives within 5 minutes.")).toBeInTheDocument();
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
    expand();
    expect(
      screen.getByText("The replication section could not be read: permission denied"),
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
