import { describe, it, expect, vi } from "vitest";
import { createElement } from "react";
import { renderHook } from "@testing-library/react";
import { QueryClient, QueryClientProvider, type InfiniteData } from "@tanstack/react-query";
import { flattenPages, nextOffset, assetKey, sharedKey, useAssetContent } from "./assets";
import type { Asset, PaginatedResponse, SharedAsset } from "../types";
import { ContentFetchError } from "@/lib/contentFetch";

function asset(id: string, overrides: Partial<Asset> = {}): Asset {
  return {
    id,
    owner_id: "u1",
    owner_email: "u@example.com",
    name: `asset-${id}`,
    content_type: "text/html",
    s3_bucket: "b",
    s3_key: "k",
    size_bytes: 1,
    tags: [],
    created_at: "2025-01-01T00:00:00Z",
    updated_at: "2025-01-01T00:00:00Z",
    ...overrides,
  } as Asset;
}

function page(
  data: Asset[],
  total: number,
  extra: Partial<PaginatedResponse<Asset>> = {},
): PaginatedResponse<Asset> {
  return { data, total, limit: 50, offset: 0, ...extra };
}

function infinite(pages: PaginatedResponse<Asset>[]): InfiniteData<PaginatedResponse<Asset>> {
  return { pages, pageParams: pages.map((_, i) => i * 50) };
}

describe("flattenPages", () => {
  it("returns undefined before the first page resolves", () => {
    expect(flattenPages(undefined, assetKey)).toBeUndefined();
    expect(flattenPages(infinite([]), assetKey)).toBeUndefined();
  });

  it("concatenates rows across pages in fetch order", () => {
    const merged = flattenPages(
      infinite([page([asset("a"), asset("b")], 3), page([asset("c")], 3)]),
      assetKey,
    );
    expect(merged?.data.map((a) => a.id)).toEqual(["a", "b", "c"]);
  });

  it("de-duplicates rows that reappear across pages (window shift on insert)", () => {
    // Offset paging over a created_at DESC list can re-emit a row when an
    // insert shifts the window; the merged list must keep only the first.
    const merged = flattenPages(
      infinite([page([asset("a"), asset("b")], 3), page([asset("b"), asset("c")], 4)]),
      assetKey,
    );
    expect(merged?.data.map((a) => a.id)).toEqual(["a", "b", "c"]);
  });

  it("takes total from the latest page and limit from the first", () => {
    const merged = flattenPages(
      infinite([page([asset("a")], 200, { limit: 50 }), page([asset("b")], 230, { limit: 25 })]),
      assetKey,
    );
    expect(merged?.total).toBe(230);
    expect(merged?.limit).toBe(50);
  });

  it("unions share summaries across pages", () => {
    const merged = flattenPages(
      infinite([
        page([asset("a")], 2, { share_summaries: { a: { has_user_share: true, has_public_link: false } } }),
        page([asset("b")], 2, { share_summaries: { b: { has_user_share: false, has_public_link: true } } }),
      ]),
      assetKey,
    );
    expect(Object.keys(merged?.share_summaries ?? {}).sort()).toEqual(["a", "b"]);
  });
});

describe("nextOffset", () => {
  it("returns the fetched-row count while more rows remain", () => {
    expect(nextOffset([page([asset("a"), asset("b")], 5)])).toBe(2);
  });

  it("returns undefined once every row is fetched", () => {
    expect(nextOffset([page([asset("a")], 2), page([asset("b")], 2)])).toBeUndefined();
  });

  it("uses the latest page's total so rows added after the first fetch stay reachable", () => {
    // First page reported total 2; a later page reports 4 (rows added). Fetched
    // count is 3, below the fresh total of 4, so paging continues (offset 3).
    // Under a first-page-total cap of 2 it would have wrongly stopped.
    expect(nextOffset([page([asset("a"), asset("b")], 2), page([asset("c")], 4)])).toBe(3);
  });

  it("stops on an empty trailing page even if total is stale-high", () => {
    // total says 3 but the last fetch returned nothing (rows deleted); do not
    // spin the Load-more button forever.
    expect(nextOffset([page([asset("a")], 3), page([], 3)])).toBeUndefined();
  });
});

describe("key extractors", () => {
  it("assetKey keys by asset id; sharedKey keys by the underlying asset id", () => {
    expect(assetKey(asset("x"))).toBe("x");
    const shared = { asset: asset("y"), share_id: "s", shared_by: "z", shared_at: "", permission: "viewer" } as SharedAsset;
    expect(sharedKey(shared)).toBe("y");
  });
});

describe("useAssetContent", () => {
  const run = (asset?: { size_bytes: number; content_type: string; name: string }) => {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    return renderHook(() => useAssetContent("a1", asset), {
      wrapper: ({ children }) => createElement(QueryClientProvider, { client: qc }, children),
    });
  };

  // The content is read once the record says it is worth reading (#1833), and
  // "worth reading" is the family's own limit (#1874): a 5 MB CSV is read, as
  // its virtualized viewer can show it, and one past 32 MB is not.
  it("waits for the record, reads within the family's limit, skips past it and a range-read family", async () => {
    const fetchMock = vi.fn(() => Promise.resolve(new Response("a,b\n1,2\n", { status: 200 })));
    vi.stubGlobal("fetch", fetchMock);
    run(undefined);
    run({ size_bytes: 40 * 1024 * 1024, content_type: "text/csv", name: "huge.csv" });
    run({ size_bytes: 1024, content_type: "application/vnd.apache.parquet", name: "t.parquet" });
    await new Promise((r) => setTimeout(r, 20));
    expect(fetchMock).not.toHaveBeenCalled();

    const { result } = run({ size_bytes: 5 * 1024 * 1024, content_type: "text/csv", name: "big.csv" });
    await vi.waitFor(() => expect(result.current.data).toBe("a,b\n1,2\n"));
    vi.unstubAllGlobals();
  });

  it("fails a read with no body with its status, and a dropped request as a network error", async () => {
    vi.stubGlobal("fetch", vi.fn(() => Promise.resolve(new Response("denied", { status: 403 }))));
    const denied = run({ size_bytes: 12, content_type: "text/csv", name: "d.csv" });
    await vi.waitFor(() => expect(denied.result.current.error).toBeInstanceOf(ContentFetchError));
    expect((denied.result.current.error as ContentFetchError).status).toBe(403);

    vi.stubGlobal("fetch", vi.fn(() => Promise.reject(new TypeError("Failed to fetch"))));
    const dropped = run({ size_bytes: 12, content_type: "text/csv", name: "d.csv" });
    await vi.waitFor(() => expect(dropped.result.current.error?.message).toBe("network error"));
    vi.unstubAllGlobals();
  });
});
