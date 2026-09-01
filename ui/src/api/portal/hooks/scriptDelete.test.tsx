import { describe, it, expect, vi, afterEach } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { useDeleteScript } from "./scriptDelete";
import { scriptsKey } from "./scriptKeys";

// Removing a script from the portal (#1575) has to leave the cache in the shape
// the reader's next screen needs: the listing refreshed, and the deleted
// script's own entries gone rather than sitting there ready to render a script
// that no longer exists if somebody walks back to its address. What is asserted
// below is exactly that — which entries survive, which do not, and that a
// refused delete changes neither.

const DELETED = {
  status: "deleted",
  name: "daily-sales-report",
  message: "daily-sales-report is gone, with its saved versions.",
};

function stubDelete() {
  vi.stubGlobal(
    "fetch",
    vi.fn(() => Promise.resolve(new Response(JSON.stringify(DELETED), { status: 200 }))),
  );
}

// harness renders the hook against a client already holding the deleted
// script's contract, which is the state the page is in when the control is
// pressed.
function harness(scriptID: string) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  qc.setQueryData([...scriptsKey, scriptID, "contract"], { owned: true });
  qc.setQueryData([...scriptsKey, "other", "contract"], { owned: true });
  const invalidated: unknown[][] = [];
  vi.spyOn(qc, "invalidateQueries").mockImplementation((filters?: { queryKey?: unknown }) => {
    invalidated.push((filters?.queryKey as unknown[]) ?? []);
    return Promise.resolve();
  });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
  const { result } = renderHook(() => useDeleteScript(scriptID), { wrapper });
  return { result, invalidated, qc };
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("useDeleteScript", () => {
  it("asks the platform to remove that script and reports what it answered", async () => {
    stubDelete();
    const { result } = harness("script-001");

    await result.current.mutateAsync();

    const call = vi.mocked(fetch).mock.calls[0]!;
    expect(call[0]).toBe("/api/v1/portal/scripts/script-001");
    expect((call[1] as RequestInit).method).toBe("DELETE");
    await waitFor(() => expect(result.current.data).toEqual(DELETED));
  });

  it("drops the deleted script's own entries and leaves every other script's alone", async () => {
    stubDelete();
    const { result, invalidated, qc } = harness("script-001");

    await result.current.mutateAsync();

    await waitFor(() =>
      expect(qc.getQueryData([...scriptsKey, "script-001", "contract"])).toBeUndefined(),
    );
    expect(qc.getQueryData([...scriptsKey, "other", "contract"])).toBeDefined();
    expect(invalidated).toEqual([[...scriptsKey]]);
  });

  it("leaves the cache alone when the delete is refused", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(() =>
        Promise.resolve(new Response(JSON.stringify({ detail: "script not found" }), { status: 404 })),
      ),
    );
    const { result, invalidated, qc } = harness("script-001");

    await expect(result.current.mutateAsync()).rejects.toThrow("script not found");

    expect(qc.getQueryData([...scriptsKey, "script-001", "contract"])).toBeDefined();
    expect(invalidated).toEqual([]);
  });
});
