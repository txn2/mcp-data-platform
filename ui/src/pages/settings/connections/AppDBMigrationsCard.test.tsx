import { describe, it, expect, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

const planMutate = vi.fn();

vi.mock("@/api/admin/hooks", () => ({
  useAppDBMigrations: vi.fn(),
  usePlanAppDBMigrations: vi.fn(),
}));

import {
  useAppDBMigrations,
  usePlanAppDBMigrations,
  type AppDBMigrationPlan,
  type AppDBMigrationReport,
} from "@/api/admin/hooks";
import { mockAppDBMigrations } from "@/mocks/data/connections";
import { AppDBMigrationsCard } from "./AppDBMigrationsCard";

type ReportQuery = ReturnType<typeof useAppDBMigrations>;
type PlanMutation = ReturnType<typeof usePlanAppDBMigrations>;

function renderCard(report: AppDBMigrationReport, plan?: AppDBMigrationPlan) {
  vi.mocked(useAppDBMigrations).mockReturnValue({
    data: report,
    isLoading: false,
    error: null,
  } as unknown as ReportQuery);
  vi.mocked(usePlanAppDBMigrations).mockReturnValue({
    mutate: planMutate,
    isPending: false,
    error: null,
    data: plan,
  } as unknown as PlanMutation);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <AppDBMigrationsCard connectionName={report.connection} />
    </QueryClientProvider>,
  );
}

const report = mockAppDBMigrations["acme-shop"]!;

describe("AppDBMigrationsCard", () => {
  it("lists each migration with its state and the history", () => {
    renderCard(report);
    expect(screen.getByText("orders")).toBeInTheDocument();
    expect(screen.getByText("order note")).toBeInTheDocument();
    expect(screen.getAllByText("applied").length).toBeGreaterThanOrEqual(2);
    expect(screen.getByText("pending")).toBeInTheDocument();
    expect(screen.getByText("mcp_schema.migrations")).toBeInTheDocument();
    expect(screen.getByText("History")).toBeInTheDocument();
  });

  it("plans the pending migrations and shows the changes and locks", () => {
    renderCard(report, {
      migrations: [3],
      changed: ["public.orders.note"],
      locks: [{ relation: "public.orders", mode: "AccessExclusiveLock" }],
      unplanned: [],
    });
    fireEvent.click(screen.getByRole("button", { name: /Plan/ }));
    expect(planMutate).toHaveBeenCalled();
    expect(screen.getByText("public.orders.note")).toBeInTheDocument();
    expect(screen.getByText("public.orders: AccessExclusiveLock")).toBeInTheDocument();
  });

  it("names drift and disables Plan with nothing pending", () => {
    renderCard({
      ...report,
      migrations: report.migrations.filter((m) => m.state === "applied"),
      drift: [{ version: 1, reason: "its checksum in the ledger differs" }],
    });
    expect(screen.getByText(/Version 1: its checksum in the ledger differs/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Plan/ })).toBeDisabled();
  });

  it("lists the schemas no application holds, each with the call that adds it", () => {
    renderCard(report);
    expect(screen.getByTestId("appdb-application")).toHaveTextContent("In no application");
    const unheld = screen.getByTestId("appdb-unheld-schemas");
    expect(unheld).toHaveTextContent("Schemas in no application");
    expect(unheld).toHaveTextContent("Migrations on these schemas are refused until an application holds them.");
    expect(
      screen.getByText("manage_application add_part application=<name> kind=schema part_id=acme-shop/public"),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Copy the call that adds public" })).toBeInTheDocument();
  });

  it("names the application holding the schema, and lists no section when every schema is held", () => {
    renderCard({ ...report, schema: "shop", application: "shop", unheld_schemas: [] });
    expect(screen.getByTestId("appdb-application")).toHaveTextContent("Application: shop");
    expect(screen.queryByTestId("appdb-unheld-schemas")).toBeNull();
  });

  it("says nothing of applications where the platform governs no schema", () => {
    const { application: _a, unheld_schemas: _u, ...older } = report;
    renderCard(older);
    expect(screen.queryByTestId("appdb-application")).toBeNull();
    expect(screen.queryByText("Schemas in no application")).toBeNull();
  });

  it("says there are none yet", () => {
    renderCard({ ...report, migrations: [], events: [] });
    expect(screen.getByText(/No migrations yet/)).toBeInTheDocument();
  });
});
