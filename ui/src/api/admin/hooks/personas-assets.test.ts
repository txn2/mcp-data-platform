import { describe, it, expect, vi, afterEach } from "vitest";
import { createElement } from "react";
import { renderHook } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ContentFetchError } from "@/lib/contentFetch";
import { useAdminAssetContent } from "./personas-assets";

function run(asset?: { size_bytes: number; content_type: string; name: string }) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return renderHook(() => useAdminAssetContent("a1", asset), {
    wrapper: ({ children }) => createElement(QueryClientProvider, { client: qc }, children),
  });
}

describe("useAdminAssetContent", () => {
  afterEach(() => vi.unstubAllGlobals());

  // The admin viewer reads the rule the portal viewer reads (#1874): it used a
  // flat 2 MB of its own, so a 5 MB CSV the portal shows was refused here.
  it("reads a 5 MB CSV through the admin route and skips one past its family's limit", async () => {
    const fetchMock = vi.fn((_url: string) => Promise.resolve(new Response("a,b\n", { status: 200 })));
    vi.stubGlobal("fetch", fetchMock);

    run({ size_bytes: 40 * 1024 * 1024, content_type: "text/csv", name: "huge.csv" });
    run(undefined);
    await new Promise((r) => setTimeout(r, 20));
    expect(fetchMock).not.toHaveBeenCalled();

    const { result } = run({ size_bytes: 5 * 1024 * 1024, content_type: "text/csv", name: "big.csv" });
    await vi.waitFor(() => expect(result.current.data).toBe("a,b\n"));
    expect(fetchMock.mock.calls[0]?.[0]).toBe("/api/v1/admin/assets/a1/content");
  });

  it("fails a read with no body with its status", async () => {
    vi.stubGlobal("fetch", vi.fn(() => Promise.resolve(new Response("", { status: 500 }))));
    const { result } = run({ size_bytes: 12, content_type: "text/csv", name: "d.csv" });
    await vi.waitFor(() => expect(result.current.error).toBeInstanceOf(ContentFetchError));
    expect(result.current.error?.message).toBe("HTTP 500");
  });
});
