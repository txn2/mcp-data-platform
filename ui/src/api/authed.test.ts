import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { authedFetch } from "./authed";
import { useAuthStore } from "@/stores/auth";

// authedFetch carries the session's credential. The Parquet viewer hands it a
// Headers instance holding a Range (#1833), which spreading into a plain object
// silently emptied.
describe("authedFetch", () => {
  const fetchMock = vi.fn((_url: string, _init?: RequestInit) => Promise.resolve(new Response("", { status: 206 })));

  beforeEach(() => {
    vi.stubGlobal("fetch", fetchMock);
    fetchMock.mockClear();
  });
  afterEach(() => vi.unstubAllGlobals());

  function sentHeaders(): Record<string, string> {
    const init = fetchMock.mock.calls[0]?.[1];
    return (init?.headers ?? {}) as Record<string, string>;
  }

  it("keeps the headers of a Headers instance and adds the API key", async () => {
    useAuthStore.setState({ authMethod: "apikey", apiKey: "k-1", csrfToken: "" });
    await authedFetch("/c", { headers: new Headers({ Range: "bytes=0-3" }) });
    expect(sentHeaders()).toEqual({ range: "bytes=0-3", "X-API-Key": "k-1" });
  });

  it("keeps the headers of a plain object, and sends no key for a cookie session", async () => {
    useAuthStore.setState({ authMethod: "cookie", apiKey: "", csrfToken: "t" });
    await authedFetch("/c", { headers: { Accept: "text/plain" } });
    expect(sentHeaders()).toEqual({ accept: "text/plain" });
    expect(fetchMock.mock.calls[0]?.[1]?.credentials).toBe("include");
  });
});
