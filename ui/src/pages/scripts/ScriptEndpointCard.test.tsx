import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import type { ScriptEndpoint } from "@/api/portal/hooks/scriptEndpoint";
import { ScriptEndpointCard } from "./ScriptEndpointCard";

// A script's endpoint (#2171): its binding and failed requests, which its
// owner reads, and the binding's setting, which only an administrator makes.
// The card folds by default, so every assertion opens it.

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
  auth_mode: "signed_in",
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
    expect(screen.getByText("Signed in")).toBeInTheDocument();
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

  it("lets an administrator bind a script, sending the form as a binding", () => {
    admin = true;
    vi.mocked(useScriptEndpoint).mockReturnValue(query({ name: "order-lookup", bound: false }));
    openCard();
    fireEvent.click(screen.getByRole("button", { name: "Bind" }));
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: " contact " } });
    fireEvent.change(screen.getByLabelText("Callers"), { target: { value: "public" } });
    fireEvent.click(screen.getByLabelText("POST"));
    fireEvent.click(screen.getByLabelText("Reads without a check"));
    fireEvent.change(screen.getByLabelText("Timeout (seconds)"), { target: { value: "20" } });
    fireEvent.change(screen.getByLabelText("Allowed origins"), { target: { value: "https://a.example.com\n\n https://b.example.com " } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    const input = save.mock.calls[0]![0];
    expect(input).toMatchObject({
      endpoint: "contact",
      auth_mode: "public",
      methods: ["GET", "POST"],
      open_reads: true,
      timeout_seconds: 20,
      max_memory_bytes: 64 << 20,
      allowed_origins: ["https://a.example.com", "https://b.example.com"],
    });
    expect(screen.queryByLabelText("Per-caller limit (a minute)")).toBeNull();
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
    vi.mocked(useScriptEndpoint).mockReturnValue(query({ ...bound, enabled: false, failures: [], auth_mode: "public", open_reads: true }));
    vi.mocked(useSetScriptEndpoint).mockReturnValue({ mutate: save, isPending: false, error: new Error("endpoint name \"orders\" is taken") } as never);
    expect(openCard()).toContain("Off at /endpoints/orders");
    expect(screen.getByText("No request failed in the last seven days.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Turn on" })).toBeInTheDocument();
    expect(screen.getByText("Yes")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    expect(screen.getByText(/is taken/)).toBeInTheDocument();
  });

  it("renders nothing where the deployment cannot bind scripts", () => {
    vi.mocked(useScriptEndpoint).mockReturnValue(query(null));
    const { container } = render(<ScriptEndpointCard scriptId="script-001" />);
    expect(container).toBeEmptyDOMElement();
  });
});
