import { describe, it, expect } from "vitest";
import type { InfiniteData } from "@tanstack/react-query";
import { flattenPages, nextOffset, assetKey, sharedKey } from "./assets";
import type { Asset, PaginatedResponse, SharedAsset } from "../types";

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
