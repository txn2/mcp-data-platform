import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import type { ScriptEndpoint } from "@/api/portal/hooks/scriptEndpoint";
import { ScriptEndpointCard } from "./ScriptEndpointCard";

// A script's endpoint (#2171, #2259): its binding and failed requests, which
// its owner reads, and the binding's setting, which only an administrator
// makes, under one of three access modes.
// The card folds by default, so every assertion opens it.

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

vi.mock("@/api/portal/hooks/scriptEndpoint", () => ({
  useScriptEndpoint: vi.fn(),
  useSetScriptEndpoint: vi.fn(),
  useSetScriptEndpointEnabled: vi.fn(),
}));

let admin = false;
vi.mock("@/stores/auth", () => ({
  useAuthStore: (selector: (s: { isAdmin: () => boolean }) => unknown) => selector({ isAdmin: () => admin }),
}));

import { useScriptEndpoint, useSetScriptEndpoint, useSetScriptEndpointEnabled } from "@/api/portal/hooks/scriptEndpoint";

const save = vi.fn();
const toggle = vi.fn();

function query<T>(data: T) {
  return { data, isLoading: false, error: null } as never;
}

const bound: ScriptEndpoint = {
  name: "order-lookup",
  bound: true,
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
    { id: "f1", version: 3, method: "GET", status: 504, error: "halted: script exceeded its time limit", duration_ms: 10002, created_at: "2026-10-18T11:00:00Z" },
  ],
};

beforeEach(() => {
  vi.clearAllMocks();
  admin = false;
  vi.mocked(useScriptEndpoint).mockReturnValue(query(bound));
  vi.mocked(useSetScriptEndpoint).mockReturnValue({ mutate: save, isPending: false, error: null } as never);
  vi.mocked(useSetScriptEndpointEnabled).mockReturnValue({ mutate: toggle, isPending: false, error: null } as never);
});

afterEach(cleanup);

function openCard(): string {
  render(<ScriptEndpointCard scriptId="script-001" />);
  const summary = screen.getByRole("heading", { name: "Endpoint" }).parentElement?.textContent ?? "";
  fireEvent.click(screen.getByRole("heading", { name: "Endpoint" }));
  return summary;
}

describe("ScriptEndpointCard (#2171)", () => {
  it("shows the owner the binding and its failed requests, with no controls", () => {
    expect(openCard()).toContain("On at /endpoints/orders");
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

  it("tells an owner who is not an administrator who binds an unbound script", () => {
    vi.mocked(useScriptEndpoint).mockReturnValue(query({ name: "order-lookup", bound: false }));
    expect(openCard()).toContain("Not bound");
    expect(screen.getByText("Only an administrator can bind a script to an endpoint.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Bind" })).toBeNull();
  });

  it("lets an administrator bind a bot_check endpoint that checks only POST", () => {
    admin = true;
    vi.mocked(useScriptEndpoint).mockReturnValue(query({ name: "order-lookup", bound: false }));
    openCard();
    fireEvent.click(screen.getByRole("button", { name: "Bind" }));
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: " contact " } });
    fireEvent.change(screen.getByLabelText("Access"), { target: { value: "bot_check" } });
    fireEvent.click(screen.getAllByLabelText("POST")[0]!);
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
    fireEvent.change(screen.getByLabelText("Bot check"), { target: { value: "signup" } });
    const checked = screen.getAllByLabelText("POST")[1]!;
    fireEvent.click(checked);
    fireEvent.change(screen.getByLabelText("Timeout (seconds)"), { target: { value: "20" } });
    fireEvent.change(screen.getByLabelText("Allowed origins"), { target: { value: "https://a.example.com\n\n https://b.example.com " } });
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
  });

  it("sends an authenticated endpoint's callers, or * for every caller, and a public one with neither", () => {
    admin = true;
    openCard();
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
    openCard();
    fireEvent.click(screen.getByRole("button", { name: "Turn off" }));
    expect(toggle).toHaveBeenCalledWith(false);
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    expect(screen.getByLabelText("Name")).toHaveValue("orders");
    expect(screen.getByLabelText("Per-caller limit (a minute)")).toHaveValue(120);
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.queryByLabelText("Name")).toBeNull();
  });

  it("shows a turned-off endpoint and the save's refusal", () => {
    admin = true;
    vi.mocked(useScriptEndpoint).mockReturnValue(
      query({ ...bound, enabled: false, failures: [], auth_mode: "bot_check", bot_check: "default", callers: [], checked_methods: ["POST"] }),
    );
    vi.mocked(useSetScriptEndpoint).mockReturnValue({ mutate: save, isPending: false, error: new Error("endpoint name \"orders\" is taken") } as never);
    expect(openCard()).toContain("Off at /endpoints/orders");
    expect(screen.getByText("No request failed in the last seven days.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Turn on" })).toBeInTheDocument();
    expect(screen.getByText("Bot check: default")).toBeInTheDocument();
    expect(screen.getByText("POST")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    expect(screen.getByText(/is taken/)).toBeInTheDocument();
  });

  it("renders nothing where the deployment cannot bind scripts", () => {
    vi.mocked(useScriptEndpoint).mockReturnValue(query(null));
    const { container } = render(<ScriptEndpointCard scriptId="script-001" />);
    expect(container).toBeEmptyDOMElement();
  });
});
