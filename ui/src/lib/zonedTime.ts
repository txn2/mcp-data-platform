// Wall-clock arithmetic in a named IANA zone, which the browser's Date cannot
// do for any zone but its own (#1891). The Schedules tab cuts its axis ticks
// at the viewer's midnights and hours, and states a fire in the schedule's own
// zone as well as the viewer's.

/** WallClock is an instant as a clock in one zone reads it. Month is 0-based,
 * as Date's is; weekday is 0 for Sunday. */
export interface WallClock {
  year: number;
  month: number;
  day: number;
  hour: number;
  minute: number;
  weekday: number;
}

const WEEKDAYS: Record<string, number> = {
  Sun: 0,
  Mon: 1,
  Tue: 2,
  Wed: 3,
  Thu: 4,
  Fri: 5,
  Sat: 6,
};

const formatters = new Map<string, Intl.DateTimeFormat>();

function formatterFor(zone: string): Intl.DateTimeFormat {
  let f = formatters.get(zone);
  if (!f) {
    f = new Intl.DateTimeFormat("en-US", {
      timeZone: zone,
      hourCycle: "h23",
      year: "numeric",
      month: "numeric",
      day: "numeric",
      hour: "numeric",
      minute: "numeric",
      second: "numeric",
      weekday: "short",
    });
    formatters.set(zone, f);
  }
  return f;
}

/** wallClock reads an instant on a clock in zone. */
export function wallClock(at: Date, zone: string): WallClock {
  const parts: Record<string, string> = {};
  for (const p of formatterFor(zone).formatToParts(at)) parts[p.type] = p.value;
  return {
    year: Number(parts.year),
    month: Number(parts.month) - 1,
    day: Number(parts.day),
    hour: Number(parts.hour),
    minute: Number(parts.minute),
    weekday: WEEKDAYS[parts.weekday ?? "Sun"] ?? 0,
  };
}

// offsetMs is how far zone's clock is ahead of UTC at an instant.
function offsetMs(at: number, zone: string): number {
  const w = wallClock(new Date(at), zone);
  const seconds = new Date(at).getUTCSeconds();
  return (
    Date.UTC(w.year, w.month, w.day, w.hour, w.minute, seconds) -
    (at - (at % 1000))
  );
}

/** WallTime is a wall-clock reading to find the instant of; month is 0-based
 * and the fields roll over as Date.UTC's do. */
export interface WallTime {
  year: number;
  month: number;
  day: number;
  hour?: number;
  minute?: number;
}

/**
 * zonedInstant is the instant a clock in zone reads the given wall time.
 * Out-of-range fields roll over as Date.UTC's do, so day 32 is the first of
 * the next month. A wall time a DST gap skips resolves to the instant an hour
 * later, the way the clock itself jumps.
 */
export function zonedInstant(zone: string, wall: WallTime): Date {
  const { year, month, day, hour = 0, minute = 0 } = wall;
  const guess = Date.UTC(year, month, day, hour, minute);
  // Near a transition the zone has two offsets, and the wall time is read
  // under each. The one whose instant reads back as the wall time asked for is
  // the answer, the earlier of the two in a fall-back hour that has both; in a
  // spring-forward gap neither reads back, and the later one is the clock
  // after its jump.
  const a = guess - offsetMs(guess, zone);
  const b = guess - offsetMs(a, zone);
  const [early, late] = a <= b ? [a, b] : [b, a];
  const want = Date.UTC(year, month, day, hour, minute);
  const reads = (at: number) => {
    const w = wallClock(new Date(at), zone);
    return Date.UTC(w.year, w.month, w.day, w.hour, w.minute) === want;
  };
  if (reads(early)) return new Date(early);
  return new Date(late);
}
