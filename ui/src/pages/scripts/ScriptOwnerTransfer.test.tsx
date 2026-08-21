import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import type { ScriptContract } from "@/api/portal/hooks/scripts";
import { ScriptOwnerTransfer } from "./ScriptOwnerTransfer";

vi.mock("@/api/portal/hooks/scripts", () => ({
  useTransferScriptOwner: vi.fn(),
}));

// The new owner is chosen from the known-users directory, narrowed to the
// people who have signed in (#1407). The hook has its own tests; here it only
// has to answer, so the control renders without a query client.
vi.mock("@/api/portal/hooks", () => ({
  useDirectoryUsers: vi.fn(),
}));

import { useDirectoryUsers } from "@/api/portal/hooks";
import { useTransferScriptOwner } from "@/api/portal/hooks/scripts";

const mockTransfer = vi.mocked(useTransferScriptOwner);
const mockDirectory = vi.mocked(useDirectoryUsers);

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

const directory = {
  users: [
    { email: "marcus.webb@example.com", first_name: "Marcus", last_name: "Webb", confirmed: true },
    { email: "admin@example.com", first_name: "", last_name: "", confirmed: true },
    { email: "sarah.chen@example.com", first_name: "Sarah", last_name: "Chen", confirmed: true },
  ],
  total: 3,
};

// mutate is the transfer the control asks for, asserted per test.
let mutate = vi.fn();

beforeEach(() => {
  vi.clearAllMocks();
  mutate = vi.fn();
  mockTransfer.mockReturnValue({ mutate, isPending: false } as never);
  mockDirectory.mockReturnValue({ data: directory } as never);
});

afterEach(cleanup);

function renderControl(overrides: Partial<ScriptContract> = {}) {
  render(<ScriptOwnerTransfer scriptId="script-001" contract={{ ...contract, ...overrides }} />);
}

// choosing a person and asking for the move are one flow; this helper walks it.
function ask(option: string) {
  fireEvent.click(screen.getByLabelText("New owner"));
  fireEvent.click(screen.getByRole("option", { name: option }));
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

  // The new owner is chosen rather than typed: an address nobody has signed in
  // with cannot open the portal, so a script handed to one is a script only
  // administrators can see.
  it("asks the directory for the people who have signed in", () => {
    renderControl();
    expect(mockDirectory).toHaveBeenCalledWith("", true, {
      confirmedOnly: true,
      limit: 100,
    });
    expect(screen.getByLabelText("New owner")).toHaveAttribute("role", "combobox");
  });

  it("offers each person by name and address", () => {
    renderControl();
    fireEvent.click(screen.getByLabelText("New owner"));

    expect(
      screen.getByRole("option", { name: "Marcus Webb — marcus.webb@example.com" }),
    ).toBeInTheDocument();
    // Somebody the directory knows no name for is still offered, by address.
    expect(screen.getByRole("option", { name: "admin@example.com" })).toBeInTheDocument();
  });

  // Offering the current owner would offer a transfer that changes nothing and
  // still writes a version.
  it("does not offer the owner it already has", () => {
    renderControl();
    fireEvent.click(screen.getByLabelText("New owner"));

    expect(screen.queryByRole("option", { name: /Sarah Chen/ })).not.toBeInTheDocument();
  });

  it("offers no move until somebody is chosen", () => {
    renderControl();
    expect(screen.getByRole("button", { name: "Transfer ownership" })).toBeDisabled();
  });

  it("names both ends of the move before making it", () => {
    renderControl();

    ask("Marcus Webb — marcus.webb@example.com");

    expect(screen.getByText(/Move this script from/)).toBeInTheDocument();
    expect(screen.getByText(/sarah.chen@example.com will no longer see it/)).toBeInTheDocument();
    expect(mutate).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "Transfer" }));
    expect(mutate).toHaveBeenCalledWith("marcus.webb@example.com", expect.anything());
  });

  it("abandons the move on cancel", () => {
    renderControl();
    ask("Marcus Webb — marcus.webb@example.com");

    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));

    expect(screen.queryByText(/Move this script from/)).not.toBeInTheDocument();
    expect(mutate).not.toHaveBeenCalled();
  });

  it("reports what the transfer means for the next run", () => {
    mockTransfer.mockReturnValue({
      mutate: (_email: string, opts: { onSuccess: (o: { message: string }) => void }) =>
        opts.onSuccess({
          message: "daily-sales-report now belongs to you and runs with the access you hold.",
        }),
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
        opts.onError(
          new Error('marcus.webb@example.com already keeps a script named "daily-sales-report"'),
        ),
      isPending: false,
    } as never);
    renderControl();

    ask("Marcus Webb — marcus.webb@example.com");
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

  // A directory larger than the page it was read under gets the box that
  // narrows it, asked of the server: without it the people past the cap would
  // be unreachable while the control looked complete.
  it("offers a search when the directory holds more people than one page", () => {
    mockDirectory.mockReturnValue({
      data: { users: directory.users, total: 214 },
    } as never);
    renderControl();

    expect(screen.getByLabelText("Search people")).toBeInTheDocument();
  });

  it("offers no search over a directory that fits", () => {
    renderControl();
    expect(screen.queryByLabelText("Search people")).not.toBeInTheDocument();
  });

  // A deployment with no directory to read, and one where nobody else has ever
  // signed in, are the same situation from here: there is nobody to hand the
  // script to, and the page says so instead of offering a dead control.
  it("says plainly when there is nobody to move the script to", () => {
    mockDirectory.mockReturnValue({ data: { users: [], total: 0 } } as never);
    renderControl();

    expect(screen.getByText(/Nobody else has signed in yet/)).toBeInTheDocument();
    expect(screen.queryByLabelText("New owner")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Transfer ownership" })).toBeDisabled();
  });
});
