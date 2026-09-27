import type {
  ScheduleFireRow,
  ScheduleRhythm,
  ScheduleSection,
} from "@/api/portal/hooks/scheduleTimeline";
import { wallClock, zonedInstant, type WallClock } from "@/lib/zonedTime";

// The geometry and wording of the Schedules tab's drawing (#1891), kept apart
// from the component so each rule is tested on its own. Nothing here computes
// a fire: the fires are the server's, expanded with the scheduler's parse, and
// this module only places them.

/**
 * truncatedBand is the span a row the server cut at its cap is drawn across,
 * from its first listed fire to the window's last fire, or null for a row
 * drawn fire by fire (#1933). Ticks for the listed fires alone would stop
 * where the list does, which reads as a schedule that stops mid-day.
 */
export function truncatedBand(
  row: Pick<ScheduleFireRow, "truncated" | "last_fire">,
  fires: number[],
  x: (at: number) => number,
): { from: number; to: number } | null {
  const first = fires[0];
  if (!row.truncated || !row.last_fire || first === undefined) return null;
  return { from: x(first), to: x(Date.parse(row.last_fire)) };
}

/** LABEL_WIDTH is the fixed column the row names sit in. */
export const LABEL_WIDTH = 300;
/** ROW_HEIGHT is one schedule's row. */
export const ROW_HEIGHT = 38;
/** AXIS_HEIGHT is the strip under the last row the step labels sit in. */
export const AXIS_HEIGHT = 26;
/** TICK_HEIGHT is a fire's mark, centered in its row. */
export const TICK_HEIGHT = 22;
/** DOT_RADIUS is a fire drawn on its own. */
export const DOT_RADIUS = 6;
/** MIN_PLOT_WIDTH is the narrowest the plot gets; below it the card scrolls. */
export const MIN_PLOT_WIDTH = 480;
/** DENSE_ROW is the fire count past which a tick is drawn thinner, so an
 * every-five-minutes row reads as a band rather than a smear. */
export const DENSE_ROW = 100;
/** FEW_FIRES is the most fires a row may have to be drawn as dots. */
export const FEW_FIRES = 12;

/** xAt places an instant on the plot: the label column, then the window
 * mapped linearly onto the plot width. */
export function xAt(
  at: number,
  from: number,
  to: number,
  plotWidth: number,
): number {
  return LABEL_WIDTH + (plotWidth * (at - from)) / (to - from);
}

/** tickWidth is the stroke of a row's ticks. */
export function tickWidth(fireCount: number): number {
  return fireCount > DENSE_ROW ? 1.3 : 2;
}

/**
 * usesDots reports a row drawn with dots in place of ticks: a few fires at
 * irregular times, each of which the reader should see on its own. One or two
 * fires have no rhythm to read and are dots too. Regular gaps within a minute
 * of each other read as a cadence and stay ticks.
 */
export function usesDots(fires: number[]): boolean {
  if (fires.length === 0 || fires.length > FEW_FIRES) return false;
  if (fires.length <= 2) return true;
  const gaps = fires.slice(1).map((f, i) => f - fires[i]!);
  const shortest = Math.min(...gaps);
  const longest = Math.max(...gaps);
  return longest - shortest > 60_000;
}

/** RHYTHM_ORDER is every rhythm, fastest first: the legend's order. */
export const RHYTHM_ORDER: ScheduleRhythm[] = [
  "minutes",
  "hours",
  "days",
  "weeks",
  "months",
];

/** RHYTHM_LABEL is a rhythm as the legend states it. */
export const RHYTHM_LABEL: Record<ScheduleRhythm, string> = {
  minutes: "Every few minutes",
  hours: "Hourly",
  days: "Daily",
  weeks: "Weekly",
  months: "Monthly or rarer",
};

/** rhythmColor is the chart token a rhythm is drawn in: color says what kind
 * of schedule a row is, not which script it is. */
export function rhythmColor(rhythm: ScheduleRhythm): string {
  return `hsl(var(--chart-${RHYTHM_ORDER.indexOf(rhythm) + 1}))`;
}

/** PAUSED_COLOR is a paused row's marks, muted whatever its rhythm. */
export const PAUSED_COLOR = "hsl(var(--muted-foreground))";

/** SECTION_TITLE is each axis's heading. */
export const SECTION_TITLE: Record<ScheduleSection, string> = {
  intraday: "Intraday",
  multi_day: "Multi-day",
  long_term: "Long-term",
};

/** SECTION_HINT says what each axis holds and over what window. */
export const SECTION_HINT: Record<ScheduleSection, string> = {
  intraday: "Schedules that fire more than once a day, over today",
  multi_day:
    "Schedules that fire at most daily and at least weekly, over this week",
  long_term:
    "Schedules that fire less than once a week, over the next three months",
};

/** countPhrase states a row's fire count over its section's window. */
export function countPhrase(section: ScheduleSection, count: number): string {
  const n = count.toLocaleString();
  switch (section) {
    case "intraday":
      return count === 0 ? "none today" : `${n} a day`;
    case "multi_day":
      return count === 0 ? "none this week" : `${n} a week`;
    case "long_term":
      return count === 0 ? "none in three months" : `${n} in three months`;
  }
}

/**
 * rowWords is a schedule in words as its row states it. The zone is named only
 * when it is not the viewer's, and by its city, because the row has the width
 * of a label column and the count after it is the part that must not be cut.
 * words is scheduleLine's sentence, which ends with ", <zone>".
 */
export function rowWords(
  words: string,
  scheduleZone: string,
  viewerZone: string,
): string {
  const zone = scheduleZone.trim() || "UTC";
  const suffix = `, ${zone}`;
  const bare = words.endsWith(suffix) ? words.slice(0, -suffix.length) : words;
  if (zone === viewerZone) return bare;
  if (!zone.includes("/")) return `${bare}, ${zone}`;
  const city = zone.split("/").pop()!.replace(/_/g, " ");
  return `${bare}, ${city} time`;
}

/**
 * rowDescription is a row's second line: its schedule in words, its count,
 * and whether it is paused, fitted to max characters by shortening the words
 * alone, so the count and the paused mark are never the part cut off.
 */
export function rowDescription(
  words: string,
  count: string,
  paused: boolean,
  max: number,
): string {
  const tail = ` · ${count}${paused ? " · paused" : ""}`;
  return `${fitLabel(words, Math.max(8, max - tail.length))}${tail}`;
}

/** fitLabel shortens a label to what the label column holds, leaving the full
 * text for the row's tooltip and accessible name. */
export function fitLabel(text: string, max: number): string {
  return text.length <= max ? text : `${text.slice(0, max - 1).trimEnd()}…`;
}

/** Tick is one gridline, and the label under it if it carries one. */
export interface Tick {
  at: number;
  label?: string;
  major: boolean;
}

/**
 * axisTicks cuts a section's gridlines at the viewer's wall-clock boundaries,
 * in the zone the server cut the window in: every three hours across the day,
 * each day (with a line every six hours) across the week, and each month (with
 * a line every Monday) across three months.
 */
export function axisTicks(
  section: ScheduleSection,
  from: Date,
  to: Date,
  zone: string,
): Tick[] {
  const start = wallClock(from, zone);
  const end = to.getTime();
  const ticks =
    section === "intraday"
      ? dayTicks(start, zone)
      : section === "multi_day"
        ? weekTicks(start, zone)
        : quarterTicks(start, end, zone);
  return ticks.filter((t) => t.at <= end);
}

// instantAt is the instant zone's clock reads a day of start's month, at an
// hour; a day past the month's end rolls into the next.
function instantAt(
  zone: string,
  start: WallClock,
  day: number,
  hour = 0,
): number {
  return zonedInstant(zone, {
    year: start.year,
    month: start.month,
    day,
    hour,
  }).getTime();
}

// dayTicks steps the day every three hours, labeled 12 AM to 12 AM.
function dayTicks(start: WallClock, zone: string): Tick[] {
  const ticks: Tick[] = [];
  for (let h = 0; h <= 24; h += 3) {
    ticks.push({
      at: instantAt(zone, start, start.day, h),
      label: hourLabel(h),
      major: true,
    });
  }
  return ticks;
}

// weekTicks labels each day of the week, with a minor line every six hours.
function weekTicks(start: WallClock, zone: string): Tick[] {
  const ticks: Tick[] = [];
  for (let d = 0; d <= 7; d++) {
    const t = instantAt(zone, start, start.day + d);
    ticks.push({
      at: t,
      label: d < 7 ? dayLabel(t, zone) : undefined,
      major: true,
    });
    if (d === 7) break;
    for (const h of [6, 12, 18]) {
      ticks.push({
        at: instantAt(zone, start, start.day + d, h),
        major: false,
      });
    }
  }
  return ticks;
}

// quarterTicks labels each month, with a minor line every Monday.
function quarterTicks(start: WallClock, end: number, zone: string): Tick[] {
  const ticks: Tick[] = [];
  for (let m = 0; m <= 3; m++) {
    const t = zonedInstant(zone, {
      year: start.year,
      month: start.month + m,
      day: 1,
    }).getTime();
    ticks.push({
      at: t,
      label: m < 3 ? monthLabel(t, zone) : undefined,
      major: true,
    });
  }
  for (
    let d = start.day, t = instantAt(zone, start, d);
    t < end;
    d++, t = instantAt(zone, start, d)
  ) {
    if (wallClock(new Date(t), zone).weekday === 1)
      ticks.push({ at: t, major: false });
  }
  return ticks;
}

// hourLabel is an hour of the day as a clock face states it: 12 AM, 3 AM ...
// 12 PM ... 12 AM.
function hourLabel(hour: number): string {
  const h = hour % 24;
  const suffix = h < 12 ? "AM" : "PM";
  return `${h % 12 === 0 ? 12 : h % 12} ${suffix}`;
}

// dayLabel is a day as the week axis names it, weekday first: "Mon 12".
// Formatting weekday and day together lets a locale order them day-first.
function dayLabel(at: number, zone: string): string {
  const weekday = new Intl.DateTimeFormat(undefined, {
    timeZone: zone,
    weekday: "short",
  }).format(at);
  return `${weekday} ${wallClock(new Date(at), zone).day}`;
}

function monthLabel(at: number, zone: string): string {
  return new Intl.DateTimeFormat(undefined, {
    timeZone: zone,
    month: "short",
    day: "numeric",
  }).format(at);
}

/**
 * fireText states a fire the way its tooltip reads: in the viewer's zone, and
 * in the schedule's own when that is a different zone, because a 7 AM New York
 * job drawn at 4 AM is only legible if both are said.
 */
export function fireText(
  at: number,
  viewerZone: string,
  scheduleZone: string,
): string[] {
  const fmt = (zone: string) =>
    new Intl.DateTimeFormat(undefined, {
      timeZone: zone,
      weekday: "short",
      month: "short",
      day: "numeric",
      hour: "numeric",
      minute: "2-digit",
      timeZoneName: "short",
    }).format(at);
  const own = scheduleZone.trim() || "UTC";
  const lines = [`${fmt(viewerZone)} your time`];
  if (!sameClock(at, viewerZone, own)) {
    const text = fmt(own);
    // "UTC" is its own short name; "EDT" does not say New York, so it is named.
    lines.push(text.endsWith(own) ? text : `${text} (${own})`);
  }
  return lines;
}

// sameClock reports two zones reading an instant identically, so a schedule
// in the viewer's own zone, or in one that agrees with it, is stated once.
function sameClock(at: number, a: string, b: string): boolean {
  if (a === b) return true;
  const x = wallClock(new Date(at), a);
  const y = wallClock(new Date(at), b);
  return x.day === y.day && x.hour === y.hour && x.minute === y.minute;
}
