import type {
  ScheduleFireRow,
  ScheduleFireWindow,
  ScheduleRhythm,
  ScheduleSection,
  ScheduleTimeline,
  ScheduleUnreadable,
} from "@/api/portal/hooks/scheduleTimeline";
import type { ScriptSchedule } from "@/api/portal/hooks/scripts";
import { wallClock, zonedInstant } from "@/lib/zonedTime";

// The mock server's stand-in for GET /portal/scripts/fires (#1891).
//
// The real route expands fires with the scheduler's own parser, and the page
// computes none. The mock has no scheduler, so it reads the five-field forms
// the fixtures and the schedule builder write (numbers, *, */n, a-b and lists)
// well enough for a demo and a screenshot. It is deliberately not a cron
// implementation: a descriptor is reported as unreadable rather than guessed.

const MAX_FIRES = 500;
const HOUR = 3_600_000;
const DAY = 24 * HOUR;

// field reads one cron field into the set of values it admits, or null for a
// form this stand-in does not read.
function field(text: string, min: number, max: number): Set<number> | null {
  const out = new Set<number>();
  for (const part of text.split(",")) {
    const m = part.match(/^(\*|\d+(?:-\d+)?)(?:\/(\d+))?$/);
    if (!m) return null;
    const step = m[2] ? Number(m[2]) : 1;
    let lo = min;
    let hi = max;
    if (m[1] !== "*") {
      const [a, b] = m[1]!.split("-").map(Number);
      lo = a!;
      hi = b ?? (m[2] ? max : a!);
    }
    for (let v = lo; v <= hi; v += step) out.add(v);
  }
  return out;
}

interface Spec {
  minute: Set<number>;
  hour: Set<number>;
  dom: Set<number>;
  month: Set<number>;
  dow: Set<number>;
  domAny: boolean;
  dowAny: boolean;
}

function parse(spec: string): Spec | null {
  const f = spec.trim().split(/\s+/);
  if (f.length !== 5) return null;
  const minute = field(f[0]!, 0, 59);
  const hour = field(f[1]!, 0, 23);
  const dom = field(f[2]!, 1, 31);
  const month = field(f[3]!, 1, 12);
  const dow = field(f[4]!, 0, 6);
  if (!minute || !hour || !dom || !month || !dow) return null;
  return {
    minute,
    hour,
    dom,
    month,
    dow,
    domAny: f[2] === "*",
    dowAny: f[4] === "*",
  };
}

// fires lists a spec's fires in [from, to), walking the zone's calendar days
// and each day's matching hours and minutes.
function fires(s: Spec, zone: string, from: number, to: number): number[] {
  const out: number[] = [];
  const start = wallClock(new Date(from - DAY), zone);
  for (let d = 0; ; d++) {
    const midnight = zonedInstant(zone, {
      year: start.year,
      month: start.month,
      day: start.day + d,
    }).getTime();
    if (midnight >= to) break;
    const w = wallClock(new Date(midnight), zone);
    const domHit = s.dom.has(w.day);
    const dowHit = s.dow.has(w.weekday);
    // Cron's rule: both day fields restricted means either one matches.
    const dayHit = s.domAny || s.dowAny ? domHit && dowHit : domHit || dowHit;
    if (!s.month.has(w.month + 1) || !dayHit) continue;
    for (const h of [...s.hour].sort((a, b) => a - b)) {
      for (const m of [...s.minute].sort((a, b) => a - b)) {
        const at = zonedInstant(zone, {
          year: w.year,
          month: w.month,
          day: w.day,
          hour: h,
          minute: m,
        }).getTime();
        if (at >= from && at < to && wallClock(new Date(at), zone).hour === h)
          out.push(at);
      }
    }
  }
  return out;
}

function rhythm(sample: number[]): ScheduleRhythm {
  const gaps = sample
    .slice(1, 10)
    .map((f, i) => f - sample[i]!)
    .sort((a, b) => a - b);
  const median = gaps[Math.floor(gaps.length / 2)];
  if (median === undefined) return "months";
  if (median < HOUR) return "minutes";
  if (median < DAY) return "hours";
  if (median < 7 * DAY) return "days";
  if (median < 28 * DAY) return "weeks";
  return "months";
}

/** MockScriptLabel is what a row carries of its script (#1992). */
export interface MockScriptLabel {
  name: string;
  owner_email: string;
  category: string;
  tags: string[];
}

/** buildMockScheduleTimeline lays the fixture schedules out for a viewer at
 * now, cutting the windows as the server does and ordering each section's
 * rows by name, ignoring case. */
export function buildMockScheduleTimeline(
  schedules: ScriptSchedule[],
  labels: Map<string, MockScriptLabel>,
  viewerZone: string,
  now: Date,
): ScheduleTimeline {
  const w = wallClock(now, viewerZone);
  const sinceMonday = (w.weekday + 6) % 7;
  const windows: { section: ScheduleSection; from: number; to: number }[] = [
    {
      section: "intraday",
      from: zonedInstant(viewerZone, {
        year: w.year,
        month: w.month,
        day: w.day,
      }).getTime(),
      to: zonedInstant(viewerZone, {
        year: w.year,
        month: w.month,
        day: w.day + 1,
      }).getTime(),
    },
    {
      section: "multi_day",
      from: zonedInstant(viewerZone, {
        year: w.year,
        month: w.month,
        day: w.day - sinceMonday,
      }).getTime(),
      to: zonedInstant(viewerZone, {
        year: w.year,
        month: w.month,
        day: w.day - sinceMonday + 7,
      }).getTime(),
    },
    {
      section: "long_term",
      from: zonedInstant(viewerZone, {
        year: w.year,
        month: w.month,
        day: 1,
      }).getTime(),
      to: zonedInstant(viewerZone, {
        year: w.year,
        month: w.month + 3,
        day: 1,
      }).getTime(),
    },
  ];
  const rows: Record<ScheduleSection, ScheduleFireRow[]> = {
    intraday: [],
    multi_day: [],
    long_term: [],
  };
  const unreadable: ScheduleUnreadable[] = [];
  const weekStart = windows[1]!.from;

  for (const sched of schedules) {
    const label = labels.get(sched.script_id);
    const name = label?.name ?? "";
    const spec = parse(sched.cron_spec);
    if (!spec) {
      unreadable.push({
        script_id: sched.script_id,
        script_name: name,
        reason: "the demo server does not expand this form",
      });
      continue;
    }
    const zone = sched.timezone || "UTC";
    const sample = fires(spec, zone, weekStart, weekStart + 28 * DAY);
    const section: ScheduleSection =
      sample.length > 28
        ? "intraday"
        : sample.length >= 4
          ? "multi_day"
          : "long_term";
    const win = windows.find((x) => x.section === section)!;
    const all = fires(spec, zone, win.from, win.to);
    rows[section].push({
      script_id: sched.script_id,
      script_name: name,
      owner_email: label?.owner_email ?? "",
      category: label?.category ?? "",
      tags: label?.tags ?? [],
      cron_spec: sched.cron_spec,
      timezone: sched.timezone,
      enabled: sched.enabled,
      rhythm: rhythm(sample.length > 1 ? sample : all),
      fire_count: all.length,
      truncated: all.length > MAX_FIRES,
      fires: all.slice(0, MAX_FIRES).map((f) => new Date(f).toISOString()),
      ...(all.length > MAX_FIRES
        ? { last_fire: new Date(all[all.length - 1]!).toISOString() }
        : {}),
    });
  }

  const sections: ScheduleFireWindow[] = windows.map((x) => ({
    section: x.section,
    from: new Date(x.from).toISOString(),
    to: new Date(x.to).toISOString(),
    rows: rows[x.section].sort(
      (a, b) =>
        a.script_name.toLowerCase().localeCompare(b.script_name.toLowerCase()) ||
        a.script_id.localeCompare(b.script_id),
    ),
  }));
  return { timezone: viewerZone, sections, unreadable };
}
