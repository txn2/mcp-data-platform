import { describe, it, expect } from "vitest";
import { byName, compareNames, compareOrdinal, comparePaths, sortNames, sortOptions } from "./sortByName";

describe("compareNames", () => {
  it("ignores case", () => {
    expect(sortNames(["beta", "Alpha", "gamma", "Delta"])).toEqual(["Alpha", "beta", "Delta", "gamma"]);
  });

  it("orders numbers by value", () => {
    expect(sortNames(["conn-10", "Conn-11", "conn-2", "conn-1"])).toEqual([
      "conn-1",
      "conn-2",
      "conn-10",
      "Conn-11",
    ]);
  });

  it("puts accented letters next to their base letter", () => {
    expect(sortNames(["zeta", "Ébène", "eagle", "fox"])).toEqual(["eagle", "Ébène", "fox", "zeta"]);
  });

  it("is a total order: names equal but for case still compare unequal, consistently", () => {
    expect(compareNames("a", "A")).not.toBe(0);
    expect(Math.sign(compareNames("a", "A"))).toBe(-Math.sign(compareNames("A", "a")));
    expect(sortNames(["a", "A", "a"])).toEqual(sortNames(["A", "a", "a"]));
    expect(compareNames("same", "same")).toBe(0);
  });
});

describe("compareOrdinal", () => {
  it("compares by code unit", () => {
    expect(compareOrdinal("2026-01-02", "2026-01-10")).toBeLessThan(0);
    expect(compareOrdinal("b", "B")).toBeGreaterThan(0);
    expect(compareOrdinal("x", "x")).toBe(0);
  });
});

describe("sortNames", () => {
  it("returns [] for no input", () => {
    expect(sortNames([])).toEqual([]);
    expect(sortNames(new Set<string>())).toEqual([]);
  });

  it("does not change its input", () => {
    const input = ["b", "a"];
    sortNames(input);
    expect(input).toEqual(["b", "a"]);
  });
});

describe("byName", () => {
  it("orders items by the name the key reads", () => {
    const items = [{ n: "conn-10" }, { n: "Conn-2" }, { n: "alpha" }];
    expect([...items].sort(byName((i) => i.n)).map((i) => i.n)).toEqual(["alpha", "Conn-2", "conn-10"]);
  });
});

describe("sortOptions", () => {
  it("keeps pinned first and last entries where they were put", () => {
    const options = [
      { value: "", label: "All connections" },
      { value: "z", label: "zeta" },
      { value: "c10", label: "conn-10" },
      { value: "new", label: "New folder" },
      { value: "c2", label: "Conn-2" },
    ];
    const out = sortOptions(options, {
      pinFirst: (o) => o.value === "",
      pinLast: (o) => o.value === "new",
    });
    expect(out.map((o) => o.label)).toEqual(["All connections", "Conn-2", "conn-10", "zeta", "New folder"]);
    expect(options[1]?.label).toBe("zeta");
  });

  it("returns [] for no options", () => {
    expect(sortOptions([])).toEqual([]);
  });
});

describe("comparePaths", () => {
  it("keeps each folder's children directly after it, each level in name order", () => {
    const paths = ["a-c", "a/b", "A", "a/B10", "a/b2", "b"];
    expect([...paths].sort(comparePaths)).toEqual(["A", "a/b", "a/b2", "a/B10", "a-c", "b"]);
  });
});
