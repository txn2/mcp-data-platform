import { describe, it, expect } from "vitest";
import type { InfiniteData } from "@tanstack/react-query";
import { nextOffset, flattenPages, toPaginated } from "./infinite";
import type { PaginatedResponse } from "../types";

interface Row {
  id: string;
}

const rowKey = (r: Row): string => r.id;

function infinite(pages: PaginatedResponse<Row>[]): InfiniteData<PaginatedResponse<Row>> {
  return { pages, pageParams: pages.map((_, i) => i * 50) };
}

describe("toPaginated", () => {
  it("adapts a {rows,total} envelope into PaginatedResponse", () => {
    const p = toPaginated([{ id: "a" }, { id: "b" }], 7, 50, 100);
    expect(p.data.map((r) => r.id)).toEqual(["a", "b"]);
    expect(p.total).toBe(7);
    expect(p.limit).toBe(50);
    expect(p.offset).toBe(100);
  });

  it("treats an undefined row list as empty (so a null-array response ends paging)", () => {
    const p = toPaginated<Row>(undefined, 0, 50, 0);
    expect(p.data).toEqual([]);
  });

  it("feeds nextOffset/flattenPages so an adapted surface reuses the shared path", () => {
    // Two adapted pages of a resources-like envelope: 2 then 1 row of total 3.
    const p0 = toPaginated([{ id: "a" }, { id: "b" }], 3, 2, 0);
    const p1 = toPaginated([{ id: "c" }], 3, 2, 2);
    // First page still has rows to fetch (2 of 3); after both, none remain.
    expect(nextOffset([p0])).toBe(2);
    expect(nextOffset([p0, p1])).toBeUndefined();
    const merged = flattenPages(infinite([p0, p1]), rowKey);
    expect(merged?.data.map((r) => r.id)).toEqual(["a", "b", "c"]);
    expect(merged?.total).toBe(3);
  });
});
