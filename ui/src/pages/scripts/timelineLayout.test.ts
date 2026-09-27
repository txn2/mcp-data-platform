import { describe, expect, it } from "vitest";
import {
  LABEL_WIDTH,
  axisTicks,
  countPhrase,
  fireText,
  fitLabel,
  rhythmColor,
  rowDescription,
  rowWords,
  tickWidth,
  usesDots,
  xAt,
} from "./timelineLayout";

const HOUR = 3_600_000;
const DAY = 24 * HOUR;

describe("timelineLayout", () => {
  it("maps a window onto the plot after the label column", () => {
    expect(xAt(0, 0, DAY, 960)).toBe(LABEL_WIDTH);
    expect(xAt(DAY / 2, 0, DAY, 960)).toBe(LABEL_WIDTH + 480);
    expect(xAt(DAY, 0, DAY, 960)).toBe(LABEL_WIDTH + 960);
  });

  it("thins the ticks of a row past a hundred fires", () => {
    expect(tickWidth(100)).toBe(2);
    expect(tickWidth(288)).toBe(1.3);
  });

  describe("usesDots", () => {
    it("draws a few irregular fires as dots", () => {
      expect(usesDots([0, 2 * HOUR, 9 * HOUR])).toBe(true);
    });
    it("draws one or two fires as dots", () => {
      expect(usesDots([HOUR])).toBe(true);
      expect(usesDots([HOUR, 5 * HOUR])).toBe(true);
    });
    it("keeps a regular cadence as ticks", () => {
      expect(usesDots([0, DAY, 2 * DAY, 3 * DAY, 4 * DAY])).toBe(false);
    });
    it("keeps a dense row as ticks however it is spaced", () => {
      const many = Array.from({ length: 13 }, (_, i) => i * i * HOUR);
      expect(usesDots(many)).toBe(false);
    });
    it("has nothing to draw for no fires", () => {
      expect(usesDots([])).toBe(false);
    });
  });

  it("colors a row by its rhythm from the chart tokens", () => {
    expect(rhythmColor("minutes")).toBe("hsl(var(--chart-1))");
    expect(rhythmColor("months")).toBe("hsl(var(--chart-5))");
  });

  it("states a count over each section's window", () => {
    expect(countPhrase("intraday", 96)).toBe("96 a day");
    expect(countPhrase("intraday", 0)).toBe("none today");
    expect(countPhrase("multi_day", 5)).toBe("5 a week");
    expect(countPhrase("long_term", 3)).toBe("3 in three months");
  });

  it("names a schedule's zone only when it is not the viewer's", () => {
    expect(rowWords("Every 5 minutes, UTC", "UTC", "UTC")).toBe(
      "Every 5 minutes",
    );
    expect(
      rowWords(
        "Every day at 7:00 AM, America/New_York",
        "America/New_York",
        "UTC",
      ),
    ).toBe("Every day at 7:00 AM, New York time");
    expect(rowWords("Every hour, UTC", "", "UTC")).toBe("Every hour");
    expect(rowWords("Every hour, UTC", "UTC", "America/Los_Angeles")).toBe(
      "Every hour, UTC",
    );
  });

  it("shortens the words of a description and never its count", () => {
    const long = "On the 1st of each month at 6:00 AM, New York time";
    const d = rowDescription(long, "3 in three months", true, 52);
    expect(d.length).toBeLessThanOrEqual(52);
    expect(d.endsWith(" · 3 in three months · paused")).toBe(true);
    expect(rowDescription("Every hour", "24 a day", false, 52)).toBe(
      "Every hour · 24 a day",
    );
  });

  it("shortens a label that does not fit, and leaves one that does", () => {
    expect(fitLabel("orders-ingest", 20)).toBe("orders-ingest");
    expect(fitLabel("a very long script name indeed", 10)).toBe("a very lo…");
  });

  describe("axisTicks", () => {
    it("steps the day every three hours, 12 AM to 12 AM", () => {
      const from = new Date("2026-01-14T08:00:00Z"); // midnight in Los Angeles
      const ticks = axisTicks(
        "intraday",
        from,
        new Date(from.getTime() + DAY),
        "America/Los_Angeles",
      );
      expect(ticks.map((t) => t.label)).toEqual([
        "12 AM",
        "3 AM",
        "6 AM",
        "9 AM",
        "12 PM",
        "3 PM",
        "6 PM",
        "9 PM",
        "12 AM",
      ]);
      expect(ticks[0]!.at).toBe(from.getTime());
      expect(ticks[8]!.at).toBe(from.getTime() + DAY);
    });

    it("draws a day that loses an hour at its real hours", () => {
      const from = new Date("2026-03-08T08:00:00Z");
      const to = new Date("2026-03-09T07:00:00Z");
      const ticks = axisTicks("intraday", from, to, "America/Los_Angeles");
      expect(ticks[ticks.length - 1]!.at).toBe(to.getTime());
      // 12 AM to 3 AM is two hours of real time: the clock skips 2 AM.
      expect(ticks[1]!.at - ticks[0]!.at).toBe(2 * HOUR);
      expect(ticks[2]!.at - ticks[1]!.at).toBe(3 * HOUR);
    });

    it("labels each day of the week and lines every six hours", () => {
      const from = new Date("2026-01-12T00:00:00Z");
      const ticks = axisTicks(
        "multi_day",
        from,
        new Date(from.getTime() + 7 * DAY),
        "UTC",
      );
      expect(ticks.filter((t) => t.label)).toHaveLength(7);
      expect(ticks[0]!.label).toMatch(/^Mon 12$/);
      expect(ticks.filter((t) => !t.major)).toHaveLength(21);
    });

    it("labels each month and lines every Monday", () => {
      const from = new Date("2026-01-01T00:00:00Z");
      const to = new Date("2026-04-01T00:00:00Z");
      const ticks = axisTicks("long_term", from, to, "UTC");
      expect(ticks.filter((t) => t.label)).toHaveLength(3);
      const mondays = ticks.filter((t) => !t.major);
      expect(mondays.length).toBe(13);
      for (const t of mondays) expect(new Date(t.at).getUTCDay()).toBe(1);
    });
  });

  describe("fireText", () => {
    const at = Date.parse("2026-01-14T12:00:00Z");
    it("states a fire in the viewer's zone and the schedule's own", () => {
      const lines = fireText(at, "America/Los_Angeles", "America/New_York");
      expect(lines).toHaveLength(2);
      expect(lines[0]).toMatch(/4:00.*your time/);
      expect(lines[1]).toMatch(/7:00.*America\/New_York/);
    });
    it("does not name a zone twice", () => {
      const lines = fireText(at, "America/Los_Angeles", "UTC");
      expect(lines[1]).toMatch(/12:00.*UTC$/);
      expect(lines[1]).not.toMatch(/\(UTC\)/);
    });
    it("states it once when the zones agree", () => {
      expect(fireText(at, "UTC", "UTC")).toHaveLength(1);
      expect(fireText(at, "UTC", "")).toHaveLength(1);
    });
  });
});
