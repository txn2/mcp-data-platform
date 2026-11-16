import { describe, it, expect, vi, afterEach, beforeEach } from "vitest";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { resetSitesMock } from "@/mocks/handlers/sites";
import { renderWithQuery } from "@/test/applicationHarness";
import { useAuthStore } from "@/stores/auth";
import type { Site } from "@/api/portal/hooks/sites";
import { RoutingCell } from "./RoutingCell";
import { SiteDomainsTab } from "./SiteDomains";

// #2659: the Site routing status on every address and domain row.

const at = "2026-11-15T12:00:00Z";

afterEach(cleanup);

describe("the routing cell (#2659)", () => {
  it("says pending until the script answers, and offers no retry", () => {
    render(<RoutingCell routing={{ status: "pending", message: "" }} onRetry={vi.fn()} />);
    expect(screen.getByText("Pending")).toBeInTheDocument();
    expect(screen.getByText("Waiting for the routing script")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Retry" })).toBeNull();
  });

  it("shows the script's message for ready and failed, and Retry on a failure", () => {
    const onRetry = vi.fn();
    render(<RoutingCell routing={{ status: "failed", message: "DNS does not point at the ingress yet", checked_at: at }} onRetry={onRetry} />);
    expect(screen.getByText("Failed")).toBeInTheDocument();
    expect(screen.getByText("DNS does not point at the ingress yet")).toBeInTheDocument();
    expect(screen.getByText(/^checked /)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(onRetry).toHaveBeenCalledOnce();
  });

  it("says why a retry was refused, under a failed row only", () => {
    const why = "the script last started under 15s ago; ask again in 9s: asked too soon";
    const { unmount } = render(<RoutingCell routing={{ status: "failed", message: "no route" }} onRetry={() => {}} retryError={why} />);
    expect(screen.getByRole("alert")).toHaveTextContent(why);
    unmount();
    render(<RoutingCell routing={{ status: "ready", message: "routed" }} onRetry={() => {}} retryError={why} />);
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("renders a message holding HTML as text (criterion 12)", () => {
    const { container } = render(<RoutingCell routing={{ status: "failed", message: '<b>bold</b><img src=x onerror="alert(1)">' }} />);
    expect(screen.getByText('<b>bold</b><img src=x onerror="alert(1)">')).toBeInTheDocument();
    expect(container.querySelector("b")).toBeNull();
    expect(container.querySelector("img")).toBeNull();
  });

  it("shows the run's error with a link to the run to an administrator only", () => {
    const { rerender } = render(<RoutingCell routing={{ status: "failed", message: "The site routing script failed", script_failed: true }} />);
    expect(screen.queryByText("Seen by administrators only")).toBeNull();
    rerender(
      <RoutingCell
        routing={{ status: "failed", message: "The site routing script failed", script_failed: true, run_id: "r1", run_script_id: "s1", run_error: "ingress API answered 503" }}
      />,
    );
    expect(screen.getByText("Seen by administrators only")).toBeInTheDocument();
    expect(screen.getByText("ingress API answered 503")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Open the run" })).toHaveAttribute("href", "/portal/automations/s1/runs/r1");
  });
});

function site(over: Partial<Site> = {}): Site {
  return {
    id: "site_1", name: "handbook", description: "", owner_email: "owner@example.com", main_environment: "prod",
    environments: [
      { name: "dev", release: 1, requires_approval: false, public_domains: [], internal_view: false, keep_releases: 50, keep_releases_default: true, releases_kept: 1, updated_at: at },
      { name: "prod", release: 1, requires_approval: false, public_domains: [], internal_view: false, keep_releases: 50, keep_releases_default: true, releases_kept: 1, updated_at: at },
    ],
    created_by: "owner@example.com", created_at: at, updated_at: at,
    address: "https://handbook.pages.example.net", addresses: {},
    pages_addresses: { dev: "https://handbook--dev.pages.example.net", prod: "https://handbook.pages.example.net" },
    domains: [{ hostname: "handbook.example.com", environment: "prod", public: true, created_by: "owner@example.com", created_at: at }],
    permissions: { manage: true, own: true, delete: false },
    ...over,
  };
}

describe("the Domains tab's Routing column (#2659)", () => {
  beforeEach(() => {
    resetSitesMock();
    useAuthStore.setState({ user: { email: "owner@example.com", is_admin: false } as never });
  });

  it("shows no Routing column while no script is chosen (criterion 1)", () => {
    renderWithQuery(<SiteDomainsTab site={site()} />);
    expect(screen.queryByRole("columnheader", { name: "Routing" })).toBeNull();
    expect(screen.queryByTestId("routing-cell")).toBeNull();
  });

  it("shows each address's and domain's status while one is", () => {
    renderWithQuery(
      <SiteDomainsTab
        site={site({
          pages_routing: { prod: { status: "ready", message: "routed" }, dev: { status: "pending", message: "" } },
          domains: [
            { hostname: "handbook.example.com", environment: "prod", public: true, created_by: "o", created_at: at,
              routing: { status: "failed", message: "The site routing script failed", script_failed: true } },
          ],
        })}
      />,
    );
    expect(screen.getAllByRole("columnheader", { name: "Routing" })).toHaveLength(2);
    const domainRow = screen.getByText("handbook.example.com").closest("tr")!;
    expect(within(domainRow).getByText("The site routing script failed")).toBeInTheDocument();
    expect(within(domainRow).getByRole("button", { name: "Retry" })).toBeInTheDocument();
    expect(screen.getByText("routed")).toBeInTheDocument();
  });
});
