import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import type { ScriptEndpoint } from "@/api/portal/hooks/scriptEndpoint";
import { endpointOf, type HandlerDetail } from "@/api/portal/hooks/scriptHandlers";
import { EndpointEditor, EndpointHandler } from "./EndpointHandler";

// A form's endpoint handler (#2178, #2179, #2441): where its submissions are
// stored, chosen once when its source is made, and the personas a script may
// decrypt its sensitive fields as.

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
  useSetScriptEndpoint: vi.fn(),
  useSetScriptEndpointEnabled: vi.fn(),
}));

let admin = true;
vi.mock("@/stores/auth", () => ({
  useAuthStore: (selector: (s: { isAdmin: () => boolean }) => unknown) => selector({ isAdmin: () => admin }),
}));

import { useSetScriptEndpoint, useSetScriptEndpointEnabled } from "@/api/portal/hooks/scriptEndpoint";

const save = vi.fn();
const toggle = vi.fn();

// newForm is what adding an endpoint to a form offers before its source is
// made: the connections its submissions could be stored on.
const newForm: ScriptEndpoint = { name: "contact", bound: false, is_form: true, form_connections: ["lake", "scratch"] };

const storedForm: HandlerDetail = {
  handler: "ep-contact",
  kind: "endpoint",
  type: "form",
  name: "contact",
  endpoint: "contact",
  url: "https://data.example.com/endpoints/contact",
  methods: ["GET", "POST"],
  auth_mode: "public",
  enabled: true,
  is_form: true,
  form: { source: "form-contact", table: "webhook_form_contact", connection: "scratch", decrypt_personas: ["credit"] },
};

function showHandler(over: Partial<HandlerDetail> = {}) {
  render(<EndpointHandler scriptId="script-001" endpoint={endpointOf({ ...storedForm, ...over })} />);
}

function addEndpoint(e: ScriptEndpoint = newForm) {
  render(<EndpointEditor scriptId="script-001" endpoint={e} onDone={vi.fn()} />);
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(useSetScriptEndpoint).mockReturnValue({ mutate: save, isPending: false, error: null } as never);
  vi.mocked(useSetScriptEndpointEnabled).mockReturnValue({ mutate: toggle, isPending: false, error: null } as never);
});

afterEach(() => {
  cleanup();
  admin = true;
  approved = [];
});

describe("EndpointHandler on a form", () => {
  it("offers a form endpoint's embed snippet (#2331)", () => {
    showHandler({ page_url: "https://data.example.com/forms/contact" });
    expect(screen.getByRole("heading", { name: "Embed on a site" })).toBeInTheDocument();
    expect(screen.getByLabelText("Embed snippet").textContent).toContain('src="https://data.example.com/forms/contact"');
  });

  it("offers no snippet for an endpoint with no page", () => {
    showHandler();
    expect(screen.queryByRole("heading", { name: "Embed on a site" })).not.toBeInTheDocument();
  });

  it("adds a form's endpoint only once a connection is chosen, and sends it with the decrypt personas", () => {
    addEndpoint();
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

  it("picks the only connection there is without asking", () => {
    addEndpoint({ ...newForm, form_connections: ["scratch"] });
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "contact" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(save.mock.calls[0]?.[0]).toMatchObject({ connection: "scratch" });
  });

  it("shows where a stored form's submissions are, and never sends the connection again", () => {
    showHandler();
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
    addEndpoint({ ...newForm, form_connections: [] });
    expect(screen.getByText(/No Trino connection has a scratch catalog/)).toBeInTheDocument();
  });
});

// #2494: a form's owner previews it, publishes it for signed-in people, and
// chooses "anyone" only once a version is approved; a bot check stays an
// administrator's choice.
describe("a form's owner (#2494)", () => {
  it("adds an internal endpoint, and is offered public access only once a version is approved", () => {
    admin = false;
    approved = [];
    addEndpoint({ ...newForm, form_connections: ["scratch"] });
    const access = screen.getByLabelText("Access") as HTMLSelectElement;
    expect(Array.from(access.options).map((o) => o.value)).toEqual(["authenticated"]);
    cleanup();

    approved = [{ status: "approved", version: 2 }];
    addEndpoint({ ...newForm, form_connections: ["scratch"] });
    const access2 = screen.getByLabelText("Access") as HTMLSelectElement;
    expect(Array.from(access2.options).map((o) => o.value)).toEqual(["authenticated", "public"]);
  });

  it("previews a published form, asks for public access, and edits and turns off their own form", () => {
    admin = false;
    approved = [];
    showHandler({ auth_mode: "authenticated", callers: ["*"] });
    expect(screen.getByRole("button", { name: "Preview form" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Request public access" })).toBeInTheDocument();
    expect(screen.getByTestId("form-public-status")).toHaveTextContent("No version is approved");
    fireEvent.click(screen.getByRole("button", { name: "Turn off" }));
    expect(toggle).toHaveBeenCalledWith(false);
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    const access = screen.getByLabelText("Access") as HTMLSelectElement;
    expect(Array.from(access.options).map((o) => o.value)).toEqual(["authenticated"]);
    cleanup();

    approved = [{ status: "approved", version: 2 }];
    showHandler({ auth_mode: "authenticated", callers: ["*"] });
    expect(screen.getByTestId("form-public-status")).toHaveTextContent("Version 2 is approved");
  });

  it("keeps a public form's mode in the list a form's owner edits it with", () => {
    admin = false;
    approved = [];
    showHandler();
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    const access = screen.getByLabelText("Access") as HTMLSelectElement;
    // An administrator made it public; its owner can still read the mode it
    // is in, and keep it, while being offered only what they may choose.
    expect(access.value).toBe("public");
    expect(Array.from(access.options).map((o) => o.value)).toEqual(["authenticated", "public"]);
  });
});
