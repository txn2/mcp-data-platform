import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import {
  render,
  screen,
  fireEvent,
  cleanup,
  within,
} from "@testing-library/react";
import type { ExternalUser } from "@/api/admin/types";

vi.mock("@/api/admin/hooks", () => ({
  useExternalUsers: vi.fn(),
  useCreateExternalUser: vi.fn(),
  useExternalUserAction: vi.fn(),
  usePersonas: vi.fn(),
}));

import {
  useCreateExternalUser,
  useExternalUserAction,
  useExternalUsers,
  usePersonas,
} from "@/api/admin/hooks";
import { ExternalUsersSection } from "./ExternalUsersSection";

// Admin > Users' external accounts (#2092).

const act = vi.fn();
const create = vi.fn();

function user(over: Partial<ExternalUser>): ExternalUser {
  return {
    id: "u1",
    email: "client@example.org",
    name: "Client",
    persona: "client-review",
    created_by: "admin@example.com",
    created_at: "2026-10-01T00:00:00Z",
    status: "active",
    shared_items: 3,
    expires_at: "2026-12-31T23:59:59Z",
    ...over,
  };
}

function withUsers(users: ExternalUser[]) {
  vi.mocked(useExternalUsers).mockReturnValue({
    data: { users },
    isError: false,
  } as never);
}

beforeEach(() => {
  vi.clearAllMocks();
  withUsers([user({})]);
  vi.mocked(useExternalUserAction).mockReturnValue({
    mutate: act,
    error: null,
  } as never);
  vi.mocked(useCreateExternalUser).mockReturnValue({
    mutate: create,
    error: null,
    isPending: false,
  } as never);
  vi.mocked(usePersonas).mockReturnValue({
    data: {
      personas: [
        { name: "analyst", display_name: "Analyst", external: false },
        {
          name: "client-review",
          display_name: "Client review",
          external: true,
        },
      ],
    },
  } as never);
});

afterEach(cleanup);

describe("ExternalUsersSection", () => {
  it("renders nothing on a deployment without external accounts", () => {
    vi.mocked(useExternalUsers).mockReturnValue({
      data: undefined,
      isError: true,
    } as never);
    const { container } = render(<ExternalUsersSection isReadOnly={false} />);
    expect(container).toBeEmptyDOMElement();
  });

  it("says what an external user is when there are none", () => {
    withUsers([]);
    render(<ExternalUsersSection isReadOnly={false} />);
    expect(screen.getByText(/None yet/)).toBeInTheDocument();
  });

  it("lists each account with what is shared with it", () => {
    render(<ExternalUsersSection isReadOnly={false} />);
    const row = screen.getByText("client@example.org").closest("tr")!;
    expect(within(row).getByText("client-review")).toBeInTheDocument();
    expect(within(row).getByText("3")).toBeInTheDocument();
  });

  it("extends, suspends and removes after a confirmation", () => {
    render(<ExternalUsersSection isReadOnly={false} />);
    fireEvent.click(screen.getByRole("button", { name: "Extend" }));
    fireEvent.change(
      screen.getByLabelText("New expiry for client@example.org"),
      { target: { value: "2027-01-31" } },
    );
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(act.mock.calls[0]![0]).toMatchObject({ id: "u1", action: "extend" });
    expect(act.mock.calls[0]![0].expiresAt).toMatch(/^2027-0[12]-/);

    fireEvent.click(screen.getByRole("button", { name: "Suspend" }));
    expect(act).toHaveBeenLastCalledWith({ id: "u1", action: "suspend" });

    fireEvent.click(screen.getByRole("button", { name: "Remove" }));
    expect(act).toHaveBeenCalledTimes(2);
    fireEvent.click(
      screen.getByRole("button", {
        name: "Remove, and revoke what is shared with them",
      }),
    );
    expect(act).toHaveBeenLastCalledWith({ id: "u1", action: "remove" });
  });

  it("resumes a suspended account", () => {
    withUsers([user({ status: "suspended" })]);
    render(<ExternalUsersSection isReadOnly={false} />);
    expect(screen.getByText("Suspended")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Suspend" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Resume" }));
    expect(act).toHaveBeenLastCalledWith({ id: "u1", action: "resume" });
  });

  it("offers only Remove on a converted account", () => {
    withUsers([
      user({ status: "converted", converted_at: "2026-10-15T00:00:00Z" }),
    ]);
    render(<ExternalUsersSection isReadOnly={false} />);
    expect(screen.getByText(/Converted/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Extend" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Suspend" })).toBeNull();
    expect(screen.getByRole("button", { name: "Remove" })).toBeInTheDocument();
  });

  it("has no actions for a read-only administrator", () => {
    render(<ExternalUsersSection isReadOnly />);
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("adds an account under an external persona only", () => {
    render(<ExternalUsersSection isReadOnly={false} />);
    fireEvent.click(screen.getByRole("button", { name: "Add external user" }));
    const options = Array.from(
      (screen.getByLabelText("Persona") as HTMLSelectElement).options,
    ).map((o) => o.value);
    expect(options).toEqual(["client-review"]);
    fireEvent.change(screen.getByLabelText("Email"), {
      target: { value: " new@example.org " },
    });
    fireEvent.change(screen.getByLabelText("Name"), {
      target: { value: "New" },
    });
    const buttons = screen.getAllByRole("button", {
      name: "Add external user",
    });
    fireEvent.click(buttons[buttons.length - 1]!);
    expect(create.mock.calls[0]![0]).toEqual({
      email: "new@example.org",
      name: "New",
      persona: "client-review",
      expires_at: undefined,
    });
  });

  it("says no persona is external yet", () => {
    vi.mocked(usePersonas).mockReturnValue({ data: { personas: [] } } as never);
    render(<ExternalUsersSection isReadOnly={false} />);
    fireEvent.click(screen.getByRole("button", { name: "Add external user" }));
    expect(screen.getByText(/No persona is external yet/)).toBeInTheDocument();
    const buttons = screen.getAllByRole("button", {
      name: "Add external user",
    });
    expect(buttons[buttons.length - 1]).toBeDisabled();
  });
});
