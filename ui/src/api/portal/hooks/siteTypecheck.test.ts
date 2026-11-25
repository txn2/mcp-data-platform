import { describe, it, expect, vi, afterEach } from "vitest";
import { createElement } from "react";
import { act, renderHook } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { isPending, useReleaseTypecheck, useRetypecheck, useSiteTypechecks } from "./siteTypecheck";

function wrapper() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return ({ children }: { children: React.ReactNode }) => createElement(QueryClientProvider, { client: qc }, children);
}

const json = (body: unknown, status = 200) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } }));

describe("site type check hooks (#2793)", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("reads a pending check as one still to finish", () => {
    expect(isPending("queued")).toBe(true);
    expect(isPending("running")).toBe(true);
    expect(isPending("clean")).toBe(false);
    expect(isPending("over_limit")).toBe(false);
    expect(isPending(undefined)).toBe(false);
  });

  it("reads a release's check and the site's summaries", async () => {
    const fetchMock = vi.fn((url: string) =>
      url.endsWith("/typechecks")
        ? json({ releases: { "12": { status: "errors", errors: 3 } } })
        : json({ release: 12, check: { id: 1, release: 12, status: "clean", tsconfig: "tsconfig.json", environment: "dev", created_at: "2026-11-01T00:00:00Z" } }),
    );
    vi.stubGlobal("fetch", fetchMock);
    const one = renderHook(() => useReleaseTypecheck("my site", 12), { wrapper: wrapper() });
    await vi.waitFor(() => expect(one.result.current.data?.check?.status).toBe("clean"));
    expect(fetchMock.mock.calls[0]![0]).toContain("/api/v1/portal/sites/my%20site/releases/12/typecheck");
    const all = renderHook(() => useSiteTypechecks("my site"), { wrapper: wrapper() });
    await vi.waitFor(() => expect(all.result.current.data?.releases["12"]?.errors).toBe(3));
  });

  it("checks a release again with POST", async () => {
    const fetchMock = vi.fn((_url: string, _init?: RequestInit) => json({ release: 12, check: null }, 202));
    vi.stubGlobal("fetch", fetchMock);
    const { result } = renderHook(() => useRetypecheck("marketing", 12), { wrapper: wrapper() });
    await act(() => result.current.mutateAsync());
    expect(fetchMock.mock.calls[0]![0]).toContain("/sites/marketing/releases/12/typecheck");
    expect(fetchMock.mock.calls[0]![1]?.method).toBe("POST");
  });
});
