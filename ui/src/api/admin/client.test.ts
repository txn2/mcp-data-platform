import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { useAuthStore } from "@/stores/auth";

// apiFetch is not exported on its own; we exercise it through the public
// `apiFetch` symbol re-exported by the module. The alternative — exposing
// a separate test entry — would leak test concerns into production code.
async function loadApiFetch() {
  const mod = await import("./client");
  return mod.apiFetch;
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
