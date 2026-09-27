import { describe, expect, it } from "vitest";
import { wallClock, zonedInstant } from "./zonedTime";

describe("zonedTime", () => {
  it("reads an instant on another zone's clock", () => {
    const w = wallClock(
      new Date("2026-01-14T15:30:00Z"),
      "America/Los_Angeles",
    );
    expect(w).toEqual({
      year: 2026,
      month: 0,
      day: 14,
      hour: 7,
      minute: 30,
      weekday: 3,
    });
  });

  it("finds the instant a zone's clock reads a wall time", () => {
    expect(
      zonedInstant("America/New_York", {
        year: 2026,
        month: 0,
        day: 14,
        hour: 7,
      }).toISOString(),
    ).toBe("2026-01-14T12:00:00.000Z");
    expect(
      zonedInstant("America/New_York", {
        year: 2026,
        month: 6,
        day: 14,
        hour: 7,
      }).toISOString(),
    ).toBe("2026-07-14T11:00:00.000Z");
    expect(
      zonedInstant("UTC", { year: 2026, month: 0, day: 32 }).toISOString(),
    ).toBe("2026-02-01T00:00:00.000Z");
  });

  it("puts a day that loses an hour at its real midnights", () => {
    const start = zonedInstant("America/Los_Angeles", {
      year: 2026,
      month: 2,
      day: 8,
    });
    const end = zonedInstant("America/Los_Angeles", {
      year: 2026,
      month: 2,
      day: 9,
    });
    expect(end.getTime() - start.getTime()).toBe(23 * 3_600_000);
  });

  it("resolves a wall time a spring-forward gap skips to the hour after", () => {
    const at = zonedInstant("America/Los_Angeles", {
      year: 2026,
      month: 2,
      day: 8,
      hour: 2,
      minute: 30,
    });
    expect(wallClock(at, "America/Los_Angeles").hour).toBe(3);
  });

  it("takes the first of a fall-back hour the clock reads twice", () => {
    expect(
      zonedInstant("America/Los_Angeles", {
        year: 2026,
        month: 10,
        day: 1,
        hour: 1,
        minute: 30,
      }).toISOString(),
    ).toBe("2026-11-01T08:30:00.000Z");
  });
});
