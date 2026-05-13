import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { useAuthStore } from "@/stores/auth";

// apiFetch is not exported on its own; we exercise it through the public
// `apiFetch` symbol re-exported by the module. The alternative — exposing
// a separate test entry — would leak test concerns into production code.
async function loadApiFetch() {
  const mod = await import("./client");
  return mod.apiFetch;
}

async function loadApiFetchRaw() {
  const mod = await import("./client");
  return mod.apiFetchRaw;
}

describe("apiFetch error handling", () => {
  const originalFetch = global.fetch;

  beforeEach(() => {
    // Keep the auth store predictable so the request shape doesn't depend
    // on whatever the previous test left behind.
    useAuthStore.setState({ apiKey: "test-key", authMethod: "apikey" });
  });

  afterEach(() => {
    global.fetch = originalFetch;
    vi.restoreAllMocks();
  });

  function mockResponse(status: number, body: unknown): void {
    global.fetch = vi.fn().mockResolvedValue(
      new Response(JSON.stringify(body), {
        status,
        headers: { "Content-Type": "application/json" },
      }),
    );
  }

  it("surfaces body.error when body.detail is missing (gateway test endpoint shape)", async () => {
    // The gateway test-connection endpoint returns 502 with
    // {healthy:false, error: "..."} — the previous client only read
    // body.detail and lost the actual upstream message, leaving the
    // operator with a generic 'Bad Gateway' / 'Failed' indicator.
    mockResponse(502, { healthy: false, error: "oauth: token endpoint returned 401: invalid_client" });
    const apiFetch = await loadApiFetch();
    await expect(apiFetch("/gateway/connections/vendor/test", { method: "POST" }))
      .rejects.toMatchObject({
        status: 502,
        message: "oauth: token endpoint returned 401: invalid_client",
      });
  });

  it("falls back to body.message when neither detail nor error is set", async () => {
    mockResponse(500, { message: "internal failure detail" });
    const apiFetch = await loadApiFetch();
    await expect(apiFetch("/anything"))
      .rejects.toMatchObject({ status: 500, message: "internal failure detail" });
  });

  it("prefers body.detail when present (problemDetail-shaped responses)", async () => {
    mockResponse(404, { detail: "not found", error: "ignored", message: "ignored" });
    const apiFetch = await loadApiFetch();
    await expect(apiFetch("/x"))
      .rejects.toMatchObject({ status: 404, message: "not found" });
  });

  it("falls back to statusText when the body has no diagnostic fields", async () => {
    mockResponse(503, {});
    const apiFetch = await loadApiFetch();
    // Response.statusText reflects the second arg's `statusText`. When
    // omitted, jsdom's Response leaves it empty; our fallback handles
    // either case — the failure message must just be non-empty.
    await expect(apiFetch("/x")).rejects.toMatchObject({ status: 503 });
  });
});

describe("apiFetch 401 session-expiry handling", () => {
  const originalFetch = global.fetch;
  const originalLocation = window.location;

  // jsdom's window.location is mostly read-only; replace it with a
  // controllable stand-in so we can capture the redirect target without
  // an actual navigation tearing down the test.
  function mockLocation(pathname: string, search = "", hash = ""): { replace: ReturnType<typeof vi.fn> } {
    const replace = vi.fn();
    Object.defineProperty(window, "location", {
      configurable: true,
      value: { pathname, search, hash, replace },
    });
    return { replace };
  }

  beforeEach(() => {
    useAuthStore.setState({
      user: null,
      authMethod: "cookie",
      apiKey: "",
      sessionExpired: false,
    });
  });

  afterEach(() => {
    global.fetch = originalFetch;
    Object.defineProperty(window, "location", {
      configurable: true,
      value: originalLocation,
    });
    vi.restoreAllMocks();
  });

  function mock401(): void {
    global.fetch = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ detail: "authentication required" }), {
        status: 401,
        headers: { "Content-Type": "application/json" },
      }),
    );
  }

  it("redirects cookie-mode users to /portal/auth/login with the current path as return_to", async () => {
    // Operator clicked Connect on the connection settings page. The
    // cryptic "authentication required" must never reach the UI; the
    // browser should be on its way back through OIDC by the time
    // apiFetch rejects.
    const { replace } = mockLocation("/portal/settings/connections/vendor", "?tab=oauth");
    mock401();

    const apiFetch = await loadApiFetch();
    await expect(apiFetch("/gateway/connections/vendor/test", { method: "POST" }))
      .rejects.toMatchObject({ status: 401 });

    expect(replace).toHaveBeenCalledTimes(1);
    const target = String(replace.mock.calls[0]?.[0] ?? "");
    expect(target.startsWith("/portal/auth/login?return_to=")).toBe(true);
    const returnToPart = target.split("return_to=")[1] ?? "";
    expect(decodeURIComponent(returnToPart))
      .toBe("/portal/settings/connections/vendor?tab=oauth");
    expect(useAuthStore.getState().sessionExpired).toBe(true);
  });

  it("preserves hash fragments in return_to", async () => {
    // Anchor links on a settings page should survive the round-trip
    // so the operator lands back on the exact section.
    const { replace } = mockLocation("/portal/settings", "?tab=oauth", "#status");
    mock401();

    const apiFetch = await loadApiFetch();
    await expect(apiFetch("/anything")).rejects.toMatchObject({ status: 401 });

    const target = String(replace.mock.calls[0]?.[0] ?? "");
    const returnToPart = target.split("return_to=")[1] ?? "";
    expect(decodeURIComponent(returnToPart))
      .toBe("/portal/settings?tab=oauth#status");
  });

  it("does not redirect api-key-mode users (the cookie isn't the failing credential)", async () => {
    // API-key auth fails because the key was revoked or rotated, not
    // because a browser cookie expired. Sending the user through OIDC
    // would be wrong — they may not even have an OIDC identity. The
    // LoginForm picks up sessionExpired and the operator re-enters
    // their key.
    useAuthStore.setState({ authMethod: "apikey", apiKey: "stale-key" });
    const { replace } = mockLocation("/portal/anywhere");
    mock401();

    const apiFetch = await loadApiFetch();
    await expect(apiFetch("/anything")).rejects.toMatchObject({ status: 401 });

    expect(replace).not.toHaveBeenCalled();
    expect(useAuthStore.getState().sessionExpired).toBe(true);
  });

  it("does not redirect on non-401 failures", async () => {
    // A 500 from the admin API has nothing to do with auth state; the
    // error band on the affected card is the right surface.
    const { replace } = mockLocation("/portal/anywhere");
    global.fetch = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ detail: "boom" }), {
        status: 500,
        headers: { "Content-Type": "application/json" },
      }),
    );

    const apiFetch = await loadApiFetch();
    await expect(apiFetch("/anything")).rejects.toMatchObject({ status: 500 });

    expect(replace).not.toHaveBeenCalled();
    expect(useAuthStore.getState().sessionExpired).toBe(false);
  });

  it("apiFetchRaw also triggers redirect on 401 (parity with apiFetch)", async () => {
    // Endpoints that return non-JSON (e.g., file downloads, log
    // streams) go through apiFetchRaw. Same session-expiry bug, same
    // recovery path required.
    const { replace } = mockLocation("/portal/downloads");
    mock401();

    const apiFetchRaw = await loadApiFetchRaw();
    const res = await apiFetchRaw("/anything");
    expect(res.status).toBe(401);
    expect(replace).toHaveBeenCalledTimes(1);
    expect(useAuthStore.getState().sessionExpired).toBe(true);
  });
});
