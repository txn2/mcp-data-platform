import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import type { ScriptGrant } from "@/api/portal/hooks/scripts";
import { ScriptGrantsCard } from "./ScriptGrantsCard";

// Who other than the owner may run a script (#1846): the grants, adding one,
// withdrawing one. The card folds by default, so every assertion opens it.

vi.mock("@/api/portal/hooks/scripts", () => ({
  useScriptGrants: vi.fn(),
  useAddScriptGrant: vi.fn(),
  useRemoveScriptGrant: vi.fn(),
}));

import {
  useAddScriptGrant,
  useRemoveScriptGrant,
  useScriptGrants,
} from "@/api/portal/hooks/scripts";

const mockGrants = vi.mocked(useScriptGrants);
const add = vi.fn();
const remove = vi.fn();

function query<T>(data: T, extra: Record<string, unknown> = {}) {
  return { data, isLoading: false, error: null, ...extra } as never;
}

const granted: ScriptGrant = {
  script_id: "script-001",
  principal_kind: "api_key",
  principal: "reporting-app",
  granted_by: "jane@example.com",
  created_at: "2026-09-20T10:00:00Z",
};

beforeEach(() => {
  vi.clearAllMocks();
  mockGrants.mockReturnValue(query([granted]));
  vi.mocked(useAddScriptGrant).mockReturnValue({ mutate: add, isPending: false } as never);
  vi.mocked(useRemoveScriptGrant).mockReturnValue({ mutate: remove, isPending: false } as never);
});

afterEach(cleanup);

// openCard renders the card, reads its folded summary, then opens it.
function openCard(): string {
  render(<ScriptGrantsCard scriptId="script-001" />);
  const summary = screen.getByRole("heading", { name: "Access" }).parentElement?.textContent ?? "";
  fireEvent.click(screen.getByRole("heading", { name: "Access" }));
  return summary;
}

describe("ScriptGrantsCard", () => {
  it("lists a grant and withdraws it", () => {
    expect(openCard()).toContain("Granted to 1");
    expect(screen.getByText("reporting-app")).toBeInTheDocument();
    expect(screen.getByRole("listitem")).toHaveTextContent("API key");
    fireEvent.click(screen.getByRole("button", { name: "Withdraw" }));
    expect(remove).toHaveBeenCalledWith({ principal_kind: "api_key", principal: "reporting-app" });
  });

  it("grants a role by name, trimmed, and clears the box when it lands", () => {
    mockGrants.mockReturnValue(query([]));
    expect(openCard()).toContain("Owner only");
    const grant = screen.getByRole("button", { name: "Grant" });
    expect(grant).toBeDisabled();
    fireEvent.change(screen.getByLabelText("Grant to"), { target: { value: "role" } });
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: " dp_reports " } });
    fireEvent.click(grant);
    expect(add).toHaveBeenCalledWith(
      { principal_kind: "role", principal: "dp_reports" },
      expect.objectContaining({ onSuccess: expect.any(Function), onError: expect.any(Function) }),
    );
  });

  it("reports a refused grant in the server's words", () => {
    add.mockImplementation((_g: unknown, opts: { onError: (e: unknown) => void }) =>
      opts.onError(new Error("principal is a role name of 1 to 200 characters")),
    );
    openCard();
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "x" } });
    fireEvent.click(screen.getByRole("button", { name: "Grant" }));
    expect(screen.getByText("principal is a role name of 1 to 200 characters")).toBeInTheDocument();
  });

  it("says when the grants cannot be read, and renders nothing where none are kept", () => {
    mockGrants.mockReturnValue(query(undefined, { error: new Error("boom") }));
    openCard();
    expect(screen.getByText("The grants could not be read.")).toBeInTheDocument();
    cleanup();
    mockGrants.mockReturnValue(query(null));
    const { container } = render(<ScriptGrantsCard scriptId="script-001" />);
    expect(container).toBeEmptyDOMElement();
  });
});
