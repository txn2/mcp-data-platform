import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import { AddKeyForm } from "./AddKeyForm";
import type { DirectoryUser } from "@/api/admin/types";

vi.mock("@/api/admin/hooks", () => ({
  useCreateAPIKey: vi.fn(),
  useDirectoryUsers: vi.fn(),
  usePersonas: () => ({ data: undefined, isLoading: false }),
}));

import { useCreateAPIKey, useDirectoryUsers } from "@/api/admin/hooks";

const mockUseCreate = vi.mocked(useCreateAPIKey);
const mockUseUsers = vi.mocked(useDirectoryUsers);

const createMutate = vi.fn();
const onCreated = vi.fn();

function person(overrides: Partial<DirectoryUser> = {}): DirectoryUser {
  return {
    email: "analyst@example.com",
    first_name: "Analyst",
    last_name: "Dev",
    source: "auth",
    confirmed: true,
    roles: ["dp_analyst"],
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...overrides,
  };
}

function directory(users: DirectoryUser[]) {
  mockUseUsers.mockReturnValue({
    data: { users, total: users.length },
    isLoading: false,
  } as unknown as ReturnType<typeof useDirectoryUsers>);
}

// bindTo opens the picker and selects the named person.
function bindTo(name: string) {
  fireEvent.click(screen.getByRole("button", { name: /service key \(not tied to a person\)/i }));
  fireEvent.click(screen.getByText(name));
}

// submitted is the body the form sent on its first (and only) create call.
function submitted(): Record<string, unknown> {
  const call = createMutate.mock.calls[0];
  if (!call) throw new Error("the form sent no create request");
  return call[0] as Record<string, unknown>;
}

beforeEach(() => {
  vi.clearAllMocks();
  directory([person()]);
  mockUseCreate.mockReturnValue({
    mutate: createMutate,
    isPending: false,
  } as unknown as ReturnType<typeof useCreateAPIKey>);
});

afterEach(cleanup);

describe("AddKeyForm: a key issued against nobody", () => {
  it("opens as a service key, asking for a contact email", () => {
    render(<AddKeyForm onCreated={onCreated} />);
    expect(screen.getByRole("button", { name: /service key \(not tied to a person\)/i })).toBeTruthy();
    expect(screen.getByLabelText(/contact email/i)).toBeTruthy();
  });

  it("refuses one with no roles, since it would reach nothing", () => {
    render(<AddKeyForm onCreated={onCreated} />);
    fireEvent.change(screen.getByPlaceholderText("e.g. ci-pipeline"), {
      target: { value: "etl" },
    });
    fireEvent.click(screen.getByRole("button", { name: /create/i }));

    expect(createMutate).not.toHaveBeenCalled();
    expect(screen.getByText(/needs at least one role, or bind it to a user/i)).toBeTruthy();
  });

  it("sends the roles typed and the contact email, and no account", () => {
    render(<AddKeyForm onCreated={onCreated} />);
    fireEvent.change(screen.getByPlaceholderText("e.g. ci-pipeline"), { target: { value: "etl" } });
    fireEvent.change(screen.getByLabelText(/contact email/i), {
      target: { value: "etl@example.com" },
    });
    const roles = screen.getByLabelText("Add role");
    fireEvent.change(roles, { target: { value: "service" } });
    fireEvent.keyDown(roles, { key: "Enter" });
    fireEvent.click(screen.getByRole("button", { name: /create/i }));

    expect(submitted()).toMatchObject({
      name: "etl",
      email: "etl@example.com",
      user_email: undefined,
      roles: ["service"],
    });
  });
});

describe("AddKeyForm: a key issued against an account", () => {
  it("offers only people the platform has seen sign in", () => {
    directory([person(), person({ email: "invited@example.com", confirmed: false })]);
    render(<AddKeyForm onCreated={onCreated} />);
    fireEvent.click(screen.getByRole("button", { name: /service key \(not tied to a person\)/i }));

    expect(screen.getByText("Analyst Dev")).toBeTruthy();
    expect(screen.queryByText("invited@example.com")).toBeNull();
  });

  it("says so when nobody has signed in, rather than showing an empty list", () => {
    directory([]);
    render(<AddKeyForm onCreated={onCreated} />);
    fireEvent.click(screen.getByRole("button", { name: /service key \(not tied to a person\)/i }));

    expect(screen.getByText(/nobody here has signed in yet/i)).toBeTruthy();
  });

  it("fills the roles with the ones that person holds, and follows them", () => {
    render(<AddKeyForm onCreated={onCreated} />);
    bindTo("Analyst Dev");

    expect(screen.getByText("dp_analyst")).toBeTruthy();
    expect(screen.getByText(/following this person/i)).toBeTruthy();
    expect(screen.getByText(/carries whatever roles they hold/i)).toBeTruthy();
    // The contact email is not asked for: a bound key carries its person's.
    expect(screen.queryByLabelText(/contact email/i)).toBeNull();
  });

  it("sends no roles at all while the key follows its person", () => {
    render(<AddKeyForm onCreated={onCreated} />);
    fireEvent.change(screen.getByPlaceholderText("e.g. ci-pipeline"), {
      target: { value: "chatgpt" },
    });
    bindTo("Analyst Dev");
    fireEvent.click(screen.getByRole("button", { name: /create/i }));

    expect(submitted()).toMatchObject({
      name: "chatgpt",
      user_email: "analyst@example.com",
      roles: undefined,
    });
  });

  it("sends the edited set once the roles are touched", () => {
    render(<AddKeyForm onCreated={onCreated} />);
    fireEvent.change(screen.getByPlaceholderText("e.g. ci-pipeline"), {
      target: { value: "narrowed" },
    });
    bindTo("Analyst Dev");

    // The placeholder is dropped once the box holds chips, so the entry is
    // reached by its accessible name.
    const roles = screen.getByLabelText("Add role");
    fireEvent.change(roles, { target: { value: "viewer" } });
    fireEvent.keyDown(roles, { key: "Enter" });

    expect(screen.getByText(/narrowed to this set/i)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: /create/i }));
    expect(submitted()).toMatchObject({
      user_email: "analyst@example.com",
      roles: ["dp_analyst", "viewer"],
    });
  });

  it("goes back to following when the roles are emptied, because that is what an empty set means", () => {
    render(<AddKeyForm onCreated={onCreated} />);
    fireEvent.change(screen.getByPlaceholderText("e.g. ci-pipeline"), {
      target: { value: "emptied" },
    });
    bindTo("Analyst Dev");
    fireEvent.click(screen.getByRole("button", { name: /remove dp_analyst/i }));

    // The label must not claim a narrowing the server would not apply: an
    // empty set on a bound key IS following the person.
    expect(screen.getByText(/following this person/i)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: /create/i }));
    expect(submitted()).toMatchObject({ user_email: "analyst@example.com", roles: undefined });
  });

  it("clears back to a service key, taking that person's roles with it", () => {
    render(<AddKeyForm onCreated={onCreated} />);
    bindTo("Analyst Dev");
    fireEvent.click(screen.getByRole("button", { name: /clear the account/i }));

    expect(screen.getByRole("button", { name: /service key \(not tied to a person\)/i })).toBeTruthy();
    expect(screen.queryByText("dp_analyst")).toBeNull();
    expect(screen.getByLabelText(/contact email/i)).toBeTruthy();
  });
});
