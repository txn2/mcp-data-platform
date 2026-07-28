import { describe, it, expect, afterEach, vi } from "vitest";
import { useAuthStore } from "./auth";

describe("loginOIDC return_to capture (#710)", () => {
  const originalLocation = window.location;

  // jsdom's window.location is mostly read-only; replace it with a
  // controllable stand-in so we can capture the assigned href without an
  // actual navigation tearing down the test.
  function mockLocation(pathname: string, search = "", hash = ""): { get href(): string } {
    let href = "";
    Object.defineProperty(window, "location", {
      configurable: true,
      value: {
        pathname,
        search,
        hash,
        set href(v: string) {
          href = v;
        },
      },
    });
    return {
      get href() {
        return href;
      },
    };
  }

  afterEach(() => {
    Object.defineProperty(window, "location", {
      configurable: true,
      value: originalLocation,
    });
    vi.restoreAllMocks();
  });

  it("redirects to the login endpoint carrying the current path as return_to", () => {
    const loc = mockLocation("/assets/asset-001");
    useAuthStore.getState().loginOIDC();
    expect(loc.href).toBe(
      "/portal/auth/login?return_to=" + encodeURIComponent("/assets/asset-001"),
    );
  });

  it("includes query and hash in the captured return_to", () => {
    const loc = mockLocation("/knowledge", "?tab=pages", "#section-2");
    useAuthStore.getState().loginOIDC();
    expect(loc.href).toBe(
      "/portal/auth/login?return_to=" +
        encodeURIComponent("/knowledge?tab=pages#section-2"),
    );
  });
});

describe("checkSession access denial (403)", () => {
  afterEach(() => {
    useAuthStore.setState({
      user: null,
      authMethod: null,
      apiKey: "",
      csrfToken: "",
      loading: true,
      sessionExpired: false,
      accessDenied: false,
      deniedEmail: "",
    });
    sessionStorage.clear();
    vi.restoreAllMocks();
  });

  function respond(status: number, body: unknown): Response {
    return {
      ok: status >= 200 && status < 300,
      status,
      json: async () => body,
    } as Response;
  }

  // The state the persona gate produces: a valid session for an account no
  // persona claims. It must not fall through to the signed-out state, which
  // would show a sign-in button that loops back to this same refusal.
  it("records access denial instead of signing the caller out", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      respond(403, { status: 403, detail: "no persona", email: "nobody@example.com" }),
    );

    await useAuthStore.getState().checkSession();

    const s = useAuthStore.getState();
    expect(s.accessDenied).toBe(true);
    expect(s.deniedEmail).toBe("nobody@example.com");
    expect(s.user).toBeNull();
    expect(s.loading).toBe(false);
    // authMethod stays "cookie" so Sign out can still clear the session.
    expect(s.authMethod).toBe("cookie");
  });

  it("renders the denial without an address when the body carries none", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(respond(403, { status: 403 }));

    await useAuthStore.getState().checkSession();

    expect(useAuthStore.getState().accessDenied).toBe(true);
    expect(useAuthStore.getState().deniedEmail).toBe("");
  });

  // A 401 is "who are you", not "you are refused": it must still reach the
  // signed-out state so the login form is offered.
  it("leaves accessDenied false on 401", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(respond(401, {}));

    await useAuthStore.getState().checkSession();

    const s = useAuthStore.getState();
    expect(s.accessDenied).toBe(false);
    expect(s.user).toBeNull();
  });

  it("clears a previous denial once the caller is granted access", async () => {
    useAuthStore.setState({ accessDenied: true, deniedEmail: "nobody@example.com" });
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      respond(200, { user_id: "u1", email: "a@example.com", roles: ["dp_analyst"], is_admin: false }),
    );

    await useAuthStore.getState().checkSession();

    const s = useAuthStore.getState();
    expect(s.accessDenied).toBe(false);
    expect(s.deniedEmail).toBe("");
    expect(s.user?.user_id).toBe("u1");
  });

  // An API key that authenticates but maps to no persona is a valid credential,
  // so the message must not blame the key.
  it("reports a persona problem, not a bad key, when loginApiKey gets 403", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(respond(403, {}));

    await expect(useAuthStore.getState().loginApiKey("k")).rejects.toThrow(/persona/);
  });

  it("keeps a stored key that authenticates but maps to no persona", async () => {
    sessionStorage.setItem("mcp-portal-api-key", "stored-key");
    const fetchMock = vi.spyOn(globalThis, "fetch");
    // Cookie attempt: unauthenticated. Stored-key attempt: authenticated but unmapped.
    fetchMock.mockResolvedValueOnce(respond(401, {}));
    fetchMock.mockResolvedValueOnce(respond(403, { email: "key@example.com" }));

    await useAuthStore.getState().checkSession();

    const s = useAuthStore.getState();
    expect(s.accessDenied).toBe(true);
    expect(s.deniedEmail).toBe("key@example.com");
    expect(sessionStorage.getItem("mcp-portal-api-key")).toBe("stored-key");
  });
});
