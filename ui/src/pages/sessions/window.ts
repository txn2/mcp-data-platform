// The sessions list rolls the audit log up by session, which reads every event
// in range rather than one page of them. The window is therefore part of the
// query, and it is a visible control rather than a silent default: an operator
// widening it knows they are widening it, and "all time" is offered plainly so
// nothing is quietly withheld.

export type SessionWindow = "24h" | "7d" | "30d" | "all";

export const DEFAULT_SESSION_WINDOW: SessionWindow = "7d";

export const SESSION_WINDOW_OPTIONS: { value: SessionWindow; label: string }[] = [
  { value: "24h", label: "Last 24 Hours" },
  { value: "7d", label: "Last 7 Days" },
  { value: "30d", label: "Last 30 Days" },
  { value: "all", label: "All Time" },
];

const HOURS: Record<Exclude<SessionWindow, "all">, number> = {
  "24h": 24,
  "7d": 24 * 7,
  "30d": 24 * 30,
};

/**
 * Returns the RFC 3339 lower bound for a window, or undefined for "all time",
 * which states no bound at all rather than a very old one.
 */
export function windowStart(window: SessionWindow, now = Date.now()): string | undefined {
  if (window === "all") return undefined;
  return new Date(now - HOURS[window] * 60 * 60 * 1000).toISOString();
}
