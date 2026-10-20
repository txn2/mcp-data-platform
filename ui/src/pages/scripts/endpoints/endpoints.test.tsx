import { beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { mockAdminEndpoints, mockEndpointDetail } from "@/mocks/data/endpoints";

// The admin Endpoints page (#2208): every binding with its automation and
// owner, a public one marked apart, filters, a row opening the endpoint, and
// the endpoint's page with its figures, refusals, failures and on/off.

const h = vi.hoisted(() => ({
  list: { data: undefined as unknown, isLoading: false, isError: false },
  detail: { data: undefined as unknown, isLoading: false, isError: false },
  ranges: [] as string[],
  setEnabled: vi.fn(),
}));

vi.mock("@/api/admin/hooks", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/admin/hooks")>();
  return {
    ...actual,
    useAdminEndpoints: (range: string) => {
      h.ranges.push(range);
      return h.list;
    },
    useAdminEndpoint: () => h.detail,
  };
});

vi.mock("@/api/portal/hooks/scriptEndpoint", () => ({
  useSetScriptEndpointEnabled: () => ({ mutate: h.setEnabled, isPending: false }),
}));

vi.mock("@/api/portal/hooks/scriptListFilter", () => ({
  useScriptListing: () => ({
    isLoading: false,
    data: { data: [{ script: { id: "script-001", name: "daily-sales-report", display_name: "Daily Sales Report", owner_email: "sarah.chen@example.com" } }] },
  }),
}));

vi.mock("@tanstack/react-query", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-query")>();
  return { ...actual, useQueryClient: () => ({ invalidateQueries: vi.fn() }) };
});

import { EndpointsTab } from "./EndpointsTab";
import { EndpointDetailPage } from "./EndpointDetailPage";
import { AdminScriptRoutes } from "../AdminScriptRoutes";

beforeEach(() => {
  cleanup();
  vi.clearAllMocks();
  h.ranges.length = 0;
  h.list = { data: { range: "day", since: "", endpoints: mockAdminEndpoints }, isLoading: false, isError: false };
  h.detail = { data: mockEndpointDetail("contact", "day"), isLoading: false, isError: false };
});

describe("EndpointsTab", () => {
  it("lists every binding with its automation and owner, marking a public one", () => {
    render(<EndpointsTab onNavigate={vi.fn()} />);
    const rows = screen.getAllByRole("row").slice(1);
    expect(rows).toHaveLength(2);
    const contact = within(rows[0]!);
    expect(contact.getByText("contact")).toBeInTheDocument();
    expect(contact.getByText("Contact Form")).toBeInTheDocument();
    expect(contact.getByText("marcus.webb@example.com")).toBeInTheDocument();
    expect(contact.getByText("Public")).toHaveAttribute("data-access", "public");
    expect(contact.getByText("1,873")).toBeInTheDocument();
    expect(within(rows[1]!).getByText("Signed in")).toHaveAttribute("data-access", "signed_in");
  });

  it("opens an endpoint on row click", () => {
    const onNavigate = vi.fn();
    render(<EndpointsTab onNavigate={onNavigate} />);
    fireEvent.click(screen.getByText("store-lookup"));
    expect(onNavigate).toHaveBeenCalledWith("/admin/automations/endpoints/store-lookup");
  });

  it("reads an empty deployment as empty, and a failed read as failed", () => {
    h.list = { data: { range: "day", since: "", endpoints: [] }, isLoading: false, isError: false };
    render(<EndpointsTab onNavigate={vi.fn()} />);
    expect(screen.getByText(/No automation answers HTTP requests yet/)).toBeInTheDocument();
    cleanup();
    h.list = { data: undefined, isLoading: false, isError: true };
    render(<EndpointsTab onNavigate={vi.fn()} />);
    expect(screen.getByText("The endpoints could not be read.")).toBeInTheDocument();
  });

  it("picks an automation to bind on its own page", () => {
    const onNavigate = vi.fn();
    render(<EndpointsTab onNavigate={onNavigate} />);
    fireEvent.click(screen.getByRole("button", { name: "Bind an automation" }));
    fireEvent.click(screen.getByRole("button", { name: /Daily Sales Report/ }));
    expect(onNavigate).toHaveBeenCalledWith("/admin/automations/script-001");
  });
});

describe("EndpointDetailPage", () => {
  it("shows the figures, refusals by reason and the failed requests", () => {
    render(<EndpointDetailPage name="contact" onNavigate={vi.fn()} />);
    expect(screen.getByRole("heading", { name: "contact" })).toBeInTheDocument();
    expect(screen.getByText("Bot check")).toBeInTheDocument();
    expect(screen.getByText("1,840")).toBeInTheDocument();
    expect(screen.getByText("handle(req) failed: the CRM answered 503")).toBeInTheDocument();
    expect(screen.queryByText("Top callers"), "a public endpoint has no signed-in callers").toBeNull();
  });

  it("turns the endpoint off and opens its automation to edit it", () => {
    const onNavigate = vi.fn();
    render(<EndpointDetailPage name="contact" onNavigate={onNavigate} />);
    fireEvent.click(screen.getByRole("button", { name: "Turn off" }));
    expect(h.setEnabled).toHaveBeenCalledWith(false, expect.anything());
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    expect(onNavigate).toHaveBeenCalledWith("/admin/automations/script-002");
    fireEvent.click(screen.getByRole("button", { name: /All endpoints/ }));
    expect(onNavigate).toHaveBeenCalledWith("/admin/automations/endpoints");
  });

  it("names the busiest callers of a signed-in endpoint", () => {
    h.detail = { data: mockEndpointDetail("store-lookup", "week"), isLoading: false, isError: false };
    render(<EndpointDetailPage name="store-lookup" onNavigate={vi.fn()} />);
    expect(screen.getByText("sarah.chen@example.com")).toBeInTheDocument();
    expect(screen.getByText("No failed request in the last 7 days.")).toBeInTheDocument();
  });
});

describe("AdminScriptRoutes", () => {
  it("routes the Endpoints tab and an endpoint before an automation's address", () => {
    render(<AdminScriptRoutes route="/admin/automations/endpoints/contact" onNavigate={vi.fn()} />);
    expect(screen.getByRole("heading", { name: "contact" })).toBeInTheDocument();
  });

  it("shows the tab the address names, after Back too", () => {
    const { rerender } = render(<AdminScriptRoutes route="/admin/automations/endpoints" onNavigate={vi.fn()} />);
    expect(screen.getByRole("tab", { name: "Endpoints" })).toHaveAttribute("aria-selected", "true");
    rerender(<AdminScriptRoutes route="/admin/automations" onNavigate={vi.fn()} />);
    expect(screen.getByRole("tab", { name: "Automations" })).toHaveAttribute("aria-selected", "true");
  });
});
