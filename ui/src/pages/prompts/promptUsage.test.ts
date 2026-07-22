import { describe, expect, it } from "vitest";
import { formatLastRun, isInactive, matchesUsageFacet, usageSortValue } from "./promptUsage";

const now = new Date("2026-07-22T12:00:00Z");
const daysAgo = (n: number) => new Date(now.getTime() - n * 24 * 60 * 60 * 1000).toISOString();

describe("isInactive", () => {
  it("treats missing usage and zero runs as inactive", () => {
    expect(isInactive(undefined, now)).toBe(true);
    expect(isInactive({ run_count: 0 }, now)).toBe(true);
  });

  it("treats a recent run as active and an old run as inactive", () => {
    expect(isInactive({ run_count: 5, last_run_at: daysAgo(3) }, now)).toBe(false);
    expect(isInactive({ run_count: 5, last_run_at: daysAgo(59) }, now)).toBe(false);
    expect(isInactive({ run_count: 5, last_run_at: daysAgo(61) }, now)).toBe(true);
  });
});

describe("formatLastRun", () => {
  it("labels never-run prompts", () => {
    expect(formatLastRun(undefined, now)).toBe("Never");
    expect(formatLastRun({ run_count: 0 }, now)).toBe("Never");
  });

  it("renders compact relative ages", () => {
    expect(formatLastRun({ run_count: 1, last_run_at: daysAgo(0) }, now)).toBe("Today");
    expect(formatLastRun({ run_count: 1, last_run_at: daysAgo(1) }, now)).toBe("Yesterday");
    expect(formatLastRun({ run_count: 1, last_run_at: daysAgo(12) }, now)).toBe("12d ago");
    expect(formatLastRun({ run_count: 1, last_run_at: daysAgo(90) }, now)).toBe("3mo ago");
    expect(formatLastRun({ run_count: 1, last_run_at: daysAgo(400) }, now)).toBe("1y ago");
  });
});

describe("matchesUsageFacet", () => {
  const active = { run_count: 3, last_run_at: daysAgo(2) };
  it("filters by activity", () => {
    expect(matchesUsageFacet("all", undefined, now)).toBe(true);
    expect(matchesUsageFacet("active", active, now)).toBe(true);
    expect(matchesUsageFacet("active", undefined, now)).toBe(false);
    expect(matchesUsageFacet("inactive", undefined, now)).toBe(true);
    expect(matchesUsageFacet("inactive", active, now)).toBe(false);
  });
});

describe("usageSortValue", () => {
  it("orders by runs and recency with missing usage at zero", () => {
    expect(usageSortValue("runs", { run_count: 7, last_run_at: daysAgo(1) })).toBe(7);
    expect(usageSortValue("runs", undefined)).toBe(0);
    const t = usageSortValue("lastRun", { run_count: 1, last_run_at: daysAgo(1) });
    expect(t).toBeGreaterThan(0);
    expect(usageSortValue("lastRun", { run_count: 1 })).toBe(0);
  });
});
