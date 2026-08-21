import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import type { ScriptContract } from "@/api/portal/hooks/scripts";
import { ScriptOwnerTransfer } from "./ScriptOwnerTransfer";

vi.mock("@/api/portal/hooks/scripts", () => ({
  useTransferScriptOwner: vi.fn(),
}));

// The picker's type-ahead reads the known-users directory. It has its own
// tests; here it only has to answer, so the control renders without a query
// client.
vi.mock("@/api/portal/hooks", () => ({
  useDirectoryUsers: () => ({ data: undefined }),
}));

import { useTransferScriptOwner } from "@/api/portal/hooks/scripts";

const mockTransfer = vi.mocked(useTransferScriptOwner);

const contract: ScriptContract = {
  id: "script-001",
  name: "daily-sales-report",
  display_name: "Daily Sales Report",
  owner_email: "sarah.chen@example.com",
  status: "active",
  enabled: true,
  params: [],
  version: 2,
};

// mutate is the transfer the control asks for, asserted per test.
let mutate = vi.fn();

beforeEach(() => {
  vi.clearAllMocks();
  mutate = vi.fn();
  mockTransfer.mockReturnValue({ mutate, isPending: false } as never);
});

afterEach(cleanup);

function renderControl(overrides: Partial<ScriptContract> = {}) {
  render(<ScriptOwnerTransfer scriptId="script-001" contract={{ ...contract, ...overrides }} />);
}

// type-ahead entry and the confirm step are one flow; this helper walks it.
function ask(email: string) {
  fireEvent.change(screen.getByPlaceholderText("New owner's email"), {
    target: { value: email },
  });
  fireEvent.click(screen.getByRole("button", { name: "Transfer ownership" }));
}

describe("ScriptOwnerTransfer", () => {
  it("states who the script belongs to and what a transfer changes", () => {
    renderControl();

    expect(screen.getByText("sarah.chen@example.com")).toBeInTheDocument();
    expect(screen.getByText(/only person who sees it/)).toBeInTheDocument();
    // The consequence an administrator comes here for: the run identity is
    // re-captured from them.
    expect(screen.getByText(/a run presents the access you hold now/)).toBeInTheDocument();
  });

  it("names both ends of the move before making it", () => {
    renderControl();

    ask("marcus.webb@example.com");

    expect(screen.getByText(/Move this script from/)).toBeInTheDocument();
    expect(screen.getByText(/sarah.chen@example.com will no longer see it/)).toBeInTheDocument();
    expect(mutate).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "Transfer" }));
    expect(mutate).toHaveBeenCalledWith("marcus.webb@example.com", expect.anything());
  });

  it("abandons the move on cancel", () => {
    renderControl();
    ask("marcus.webb@example.com");

    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));

    expect(screen.queryByText(/Move this script from/)).not.toBeInTheDocument();
    expect(mutate).not.toHaveBeenCalled();
  });

  it("refuses a transfer to the owner it already has, and offers none with no address", () => {
    renderControl();

    expect(screen.getByRole("button", { name: "Transfer ownership" })).toBeDisabled();

    fireEvent.change(screen.getByPlaceholderText("New owner's email"), {
      target: { value: "Sarah.Chen@Example.com" },
    });

    expect(screen.getByRole("button", { name: "Transfer ownership" })).toBeDisabled();
    expect(screen.getByText(/already belongs to that person/)).toBeInTheDocument();
  });

  it("reports what the transfer means for the next run", () => {
    mockTransfer.mockReturnValue({
      mutate: (_email: string, opts: { onSuccess: (o: { message: string }) => void }) =>
        opts.onSuccess({ message: "daily-sales-report now belongs to you and runs with the access you hold." }),
      isPending: false,
    } as never);
    renderControl();

    ask("admin@example.com");
    fireEvent.click(screen.getByRole("button", { name: "Transfer" }));

    expect(screen.getByText(/now belongs to you/)).toBeInTheDocument();
  });

  it("surfaces a refusal in the words the server used", () => {
    mockTransfer.mockReturnValue({
      mutate: (_email: string, opts: { onError: (e: Error) => void }) =>
        opts.onError(new Error("marcus.webb@example.com already keeps a script named \"daily-sales-report\"")),
      isPending: false,
    } as never);
    renderControl();

    ask("marcus.webb@example.com");
    fireEvent.click(screen.getByRole("button", { name: "Transfer" }));

    expect(screen.getByText(/already keeps a script named/)).toBeInTheDocument();
  });

  // A script authored by a principal carrying no address belongs to nobody
  // until an administrator adopts it, which is the other reason this exists.
  it("adopts an ownerless script", () => {
    renderControl({ owner_email: "" });

    expect(screen.getByText("nobody")).toBeInTheDocument();
    ask("admin@example.com");
    expect(screen.getByText(/It has belonged to nobody until now/)).toBeInTheDocument();
  });
});
