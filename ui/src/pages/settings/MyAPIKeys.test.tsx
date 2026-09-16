import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import { MyAPIKeys } from "./MyAPIKeys";
import type { MyAPIKey } from "@/api/portal/hooks";

vi.mock("@/api/portal/hooks", () => ({
  useMyAPIKeys: vi.fn(),
  useCreateMyAPIKey: vi.fn(),
  useRevokeMyAPIKey: vi.fn(),
}));

import {
  useMyAPIKeys,
  useCreateMyAPIKey,
  useRevokeMyAPIKey,
} from "@/api/portal/hooks";

const mockUseKeys = vi.mocked(useMyAPIKeys);
const mockUseCreate = vi.mocked(useCreateMyAPIKey);
const mockUseRevoke = vi.mocked(useRevokeMyAPIKey);

const createMutate = vi.fn();
const revokeMutate = vi.fn();
const refetch = vi.fn();

function key(overrides: Partial<MyAPIKey> = {}): MyAPIKey {
  return { name: "chatgpt", roles: ["analyst"], ...overrides };
}

// keys drives the listing hook for one test.
function keys(list: MyAPIKey[] | undefined, extra: Record<string, unknown> = {}) {
  mockUseKeys.mockReturnValue({
    data: list === undefined ? undefined : { keys: list, total: list.length },
    isLoading: false,
    error: null,
    refetch,
    ...extra,
  } as unknown as ReturnType<typeof useMyAPIKeys>);
}

beforeEach(() => {
  vi.clearAllMocks();
  keys([]);
  mockUseCreate.mockReturnValue({
    mutate: createMutate,
    isPending: false,
  } as unknown as ReturnType<typeof useCreateMyAPIKey>);
  mockUseRevoke.mockReturnValue({
    mutate: revokeMutate,
    isPending: false,
  } as unknown as ReturnType<typeof useRevokeMyAPIKey>);
});

afterEach(cleanup);

describe("MyAPIKeys: what a person is shown", () => {
  it("says plainly that a key here authenticates as them", () => {
    render(<MyAPIKeys />);
    expect(screen.getByText(/authenticates as you, with the roles/i)).toBeTruthy();
  });

  it("states when they hold none rather than showing an empty table", () => {
    render(<MyAPIKeys />);
    expect(screen.getByText("You have no API keys.")).toBeTruthy();
    expect(screen.queryByRole("table")).toBeNull();
  });

  it("shows a loading state while the list is read", () => {
    keys(undefined, { isLoading: true });
    render(<MyAPIKeys />);
    expect(screen.getByText("Loading...")).toBeTruthy();
  });

  it("offers a retry when the list cannot be read", () => {
    keys(undefined, { error: new Error("network down") });
    render(<MyAPIKeys />);
    expect(screen.getByText(/failed to load your api keys/i)).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: /retry/i }));
    expect(refetch).toHaveBeenCalled();
  });

  it("lists each key with the roles it carries and when it ends", () => {
    keys([
      key({ name: "chatgpt", roles: ["analyst", "viewer"] }),
      key({ name: "nifi", roles: ["analyst"], expires_at: "2030-01-02T00:00:00Z" }),
    ]);
    render(<MyAPIKeys />);

    expect(screen.getByText("chatgpt")).toBeTruthy();
    expect(screen.getByText("nifi")).toBeTruthy();
    expect(screen.getByText("viewer")).toBeTruthy();
    // A key with no end date says so rather than leaving the cell blank. The
    // expiration picker offers "Never" too, so both are present.
    expect(screen.getAllByText("Never").length).toBeGreaterThan(1);
  });

  it("marks an expired key rather than listing it as live", () => {
    keys([key({ name: "old", expired: true, expires_at: "2020-01-02T00:00:00Z" })]);
    render(<MyAPIKeys />);
    expect(screen.getByText("Expired")).toBeTruthy();
  });
});

describe("MyAPIKeys: issuing one", () => {
  it("asks for a name and an expiration, and for nothing else", () => {
    render(<MyAPIKeys />);
    expect(screen.getByLabelText("Name")).toBeTruthy();
    expect(screen.getByText("Expiration")).toBeTruthy();
    // There is no roles control: a person cannot widen their own key. The
    // description names roles in prose, so this asks for the control.
    expect(screen.queryByLabelText(/^roles$/i)).toBeNull();
    expect(screen.queryByPlaceholderText(/role/i)).toBeNull();
  });

  it("will not submit an empty name", () => {
    render(<MyAPIKeys />);
    const button = screen.getByRole("button", { name: /create key/i });
    expect(button.hasAttribute("disabled")).toBe(true);

    fireEvent.click(button);
    expect(createMutate).not.toHaveBeenCalled();
  });

  it("sends the name typed, and no expiry when none is chosen", () => {
    render(<MyAPIKeys />);
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "  chatgpt  " } });
    fireEvent.click(screen.getByRole("button", { name: /create key/i }));

    expect(createMutate).toHaveBeenCalledTimes(1);
    expect(createMutate.mock.calls[0]?.[0]).toEqual({ name: "chatgpt", expires_in: undefined });
  });

  it("shows the key once, with the warning, after it is issued", () => {
    createMutate.mockImplementation((_body, opts) => {
      opts.onSuccess({
        name: "chatgpt",
        key: "3f9a1c07",
        roles: ["analyst"],
        warning: "Store this key securely. It will not be shown again.",
      });
    });
    render(<MyAPIKeys />);
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "chatgpt" } });
    fireEvent.click(screen.getByRole("button", { name: /create key/i }));

    expect(screen.getByText("Key created: chatgpt")).toBeTruthy();
    expect(screen.getByText("3f9a1c07")).toBeTruthy();
    expect(screen.getByText(/will not be shown again/i)).toBeTruthy();
  });

  it("surfaces a refusal instead of leaving the form silent", () => {
    createMutate.mockImplementation((_body, opts) => {
      opts.onError(new Error("you already have a key named \"chatgpt\""));
    });
    render(<MyAPIKeys />);
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "chatgpt" } });
    fireEvent.click(screen.getByRole("button", { name: /create key/i }));

    expect(screen.getByText(/you already have a key named/i)).toBeTruthy();
  });
});

describe("MyAPIKeys: revoking one", () => {
  it("asks before revoking, and does nothing until confirmed", () => {
    keys([key({ name: "chatgpt" })]);
    render(<MyAPIKeys />);

    fireEvent.click(screen.getByRole("button", { name: "Revoke chatgpt" }));
    expect(revokeMutate).not.toHaveBeenCalled();

    // The confirm and cancel pair replaces the single action.
    expect(screen.getByRole("button", { name: "Revoke" })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(revokeMutate).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "Revoke chatgpt" })).toBeTruthy();
  });

  it("revokes by the name its owner gave it once confirmed", () => {
    keys([key({ name: "chatgpt" })]);
    render(<MyAPIKeys />);

    fireEvent.click(screen.getByRole("button", { name: "Revoke chatgpt" }));
    fireEvent.click(screen.getByRole("button", { name: "Revoke" }));

    expect(revokeMutate).toHaveBeenCalledTimes(1);
    expect(revokeMutate.mock.calls[0]?.[0]).toBe("chatgpt");
  });

  it("shows why a revoke failed and stops asking", () => {
    keys([key({ name: "chatgpt" })]);
    revokeMutate.mockImplementation((_name, opts) => {
      opts.onError(new Error("revoking the key failed"));
    });
    render(<MyAPIKeys />);

    fireEvent.click(screen.getByRole("button", { name: "Revoke chatgpt" }));
    fireEvent.click(screen.getByRole("button", { name: "Revoke" }));

    expect(screen.getByText("revoking the key failed")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Revoke chatgpt" })).toBeTruthy();
  });
});
