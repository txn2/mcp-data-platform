import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import type { ScriptEndpoint, ScriptEndpointInput } from "@/api/portal/hooks/scriptEndpoint";
import { endpointOf, type HandlerDetail } from "@/api/portal/hooks/scriptHandlers";
import { EndpointHandler } from "./EndpointHandler";
import { refusalLabel } from "./endpoints/endpointLabels";

// A public application's endpoint (#2553, #2554, #2556): a catch-all path, the
// sites a page may frame, call and post to, and a per-address limit, with the
// warning a save carries when the deployment trusts no proxy. Shown on the
// endpoint handler (#2441).

vi.mock("@/api/admin/hooks", () => ({
  useBotChecks: () => ({ data: { key_available: true, checks: [] } }),
  usePersonas: () => ({ data: { personas: [] }, isLoading: false }),
}));

vi.mock("@/api/portal/hooks/approvals", () => ({
  useApprovals: () => ({ data: [] }),
  useOpenFormPreview: () => ({ mutate: vi.fn(), isPending: false, error: null, data: undefined }),
  useRequestApproval: () => ({ mutate: vi.fn(), isPending: false, error: null }),
}));

vi.mock("@/api/portal/hooks/scriptEndpoint", () => ({
  useSetScriptEndpoint: vi.fn(),
  useSetScriptEndpointEnabled: vi.fn(),
}));

vi.mock("@/stores/auth", () => ({
  useAuthStore: (selector: (s: { isAdmin: () => boolean }) => unknown) => selector({ isAdmin: () => true }),
}));

import { useSetScriptEndpoint, useSetScriptEndpointEnabled } from "@/api/portal/hooks/scriptEndpoint";

const save = vi.fn();

const storefront: HandlerDetail = {
  handler: "ep-storefront",
  kind: "endpoint",
  type: "endpoint",
  name: "storefront",
  endpoint: "storefront",
  url: "https://platform.example.com/endpoints/storefront",
  methods: ["GET", "POST"],
  path: "/{route...}",
  auth_mode: "public",
  enabled: true,
  response_mode: "page",
  page_address: "https://shop.pages.example.net/",
  page_cdns: ["https://js.stripe.com"],
  page_frame_origins: ["https://js.stripe.com", "https://*.js.stripe.com"],
  page_connect_origins: ["https://api.stripe.com"],
  rate_limit: 600,
  ip_rate_limit: 30,
};

function show(e: HandlerDetail) {
  render(<EndpointHandler scriptId="script-001" endpoint={endpointOf(e)} />);
}

const saved = endpointOf(storefront);

function fact(label: string): string {
  const dt = screen.getAllByText(label).find((el) => el.tagName === "DT");
  return dt?.nextElementSibling?.textContent ?? "";
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(useSetScriptEndpoint).mockReturnValue({ mutate: save, isPending: false, error: null } as never);
  vi.mocked(useSetScriptEndpointEnabled).mockReturnValue({ mutate: vi.fn(), isPending: false, error: null } as never);
});

afterEach(cleanup);

describe("a page endpoint handler", () => {
  it("names a catch-all path, the per-address limit and each list the policy opens", () => {
    show(storefront);
    expect(fact("Path")).toBe("/{route...} · every path under it, as route");
    expect(fact("Per-address limit")).toBe("30 a minute for each address, on each replica");
    expect(fact("Loads from")).toBe("https://js.stripe.com");
    expect(fact("Framed sites")).toBe("https://js.stripe.com, https://*.js.stripe.com");
    expect(fact("Called sites")).toBe("https://api.stripe.com");
    expect(fact("Form destinations")).toBe("None");
  });

  it("shows no per-address limit when none is set, and no page lists on an api endpoint", () => {
    show({ ...storefront, ip_rate_limit: 0, response_mode: "api", path: "/{id}" });
    expect(screen.queryByText("Per-address limit")).not.toBeInTheDocument();
    expect(screen.queryByText("Framed sites")).not.toBeInTheDocument();
    expect(fact("Path")).toBe("/{id}");
  });
});

describe("editing a page endpoint", () => {
  it("sends each list and the per-address limit, and shows the save's warning", () => {
    show(storefront);
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    expect(screen.getByText(/A last segment written \{name\.\.\.\} matches the rest of the path/)).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Form destinations"), { target: { value: "https://checkout.example-pay.test\n\n" } });
    fireEvent.change(screen.getByLabelText("Per-address limit (a minute)"), { target: { value: "45" } });
    save.mockImplementation((_input: ScriptEndpointInput, opts: { onSuccess: (saved: ScriptEndpoint) => void }) =>
      opts.onSuccess({ ...saved, warning: "This deployment trusts no proxy for a visitor's address." }),
    );
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    const sent = save.mock.calls[0]?.[0] as ScriptEndpointInput;
    expect(sent.page_frame_origins).toEqual(["https://js.stripe.com", "https://*.js.stripe.com"]);
    expect(sent.page_connect_origins).toEqual(["https://api.stripe.com"]);
    expect(sent.page_form_origins).toEqual(["https://checkout.example-pay.test"]);
    expect(sent.ip_rate_limit).toBe(45);
    expect(screen.getByTestId("endpoint-save-warning").textContent).toBe(
      "Saved. This deployment trusts no proxy for a visitor's address.",
    );
  });

  it("sends no lists from an api endpoint, and 0 for an emptied per-address limit", () => {
    show(storefront);
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    fireEvent.change(screen.getByLabelText("Answers with"), { target: { value: "api" } });
    expect(screen.queryByLabelText("Framed sites")).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Per-address limit (a minute)"), { target: { value: "" } });
    save.mockImplementation((_input: ScriptEndpointInput, opts: { onSuccess: (s: ScriptEndpoint) => void }) => opts.onSuccess(saved));
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    const sent = save.mock.calls[0]?.[0] as ScriptEndpointInput;
    expect(sent.page_frame_origins).toEqual([]);
    expect(sent.page_cdns).toEqual([]);
    expect(sent.ip_rate_limit).toBe(0);
    expect(screen.queryByTestId("endpoint-save-warning")).not.toBeInTheDocument();
  });
});

it("labels a per-address refusal", () => {
  expect(refusalLabel("ip_rate_limit")).toBe("Per-address limit");
});
