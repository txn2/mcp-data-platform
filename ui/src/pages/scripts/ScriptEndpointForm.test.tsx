import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import type { ScriptEndpoint } from "@/api/portal/hooks/scriptEndpoint";
import { ScriptEndpointCard } from "./ScriptEndpointCard";

// A form's endpoint (#2178, #2179): where its submissions are stored, chosen
// once when its source is made, and the personas a script may decrypt its
// sensitive fields as.

vi.mock("@/api/admin/hooks", () => ({
  useBotChecks: () => ({ data: { key_available: true, checks: [] } }),
  usePersonas: () => ({ data: { personas: [{ name: "analyst" }, { name: "credit" }] }, isLoading: false }),
}));

let approved: { status: string; version: number }[] = [];
vi.mock("@/api/portal/hooks/approvals", () => ({
  useApprovals: () => ({ data: approved }),
  useOpenFormPreview: () => ({ mutate: vi.fn(), isPending: false, error: null, data: undefined }),
  useRequestApproval: () => ({ mutate: vi.fn(), isPending: false, error: null }),
}));

vi.mock("@/api/portal/hooks/scriptEndpoint", () => ({
  useScriptEndpoint: vi.fn(),
  useSetScriptEndpoint: vi.fn(),
  useSetScriptEndpointEnabled: vi.fn(),
}));

let admin = true;
vi.mock("@/stores/auth", () => ({
  useAuthStore: (selector: (s: { isAdmin: () => boolean }) => unknown) => selector({ isAdmin: () => admin }),
}));

import { useScriptEndpoint, useSetScriptEndpoint, useSetScriptEndpointEnabled } from "@/api/portal/hooks/scriptEndpoint";

const save = vi.fn();

const unboundForm: ScriptEndpoint = { name: "contact", bound: false, is_form: true, form_connections: ["lake", "scratch"] };

const storedForm: ScriptEndpoint = {
  name: "contact",
  bound: true,
  endpoint: "contact",
  url: "https://data.example.com/endpoints/contact",
  methods: ["GET", "POST"],
  auth_mode: "public",
  enabled: true,
  is_form: true,
  form: { source: "form-contact", table: "webhook_form_contact", connection: "scratch", decrypt_personas: ["credit"] },
};

function show(e: ScriptEndpoint) {
  vi.mocked(useScriptEndpoint).mockReturnValue({ data: e, isLoading: false, error: null } as never);
  render(<ScriptEndpointCard scriptId="script-001" />);
  fireEvent.click(screen.getByRole("heading", { name: "Endpoint" }));
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(useSetScriptEndpoint).mockReturnValue({ mutate: save, isPending: false, error: null } as never);
  vi.mocked(useSetScriptEndpointEnabled).mockReturnValue({ mutate: vi.fn(), isPending: false, error: null } as never);
});

afterEach(() => {
  cleanup();
  admin = true;
  approved = [];
});

describe("ScriptEndpointCard on a form", () => {
  it("offers a bound form's embed snippet (#2331)", () => {
    show({ ...storedForm, page_url: "https://data.example.com/forms/contact" });
    expect(screen.getByRole("heading", { name: "Embed on a site" })).toBeInTheDocument();
    expect(screen.getByLabelText("Embed snippet").textContent).toContain('src="https://data.example.com/forms/contact"');
  });

  it("offers no snippet for an endpoint with no page", () => {
    show(storedForm);
    expect(screen.queryByRole("heading", { name: "Embed on a site" })).not.toBeInTheDocument();
  });

  it("says an unbound form shows a page and stores its submissions", () => {
    show(unboundForm);
    expect(screen.getByText(/Bind it to an endpoint to show it at \/forms\//)).toBeInTheDocument();
  });

  it("binds a form only once a connection is chosen, and sends it with the decrypt personas", () => {
    show(unboundForm);
    fireEvent.click(screen.getByRole("button", { name: "Publish" }));
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "contact" } });
    fireEvent.change(screen.getByLabelText("Access"), { target: { value: "public" } });
    const saveButton = screen.getByRole("button", { name: "Save" });
    expect(saveButton).toBeDisabled();
    fireEvent.change(screen.getByLabelText("Connection"), { target: { value: "lake" } });
    fireEvent.click(screen.getByRole("checkbox", { name: "credit" }));
    expect(saveButton).toBeEnabled();
    fireEvent.click(saveButton);
    expect(save).toHaveBeenCalledWith(
      expect.objectContaining({ endpoint: "contact", connection: "lake", decrypt_personas: ["credit"] }),
      expect.anything(),
    );
  });

  it("shows where a stored form's submissions are, and never sends the connection again", () => {
    show(storedForm);
    expect(screen.getAllByText("Stored in webhook_form_contact on scratch").length).toBeGreaterThan(0);
    expect(screen.getByText("credit")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    expect(screen.queryByRole("combobox", { name: "Connection" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("checkbox", { name: "analyst" }));
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    const sent = save.mock.calls[0]?.[0] as Record<string, unknown>;
    expect(sent).not.toHaveProperty("connection");
    expect(sent.decrypt_personas).toEqual(["credit", "analyst"]);
  });

  it("says no connection can hold a form's submissions", () => {
    show({ ...unboundForm, form_connections: [] });
    fireEvent.click(screen.getByRole("button", { name: "Publish" }));
    expect(screen.getByText(/No Trino connection has a scratch catalog/)).toBeInTheDocument();
  });
});

// #2494: a form's owner previews it, publishes it for signed-in people, and
// chooses "anyone" only once a version is approved; a bot check stays an
// administrator's choice.
describe("a form's owner (#2494)", () => {
  it("publishes an internal form and offers public access only once a version is approved", () => {
    admin = false;
    approved = [];
    show({ ...unboundForm, form_connections: ["scratch"] });
    expect(screen.getByText(/This form is not published/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Preview form" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Request public access" })).toBeInTheDocument();
    expect(screen.getByTestId("form-public-status")).toHaveTextContent("No version is approved");

    fireEvent.click(screen.getByRole("button", { name: "Publish" }));
    const access = screen.getByLabelText("Access") as HTMLSelectElement;
    expect(Array.from(access.options).map((o) => o.value)).toEqual(["authenticated"]);
    cleanup();

    approved = [{ status: "approved", version: 2 }];
    show({ ...unboundForm, form_connections: ["scratch"] });
    expect(screen.getByTestId("form-public-status")).toHaveTextContent("Version 2 is approved");
    fireEvent.click(screen.getByRole("button", { name: "Publish" }));
    const access2 = screen.getByLabelText("Access") as HTMLSelectElement;
    expect(Array.from(access2.options).map((o) => o.value)).toEqual(["authenticated", "public"]);
  });
});
