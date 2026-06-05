import { describe, it, expect } from "vitest";
import { parseTags } from "./tags";

describe("parseTags", () => {
  it("trims, lowercases, drops empties, and de-duplicates preserving order", () => {
    expect(parseTags("  Sales , reporting,  SALES ,, ops ")).toEqual(["sales", "reporting", "ops"]);
  });

  it("returns an empty array for blank input", () => {
    expect(parseTags("")).toEqual([]);
    expect(parseTags("  ,  , ")).toEqual([]);
  });

  it("handles a single tag", () => {
    expect(parseTags("Finance")).toEqual(["finance"]);
  });
});
