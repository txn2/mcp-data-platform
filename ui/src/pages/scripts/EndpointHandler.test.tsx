import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import type { ScriptEndpoint } from "@/api/portal/hooks/scriptEndpoint";
import { endpointOf, type HandlerDetail } from "@/api/portal/hooks/scriptHandlers";
import { EndpointEditor, EndpointHandler } from "./EndpointHandler";

// One of a script's endpoint handlers (#2171, #2259, #2441): its settings and
// failed requests, which its owner reads, and its setting, which only an
// administrator makes, under one of three access modes. A new endpoint is
// added through EndpointEditor, which the Handlers section opens from Add
// handler; ScriptHandlers.test.tsx covers what that menu offers.

vi.mock("@/api/admin/hooks", () => ({
  useBotChecks: () => ({
    data: {
      key_available: true,
      checks: [
        { name: "default", provider: "pow", usable: true, used_by: [] },
        { name: "signup", provider: "turnstile", usable: true, used_by: [] },
      ],
    },
  }),
}));

const approved: { status: string; version: number }[] = [];
vi.mock("@/api/portal/hooks/approvals", () => ({
  useApprovals: () => ({ data: approved }),
  useOpenFormPreview: () => ({ mutate: vi.fn(), isPending: false, error: null, data: undefined }),
  useRequestApproval: () => ({ mutate: vi.fn(), isPending: false, error: null }),
}));

vi.mock("@/api/portal/hooks/scriptEndpoint", () => ({
  useSetScriptEndpoint: vi.fn(),
  useSetScriptEndpointEnabled: vi.fn(),
}));

let admin = false;
vi.mock("@/stores/auth", () => ({
  useAuthStore: (selector: (s: { isAdmin: () => boolean }) => unknown) => selector({ isAdmin: () => admin }),
}));

import { useSetScriptEndpoint, useSetScriptEndpointEnabled } from "@/api/portal/hooks/scriptEndpoint";

const save = vi.fn();
const toggle = vi.fn();

// handler is an endpoint handler as GET /portal/scripts/{id}/handlers lists
// it: the endpoint's settings with the handler's id, kind and type.
const handler: HandlerDetail = {
  handler: "ep-orders",
  kind: "endpoint",
  type: "endpoint",
  name: "order-lookup",
  endpoint: "orders",
  url: "https://data.example.com/endpoints/orders/{id}",
  methods: ["GET", "POST"],
  path: "/{id}",
  auth_mode: "authenticated",
  callers: ["persona:analyst", "api_key:partner-sync"],
  checked_methods: [],
  enabled: true,
  timeout_seconds: 10,
  max_memory_bytes: 64 << 20,
  max_body_bytes: 1 << 20,
  allowed_origins: ["https://forms.example.com"],
  rate_limit: 600,
  caller_rate_limit: 120,
  updated_by: "admin@example.com",
  updated_at: "2026-10-18T10:00:00Z",
  failures: [
    {
      id: "f1",
      version: 3,
      method: "GET",
      status: 504,
      error: "halted: script exceeded its time limit",
      duration_ms: 10002,
      created_at: "2026-10-18T11:00:00Z",
    },
  ],
};

// A new endpoint, as the Handlers section hands the editor one: what adding
// an endpoint offers, not bound yet.
const fresh: ScriptEndpoint = { name: "order-lookup", bound: false };

function fact(label: string): string {
  const dt = screen.getAllByText(label).find((el) => el.tagName === "DT");
  return dt?.nextElementSibling?.textContent ?? "";
}

function show(over: Partial<HandlerDetail> = {}) {
  render(<EndpointHandler scriptId="script-001" endpoint={endpointOf({ ...handler, ...over })} />);
}

beforeEach(() => {
  vi.clearAllMocks();
  admin = false;
  vi.mocked(useSetScriptEndpoint).mockReturnValue({ mutate: save, isPending: false, error: null } as never);
  vi.mocked(useSetScriptEndpointEnabled).mockReturnValue({ mutate: toggle, isPending: false, error: null } as never);
});

afterEach(cleanup);

describe("EndpointHandler (#2171)", () => {
  it("shows the owner the endpoint and its failed requests, with no controls", () => {
    show();
    expect(fact("Status")).toBe("On");
    expect(screen.getByText("https://data.example.com/endpoints/orders/{id}")).toBeInTheDocument();
    expect(screen.getByText("Authenticated (API key or session)")).toBeInTheDocument();
    expect(screen.getByText("Persona analyst, API key partner-sync")).toBeInTheDocument();
    expect(screen.getByText("64 MiB")).toBeInTheDocument();
    expect(screen.getByText("https://forms.example.com")).toBeInTheDocument();
    expect(screen.getByText("Failed requests")).toBeInTheDocument();
    expect(screen.getByText("504")).toBeInTheDocument();
    expect(screen.getByText(/exceeded its time limit/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Edit" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Turn off" })).toBeNull();
  });

  it("acts on the handler it shows, by its id", () => {
    admin = true;
    show();
    // Each endpoint of a script is its own handler (#2441): turning one off
    // and saving one name that handler, never the script.
    expect(useSetScriptEndpointEnabled).toHaveBeenCalledWith("ep-orders");
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    expect(useSetScriptEndpoint).toHaveBeenLastCalledWith("script-001", "ep-orders");
  });

  it("lets an administrator add a bot_check endpoint that checks only POST", () => {
    admin = true;
    const onDone = vi.fn();
    render(<EndpointEditor scriptId="script-001" endpoint={fresh} onDone={onDone} />);
    // A new endpoint names no handler, so the save adds one.
    expect(useSetScriptEndpoint).toHaveBeenLastCalledWith("script-001", undefined);
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: " contact " } });
    fireEvent.change(screen.getByLabelText("Access"), { target: { value: "bot_check" } });
    fireEvent.click(screen.getAllByLabelText("POST")[0]!);
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
    fireEvent.change(screen.getByLabelText("Bot check"), { target: { value: "signup" } });
    const checked = screen.getAllByLabelText("POST")[1]!;
    fireEvent.click(checked);
    fireEvent.change(screen.getByLabelText("Timeout (seconds)"), { target: { value: "20" } });
    fireEvent.change(screen.getByLabelText("Allowed origins"), {
      target: { value: "https://a.example.com\n\n https://b.example.com " },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    const input = save.mock.calls[0]![0];
    expect(input).toMatchObject({
      endpoint: "contact",
      auth_mode: "bot_check",
      bot_check: "signup",
      checked_methods: ["POST"],
      methods: ["GET", "POST"],
      timeout_seconds: 20,
      max_memory_bytes: 64 << 20,
      allowed_origins: ["https://a.example.com", "https://b.example.com"],
    });
    expect(input).not.toHaveProperty("callers");
    expect(screen.queryByLabelText("Per-caller limit (a minute)")).toBeNull();

    // The save's outcome is handed back, so the section can close the form
    // and show what the save said.
    const saved = { ...fresh, bound: true, warning: "This deployment trusts no proxy for a visitor's address." };
    (save.mock.calls[0]![1] as { onSuccess: (s: ScriptEndpoint) => void }).onSuccess(saved);
    expect(onDone).toHaveBeenCalledWith(saved);
  });

  it("cancels adding an endpoint without saving", () => {
    admin = true;
    const onDone = vi.fn();
    render(<EndpointEditor scriptId="script-001" endpoint={fresh} onDone={onDone} />);
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onDone).toHaveBeenCalledWith();
    expect(save).not.toHaveBeenCalled();
  });

  it("sends an authenticated endpoint's callers, or * for every caller, and a public one with neither", () => {
    admin = true;
    show();
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    expect(screen.getByLabelText("Callers")).toHaveValue("persona:analyst\napi_key:partner-sync");
    fireEvent.change(screen.getByLabelText("Callers"), { target: { value: "persona:ops\n\n api_key:etl " } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(save.mock.calls[0]![0]).toMatchObject({ auth_mode: "authenticated", callers: ["persona:ops", "api_key:etl"] });

    fireEvent.click(screen.getByLabelText("Every authenticated caller"));
    expect(screen.queryByLabelText("Callers")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(save.mock.calls[1]![0]).toMatchObject({ callers: ["*"] });

    fireEvent.change(screen.getByLabelText("Access"), { target: { value: "public" } });
    expect(screen.getByText(/Answers anyone, with no check/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    const pub = save.mock.calls[2]![0];
    expect(pub).toMatchObject({ auth_mode: "public" });
    expect(pub).not.toHaveProperty("callers");
    expect(pub).not.toHaveProperty("bot_check");
  });

  it("lets an administrator turn the endpoint off and edit it, and cancel", () => {
    admin = true;
    show();
    fireEvent.click(screen.getByRole("button", { name: "Turn off" }));
    expect(toggle).toHaveBeenCalledWith(false);
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    expect(screen.getByLabelText("Name")).toHaveValue("orders");
    expect(screen.getByLabelText("Per-caller limit (a minute)")).toHaveValue(120);
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.queryByLabelText("Name")).toBeNull();
    expect(screen.getByRole("button", { name: "Edit" })).toBeInTheDocument();
  });

  it("shows a turned-off endpoint and the save's refusal", () => {
    admin = true;
    vi.mocked(useSetScriptEndpoint).mockReturnValue({
      mutate: save,
      isPending: false,
      error: new Error('endpoint name "orders" is taken'),
    } as never);
    show({ enabled: false, failures: [], auth_mode: "bot_check", bot_check: "default", callers: [], checked_methods: ["POST"] });
    expect(fact("Status")).toBe("Off");
    expect(screen.getByText("No request failed in the last seven days.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Turn on" })).toBeInTheDocument();
    expect(screen.getByText("Bot check: default")).toBeInTheDocument();
    expect(screen.getByText("POST")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Turn on" }));
    expect(toggle).toHaveBeenCalledWith(true);
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    expect(screen.getByText(/is taken/)).toBeInTheDocument();
  });

  it("shows the refusal of turning it on or off", () => {
    admin = true;
    vi.mocked(useSetScriptEndpointEnabled).mockReturnValue({
      mutate: toggle,
      isPending: false,
      error: new Error("a form served to callers with no session needs an approved version"),
    } as never);
    show();
    expect(screen.getByText(/needs an approved version/)).toBeInTheDocument();
  });
});
