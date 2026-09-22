import { afterEach, describe, expect, it, vi } from "vitest";

import { coverageFigure, untilTime } from "./helpers";

describe("untilTime", () => {
  // Restored here rather than at the end of the frozen-clock test: a failed
  // assertion there would skip the restore and leak the frozen clock into
  // the cases below, turning one real failure into a cascade.
  afterEach(() => {
    vi.useRealTimers();
  });

  it("renders a forward-looking distance for a future instant", () => {
    const now = new Date("2026-08-18T12:00:00Z");
    vi.useFakeTimers();
    vi.setSystemTime(now);
    expect(untilTime(new Date(now.getTime() + 45_000).toISOString())).toBe(
      "in 45s",
    );
    expect(untilTime(new Date(now.getTime() + 30 * 60_000).toISOString())).toBe(
      "in 30m",
    );
    expect(
      untilTime(new Date(now.getTime() + 6 * 3_600_000).toISOString()),
    ).toBe("in 6h");
    expect(
      untilTime(new Date(now.getTime() + 50 * 3_600_000).toISOString()),
    ).toBe("in 2d");
    vi.useRealTimers();
  });

  // A deadline that has already passed means the next sweep picks the unit
  // up, so it must not render as a negative distance or as "never".
  it("renders an elapsed deadline as imminent", () => {
    expect(untilTime(new Date(Date.now() - 60_000).toISOString())).toBe(
      "shortly",
    );
  });

  it("renders nothing for a missing or unparseable value", () => {
    expect(untilTime(undefined)).toBe("");
    expect(untilTime("not a date")).toBe("");
  });
});

describe("coverageFigure", () => {
  it("never reads 100% while a vector is missing", () => {
    expect(coverageFigure(833_090, 835_775)).toEqual({
      complete: false,
      text: "99.6%",
      width: 99.6,
    });
    expect(coverageFigure(835_774, 835_775)).toEqual({
      complete: false,
      text: "99.9%",
      width: 99.9,
    });
    expect(coverageFigure(1, 3)).toEqual({
      complete: false,
      text: "33.3%",
      width: 33.3,
    });
    expect(coverageFigure(0, 10)).toEqual({
      complete: false,
      text: "0.0%",
      width: 0,
    });
  });

  it("is complete only when indexed reaches expected", () => {
    expect(coverageFigure(835_775, 835_775)).toEqual({
      complete: true,
      text: "100%",
      width: 100,
    });
    expect(coverageFigure(12, 10).complete).toBe(true);
  });
});
