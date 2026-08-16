import { describe, it, expect } from "vitest";
import {
  DEFAULT_SESSION_WINDOW,
  SESSION_WINDOW_OPTIONS,
  windowStart,
} from "./window";

const NOW = Date.parse("2026-08-16T12:00:00Z");

describe("the sessions list window", () => {
  it("bounds the query to the chosen span", () => {
    expect(windowStart("24h", NOW)).toBe("2026-08-15T12:00:00.000Z");
    expect(windowStart("7d", NOW)).toBe("2026-08-09T12:00:00.000Z");
    expect(windowStart("30d", NOW)).toBe("2026-07-17T12:00:00.000Z");
  });

  it("states no bound at all for all time, rather than a very old one", () => {
    expect(windowStart("all", NOW)).toBeUndefined();
  });

  it("offers all time, so a bounded default never silently withholds", () => {
    expect(SESSION_WINDOW_OPTIONS.map((o) => o.value)).toContain("all");
    expect(windowStart(DEFAULT_SESSION_WINDOW, NOW)).toBeDefined();
  });
});
