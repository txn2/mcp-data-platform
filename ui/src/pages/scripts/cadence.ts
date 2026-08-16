// Cadence is a schedule as the person who owns the automation thinks about it,
// and cron is the wire format underneath (#1307).
//
// The portal used to ask for the cron expression directly. That is a
// developer's input on a page written for the person whose report it is: "0 7 *
// * 1-5" is not something an analyst should have to know, or be able to get
// subtly wrong at 3am on the 31st. So the page offers the cadences people
// actually ask for, and this module is the one place that translates between
// them and the expression the platform stores.
//
// It is deliberately not a general cron library. Anything this module cannot
// express is still settable — the editor keeps an advanced field, and an
// expression an agent wrote through manage_script must render here rather than
// be silently rewritten — so every function below has an honest "custom" answer
// instead of a wrong one.

/** Cadence is the shape of a schedule the builder can express. */
export type Cadence =
  | { kind: "hourly"; minute: number }
  | { kind: "daily"; hour: number; minute: number }
  | { kind: "weekdays"; hour: number; minute: number }
  | { kind: "weekly"; days: number[]; hour: number; minute: number }
  | { kind: "monthly"; day: number; hour: number; minute: number }
  | { kind: "custom"; spec: string };

/** CadenceKind is the choice a person makes before filling in the details. */
export type CadenceKind = Cadence["kind"];

/** DAY_NAMES is cron's day-of-week order: 0 is Sunday. */
export const DAY_NAMES = [
  "Sunday",
  "Monday",
  "Tuesday",
  "Wednesday",
  "Thursday",
  "Friday",
  "Saturday",
] as const;

/** DEFAULT_CADENCE is what a script with no schedule starts from: a daily
 * morning run, which is what most reports turn out to want. */
export const DEFAULT_CADENCE: Cadence = { kind: "daily", hour: 7, minute: 0 };

/** toCron renders a cadence as the five-field expression the platform stores. */
export function toCron(c: Cadence): string {
  switch (c.kind) {
    case "hourly":
      return `${c.minute} * * * *`;
    case "daily":
      return `${c.minute} ${c.hour} * * *`;
    case "weekdays":
      return `${c.minute} ${c.hour} * * 1-5`;
    case "weekly":
      return `${c.minute} ${c.hour} * * ${weekdayList(c.days)}`;
    case "monthly":
      return `${c.minute} ${c.hour} ${c.day} * *`;
    case "custom":
      return c.spec.trim();
  }
}

// weekdayList renders the chosen days in cron's own order, deduplicated. An
// empty selection cannot fire at all, so it falls back to every day rather than
// producing an expression the server would refuse.
function weekdayList(days: number[]): string {
  const chosen = [...new Set(days)].filter((d) => d >= 0 && d <= 6).sort((a, b) => a - b);
  return chosen.length === 0 ? "*" : chosen.join(",");
}

/**
 * fromCron reads an expression back into a cadence, so opening a schedule
 * somebody else set — or an agent set — starts from what it actually does.
 *
 * Anything outside the shapes the builder produces comes back as custom, which
 * is what keeps this from quietly rewriting an expression it does not fully
 * understand.
 */
export function fromCron(spec: string): Cadence {
  const trimmed = spec.trim();
  const fields = trimmed.split(/\s+/);
  if (fields.length !== 5) return custom(trimmed);

  const [min, hr, dom, mon, dow] = fields as [string, string, string, string, string];
  const minute = wholeNumber(min, 0, 59);
  // A month field narrows the cadence to particular months, which the builder
  // does not offer, so anything but "every month" is custom.
  if (minute === null || mon !== "*") return custom(trimmed);

  // Hourly is the one shape with no hour of its own.
  if (hr === "*") {
    return dom === "*" && dow === "*" ? { kind: "hourly", minute } : custom(trimmed);
  }
  const hour = wholeNumber(hr, 0, 23);
  if (hour === null) return custom(trimmed);
  return atTimeOfDay(trimmed, dom, dow, hour, minute);
}

// atTimeOfDay reads the two day fields of an expression whose time of day is
// already known: which days of the month, or which days of the week.
function atTimeOfDay(
  spec: string,
  dom: string,
  dow: string,
  hour: number,
  minute: number,
): Cadence {
  if (dom === "*") return onDaysOfWeek(spec, dow, hour, minute);
  const day = wholeNumber(dom, 1, 31);
  // Both day fields set is cron's OR, not an AND, and the builder has no shape
  // for it.
  if (day === null || dow !== "*") return custom(spec);
  return { kind: "monthly", day, hour, minute };
}

// onDaysOfWeek reads the day-of-week field of a daily-or-weekly expression.
function onDaysOfWeek(spec: string, dow: string, hour: number, minute: number): Cadence {
  if (dow === "*") return { kind: "daily", hour, minute };
  if (dow === "1-5") return { kind: "weekdays", hour, minute };
  const days = dayList(dow);
  return days ? { kind: "weekly", days, hour, minute } : custom(spec);
}

// custom is the honest answer for an expression this module does not model:
// it is kept verbatim, so nothing an agent wrote is rewritten by being read.
function custom(spec: string): Cadence {
  return { kind: "custom", spec };
}

// wholeNumber parses one cron field as a plain number in range, rejecting the
// step, range, and list forms the builder does not produce.
function wholeNumber(field: string, min: number, max: number): number | null {
  if (!/^\d{1,2}$/.test(field)) return null;
  const n = Number(field);
  return n >= min && n <= max ? n : null;
}

// dayList parses a comma-separated day-of-week list, or null when the field is
// any other cron form.
function dayList(field: string): number[] | null {
  const parts = field.split(",");
  const days: number[] = [];
  for (const p of parts) {
    const n = wholeNumber(p, 0, 6);
    if (n === null) return null;
    days.push(n);
  }
  return days.length > 0 ? days : null;
}

/**
 * describe renders a cadence as the sentence a person reads to check it, in
 * their own words rather than cron's. The timezone is named because a schedule
 * means nothing without one: 7am is a different instant in every zone somebody
 * might have meant.
 */
export function describe(c: Cadence, timezone: string): string {
  const zone = timezone.trim() || "UTC";
  switch (c.kind) {
    case "hourly":
      return `Every hour at ${pad(c.minute)} minutes past, ${zone}`;
    case "daily":
      return `Every day at ${clockTime(c.hour, c.minute)}, ${zone}`;
    case "weekdays":
      return `Every weekday at ${clockTime(c.hour, c.minute)}, ${zone}`;
    case "weekly":
      return `Every ${dayPhrase(c.days)} at ${clockTime(c.hour, c.minute)}, ${zone}`;
    case "monthly":
      return `On the ${ordinal(c.day)} of each month at ${clockTime(c.hour, c.minute)}, ${zone}`;
    case "custom":
      return c.spec ? `Custom schedule: ${c.spec}, ${zone}` : "No cadence set";
  }
}

/** describeCron is describe over an expression, for a cadence read back from
 * the server rather than built here. */
export function describeCron(spec: string, timezone: string): string {
  return describe(fromCron(spec), timezone);
}

// clockTime renders an hour and minute the way a clock does, not the way cron
// does.
function clockTime(hour: number, minute: number): string {
  const suffix = hour < 12 ? "AM" : "PM";
  const h12 = hour % 12 === 0 ? 12 : hour % 12;
  return `${h12}:${pad(minute)} ${suffix}`;
}

function pad(n: number): string {
  return n.toString().padStart(2, "0");
}

// dayPhrase lists the chosen days: "Monday", "Monday and Thursday", "Monday,
// Wednesday and Friday".
function dayPhrase(days: number[]): string {
  const names = [...new Set(days)]
    .filter((d) => d >= 0 && d <= 6)
    .sort((a, b) => a - b)
    .map((d) => DAY_NAMES[d]!);
  if (names.length === 0) return "day";
  if (names.length === 1) return names[0]!;
  return `${names.slice(0, -1).join(", ")} and ${names[names.length - 1]}`;
}

// ordinal renders a day of the month as people say it. The 31st exists in seven
// months; the platform's own misfire policy is what handles a month without it,
// and the builder says so rather than pretending otherwise.
function ordinal(n: number): string {
  const rem100 = n % 100;
  if (rem100 >= 11 && rem100 <= 13) return `${n}th`;
  switch (n % 10) {
    case 1:
      return `${n}st`;
    case 2:
      return `${n}nd`;
    case 3:
      return `${n}rd`;
    default:
      return `${n}th`;
  }
}

/** skipsShortMonths reports a monthly cadence on a day some months do not have,
 * which fires in the months that do and simply does not in the months that
 * don't. Saying so is the difference between a schedule somebody trusts and one
 * they quietly stop trusting in February. */
export function skipsShortMonths(c: Cadence): boolean {
  return c.kind === "monthly" && c.day > 28;
}
