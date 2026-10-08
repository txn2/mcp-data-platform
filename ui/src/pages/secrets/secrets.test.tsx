import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { mockSecrets } from "@/mocks/data/secrets";

const h = vi.hoisted(() => ({
  list: { data: undefined as unknown, isLoading: false, isError: false },
  one: { data: undefined as unknown, isLoading: false, error: null as unknown },
  put: vi.fn(),
  putError: null as unknown,
  remove: vi.fn(),
}));

vi.mock("@/api/admin/hooks", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/admin/hooks")>();
  return {
    ...actual,
    useSecrets: () => h.list,
    useSecret: () => h.one,
    usePutSecret: () => ({
      mutate: h.put,
      isPending: false,
      error: h.putError,
    }),
    useDeleteSecret: () => ({
      mutateAsync: h.remove,
      isPending: false,
      error: null,
    }),
    usePersonas: () => ({
      data: { personas: [{ name: "admin" }, { name: "finance" }] },
      isLoading: false,
    }),
    useConnections: () => ({
      data: {
        connections: [
          { kind: "api", name: "selenium-grid" },
          { kind: "trino", name: "warehouse" },
        ],
      },
      isLoading: false,
    }),
  };
});

vi.mock("@/components/LoadingIndicator", () => ({
  LoadingIndicator: () => <div>Loading</div>,
}));

import { AdminSecretRoutes } from "./AdminSecretRoutes";

const nav = vi.fn();
const back = vi.fn();
const at = (route: string) =>
  render(<AdminSecretRoutes route={route} onNavigate={nav} onBack={back} />);

beforeEach(() => {
  cleanup();
  h.list = { data: { secrets: mockSecrets }, isLoading: false, isError: false };
  h.one = { data: mockSecrets[0], isLoading: false, error: null };
  h.put.mockReset();
  h.remove.mockReset();
  h.putError = null;
  nav.mockReset();
  back.mockReset();
});

describe("Secrets list", () => {
  it("lists each secret with its placeholder and scope, and opens one on row click", () => {
    at("/admin/secrets");
    expect(screen.getByText("{{secret:portal_password}}")).toBeTruthy();
    expect(screen.getByText("Any")).toBeTruthy();
    expect(screen.getByText("finance")).toBeTruthy();
    fireEvent.click(screen.getByText("billing_api_key"));
    expect(nav).toHaveBeenCalledWith("/admin/secrets/billing_api_key");
    fireEvent.click(screen.getByRole("button", { name: /New secret/ }));
    expect(nav).toHaveBeenCalledWith("/admin/secrets/new");
  });

  it("says when there is none, and when the list cannot be read", () => {
    h.list = { data: { secrets: [] }, isLoading: false, isError: false };
    at("/admin/secrets");
    expect(screen.getByText("No secret yet.")).toBeTruthy();
    cleanup();
    h.list = { data: undefined, isLoading: false, isError: true };
    at("/admin/secrets");
    expect(screen.getByText("The secrets could not be read.")).toBeTruthy();
  });
});

describe("Secret editor", () => {
  it("creates a secret with its value and scope", () => {
    at("/admin/secrets/new");
    fireEvent.click(screen.getByRole("button", { name: "Create secret" }));
    expect(screen.getByText("A new secret needs a value.")).toBeTruthy();
    expect(h.put).not.toHaveBeenCalled();

    fireEvent.change(screen.getByLabelText(/^Name/), {
      target: { value: "portal_pw" },
    });
    fireEvent.change(screen.getByLabelText(/^Value/), {
      target: { value: "hunter22" },
    });
    fireEvent.click(screen.getByLabelText("selenium-grid"));
    expect(screen.queryByLabelText("warehouse")).toBeNull();
    fireEvent.click(screen.getByLabelText("finance"));
    fireEvent.click(screen.getByRole("button", { name: "Create secret" }));
    expect(h.put).toHaveBeenCalledWith(
      {
        name: "portal_pw",
        body: {
          description: "",
          value: "hunter22",
          allow_connections: ["selenium-grid"],
          allow_personas: ["finance"],
        },
      },
      expect.anything(),
    );
  });

  it("changes a secret without resending its value", () => {
    at("/admin/secrets/portal_password");
    expect(
      screen.getByText("Leave empty to keep the stored value."),
    ).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    expect(h.put.mock.calls[0]![0].body).not.toHaveProperty("value");
  });

  it("shows what the server refused", () => {
    h.putError = new Error(
      "invalid secret: value must be at least 6 characters",
    );
    at("/admin/secrets/portal_password");
    expect(screen.getByText(/at least 6 characters/)).toBeTruthy();
  });

  it("deletes after confirming", async () => {
    at("/admin/secrets/portal_password");
    fireEvent.click(screen.getByRole("button", { name: /Delete/ }));
    fireEvent.click(
      screen.getAllByRole("button", { name: "Delete" }).slice(-1)[0]!,
    );
    await waitFor(() =>
      expect(h.remove).toHaveBeenCalledWith("portal_password"),
    );
    await waitFor(() => expect(back).toHaveBeenCalled());
  });

  it("says when a secret cannot be read, and waits while it loads", () => {
    h.one = { data: undefined, isLoading: false, error: new Error("404") };
    at("/admin/secrets/gone");
    expect(screen.getByText("This secret could not be read.")).toBeTruthy();
    cleanup();
    h.one = { data: undefined, isLoading: true, error: null };
    const { container } = at("/admin/secrets/portal_password");
    expect(container.textContent).toBe("Loading");
  });

  it("renders nothing for a path outside the section", () => {
    const { container } = at("/admin/secrets/a/b");
    expect(container.textContent).toBe("");
  });
});
