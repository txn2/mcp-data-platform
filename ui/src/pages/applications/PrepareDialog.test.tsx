import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import type { ApplicationPreparePlan } from "@/api/portal/hooks/applicationEnvironments";
import type { ApplicationDetail } from "@/api/portal/hooks/applications";
import { PrepareDialog } from "./PrepareDialog";

// The prepare dialog's refusals (#2709): a file the save gate refuses holds
// the save and says why, and a save refused at the server shows the plan it
// carries.

const { state } = vi.hoisted(() => ({
  state: { plan: null as ApplicationPreparePlan | null, save: vi.fn() },
}));
vi.mock("@/api/portal/hooks/applicationEnvironments", async (orig) => ({
  ...(await orig<typeof import("@/api/portal/hooks/applicationEnvironments")>()),
  usePreparePlan: () => ({ data: state.plan, isLoading: false, error: null }),
  useSavePrepare: () => ({ mutate: state.save, isPending: false }),
}));

function plan(refusal?: string): ApplicationPreparePlan {
  return {
    application: "shop", schema: "orders", ready: false, saved: false,
    counts: { scripts: 1, libraries: 0, migrations: 0 },
    values: [],
    files: [
      {
        kind: "script", id: "s1", name: "checkout", version: 17, names: 1, replayed: ["run_1"], differences: refusal ? [{}] : [],
        before: '"INSERT INTO orders.invoices (customer, total)"', after: '"INSERT INTO invoices (customer, total)"',
        ...(refusal ? { refusal } : {}),
      },
    ],
  };
}

const detail = {
  application: {
    id: "app_1", name: "shop", description: "", owner_email: "", connection: "app-db", schema: "orders", schema_adopted_version: 0,
    current_release: 1, created_by: "", created_at: "", updated_at: "", parts: [],
    environments: [{ name: "prod", release: 1, requires_approval: false, schema: "orders", updated_by: "", updated_at: "" }],
  },
  parts: [], current_release: null, pending: [], problems: [], findings: [], releases: [],
} as ApplicationDetail;

describe("PrepareDialog", () => {
  beforeEach(() => {
    state.save.mockReset();
  });

  it("holds the save while the save gate refuses a file, naming why", () => {
    state.plan = plan("a behavior change needs a summary and the person's agreement");
    render(<PrepareDialog detail={detail} isAdmin onClose={vi.fn()} />);
    expect(screen.getByTestId("prepare-replay")).toHaveTextContent(
      "The save gate refuses 1 file, so nothing is saved until each is fixed: see the refusal under each.",
    );
    expect(screen.getByText("a behavior change needs a summary and the person's agreement")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Save 1 new version" })).toBeDisabled();
  });

  it("shows a refused save's plan and its detail", () => {
    state.plan = plan();
    const err = Object.assign(new Error("prepare saves nothing while the save gate refuses a file"), {
      body: { plan: plan("replayed run run_1 answered differently") },
    });
    state.save.mockImplementation((...args: unknown[]) => (args[1] as { onError: (e: unknown) => void }).onError(err));
    render(<PrepareDialog detail={detail} isAdmin onClose={vi.fn()} />);
    expect(screen.getByTestId("prepare-replay")).toHaveTextContent("Replayed 1 run of the changed scripts: every answer is the same.");
    fireEvent.click(screen.getByRole("button", { name: "Save 1 new version" }));
    expect(screen.getByText("prepare saves nothing while the save gate refuses a file")).toBeInTheDocument();
    expect(screen.getByText("replayed run run_1 answered differently")).toBeInTheDocument();
  });

  it("offers a reader no save, and an application naming nothing is ready", () => {
    state.plan = plan();
    const { unmount } = render(<PrepareDialog detail={detail} isAdmin={false} onClose={vi.fn()} />);
    expect(screen.queryByRole("button", { name: /^Save/ })).toBeNull();
    unmount();
    state.plan = { ...plan(), files: [], ready: true };
    render(<PrepareDialog detail={detail} isAdmin onClose={vi.fn()} />);
    expect(screen.getByText("Nothing in shop names its schema or its roles. It is ready for an environment of its own.")).toBeInTheDocument();
  });

  it("lists the quoted values prepare leaves as written, with and without files to change", () => {
    state.plan = { ...plan(), values: [{ kind: "script", name: "checkout", line: 12, text: "orders.invoices" }] };
    const { unmount } = render(<PrepareDialog detail={detail} isAdmin onClose={vi.fn()} />);
    expect(screen.getByTestId("prepare-values")).toHaveTextContent("checkout line 12: 'orders.invoices'");
    expect(screen.getByText("Left as written")).toBeInTheDocument();
    unmount();
    state.plan = { ...plan(), files: [], ready: true, values: [{ kind: "script", name: "events", line: 3, text: "orders.outbox" }] };
    render(<PrepareDialog detail={detail} isAdmin onClose={vi.fn()} />);
    expect(screen.getByTestId("prepare-values")).toHaveTextContent("events line 3: 'orders.outbox'");
  });
});
