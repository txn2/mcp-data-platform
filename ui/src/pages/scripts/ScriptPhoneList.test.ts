import { describe, it, expect } from "vitest";
import type { PortalScriptRow } from "@/api/portal/hooks/scripts";
import { whatItDoes, whoAndHow } from "./ScriptPhoneList";

// #2601: a phone row says what the automation does and how it went.

function row(overrides: Partial<PortalScriptRow> = {}): PortalScriptRow {
  return {
    script: { id: "s1", name: "s1", display_name: "S1", owner_email: "dana@example.com", status: "active", enabled: true, version: 1, updated_at: new Date().toISOString() },
    owned: true,
    ...overrides,
  } as PortalScriptRow;
}

type ListedSchedule = NonNullable<PortalScriptRow["schedules"]>[number];

function schedule(overrides: Partial<ListedSchedule> = {}): ListedSchedule {
  return { id: "x", script_id: "s1", cron_spec: "0 6 * * *", timezone: "UTC", enabled: true, ...overrides } as ListedSchedule;
}

describe("whatItDoes", () => {
  it("is what a library is for, an endpoint without its prefix, or on demand", () => {
    expect(whatItDoes(row({ script: { ...row().script, library: true } }))).toBe("Loaded by other automations");
    expect(whatItDoes(row({ endpoints: [{ name: "s", path: "/endpoints/store-survey", auth_mode: "public", enabled: false }] }))).toBe(
      "Endpoint store-survey · Off",
    );
    expect(whatItDoes(row())).toBe("On demand");
  });

  it("is a schedule's cadence, and that it is paused", () => {
    const paused = row({ schedules: [schedule({ enabled: false })] });
    expect(whatItDoes(paused)).toMatch(/paused$/);
  });

  // #2434: a script holds several schedules; the row states the first and
  // counts the rest, and a schedule is what a row leads with over an endpoint.
  it("counts the schedules past the first, and leads with a schedule over an endpoint", () => {
    const three = row({
      schedules: [schedule({ enabled: false }), schedule({ id: "y", cron_spec: "0 18 * * *" }), schedule({ id: "z" })],
      endpoints: [{ name: "s", path: "/endpoints/store-survey", auth_mode: "public", enabled: true }],
    });
    expect(whatItDoes(three)).toBe("Every day at 6:00 AM, UTC · paused · +2 more");
    expect(whatItDoes(row({ schedules: [schedule({ enabled: false })] }))).toBe("Every day at 6:00 AM, UTC · paused");
    expect(whatItDoes(row({ schedules: [] }))).toBe("On demand");
  });
});

describe("whoAndHow", () => {
  it("is the author and the last run in a word", () => {
    expect(whoAndHow(row())).toBe("dana@example.com · never run");
    const ran = row({ last_run: { id: "r", status: "failed", trigger: "schedule", version: 1, finished_at: new Date(Date.now() - 3 * 3600_000).toISOString() } as PortalScriptRow["last_run"] });
    expect(whoAndHow(ran)).toBe("dana@example.com · failed 3h ago");
    const running = row({ last_run: { id: "r", status: "running", trigger: "manual", version: 1 } as PortalScriptRow["last_run"] });
    expect(whoAndHow(running)).toBe("dana@example.com · running");
  });
});
