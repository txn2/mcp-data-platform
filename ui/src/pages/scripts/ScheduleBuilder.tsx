import { SegmentedControl } from "@/components/patterns/SegmentedControl";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  DAY_NAMES,
  describe,
  skipsShortMonths,
  toCron,
  type Cadence,
  type CadenceKind,
} from "./cadence";

// ScheduleBuilder is how a person says when their automation runs (#1307).
//
// It asks the question in their terms — how often, at what time, in which zone
// — and renders the cron expression itself as a read-back, not as an input.
// Cron is a precise notation for people who already know it, and the owner of a
// report is not required to be one of them.
//
// The expression is still reachable: "Custom" is an escape hatch for a cadence
// the builder cannot express, and it is where an expression an agent wrote
// through manage_script lands rather than being silently rewritten into
// something the builder does understand.

const KINDS: { value: CadenceKind; label: string; text: string }[] = [
  { value: "hourly", label: "Every hour", text: "Hourly" },
  { value: "daily", label: "Every day", text: "Daily" },
  { value: "weekdays", label: "Every weekday, Monday to Friday", text: "Weekdays" },
  { value: "weekly", label: "On chosen days of the week", text: "Weekly" },
  { value: "monthly", label: "On one day of each month", text: "Monthly" },
  { value: "custom", label: "A cron expression, for a cadence the choices above cannot express", text: "Custom" },
];

interface Props {
  cadence: Cadence;
  timezone: string;
  busy: boolean;
  /** The cadence in force, so the builder can show the change rather than
   * repeating the sentence the card already states above it. Null when the
   * script has no schedule and anything chosen here is new. */
  saved: { cron: string; timezone: string } | null;
  onCadenceChange: (cadence: Cadence) => void;
  onTimezoneChange: (timezone: string) => void;
}

export function ScheduleBuilder({
  cadence,
  timezone,
  busy,
  saved,
  onCadenceChange,
  onTimezoneChange,
}: Props) {
  return (
    <div className="space-y-4">
      <div className="space-y-1.5">
        <Label>Repeats</Label>
        <SegmentedControl
          label="How often this script runs"
          value={cadence.kind}
          onChange={(kind) => onCadenceChange(switchKind(cadence, kind))}
          options={KINDS}
          className="flex-wrap"
        />
      </div>

      <div className="grid gap-4 sm:grid-cols-2">
        <CadenceDetail cadence={cadence} busy={busy} onChange={onCadenceChange} />
        <TimezoneField timezone={timezone} busy={busy} onChange={onTimezoneChange} />
      </div>

      <Readback cadence={cadence} timezone={timezone} saved={saved} />
    </div>
  );
}

// switchKind moves to another kind, carrying the time of day across so choosing
// "Weekly" after setting 7:00 AM does not silently reset it to midnight.
function switchKind(current: Cadence, kind: CadenceKind): Cadence {
  const { hour, minute } = timeOf(current);
  switch (kind) {
    case "hourly":
      return { kind, minute };
    case "daily":
    case "weekdays":
      return { kind, hour, minute };
    case "weekly":
      return { kind, days: current.kind === "weekly" ? current.days : [1], hour, minute };
    case "monthly":
      return { kind, day: current.kind === "monthly" ? current.day : 1, hour, minute };
    case "custom":
      return { kind, spec: toCron(current) };
  }
}

// timeOf is the time of day a cadence carries, defaulting to 07:00 for the
// kinds that have none.
function timeOf(c: Cadence): { hour: number; minute: number } {
  switch (c.kind) {
    case "hourly":
      return { hour: 7, minute: c.minute };
    case "daily":
    case "weekdays":
    case "weekly":
    case "monthly":
      return { hour: c.hour, minute: c.minute };
    case "custom":
      return { hour: 7, minute: 0 };
  }
}

// CadenceDetail is the one extra question each kind needs: which minute, what
// time, which days, which day of the month, or the expression itself.
function CadenceDetail({
  cadence,
  busy,
  onChange,
}: {
  cadence: Cadence;
  busy: boolean;
  onChange: (c: Cadence) => void;
}) {
  switch (cadence.kind) {
    case "hourly":
      return (
        <NumberField
          id="script-minute"
          label="Minutes past the hour"
          hint="0 is on the hour."
          value={cadence.minute}
          min={0}
          max={59}
          busy={busy}
          onChange={(minute) => onChange({ ...cadence, minute })}
        />
      );
    case "daily":
    case "weekdays":
      return <TimeField cadence={cadence} busy={busy} onChange={onChange} />;
    case "weekly":
      return (
        <div className="space-y-4">
          <WeekdayPicker
            days={cadence.days}
            busy={busy}
            onChange={(days) => onChange({ ...cadence, days })}
          />
          <TimeField cadence={cadence} busy={busy} onChange={onChange} />
        </div>
      );
    case "monthly":
      return (
        <div className="space-y-4">
          <NumberField
            id="script-day-of-month"
            label="Day of the month"
            hint="1 to 31."
            value={cadence.day}
            min={1}
            max={31}
            busy={busy}
            onChange={(day) => onChange({ ...cadence, day })}
          />
          <TimeField cadence={cadence} busy={busy} onChange={onChange} />
        </div>
      );
    case "custom":
      return (
        <div className="space-y-1.5">
          <Label htmlFor="script-cron">Cron expression</Label>
          <Input
            id="script-cron"
            value={cadence.spec}
            placeholder="0 7 * * 1-5"
            disabled={busy}
            className="font-mono"
            onChange={(e) => onChange({ kind: "custom", spec: e.target.value })}
          />
          <p className="text-xs text-muted-foreground">
            Five fields — minute, hour, day of month, month, day of week — or a descriptor
            such as @daily. A script fires at most once a minute.
          </p>
        </div>
      );
  }
}

// TimeField is the time of day, as a clock rather than as two cron fields.
function TimeField({
  cadence,
  busy,
  onChange,
}: {
  cadence: Extract<Cadence, { hour: number; minute: number }>;
  busy: boolean;
  onChange: (c: Cadence) => void;
}) {
  return (
    <div className="space-y-1.5">
      <Label htmlFor="script-time">Time</Label>
      <Input
        id="script-time"
        type="time"
        value={`${two(cadence.hour)}:${two(cadence.minute)}`}
        disabled={busy}
        className="w-40"
        onChange={(e) => {
          const parsed = parseTime(e.target.value);
          if (parsed) onChange({ ...cadence, ...parsed });
        }}
      />
      <p className="text-xs text-muted-foreground">
        Read in the timezone beside it, so the run keeps its wall clock across a
        daylight-saving change.
      </p>
    </div>
  );
}

// parseTime reads the "HH:MM" an input[type=time] holds. A browser that hands
// back something else leaves the cadence alone rather than moving it to
// midnight.
function parseTime(value: string): { hour: number; minute: number } | null {
  const m = /^(\d{1,2}):(\d{2})/.exec(value);
  if (!m) return null;
  const hour = Number(m[1]);
  const minute = Number(m[2]);
  if (hour > 23 || minute > 59) return null;
  return { hour, minute };
}

function two(n: number): string {
  return n.toString().padStart(2, "0");
}

// WeekdayPicker is seven toggles rather than a multi-select: the whole week is
// visible at once, and what is chosen is legible without opening anything.
function WeekdayPicker({
  days,
  busy,
  onChange,
}: {
  days: number[];
  busy: boolean;
  onChange: (days: number[]) => void;
}) {
  const toggle = (day: number) =>
    onChange(days.includes(day) ? days.filter((d) => d !== day) : [...days, day].sort());
  return (
    <div className="space-y-1.5">
      <Label>Days</Label>
      <div role="group" aria-label="Days of the week" className="flex flex-wrap gap-1">
        {DAY_NAMES.map((name, day) => {
          const on = days.includes(day);
          return (
            <Button
              key={name}
              type="button"
              size="xs"
              variant={on ? "secondary" : "ghost"}
              aria-pressed={on}
              aria-label={name}
              disabled={busy}
              className={!on ? "text-muted-foreground" : undefined}
              onClick={() => toggle(day)}
            >
              {name.slice(0, 3)}
            </Button>
          );
        })}
      </div>
      {days.length === 0 && (
        <p className="text-xs text-muted-foreground">
          No day is chosen, so this would run every day. Pick at least one.
        </p>
      )}
    </div>
  );
}

// NumberField is a bounded whole number. It keeps the typed text when it is not
// yet a number so a field cannot fight the person filling it in.
function NumberField({
  id,
  label,
  hint,
  value,
  min,
  max,
  busy,
  onChange,
}: {
  id: string;
  label: string;
  hint: string;
  value: number;
  min: number;
  max: number;
  busy: boolean;
  onChange: (value: number) => void;
}) {
  return (
    <div className="space-y-1.5">
      <Label htmlFor={id}>{label}</Label>
      <Input
        id={id}
        type="number"
        min={min}
        max={max}
        value={value}
        disabled={busy}
        className="w-40"
        onChange={(e) => {
          const n = Number(e.target.value);
          if (Number.isInteger(n) && n >= min && n <= max) onChange(n);
        }}
      />
      <p className="text-xs text-muted-foreground">{hint}</p>
    </div>
  );
}

// TimezoneField offers the zones the runtime knows, with the reader's own first
// — a schedule is nearly always meant in the zone the person setting it is in,
// and UTC is the platform's default for everything else.
function TimezoneField({
  timezone,
  busy,
  onChange,
}: {
  timezone: string;
  busy: boolean;
  onChange: (timezone: string) => void;
}) {
  const zones = zoneOptions(timezone);
  return (
    <div className="space-y-1.5">
      <Label htmlFor="script-timezone">Timezone</Label>
      <select
        id="script-timezone"
        value={timezone || "UTC"}
        disabled={busy}
        onChange={(e) => onChange(e.target.value)}
        className="border-input bg-background h-9 w-full rounded-md border px-3 py-1 text-sm shadow-xs disabled:opacity-50"
      >
        {zones.map((z) => (
          <option key={z} value={z}>
            {z}
          </option>
        ))}
      </select>
      <p className="text-xs text-muted-foreground">
        The zone the time above is read in. The platform stores UTC when none is chosen.
      </p>
    </div>
  );
}

// zoneOptions lists every IANA zone the runtime carries, with UTC, the
// reader's own zone, and the zone already saved pinned to the front so the
// common choices are not buried in a list of six hundred.
function zoneOptions(current: string): string[] {
  // supportedValuesOf is ES2022 and present in every browser the portal
  // supports, but not in the lib target the project compiles against, so it is
  // reached through a guarded cast rather than by widening the whole target.
  const intl = Intl as typeof Intl & { supportedValuesOf?: (key: string) => string[] };
  const supported = typeof intl.supportedValuesOf === "function"
    ? intl.supportedValuesOf("timeZone")
    : FALLBACK_ZONES;
  const local = Intl.DateTimeFormat().resolvedOptions().timeZone;
  const head = [current, local, "UTC"].filter((z): z is string => !!z);
  return [...new Set([...head, ...supported])];
}

// FALLBACK_ZONES is the short list a runtime without supportedValuesOf offers.
// It is deliberately not a full IANA table: the platform accepts any zone the
// server can load, and the custom cadence plus an agent's manage_script call
// reach the rest.
const FALLBACK_ZONES = [
  "UTC",
  "America/Los_Angeles",
  "America/Denver",
  "America/Chicago",
  "America/New_York",
  "Europe/London",
  "Europe/Berlin",
  "Asia/Kolkata",
  "Asia/Singapore",
  "Asia/Tokyo",
  "Australia/Sydney",
];

// Readback is the sentence that decides whether the builder got it right: what
// this form will save, in words, naming the zone because the same clock time is
// a different instant in each.
//
// It appears only once the choices differ from what is in force. Unchanged, it
// would repeat the sentence the card already states above the form, and two
// identical sentences one above the other read as a page that has lost track of
// itself.
function Readback({
  cadence,
  timezone,
  saved,
}: {
  cadence: Cadence;
  timezone: string;
  saved: { cron: string; timezone: string } | null;
}) {
  const spec = toCron(cadence);
  const unchanged = saved !== null && saved.cron === spec && saved.timezone === timezone;
  if (unchanged) return null;
  return (
    <div className="space-y-2">
      <p className="text-sm">
        <span className="text-muted-foreground">{saved ? "Saves as: " : "Runs: "}</span>
        {describe(cadence, timezone)}
      </p>
      {cadence.kind !== "custom" && (
        <p className="font-mono text-xs text-muted-foreground">{spec}</p>
      )}
      {skipsShortMonths(cadence) && (
        <Alert>
          <AlertDescription>
            Months without that day are skipped rather than moved — a run on the 31st happens
            in the seven months that have one.
          </AlertDescription>
        </Alert>
      )}
    </div>
  );
}
