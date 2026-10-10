import { describe, it, expect, vi, afterEach } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { useScriptListing, type ScriptListFilter } from "./scriptListFilter";

// A click on a column header changes the listing's order and so its query.
// The rows already shown stay until the new order arrives: with nothing kept,
// the listing read as loading and replaced the whole table, the header that
// was clicked included, with a loading line on every sort.

afterEach(() => {
  vi.unstubAllGlobals();
});

function listing(names: string[]) {
  return new Response(
    JSON.stringify({ data: names.map((n) => ({ script: { id: n, display_name: n } })), total: names.length }),
    { status: 200, headers: { "Content-Type": "application/json" } },
  );
}

describe("useScriptListing", () => {
  it("keeps the rows shown while a new order loads", async () => {
    let release: (r: Response) => void = () => {};
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(listing(["Alpha", "Beta"]))
      .mockReturnValueOnce(new Promise<Response>((resolve) => (release = resolve)));
    vi.stubGlobal("fetch", fetch);
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={qc}>{children}</QueryClientProvider>
    );
    const { result, rerender } = renderHook((filter: ScriptListFilter) => useScriptListing(filter), {
      wrapper,
      initialProps: { sort: "display_name", dir: "asc" },
    });
    await waitFor(() => expect(result.current.data?.data).toHaveLength(2));

    rerender({ sort: "display_name", dir: "desc" });
    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(2));
    expect(result.current.isLoading).toBe(false);
    expect(result.current.data?.data.map((r) => r.script.display_name)).toEqual(["Alpha", "Beta"]);

    release(listing(["Beta", "Alpha"]));
    await waitFor(() =>
      expect(result.current.data?.data.map((r) => r.script.display_name)).toEqual(["Beta", "Alpha"]),
    );
  });
});
