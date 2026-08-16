import { describe, it, expect } from "vitest";
import {
  dateColumnFor,
  dateLabelFor,
  defaultDirFor,
  sortRowsBy,
  timeValue,
  toggleSort,
  type AssetSortKey,
  type ListSort,
} from "./listSort";

describe("toggleSort", () => {
  it("reverses the column that is already sorted", () => {
    const current: ListSort<AssetSortKey> = { key: "updated_at", dir: "desc" };
    expect(toggleSort(current, "updated_at")).toEqual({ key: "updated_at", dir: "asc" });
    expect(toggleSort({ key: "updated_at", dir: "asc" }, "updated_at")).toEqual({
      key: "updated_at",
      dir: "desc",
    });
  });

  it("adopts a new column at its own default direction", () => {
    const current: ListSort<AssetSortKey> = { key: "updated_at", dir: "asc" };
    // Text reads A-Z; a size reads largest first. Carrying the previous
    // direction over would open Name at Z-A.
    expect(toggleSort(current, "name")).toEqual({ key: "name", dir: "asc" });
    expect(toggleSort(current, "size_bytes")).toEqual({ key: "size_bytes", dir: "desc" });
  });
});

describe("defaultDirFor", () => {
  it("reads text upward and everything else downward", () => {
    expect(defaultDirFor("name")).toBe("asc");
    expect(defaultDirFor("updated_at")).toBe("desc");
    expect(defaultDirFor("size_bytes")).toBe("desc");
  });
});

describe("dateColumnFor", () => {
  it("shows the timestamp the list is ordered by", () => {
    expect(dateColumnFor("created_at")).toBe("created_at");
    expect(dateColumnFor("updated_at")).toBe("updated_at");
  });

  it("falls back to last-touched for orderings that are not dates", () => {
    expect(dateColumnFor("name")).toBe("updated_at");
    expect(dateColumnFor("size_bytes")).toBe("updated_at");
  });
});

describe("dateLabelFor", () => {
  it("names a shared row's date by when it was shared", () => {
    expect(dateLabelFor("updated_at", true)).toBe("Shared");
    expect(dateLabelFor("created_at", true)).toBe("Shared");
  });

  it("otherwise names the column", () => {
    expect(dateLabelFor("updated_at", false)).toBe("Updated");
    expect(dateLabelFor("created_at", false)).toBe("Created");
  });
});

describe("timeValue", () => {
  it("compares instants, not the text of their offsets", () => {
    // The same moment written two ways. Comparing the strings would order
    // these by their offset; comparing the instants finds them equal.
    expect(timeValue("2025-06-01T12:00:00Z")).toBe(timeValue("2025-06-01T07:00:00-05:00"));
  });

  it("treats a missing or unparseable timestamp as the epoch", () => {
    expect(timeValue(undefined)).toBe(0);
    expect(timeValue("not a date")).toBe(0);
  });
});

describe("sortRowsBy", () => {
  const rows = [
    { id: "b", name: "beta", size: 10 },
    { id: "a", name: "alpha", size: 30 },
    { id: "c", name: "gamma", size: 20 },
  ];

  it("orders numerically without stringifying", () => {
    // "10" sorts before "3" as text; as numbers it does not.
    const nums = [{ id: "a", n: 3 }, { id: "b", n: 10 }, { id: "c", n: 2 }];
    expect(sortRowsBy(nums, "asc", (r) => r.n, (r) => r.id).map((r) => r.n)).toEqual([2, 3, 10]);
  });

  it("orders text both ways", () => {
    expect(sortRowsBy(rows, "asc", (r) => r.name, (r) => r.id).map((r) => r.name)).toEqual([
      "alpha",
      "beta",
      "gamma",
    ]);
    expect(sortRowsBy(rows, "desc", (r) => r.name, (r) => r.id).map((r) => r.name)).toEqual([
      "gamma",
      "beta",
      "alpha",
    ]);
  });

  it("breaks ties on id so equal values cannot swap places", () => {
    const tied = [
      { id: "c", n: 1 },
      { id: "a", n: 1 },
      { id: "b", n: 1 },
    ];
    expect(sortRowsBy(tied, "asc", (r) => r.n, (r) => r.id).map((r) => r.id)).toEqual([
      "a",
      "b",
      "c",
    ]);
    expect(sortRowsBy(tied, "desc", (r) => r.n, (r) => r.id).map((r) => r.id)).toEqual([
      "c",
      "b",
      "a",
    ]);
  });

  it("leaves the caller's array untouched", () => {
    const original = [...rows];
    sortRowsBy(rows, "asc", (r) => r.name, (r) => r.id);
    expect(rows).toEqual(original);
  });
});
