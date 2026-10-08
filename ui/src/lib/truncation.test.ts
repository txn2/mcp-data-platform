import { describe, expect, it } from "vitest";
import { isTruncated, truncationLabel, truncationOf, TRUNCATED_TAG } from "./truncation";

describe("truncation", () => {
  it("reads the tag", () => {
    expect(isTruncated(["sales", TRUNCATED_TAG])).toBe(true);
    expect(isTruncated(["sales"])).toBe(false);
    expect(isTruncated(undefined)).toBe(false);
  });

  it("reads a version's cut", () => {
    expect(truncationOf({ truncated: true, limit_applied: 100000, limit_unit: "rows", limit_source: "deployment" })).toEqual({
      limit: 100000,
      unit: "rows",
      source: "deployment",
    });
    expect(truncationOf({ truncated: false, limit_applied: 5 })).toBeNull();
    expect(truncationOf({ run_id: "r1" })).toBeNull();
    expect(truncationOf(undefined)).toBeNull();
    expect(truncationOf({ truncated: true })).toEqual({ limit: 0, unit: "rows", source: "" });
  });

  it("says it plainly", () => {
    expect(truncationLabel({ limit: 100000, unit: "rows", source: "deployment" })).toBe(
      "Incomplete: truncated at 100,000 rows",
    );
    expect(truncationLabel({ limit: 10, unit: "pages", source: "request" })).toBe("Incomplete: truncated at 10 pages");
    expect(truncationLabel(null)).toBe("Incomplete: the export that wrote this file was cut at a limit");
  });
});
