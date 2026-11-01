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
  codeAsked: false,
}));

vi.mock("@/api/admin/hooks", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/admin/hooks")>();
  return {
    ...actual,
    useSecrets: () => h.list,
    useSecret: () => h.one,
    useSecretCode: (_name: string, enabled: boolean) => {
      h.codeAsked = enabled;
      return {
        data: enabled ? { code: "482913", seconds_left: 17, totp: { algorithm: "SHA1", digits: 6, period: 30 } } : undefined,
        isFetching: false,
        error: null,
        refetch: vi.fn(),
      };
    },
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
          { kind: "graphql", name: "tableau-meta" },
          { kind: "mcp", name: "vendor-mcp" },
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
    expect(screen.getAllByText("Any").length).toBeGreaterThan(0);
    // An authenticator seed is named by its code's placeholder (#2065).
    expect(screen.getByText("{{totp:portal_mfa}}")).toBeTruthy();
    expect(screen.getByText("finance")).toBeTruthy();
    fireEvent.click(screen.getByText("billing_api_key"));
    expect(nav).toHaveBeenCalledWith("/admin/secrets/billing_api_key");
    fireEvent.click(screen.getByRole("button", { name: /New secret/ }));
    expect(nav).toHaveBeenCalledWith("/admin/secrets/new");
  });

  it("wraps a long description, name and connection list inside their columns (#2070)", () => {
    at("/admin/secrets");
    // TableCell sets whitespace-nowrap on every cell; a cell that holds free
    // text must override it, or the text runs past its max-w under the next
    // column. jsdom draws nothing, so the class is what can be asserted.
    const wraps = (el: HTMLElement | null) => {
      const cell = el?.closest("td");
      expect(cell).toBeTruthy();
      expect(cell!.className).toContain("whitespace-normal");
      expect(cell!.className).not.toContain("whitespace-nowrap");
    };
    wraps(screen.getByText(/reports-reader@acme-reporting/));
    wraps(screen.getByText("reporting_service_account"));
    wraps(screen.getByText("google-drive-exports"));
    wraps(screen.getByText("reporting-analyst"));
  });

  it("narrows the table by name, description, connection and persona, and clears (#2352)", () => {
    window.history.replaceState(null, "", "/portal/admin/secrets");
    at("/admin/secrets");
    const search = screen.getByRole("textbox", { name: "Search secrets" });
    const rows = () => screen.getAllByRole("row").slice(1).map((r) => r.textContent ?? "");

    fireEvent.change(search, { target: { value: "PORTAL_" } });
    expect(rows()).toHaveLength(2);
    expect(rows().join()).toContain("portal_password");
    expect(rows().join()).toContain("portal_mfa");

    fireEvent.change(search, { target: { value: "export call" } });
    expect(rows()).toHaveLength(1);
    expect(rows()[0]).toContain("billing_api_key");

    fireEvent.change(search, { target: { value: "google-drive" } });
    expect(rows()).toHaveLength(1);
    expect(rows()[0]).toContain("reporting_service_account");

    // A persona name finds the secrets that list it and the ones no persona
    // list restricts, since those are allowed for every persona.
    fireEvent.change(search, { target: { value: "finance" } });
    expect(rows().map((r) => r.split("{{")[0])).toEqual(["portal_password", "billing_api_key", "portal_mfa"]);

    fireEvent.change(search, { target: { value: "nothing-like-this" } });
    expect(screen.getByText("No secrets match.")).toBeTruthy();

    fireEvent.change(search, { target: { value: "" } });
    expect(rows()).toHaveLength(4);
  });

  it("keeps the search in the address, and reads it back on a reload (#2352)", () => {
    window.history.replaceState(null, "", "/portal/admin/secrets");
    at("/admin/secrets");
    fireEvent.change(screen.getByRole("textbox", { name: "Search secrets" }), {
      target: { value: "billing api" },
    });
    expect(window.location.search).toBe("?q=billing+api");

    cleanup();
    at("/admin/secrets");
    expect((screen.getByRole("textbox", { name: "Search secrets" }) as HTMLInputElement).value).toBe("billing api");
    expect(screen.getAllByRole("row")).toHaveLength(2);
    window.history.replaceState(null, "", "/portal/admin/secrets");
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
    // A graphql or mcp connection's configuration may name a secret too
    // (#2066), and so may a trino password (#1806).
    expect(screen.getByLabelText("tableau-meta")).toBeTruthy();
    expect(screen.getByLabelText("vendor-mcp")).toBeTruthy();
    expect(screen.getByLabelText("warehouse")).toBeTruthy();
    fireEvent.click(screen.getByLabelText("finance"));
    fireEvent.click(screen.getByRole("button", { name: "Create secret" }));
    expect(h.put).toHaveBeenCalledWith(
      {
        name: "portal_pw",
        body: {
          description: "",
          kind: "value",
          value: "hunter22",
          allow_connections: ["selenium-grid"],
          allow_personas: ["finance"],
        },
      },
      expect.anything(),
    );
  });

  // #2388: a reserved use the platform reads (a bot check's provider key) is
  // offered as a Platform use rather than a connection; choosing bot_check
  // clears and disables the personas, which such a secret names none of.
  it("offers platform uses, and a bot check secret names no personas", () => {
    at("/admin/secrets/new");
    fireEvent.change(screen.getByLabelText(/^Name/), { target: { value: "forms_turnstile" } });
    fireEvent.change(screen.getByLabelText(/^Value/), { target: { value: "0x4AAA-secret" } });
    fireEvent.click(screen.getByLabelText("finance"));
    fireEvent.click(screen.getByLabelText(/^bot_check/));
    expect(screen.getByRole("group", { name: "Personas" })).toBeDisabled();
    expect((screen.getByLabelText("finance") as HTMLInputElement).checked).toBe(false);
    expect(screen.getByLabelText(/^script_token/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Create secret" }));
    expect(h.put).toHaveBeenCalledWith(
      {
        name: "forms_turnstile",
        body: {
          description: "",
          kind: "value",
          value: "0x4AAA-secret",
          allow_connections: ["bot_check"],
          allow_personas: [],
        },
      },
      expect.anything(),
    );
  });

  it("keeps a stored secret's platform use apart from its connections when it is changed", () => {
    h.one = {
      data: { ...mockSecrets[0], allow_connections: ["bot_check", "selenium-grid"], allow_personas: [] },
      isLoading: false,
      error: null,
    };
    at("/admin/secrets/" + (mockSecrets[0] as { name: string }).name);
    const connections = screen.getByRole("group", { name: "Connections" });
    expect(connections.textContent).not.toContain("bot_check");
    expect((screen.getByLabelText(/^bot_check/) as HTMLInputElement).checked).toBe(true);
    fireEvent.click(screen.getByLabelText("warehouse"));
    fireEvent.click(screen.getByRole("button", { name: /Save/ }));
    expect(h.put).toHaveBeenCalledWith(
      expect.objectContaining({
        body: expect.objectContaining({ allow_connections: ["bot_check", "selenium-grid", "warehouse"] }),
      }),
      expect.anything(),
    );
  });

  // #2065: the seed is typed where the value would be, the kind is sent, and
  // a stored seed shows its parameters and, on request, its current code.
  it("creates an authenticator seed", () => {
    at("/admin/secrets/new");
    fireEvent.change(screen.getByLabelText(/^Name/), { target: { value: "vendor_mfa" } });
    fireEvent.click(screen.getByRole("combobox", { name: "Kind" }));
    fireEvent.click(screen.getByRole("option", { name: "Authenticator seed" }));
    fireEvent.change(screen.getByLabelText(/^Seed/), {
      target: { value: "otpauth://totp/V:ops?secret=GEZDGNBVGY3TQOJQ" },
    });
    fireEvent.click(screen.getByLabelText("selenium-grid"));
    fireEvent.click(screen.getByRole("button", { name: "Create secret" }));
    expect(h.put.mock.calls[h.put.mock.calls.length - 1]![0].body).toMatchObject({
      kind: "totp",
      value: "otpauth://totp/V:ops?secret=GEZDGNBVGY3TQOJQ",
    });
  });

  it("shows a stored seed's parameters and its current code", () => {
    h.one = { data: mockSecrets.find((m) => m.name === "portal_mfa"), isLoading: false, error: null };
    at("/admin/secrets/portal_mfa");
    expect(screen.getByText("{{totp:portal_mfa}}")).toBeTruthy();
    expect(screen.getByText("30 seconds")).toBeTruthy();
    expect(h.codeAsked).toBe(false);
    expect(screen.queryByTestId("current-code")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Current code" }));
    expect(screen.getByTestId("current-code").textContent).toContain("482913");
    expect(screen.getByTestId("current-code").textContent).toContain("17 seconds left");
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
