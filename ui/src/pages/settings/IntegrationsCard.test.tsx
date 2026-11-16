import { describe, it, expect, vi, afterEach, beforeEach } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import type { IntegrationCandidate, IntegrationOverview } from "@/api/admin/types";
import * as hooks from "@/api/admin/hooks";
import { IntegrationsCard } from "./IntegrationsCard";

// #2659: Admin > Settings > Integrations, in each state the approved mockups
// draw: no script, the picker, a picker with nothing to offer, a script chosen
// and its last run failed.

vi.mock("@/api/admin/hooks", () => ({
  useIntegrations: vi.fn(),
  useIntegrationScripts: vi.fn(),
  useBindIntegration: vi.fn(),
  useClearIntegration: vi.fn(),
  useRetryIntegration: vi.fn(),
}));

const at = "2026-11-15T12:00:00Z";
const bind = vi.fn();
const clear = vi.fn();
const retry = vi.fn();

function op(over: Partial<IntegrationOverview> = {}): IntegrationOverview {
  return {
    operation: "site_routing", label: "Site routing", function: "route", items_field: "hosts", key_field: "hostname",
    counts: {}, items: [], total: 23, ...over,
  };
}

const bound = op({
  binding: {
    id: "b1", operation: "site_routing", script_id: "s1", interval_seconds: 900, retry_requested: false,
    last_status: "succeeded", last_reason: "change", last_run_id: "r1", last_finished_at: at,
    created_by: "ops@example.com", updated_by: "ops@example.com", created_at: at, updated_at: at,
  },
  script_name: "ingress-routes", running_version: 7, approved_by: "ops@example.com",
  counts: { ready: 21, pending: 1, failed: 1 },
  items: [
    { key: "handbook.pages.example.net", entry: { kind: "pages", site: "handbook", environment: "prod" }, status: "ready", message: "routed" },
    { key: "legacy.example.org", entry: { kind: "domain", site: "old-blog", environment: "prod" }, status: "failed", message: "DNS does not point at the ingress yet" },
    { key: "shop.example.com", entry: { kind: "domain", public: true, site: "shop", environment: "prod" }, status: "pending", message: "" },
    { key: "zz.example.com", entry: { kind: "application", application: "zz" }, status: "ready", message: "" },
  ],
});

function serve(list: IntegrationOverview[], scripts: IntegrationCandidate[] = []) {
  vi.mocked(hooks.useIntegrations).mockReturnValue({ data: { integrations: list }, isLoading: false, error: null } as never);
  vi.mocked(hooks.useIntegrationScripts).mockReturnValue({ data: { scripts }, isSuccess: true } as never);
  vi.mocked(hooks.useBindIntegration).mockReturnValue({ mutate: bind, isPending: false, error: null } as never);
  vi.mocked(hooks.useClearIntegration).mockReturnValue({ mutate: clear, isPending: false } as never);
  vi.mocked(hooks.useRetryIntegration).mockReturnValue({ mutate: retry, isPending: false } as never);
}

beforeEach(() => {
  bind.mockReset();
  clear.mockReset();
  retry.mockReset();
});
afterEach(cleanup);

describe("Integrations settings (#2659)", () => {
  it("lists Site routing with no script, and how many hostnames it would route", () => {
    serve([op()]);
    render(<IntegrationsCard isReadOnly={false} />);
    expect(screen.getByText("Site routing")).toBeInTheDocument();
    expect(screen.getByText("No script")).toBeInTheDocument();
    expect(screen.getByText("route(req)")).toBeInTheDocument();
    expect(screen.getByText("23")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Choose script" })).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: /docs/i })).toBeNull();
  });

  it("chooses a script defining route(req), approving its version as the administrator", () => {
    serve([op()], [
      { id: "s1", name: "ingress-routes", owner_email: "ops@example.com", version: 7, running_version: 0, running_serves: false },
      { id: "s2", name: "gateway-hosts", owner_email: "jane@example.com", version: 3, running_version: 3, approved_by: "ops@example.com", running_serves: true },
    ]);
    render(<IntegrationsCard isReadOnly={false} />);
    fireEvent.click(screen.getByRole("button", { name: "Choose script" }));
    expect(screen.getByText("Choose the site routing script")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("radio", { name: /ingress-routes/ }));
    expect(screen.getByText(/Version 7 defines route\(req\)\. It has not been approved for handlers\./)).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Call it with every item every"), { target: { value: "30" } });
    fireEvent.click(screen.getByRole("button", { name: "Use this script" }));
    expect(bind).toHaveBeenCalledWith({ script_id: "s1", interval_seconds: 1800, approve: true }, expect.anything());

    fireEvent.click(screen.getByRole("radio", { name: /gateway-hosts/ }));
    expect(screen.queryByText(/has not been approved/)).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Use this script" }));
    expect(bind).toHaveBeenLastCalledWith({ script_id: "s2", interval_seconds: 1800, approve: false }, expect.anything());
  });

  it("says when no script defines route(req)", () => {
    serve([op()], []);
    render(<IntegrationsCard isReadOnly={false} />);
    fireEvent.click(screen.getByRole("button", { name: "Choose script" }));
    expect(screen.getByTestId("integration-no-scripts")).toHaveTextContent("No script defines route(req)");
    expect(screen.queryByRole("button", { name: "Use this script" })).toBeNull();
    expect(screen.getByRole("link", { name: "New automation" })).toBeInTheDocument();
  });

  it("shows a chosen script, its last run, the counts and the failed items first", () => {
    serve([bound]);
    render(<IntegrationsCard isReadOnly={false} />);
    expect(screen.getByText("On")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "ingress-routes" })).toHaveAttribute("href", "/portal/automations/s1");
    expect(screen.getByText(/version 7, approved for handlers by ops@example.com/)).toBeInTheDocument();
    expect(screen.getByText(/after an item changed/)).toBeInTheDocument();
    expect(screen.getByText("21 ready")).toBeInTheDocument();
    expect(screen.getByText("1 failed")).toBeInTheDocument();
    const rows = screen.getAllByRole("row");
    expect(rows[1]).toHaveTextContent("legacy.example.org");
    expect(screen.queryByText("zz.example.com")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Show all 23" }));
    expect(screen.getByText("zz.example.com")).toBeInTheDocument();
    expect(screen.getByText("shop · prod · public domain")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Retry now" }));
    expect(retry).toHaveBeenCalledOnce();
    fireEvent.click(screen.getByRole("button", { name: "Clear" }));
    expect(clear).toHaveBeenCalledOnce();
  });

  it("shows a failed run's error to the administrator, with the run", () => {
    serve([
      op({
        ...bound,
        binding: { ...bound.binding!, last_status: "failed", last_error: "ingress-routes:41: route: ingress API answered 503" },
        counts: { failed: 23 },
      }),
    ]);
    render(<IntegrationsCard isReadOnly={false} />);
    expect(screen.getByText("Last run failed")).toBeInTheDocument();
    expect(screen.getByText("ingress-routes:41: route: ingress API answered 503")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Open the run" })).toHaveAttribute("href", "/portal/automations/s1/runs/r1");
  });

  it("offers no actions on a read-only deployment, and says when the read failed", () => {
    serve([bound]);
    render(<IntegrationsCard isReadOnly />);
    expect(screen.queryByRole("button", { name: "Retry now" })).toBeNull();
    cleanup();
    vi.mocked(hooks.useIntegrations).mockReturnValue({ data: undefined, isLoading: false, error: new Error("x") } as never);
    render(<IntegrationsCard isReadOnly={false} />);
    expect(screen.getByText("The integrations could not be read.")).toBeInTheDocument();
  });
});
