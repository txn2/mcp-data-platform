import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, within } from "@testing-library/react";
import type { ApplicationDetail } from "@/api/portal/hooks/applications";
import { ApplicationEnvironments } from "./ApplicationEnvironments";
import { ApplicationVariables } from "./ApplicationVariables";

const { calls } = vi.hoisted(() => ({
  calls: { promote: vi.fn(), rollback: vi.fn(), add: vi.fn(), set: vi.fn(), del: vi.fn(), remove: vi.fn() },
}));
vi.mock("@/api/portal/hooks/applicationEnvironments", async (orig) => ({
  ...(await orig<typeof import("@/api/portal/hooks/applicationEnvironments")>()),
  usePromoteEnvironment: () => ({ mutate: calls.promote, isPending: false }),
  useRollbackEnvironment: () => ({ mutate: calls.rollback, isPending: false }),
  useAddEnvironment: () => ({ mutate: calls.add, isPending: false }),
  useRemoveEnvironment: () => ({ mutate: calls.remove, isPending: false }),
  useSetVariable: () => ({
    mutate: (v: unknown, o: { onSuccess?: () => void }) => {
      calls.set(v, o);
      o.onSuccess?.();
    },
    isPending: false,
  }),
  useDeleteVariable: () => ({ mutate: calls.del, isPending: false }),
  useApplicationVariables: () => ({
    data: {
      variables: [
        { environment: "", name: "API_BASE", value: "https://sandbox.example.com", updated_by: "", updated_at: "" },
        { environment: "prod", name: "API_BASE", value: "https://pay.example.com", updated_by: "", updated_at: "" },
        { environment: "prod", name: "KEY", value: "{{secret:live}}", updated_by: "", updated_at: "" },
      ],
    },
    error: null,
  }),
}));

function detail(): ApplicationDetail {
  return {
    application: {
      id: "app_1", name: "shop", description: "", owner_email: "o@example.com", connection: "", schema: "",
      schema_adopted_version: 0, current_release: 6, created_by: "", created_at: "", updated_at: "", parts: [],
      environments: [
        { name: "dev", release: 8, requires_approval: false, updated_by: "jane@example.com", updated_at: "2025-01-01T00:00:00Z" },
        { name: "stage", release: 7, requires_approval: false, updated_by: "", updated_at: "" },
        { name: "prod", release: 6, requires_approval: true, updated_by: "", updated_at: "" },
      ],
    },
    parts: [], current_release: null, pending: [], problems: [], findings: [], releases: [],
  } as ApplicationDetail;
}

// Environments and variables on the application's page (#2696).
describe("ApplicationEnvironments", () => {
  beforeEach(() => Object.values(calls).forEach((f) => f.mockReset()));

  it("shows each environment's release and address, and promotes one step", () => {
    calls.promote.mockImplementation((_v: unknown, o: { onSuccess?: () => void }) => o.onSuccess?.());
    render(<ApplicationEnvironments detail={detail()} isAdmin />);
    const dev = screen.getByTestId("environment-dev");
    expect(dev.textContent).toContain("Release 8");
    expect(dev.textContent).toContain("/environments/dev/endpoints/");
    const prod = screen.getByTestId("environment-prod");
    expect(prod.textContent).toContain("Requires approval");
    expect(prod.textContent).toContain("/endpoints/");
    expect(within(prod).queryByRole("button", { name: /Promote/ })).toBeNull();
    fireEvent.click(within(dev).getByRole("button", { name: "Promote dev to stage" }));
    expect(calls.promote).not.toHaveBeenCalled();
    fireEvent.click(within(screen.getByTestId("promote-dialog")).getByRole("button", { name: "Promote" }));
    expect(calls.promote).toHaveBeenCalledWith({ environment: "dev" }, expect.anything());
    fireEvent.click(within(prod).getByRole("button", { name: "Roll back prod" }));
    expect(calls.rollback).toHaveBeenCalledWith({ environment: "prod" }, expect.anything());
  });

  it("adds an environment before the last", () => {
    render(<ApplicationEnvironments detail={detail()} isAdmin />);
    fireEvent.click(screen.getByRole("button", { name: "Add environment" }));
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "uat" } });
    fireEvent.click(screen.getByLabelText("Promotions here need approval"));
    fireEvent.click(screen.getByRole("button", { name: "Add" }));
    expect(calls.add).toHaveBeenCalledWith({ name: "uat", after: "stage", requires_approval: true }, expect.anything());
  });

  it("shows a reader no controls", () => {
    render(<ApplicationEnvironments detail={detail()} isAdmin={false} />);
    expect(screen.queryByRole("button", { name: /Promote|Roll back|Add environment/ })).toBeNull();
  });
});

// Each environment its own schema (#2709): the schema and sites rows, the
// promote dialog's list, the add dialog's schema, and removal.
function schemaDetail(): ApplicationDetail {
  const d = detail();
  d.application.connection = "app-db";
  d.application.schema = "orders";
  d.application.environments = [
    { name: "dev", release: 8, requires_approval: false, schema: "orders_dev", updated_by: "", updated_at: "" },
    { name: "prod", release: 7, requires_approval: true, schema: "orders", updated_by: "", updated_at: "" },
  ];
  d.schema = {
    connection: "app-db", schema: "orders", migration_version: 13, released_version: 13, pending: [14], adopted_version: 0,
    run_role: "orders_run", write_role: "",
    environments: [
      { environment: "dev", schema: "orders_dev", migration_version: 14, pending: [] },
      { environment: "prod", schema: "orders", migration_version: 13, pending: [14, 15] },
    ],
  };
  d.site_parts = [
    { part_id: "shop-web/dev", release_mode: "coupled", serves: 31, step: 32, contract: "", compatible: true },
    { part_id: "shop-web/prod", release_mode: "coupled", serves: 29, step: 31, contract: "", compatible: true },
  ];
  const pins = (a: number, b: number, site: number) => [
    { kind: "script" as const, part_id: "s1", version: a },
    { kind: "script" as const, part_id: "s2", version: b },
    { kind: "script" as const, part_id: "s3", version: 1 },
    { kind: "site" as const, part_id: "shop-web/prod", version: site },
  ];
  const rel = (number: number, mv: number, p: ReturnType<typeof pins>) =>
    ({ number, action: "release", migration_version: mv, changes: { parts: [], applied: [], reverted: [] }, created_by: "", created_at: "", pins: p }) as ApplicationDetail["releases"][number];
  d.releases = [rel(8, 14, pins(5, 3, 31)), rel(7, 13, pins(4, 2, 29))];
  return d;
}

describe("ApplicationEnvironments with a schema per environment", () => {
  beforeEach(() => Object.values(calls).forEach((f) => f.mockReset()));

  it("shows the schema each environment runs in and the sites calling it", () => {
    render(<ApplicationEnvironments detail={schemaDetail()} isAdmin={false} />);
    expect(screen.getByTestId("environment-schema-dev").textContent).toBe("orders_devon app-db · migration 14");
    expect(screen.getByTestId("environment-schema-prod").textContent).toContain("orders");
    expect(screen.getByTestId("environment-sites-dev").textContent).toBe("shop-web / dev");
    expect(screen.getByTestId("environment-sites-prod").textContent).toBe("shop-web / prod");
  });

  it("says when environments share a schema", () => {
    const d = schemaDetail();
    d.schema!.environments![0] = { environment: "dev", schema: "orders", migration_version: 13, pending: [], shared: "prod" };
    render(<ApplicationEnvironments detail={d} isAdmin={false} />);
    expect(screen.getByTestId("environment-schema-dev").textContent).toContain("shared with prod");
  });

  it("lists what a promote does before doing it", () => {
    render(<ApplicationEnvironments detail={schemaDetail()} isAdmin />);
    fireEvent.click(screen.getByRole("button", { name: "Promote dev to prod" }));
    const dialog = screen.getByTestId("promote-dialog");
    expect(within(dialog).getByRole("heading", { name: "Promote release 8 to prod" })).toBeInTheDocument();
    expect(dialog.textContent).toContain("prod runs release 7. Promoting release 8:");
    const items = within(screen.getByTestId("promote-list")).getAllByRole("listitem").map((li) => li.textContent);
    expect(items).toEqual(["runs 2 changed scripts in prod", "applies migration 14 to orders", "moves shop-web / prod (coupled) to release 31"]);
    expect(dialog.textContent).toContain("Data is not copied. prod keeps its variables and its schema's rows; dev keeps its own.");
    fireEvent.click(within(dialog).getByRole("button", { name: "Promote" }));
    expect(calls.promote).toHaveBeenCalledWith({ environment: "dev" }, expect.anything());
  });

  it("adds an environment with the schema it runs in, defaulting to <schema>_<name>", () => {
    render(<ApplicationEnvironments detail={schemaDetail()} isAdmin />);
    fireEvent.click(screen.getByRole("button", { name: "Add environment" }));
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "qa-2" } });
    const schema = screen.getByLabelText("Schema");
    expect(schema).toHaveAttribute("placeholder", "orders_qa_2");
    expect(screen.getByText("On app-db, with its own run role. Built from prod's structure, with no rows.")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Add" }));
    expect(calls.add).toHaveBeenLastCalledWith({ name: "qa-2", after: "dev", requires_approval: false }, expect.anything());
    fireEvent.change(screen.getByLabelText("Schema"), { target: { value: "orders_qa" } });
    fireEvent.click(screen.getByRole("button", { name: "Add" }));
    expect(calls.add).toHaveBeenLastCalledWith({ name: "qa-2", after: "dev", requires_approval: false, schema: "orders_qa" }, expect.anything());
  });

  it("gives an application holding no schema no schema field", () => {
    render(<ApplicationEnvironments detail={detail()} isAdmin />);
    fireEvent.click(screen.getByRole("button", { name: "Add environment" }));
    expect(screen.queryByLabelText("Schema")).toBeNull();
  });

  it("removes an environment, saying its own schema is kept, and shows a refusal", () => {
    calls.remove.mockImplementation((_i: unknown, o: { onError: (e: Error) => void }) =>
      o.onError(new Error("dev can't be removed: site part shop-web/dev calls it. Remove that site part first")),
    );
    render(<ApplicationEnvironments detail={schemaDetail()} isAdmin />);
    expect(within(screen.getByTestId("environment-prod")).queryByRole("button", { name: "Remove prod" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Remove dev" }));
    expect(screen.getByText(/The schema orders_dev is kept until an administrator drops it\./)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Remove" }));
    expect(calls.remove).toHaveBeenCalledWith({ environment: "dev" }, expect.anything());
    expect(screen.getByText("dev can't be removed: site part shop-web/dev calls it. Remove that site part first")).toBeInTheDocument();
  });

  it("shows the prepare banner and holds Add environment while files name the schema", () => {
    const d = schemaDetail();
    d.application.environments = [d.application.environments![1]!];
    d.prepare = { scripts: 41, libraries: 2, migrations: 3 };
    render(<ApplicationEnvironments detail={d} isAdmin />);
    expect(screen.getByTestId("prepare-banner").textContent).toContain(
      "shop runs in prod only. 41 scripts, 2 libraries and 3 pending migrations name the schema orders or its roles.",
    );
    expect(screen.getByRole("button", { name: "Add environment" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Review the change" })).toBeInTheDocument();
  });
});

describe("ApplicationVariables", () => {
  beforeEach(() => Object.values(calls).forEach((f) => f.mockReset()));

  it("lays the values out by environment, defaults inherited and secret references shown as written", () => {
    render(<ApplicationVariables detail={detail()} canEdit />);
    const table = screen.getByTestId("application-variables");
    const row = within(table).getByText("API_BASE").closest("tr")!;
    expect(row.textContent).toContain("https://sandbox.example.com");
    expect(row.textContent).toContain("default");
    expect(row.textContent).toContain("https://pay.example.com");
    expect(within(table).getByText("{{secret:live}}")).toBeInTheDocument();
  });

  it("sets a value for one environment and falls back to the default", () => {
    render(<ApplicationVariables detail={detail()} canEdit />);
    fireEvent.click(screen.getByRole("button", { name: "Edit API_BASE for prod" }));
    fireEvent.change(screen.getByLabelText("Value"), { target: { value: "https://new.example.com" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(calls.set).toHaveBeenCalledWith(
      { name: "API_BASE", environment: "prod", value: "https://new.example.com" },
      expect.anything(),
    );
    fireEvent.click(screen.getByRole("button", { name: "Edit API_BASE for prod" }));
    fireEvent.click(screen.getByRole("button", { name: "Use the default for prod" }));
    expect(calls.del).toHaveBeenCalledWith({ name: "API_BASE", environment: "prod" }, expect.anything());
  });

  it("is not shown to someone who may not edit the application", () => {
    render(<ApplicationVariables detail={detail()} canEdit={false} />);
    expect(screen.queryByTestId("application-variables")).toBeNull();
  });
});
