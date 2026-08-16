import { describe as suite, it, expect } from "vitest";
import {
  DEFAULT_CADENCE,
  describe as describeCadence,
  describeCron,
  fromCron,
  skipsShortMonths,
  toCron,
  type Cadence,
} from "./cadence";

// The translation between what a person picks and what the platform stores is
// the whole of this module, so it is tested as a round trip: every cadence the
// builder can produce must render to an expression that reads back as the same
// cadence. A drift there would silently re-time somebody's report.

const cadences: Cadence[] = [
  { kind: "hourly", minute: 0 },
  { kind: "hourly", minute: 45 },
  { kind: "daily", hour: 7, minute: 0 },
  { kind: "daily", hour: 0, minute: 5 },
  { kind: "daily", hour: 23, minute: 59 },
  { kind: "weekdays", hour: 7, minute: 30 },
  { kind: "weekly", days: [1], hour: 9, minute: 0 },
  { kind: "weekly", days: [0, 6], hour: 18, minute: 15 },
  { kind: "monthly", day: 1, hour: 6, minute: 0 },
  { kind: "monthly", day: 31, hour: 6, minute: 0 },
];

suite("cadence round trip", () => {
  it.each(cadences)("survives $kind", (c) => {
    expect(fromCron(toCron(c))).toEqual(c);
  });

  it("renders the expressions the platform documents", () => {
    expect(toCron({ kind: "weekdays", hour: 7, minute: 0 })).toBe("0 7 * * 1-5");
    expect(toCron({ kind: "daily", hour: 7, minute: 0 })).toBe("0 7 * * *");
    expect(toCron({ kind: "hourly", minute: 30 })).toBe("30 * * * *");
    expect(toCron({ kind: "weekly", days: [1, 3, 5], hour: 8, minute: 0 })).toBe("0 8 * * 1,3,5");
    expect(toCron({ kind: "monthly", day: 15, hour: 8, minute: 0 })).toBe("0 8 15 * *");
  });

  // A weekly cadence with nothing selected cannot fire; it falls back to every
  // day rather than producing an expression the server would refuse.
  it("never emits an empty day list", () => {
    expect(toCron({ kind: "weekly", days: [], hour: 8, minute: 0 })).toBe("0 8 * * *");
  });

  it("orders and deduplicates the days it was given", () => {
    expect(toCron({ kind: "weekly", days: [5, 1, 1], hour: 8, minute: 0 })).toBe("0 8 * * 1,5");
  });
});

suite("cadence from an expression it did not write", () => {
  // Anything outside the builder's shapes has to come back as custom: rewriting
  // an expression this module does not fully understand would re-time a
  // schedule somebody deliberately set through the tool.
  it.each([
    "*/30 * * * *",
    "0 7 * * 1-5,0",
    "@daily",
    "0 0 1 1 *",
    "0 0 * * 1-3",
    "0 9-17 * * *",
    "not a cron expression",
    "",
  ])("treats %s as custom", (spec) => {
    expect(fromCron(spec).kind).toBe("custom");
  });

  it("keeps the expression it could not read, so nothing is lost", () => {
    expect(fromCron(" */30 * * * * ")).toEqual({ kind: "custom", spec: "*/30 * * * *" });
  });

  it("reads a descriptor as custom rather than guessing", () => {
    expect(fromCron("@hourly")).toEqual({ kind: "custom", spec: "@hourly" });
  });
});

suite("describing a cadence", () => {
  it.each([
    [{ kind: "daily", hour: 7, minute: 0 } as Cadence, "Every day at 7:00 AM, UTC"],
    [{ kind: "daily", hour: 0, minute: 0 } as Cadence, "Every day at 12:00 AM, UTC"],
    [{ kind: "daily", hour: 12, minute: 30 } as Cadence, "Every day at 12:30 PM, UTC"],
    [{ kind: "daily", hour: 13, minute: 5 } as Cadence, "Every day at 1:05 PM, UTC"],
    [{ kind: "weekdays", hour: 7, minute: 0 } as Cadence, "Every weekday at 7:00 AM, UTC"],
    [{ kind: "hourly", minute: 5 } as Cadence, "Every hour at 05 minutes past, UTC"],
    [
      { kind: "weekly", days: [1, 3, 5], hour: 8, minute: 0 } as Cadence,
      "Every Monday, Wednesday and Friday at 8:00 AM, UTC",
    ],
    [
      { kind: "weekly", days: [2], hour: 8, minute: 0 } as Cadence,
      "Every Tuesday at 8:00 AM, UTC",
    ],
    [
      { kind: "monthly", day: 1, hour: 6, minute: 0 } as Cadence,
      "On the 1st of each month at 6:00 AM, UTC",
    ],
    [
      { kind: "monthly", day: 22, hour: 6, minute: 0 } as Cadence,
      "On the 22nd of each month at 6:00 AM, UTC",
    ],
    [
      { kind: "monthly", day: 13, hour: 6, minute: 0 } as Cadence,
      "On the 13th of each month at 6:00 AM, UTC",
    ],
  ])("says what %o does", (c, expected) => {
    expect(describeCadence(c, "UTC")).toBe(expected);
  });

  it("names the zone it was given, because 7am is a different instant in each", () => {
    expect(describeCadence(DEFAULT_CADENCE, "America/Los_Angeles")).toBe(
      "Every day at 7:00 AM, America/Los_Angeles",
    );
    expect(describeCadence(DEFAULT_CADENCE, "  ")).toContain("UTC");
  });

  it("shows a custom expression rather than pretending to read it", () => {
    expect(describeCron("*/30 * * * *", "UTC")).toBe("Custom schedule: */30 * * * *, UTC");
  });

  it("says plainly when there is no cadence at all", () => {
    expect(describeCron("", "UTC")).toBe("No cadence set");
  });

  it("describes a stored expression the same way it describes a built one", () => {
    expect(describeCron("0 7 * * 1-5", "UTC")).toBe(describeCadence({ kind: "weekdays", hour: 7, minute: 0 }, "UTC"));
  });
});

suite("months that do not have the chosen day", () => {
  it("flags a monthly cadence past the 28th", () => {
    expect(skipsShortMonths({ kind: "monthly", day: 31, hour: 6, minute: 0 })).toBe(true);
    expect(skipsShortMonths({ kind: "monthly", day: 29, hour: 6, minute: 0 })).toBe(true);
    expect(skipsShortMonths({ kind: "monthly", day: 28, hour: 6, minute: 0 })).toBe(false);
    expect(skipsShortMonths({ kind: "daily", hour: 6, minute: 0 })).toBe(false);
  });
});
