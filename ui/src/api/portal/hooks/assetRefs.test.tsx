import { describe, it, expect, vi, afterEach } from "vitest";
import { renderHook } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { useAddAssetRef, useRemoveAssetRef } from "./assetRefs";

// The server rewrites an asset's declared references into working URLs as it
// serves the content, so adding or removing one changes the body the viewer
// shows without changing the asset's content or version (#1835). Each change
// must therefore refetch the content and every version body the viewer holds,
// or the page keeps rendering the old URIs.

function harness<T>(hook: () => T) {
  vi.stubGlobal(
    "fetch",
    vi.fn(() => Promise.resolve(new Response(JSON.stringify({ references: [] }), { status: 200 }))),
  );
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const invalidated: unknown[][] = [];
  vi.spyOn(qc, "invalidateQueries").mockImplementation((filters?: { queryKey?: unknown }) => {
    invalidated.push((filters?.queryKey as unknown[]) ?? []);
    return Promise.resolve();
  });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
  const { result } = renderHook(hook, { wrapper });
  return { result, invalidated };
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("asset reference mutations", () => {
  it("refetch the served content when a reference is added", async () => {
    const { result, invalidated } = harness(() => useAddAssetRef("a1"));
    await result.current.mutateAsync({ kind: "resource", id: "res-logo" });
    expect(invalidated).toContainEqual(["asset-content", "a1"]);
    expect(invalidated).toContainEqual(["version-content", "a1"]);
  });

  it("refetch the served content when a reference is removed", async () => {
    const { result, invalidated } = harness(() => useRemoveAssetRef("a1"));
    await result.current.mutateAsync({ kind: "resource", id: "res-logo" });
    expect(invalidated).toContainEqual(["asset-content", "a1"]);
    expect(invalidated).toContainEqual(["version-content", "a1"]);
  });
});
