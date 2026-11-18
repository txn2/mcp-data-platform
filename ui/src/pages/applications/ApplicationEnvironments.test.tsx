import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, within } from "@testing-library/react";
import type { ApplicationDetail } from "@/api/portal/hooks/applications";
import { ApplicationEnvironments } from "./ApplicationEnvironments";
import { ApplicationVariables } from "./ApplicationVariables";

const { calls } = vi.hoisted(() => ({
  calls: { promote: vi.fn(), rollback: vi.fn(), add: vi.fn(), set: vi.fn(), del: vi.fn() },
}));
vi.mock("@/api/portal/hooks/applicationEnvironments", async (orig) => ({
  ...(await orig<typeof import("@/api/portal/hooks/applicationEnvironments")>()),
  usePromoteEnvironment: () => ({ mutate: calls.promote, isPending: false }),
  useRollbackEnvironment: () => ({ mutate: calls.rollback, isPending: false }),
  useAddEnvironment: () => ({ mutate: calls.add, isPending: false }),
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
    render(<ApplicationEnvironments detail={detail()} isAdmin />);
    const dev = screen.getByTestId("environment-dev");
    expect(dev.textContent).toContain("Release 8");
    expect(dev.textContent).toContain("/environments/dev/endpoints/");
    const prod = screen.getByTestId("environment-prod");
    expect(prod.textContent).toContain("Requires approval");
    expect(prod.textContent).toContain("/endpoints/");
    expect(within(prod).queryByRole("button", { name: /Promote/ })).toBeNull();
    fireEvent.click(within(dev).getByRole("button", { name: "Promote dev to stage" }));
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
