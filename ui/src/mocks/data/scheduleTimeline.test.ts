import { describe, expect, it } from "vitest";
import type { ScriptSchedule } from "@/api/portal/hooks/scripts";
import { buildMockScheduleTimeline } from "./scheduleTimeline";

// The demo server's layout has to agree with the real route's on the shapes
// the demo and the screenshots show, or a capture would picture a page the
// platform never draws. These are the ticket's four schedules (#1891), the same
// ones internal/httpserver/scripthttp/fireshttp pins on the server.
const sched = (
  id: string,
  cron: string,
  tz = "UTC",
  enabled = true,
): ScriptSchedule => ({
  id: `sched-${id}`,
  script_id: id,
  cron_spec: cron,
  timezone: tz,
  enabled,
});

const wednesday = new Date("2026-01-14T15:30:00Z");

describe("buildMockScheduleTimeline", () => {
  it("files the ticket's four schedules as the server does", () => {
    const tl = buildMockScheduleTimeline(
      [
        sched("a", "*/5 * * * *"),
        sched("b", "35 * * * *"),
        sched("c", "0 7 * * 1-5"),
        sched("d", "0 6 1 * *"),
      ],
      new Map([
        ["a", { name: "five", owner_email: "jane@example.com", category: "ingest", tags: ["sales"] }],
        ["b", { name: "Thirty-five", owner_email: "carol@example.com", category: "", tags: [] }],
      ]),
      "UTC",
      wednesday,
    );
    const [day, week, long] = tl.sections;
    expect(day!.rows.map((r) => [r.script_id, r.fire_count, r.rhythm])).toEqual(
      [
        ["a", 288, "minutes"],
        ["b", 24, "hours"],
      ],
    );
    // Rows are in name order, ignoring case (#1992), and carry the script's labels.
    expect(day!.rows[0]!.script_name).toBe("five");
    expect(day!.rows[0]!.tags).toEqual(["sales"]);
    expect(day!.rows[1]!.owner_email).toBe("carol@example.com");
    expect(
      week!.rows.map((r) => [r.script_id, r.fire_count, r.rhythm]),
    ).toEqual([["c", 5, "days"]]);
    expect(week!.from).toBe("2026-01-12T00:00:00.000Z");
    expect(
      long!.rows.map((r) => [r.script_id, r.fire_count, r.rhythm]),
    ).toEqual([["d", 3, "months"]]);
  });

  it("expands a schedule in its own zone", () => {
    const tl = buildMockScheduleTimeline(
      [sched("ny", "0 7 * * *", "America/New_York")],
      new Map(),
      "America/Los_Angeles",
      wednesday,
    );
    expect(tl.sections[1]!.rows[0]!.fires[0]).toBe("2026-01-12T12:00:00.000Z");
  });

  it("reports a form it does not read rather than guessing", () => {
    const tl = buildMockScheduleTimeline(
      [sched("x", "@every 5m")],
      new Map(),
      "UTC",
      wednesday,
    );
    expect(tl.unreadable).toHaveLength(1);
  });
});
